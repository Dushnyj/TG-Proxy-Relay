package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/topology"
)

type topologyManager struct {
	mu           sync.RWMutex
	refreshMu    sync.Mutex
	onDemandMu   sync.Mutex
	lastOnDemand time.Time
	base         config.TelegramConfig
	remote       config.TopologyConfig
	publicKey    ed25519.PublicKey
	current      *topology.Snapshot
	client       *http.Client
}

func newTopologyManager(telegram config.TelegramConfig) (*topologyManager, error) {
	manager := &topologyManager{base: telegram, remote: telegram.Topology}
	if strings.TrimSpace(telegram.Topology.URL) == "" {
		return manager, nil
	}
	key, err := decodeTopologyPublicKey(telegram.Topology.PublicKey)
	if err != nil {
		return nil, err
	}
	manager.publicKey = key
	manager.client = &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialSafeTopologySource,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     30 * time.Second,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("topology redirects are disabled")
		},
	}
	if data, err := os.ReadFile(telegram.Topology.StatePath); err == nil {
		snapshot, verifyErr := topology.Verify(data, key, time.Now().UTC(), true)
		if verifyErr != nil {
			return nil, fmt.Errorf("verify last-known-good topology: %w", verifyErr)
		}
		manager.current = &snapshot
		if !snapshot.ExpiresAt.After(time.Now().UTC()) {
			log.Printf("using expired signed last-known-good topology generation %d", snapshot.Generation)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load last-known-good topology: %w", err)
	}
	return manager, nil
}

func (m *topologyManager) start(ctx context.Context) {
	if m == nil || strings.TrimSpace(m.remote.URL) == "" {
		return
	}
	go func() {
		failures := 0
		for {
			if m.refresh(ctx) {
				failures = 0
			} else {
				failures++
			}
			delay := nextTopologyRefreshDelay(
				time.Duration(m.remote.RefreshIntervalSec)*time.Second,
				failures, rand.Float64())
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}()
}

func (m *topologyManager) refresh(parent context.Context) bool {
	if m == nil || m.client == nil || strings.TrimSpace(m.remote.URL) == "" {
		return false
	}
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	if err := parent.Err(); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.remote.URL, nil)
	if err != nil {
		log.Printf("topology refresh request failed: %v", err)
		return false
	}
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		log.Printf("topology refresh failed: %v", err)
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		log.Printf("topology refresh HTTP %d", response.StatusCode)
		return false
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		log.Printf("topology refresh body is invalid")
		return false
	}
	snapshot, err := topology.Verify(data, m.publicKey, time.Now().UTC(), false)
	if err != nil {
		log.Printf("topology refresh rejected: %v", err)
		return false
	}
	m.mu.Lock()
	if m.current != nil && snapshot.Generation <= m.current.Generation {
		m.mu.Unlock()
		return true
	}
	if err := persistTopologySnapshot(m.remote.StatePath, snapshot.Raw); err != nil {
		m.mu.Unlock()
		log.Printf("topology last-known-good persistence failed: %v", err)
		return false
	}
	m.current = &snapshot
	m.mu.Unlock()
	log.Printf("topology generation %d applied", snapshot.Generation)
	return true
}

// ensureRoute closes the short race between Telegram publishing a new DC and the normal
// refresh timer. A single authenticated request may trigger one signed refresh; a global
// lock and cooldown prevent arbitrary DC values from turning the update source into a DoS
// target. The signed LKG remains authoritative when the source is unavailable or invalid.
func (m *topologyManager) ensureRoute(parent context.Context, dc int, test, media bool) []string {
	if addresses := m.addresses(dc, test, media); len(addresses) > 0 {
		return addresses
	}
	if m == nil || strings.TrimSpace(m.remote.URL) == "" {
		return nil
	}
	m.onDemandMu.Lock()
	defer m.onDemandMu.Unlock()
	if addresses := m.addresses(dc, test, media); len(addresses) > 0 {
		return addresses
	}
	now := time.Now()
	if !m.lastOnDemand.IsZero() && now.Sub(m.lastOnDemand) < 30*time.Second {
		return nil
	}
	m.lastOnDemand = now
	m.refresh(parent)
	return m.addresses(dc, test, media)
}

func nextTopologyRefreshDelay(base time.Duration, failures int, randomUnit float64) time.Duration {
	if base <= 0 {
		base = 15 * time.Minute
	}
	if randomUnit < 0 {
		randomUnit = 0
	} else if randomUnit > 1 {
		randomUnit = 1
	}
	delay := base
	if failures > 0 {
		exponent := failures - 1
		if exponent > 8 {
			exponent = 8
		}
		delay = 30 * time.Second * time.Duration(1<<exponent)
		if delay > base {
			delay = base
		}
	}
	// Successes refresh at 90-110% of the configured interval. Failures retry at
	// 80-120% of exponential backoff so a fleet cannot synchronize on an outage.
	spread := 0.2
	if failures > 0 {
		spread = 0.4
	}
	factor := 1 - spread/2 + spread*randomUnit
	return time.Duration(float64(delay) * factor)
}

func (m *topologyManager) addresses(dc int, test, media bool) []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	snapshot := m.current
	if snapshot != nil {
		routes := snapshot.Production
		if test {
			routes = snapshot.Test
		}
		options := append([]config.TelegramEndpoint(nil), routes[dc]...)
		m.mu.RUnlock()
		isolated := config.TelegramConfig{DCOptions: map[int][]config.TelegramEndpoint{dc: options}}
		if test {
			isolated = config.TelegramConfig{TestDCOptions: map[int][]config.TelegramEndpoint{dc: options}}
		}
		return isolated.AddressesForRoute(dc, test, media)
	}
	base := m.base
	m.mu.RUnlock()
	return base.AddressesForRoute(dc, test, media)
}

func (m *topologyManager) configuredDCs(test bool) []int {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[int]struct{})
	if m.current != nil {
		routes := m.current.Production
		if test {
			routes = m.current.Test
		}
		for dc, endpoints := range routes {
			if dc > 0 && len(endpoints) > 0 {
				seen[dc] = struct{}{}
			}
		}
	} else {
		legacy, options := m.base.DCMap, m.base.DCOptions
		if test {
			legacy, options = m.base.TestDCMap, m.base.TestDCOptions
		}
		for dc, address := range legacy {
			if dc > 0 && strings.TrimSpace(address) != "" {
				seen[dc] = struct{}{}
			}
		}
		for dc, endpoints := range options {
			if dc > 0 && len(endpoints) > 0 {
				seen[dc] = struct{}{}
			}
		}
	}
	result := make([]int, 0, len(seen))
	for dc := range seen {
		result = append(result, dc)
	}
	sort.Ints(result)
	return result
}

func (m *topologyManager) source() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return "owner-config"
	}
	return fmt.Sprintf("signed-bundle:%d", m.current.Generation)
}

func (m *topologyManager) dynamic() bool {
	return m != nil && strings.TrimSpace(m.remote.URL) != ""
}

func (m *topologyManager) revision() uint64 {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return 0
	}
	return m.current.Generation
}

func dialSafeTopologySource(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid topology source address: %w", err)
	}
	var addresses []net.IPAddr
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		addresses, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("topology source resolved to no addresses")
	}
	for _, candidate := range addresses {
		if !config.IsSafeTelegramIP(candidate.IP) {
			return nil, errors.New("topology source resolved to a non-public address")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var last error
	for _, candidate := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network,
			net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		last = dialErr
	}
	return nil, last
}

func decodeTopologyPublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	}
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("invalid topology Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

func persistTopologySnapshot(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".tgproxy-topology-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if handle, err := os.Open(directory); err == nil {
			defer handle.Close()
			return handle.Sync()
		}
	}
	return nil
}
