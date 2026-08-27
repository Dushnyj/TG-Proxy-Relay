package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

const (
	controlStateVersion = 2
	maxControlBodyBytes = 32 * 1024
	maxTrackedClients   = 4096
	maxLocationCache    = 2048
	clientPersistDelay  = 2 * time.Second
	clientPersistRetry  = 30 * time.Second
)

type controlPlane struct {
	mu            sync.Mutex
	baseTokens    []config.Token
	adminTokens   []config.Token
	statePath     string
	state         controlState
	active        map[string]map[uint64]*trackedSession
	nextSessionID uint64
	resolver      geoIPResolver
	locationCache map[string]cachedLocation
	locationBusy  map[string]struct{}
	clientDirty   bool
	clientFlush   *time.Timer
	shuttingDown  bool
}

type controlState struct {
	Version        int                       `json:"version"`
	AddedTokens    []config.Token            `json:"addedTokens,omitempty"`
	RevokedHashes  []string                  `json:"revokedHashes,omitempty"`
	Clients        map[string]*trackedClient `json:"clients,omitempty"`
	BlockedDevices map[string]string         `json:"blockedDevices,omitempty"`
}

type trackedClient struct {
	Key          string `json:"key"`
	TokenID      string `json:"tokenId"`
	DeviceID     string `json:"deviceId"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	AppVersion   string `json:"appVersion,omitempty"`
	AppCode      string `json:"appCode,omitempty"`
	Android      string `json:"android,omitempty"`
	Country      string `json:"country,omitempty"`
	City         string `json:"city,omitempty"`
	RemoteIP     string `json:"remoteIp,omitempty"`
	FirstSeen    string `json:"firstSeen"`
	LastSeen     string `json:"lastSeen"`
}

type trackedSession struct {
	clientKey string
	close     func()
}

type geoIPResolver interface {
	Resolve(ctx context.Context, ip string) (location, error)
}

type location struct {
	Country string
	City    string
}

type cachedLocation struct {
	Location location
	At       time.Time
}

type httpGeoIPResolver struct {
	template string
	client   *http.Client
}

type tokenView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CreatedAt     string `json:"createdAt,omitempty"`
	ActiveDevices int    `json:"activeDevices"`
	KnownDevices  int    `json:"knownDevices"`
}

type clientView struct {
	TokenID        string `json:"tokenId"`
	DeviceID       string `json:"deviceId"`
	Manufacturer   string `json:"manufacturer,omitempty"`
	Model          string `json:"model,omitempty"`
	AppVersion     string `json:"appVersion,omitempty"`
	AppCode        string `json:"appCode,omitempty"`
	Android        string `json:"android,omitempty"`
	Country        string `json:"country,omitempty"`
	City           string `json:"city,omitempty"`
	RemoteIP       string `json:"remoteIp,omitempty"`
	FirstSeen      string `json:"firstSeen"`
	LastSeen       string `json:"lastSeen"`
	ActiveSessions int    `json:"activeSessions"`
	Blocked        bool   `json:"blocked"`
	BlockedAt      string `json:"blockedAt,omitempty"`
}

type overviewResponse struct {
	Tokens  []tokenView  `json:"tokens"`
	Clients []clientView `json:"clients"`
}

type createdTokenResponse struct {
	Token  tokenView `json:"token"`
	Secret string    `json:"secret"`
}

func newControlPlane(cfg config.Config) (*controlPlane, error) {
	control := &controlPlane{
		baseTokens:    cloneTokens(cfg.Tokens),
		adminTokens:   cloneTokens(cfg.Admin.Tokens),
		statePath:     strings.TrimSpace(cfg.Admin.StatePath),
		active:        make(map[string]map[uint64]*trackedSession),
		locationCache: make(map[string]cachedLocation),
		locationBusy:  make(map[string]struct{}),
		state: controlState{
			Version:        controlStateVersion,
			Clients:        make(map[string]*trackedClient),
			BlockedDevices: make(map[string]string),
		},
	}
	if control.enabled() && control.statePath != "" {
		if err := control.load(); err != nil {
			return nil, err
		}
		if err := control.validateStateAgainstConfig(); err != nil {
			return nil, err
		}
	}
	if template := strings.TrimSpace(cfg.Admin.GeoIPURL); template != "" {
		control.resolver = &httpGeoIPResolver{
			template: template,
			client:   &http.Client{Timeout: 4 * time.Second},
		}
	}
	return control, nil
}

func cloneTokens(source []config.Token) []config.Token {
	result := make([]config.Token, len(source))
	copy(result, source)
	return result
}

func (c *controlPlane) enabled() bool {
	return c != nil && len(c.adminTokens) > 0
}

func (c *controlPlane) authorizeAdmin(header string) bool {
	if c == nil {
		return false
	}
	return authorizeTokens(c.adminTokens, header)
}

func (c *controlPlane) authorizeClient(header string) (config.Token, bool) {
	if c == nil {
		return config.Token{}, false
	}
	raw := bearerToken(header)
	if raw == "" {
		return config.Token{}, false
	}
	hash := config.TokenHash(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revokedLocked(hash) {
		return config.Token{}, false
	}
	for _, token := range c.allTokensLocked() {
		if constantHashEqual(token.Hash, hash) {
			token.Hash = config.NormalizeTokenHash(token.Hash)
			token.ID = config.StableTokenID(token)
			return token, true
		}
	}
	return config.Token{}, false
}

func authorizeTokens(tokens []config.Token, header string) bool {
	raw := bearerToken(header)
	if raw == "" {
		return false
	}
	hash := config.TokenHash(raw)
	for _, token := range tokens {
		if constantHashEqual(token.Hash, hash) {
			return true
		}
	}
	return false
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func constantHashEqual(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(strings.TrimPrefix(config.NormalizeTokenHash(left), "sha256:"))
	rightBytes, rightErr := hex.DecodeString(strings.TrimPrefix(config.NormalizeTokenHash(right), "sha256:"))
	if leftErr != nil || rightErr != nil || len(leftBytes) != sha256.Size ||
		len(rightBytes) != sha256.Size || len(leftBytes) != len(rightBytes) {
		return false
	}
	var different byte
	for index := range leftBytes {
		different |= leftBytes[index] ^ rightBytes[index]
	}
	return different == 0
}

func (c *controlPlane) createToken(name string) (config.Token, string, error) {
	if c == nil || !c.enabled() {
		return config.Token{}, "", errors.New("owner API is disabled")
	}
	cleanName := sanitizeDisplay(name, 128)
	if cleanName == "" {
		cleanName = "Подключение"
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return config.Token{}, "", err
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return config.Token{}, "", err
	}
	raw := "tgpr_" + base64.RawURLEncoding.EncodeToString(rawBytes)
	token := config.Token{
		ID:        "tok_" + hex.EncodeToString(idBytes),
		Name:      cleanName,
		Hash:      config.TokenHash(raw),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.allTokensLocked()) >= 1024 {
		return config.Token{}, "", errors.New("too many tokens")
	}
	c.state.AddedTokens = append(c.state.AddedTokens, token)
	if err := c.persistCriticalLocked(); err != nil {
		c.state.AddedTokens = c.state.AddedTokens[:len(c.state.AddedTokens)-1]
		return config.Token{}, "", err
	}
	return token, raw, nil
}

func (c *controlPlane) deleteToken(id string) error {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return errors.New("token id is required")
	}
	c.mu.Lock()
	var found *config.Token
	for _, token := range c.allTokensLocked() {
		candidate := token
		if config.StableTokenID(candidate) == cleanID {
			found = &candidate
			break
		}
	}
	if found == nil {
		c.mu.Unlock()
		return os.ErrNotExist
	}
	hash := config.NormalizeTokenHash(found.Hash)
	baseToken := false
	for _, token := range c.baseTokens {
		if config.NormalizeTokenHash(token.Hash) == hash {
			baseToken = true
			break
		}
	}
	previousAdded := cloneTokens(c.state.AddedTokens)
	previousRevoked := append([]string(nil), c.state.RevokedHashes...)
	removedClients := make(map[string]*trackedClient)
	removedBlocked := make(map[string]string)
	filtered := c.state.AddedTokens[:0]
	for _, token := range c.state.AddedTokens {
		if config.NormalizeTokenHash(token.Hash) != hash {
			filtered = append(filtered, token)
		}
	}
	c.state.AddedTokens = filtered
	if baseToken && !containsString(c.state.RevokedHashes, hash) {
		if len(c.state.RevokedHashes) >= 2048 {
			c.state.AddedTokens = previousAdded
			c.mu.Unlock()
			return errors.New("too many persisted token revocations")
		}
		c.state.RevokedHashes = append(c.state.RevokedHashes, hash)
	}
	for key, client := range c.state.Clients {
		if client != nil && client.TokenID == cleanID {
			removedClients[key] = client
			if blockedAt := c.state.BlockedDevices[key]; blockedAt != "" {
				removedBlocked[key] = blockedAt
				delete(c.state.BlockedDevices, key)
			}
			delete(c.state.Clients, key)
		}
	}
	if err := c.persistCriticalLocked(); err != nil {
		c.state.AddedTokens = previousAdded
		c.state.RevokedHashes = previousRevoked
		for key, client := range removedClients {
			c.state.Clients[key] = client
		}
		for key, blockedAt := range removedBlocked {
			c.state.BlockedDevices[key] = blockedAt
		}
		c.mu.Unlock()
		return err
	}
	closers := make([]func(), 0)
	for _, session := range c.active[cleanID] {
		if session != nil && session.close != nil {
			closers = append(closers, session.close)
		}
	}
	c.mu.Unlock()
	for _, closeSession := range closers {
		closeSession()
	}
	return nil
}

func (c *controlPlane) deviceBlocked(token config.Token, r *http.Request) bool {
	if c == nil || !c.enabled() {
		return false
	}
	tokenID, deviceID := sessionIdentity(token, r)
	key := tokenID + ":" + deviceID
	c.mu.Lock()
	defer c.mu.Unlock()
	_, blocked := c.state.BlockedDevices[key]
	return blocked
}

func sessionIdentity(token config.Token, r *http.Request) (string, string) {
	tokenID := config.StableTokenID(token)
	deviceID := ""
	remoteIP := ""
	if r != nil {
		deviceID = sanitizeIdentifier(r.Header.Get("X-TGProxy-Device-ID"), 96)
		remoteIP = clientRemoteIP(r)
	}
	if deviceID == "" {
		sum := sha256.Sum256([]byte(tokenID + "\n" + remoteIP))
		deviceID = "legacy_" + hex.EncodeToString(sum[:8])
	}
	return tokenID, deviceID
}

func (c *controlPlane) disconnectDevice(tokenID, deviceID string) error {
	key, err := deviceKey(tokenID, deviceID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.state.Clients[key] == nil {
		c.mu.Unlock()
		return os.ErrNotExist
	}
	closers := c.deviceClosersLocked(key)
	c.mu.Unlock()
	for _, closeSession := range closers {
		closeSession()
	}
	return nil
}

func (c *controlPlane) setDeviceBlocked(tokenID, deviceID string, blocked bool) error {
	key, err := deviceKey(tokenID, deviceID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	// Validate and mutate under one lock. Otherwise a concurrent token deletion can remove
	// the client between validation and persistence and leave an orphan blockedDevices entry
	// that makes the durable state impossible to load after restart.
	if c.state.Clients[key] == nil {
		c.mu.Unlock()
		return os.ErrNotExist
	}
	previous, existed := c.state.BlockedDevices[key]
	if blocked {
		c.state.BlockedDevices[key] = time.Now().UTC().Format(time.RFC3339)
	} else {
		delete(c.state.BlockedDevices, key)
	}
	if err := c.persistCriticalLocked(); err != nil {
		if existed {
			c.state.BlockedDevices[key] = previous
		} else {
			delete(c.state.BlockedDevices, key)
		}
		c.mu.Unlock()
		return err
	}
	closers := []func(){}
	if blocked {
		closers = c.deviceClosersLocked(key)
	}
	c.mu.Unlock()
	for _, closeSession := range closers {
		closeSession()
	}
	return nil
}

func deviceKey(tokenID, deviceID string) (string, error) {
	cleanToken := sanitizeIdentifier(tokenID, 96)
	cleanDevice := sanitizeIdentifier(deviceID, 96)
	if cleanToken == "" || cleanDevice == "" {
		return "", errors.New("invalid device identity")
	}
	return cleanToken + ":" + cleanDevice, nil
}

func (c *controlPlane) deviceClosersLocked(clientKey string) []func() {
	closers := make([]func(), 0)
	for _, sessions := range c.active {
		for _, session := range sessions {
			if session != nil && session.clientKey == clientKey && session.close != nil {
				closers = append(closers, session.close)
			}
		}
	}
	return closers
}

func (c *controlPlane) registerSession(token config.Token, r *http.Request,
	closeSession func()) (func(), bool) {
	if c == nil || !c.enabled() {
		return func() {}, true
	}
	tokenID, deviceID := sessionIdentity(token, r)
	remoteIP := clientRemoteIP(r)
	clientKey := tokenID + ":" + deviceID
	now := time.Now().UTC().Format(time.RFC3339)

	c.mu.Lock()
	// Authorization and WebSocket setup are separated by the upstream dial. Recheck while
	// holding the same lock used by token deletion so a connection cannot slip in after its
	// token was revoked but before the session entered the active-session map.
	if c.shuttingDown || !c.clientTokenActiveLocked(token) {
		c.mu.Unlock()
		return func() {}, false
	}
	if _, blocked := c.state.BlockedDevices[clientKey]; blocked {
		c.mu.Unlock()
		return func() {}, false
	}
	c.pruneClientsLocked()
	client := c.state.Clients[clientKey]
	if client == nil {
		client = &trackedClient{Key: clientKey, TokenID: tokenID, DeviceID: deviceID, FirstSeen: now}
		c.state.Clients[clientKey] = client
	}
	client.Manufacturer = sanitizeDisplay(r.Header.Get("X-TGProxy-Manufacturer"), 96)
	client.Model = sanitizeDisplay(r.Header.Get("X-TGProxy-Model"), 128)
	client.AppVersion = sanitizeDisplay(r.Header.Get("X-TGProxy-App-Version"), 64)
	client.AppCode = sanitizeDisplay(r.Header.Get("X-TGProxy-App-Code"), 32)
	client.Android = sanitizeDisplay(r.Header.Get("X-TGProxy-Android"), 64)
	client.RemoteIP = remoteIP
	client.LastSeen = now
	c.nextSessionID++
	sessionID := c.nextSessionID
	byToken := c.active[tokenID]
	if byToken == nil {
		byToken = make(map[uint64]*trackedSession)
		c.active[tokenID] = byToken
	}
	byToken[sessionID] = &trackedSession{clientKey: clientKey, close: closeSession}
	c.scheduleClientPersistLocked()
	resolver := c.resolver
	needsLocation := remoteIP != "" && client.Country == "" && publicIP(remoteIP)
	if needsLocation && resolver != nil {
		if cached, ok := c.locationCache[remoteIP]; ok && time.Since(cached.At) < 24*time.Hour {
			client.Country = cached.Location.Country
			client.City = cached.Location.City
			c.scheduleClientPersistLocked()
			needsLocation = false
		} else if _, busy := c.locationBusy[remoteIP]; busy {
			needsLocation = false
		} else {
			c.locationBusy[remoteIP] = struct{}{}
		}
	}
	c.mu.Unlock()

	if needsLocation && resolver != nil {
		go c.resolveLocation(clientKey, remoteIP)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			if sessions := c.active[tokenID]; sessions != nil {
				delete(sessions, sessionID)
				if len(sessions) == 0 {
					delete(c.active, tokenID)
				}
			}
			if record := c.state.Clients[clientKey]; record != nil {
				record.LastSeen = time.Now().UTC().Format(time.RFC3339)
			}
			c.scheduleClientPersistLocked()
			c.mu.Unlock()
		})
	}, true
}

func (c *controlPlane) shutdown() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.shuttingDown = true
	closers := make([]func(), 0)
	for _, sessions := range c.active {
		for _, session := range sessions {
			if session != nil && session.close != nil {
				closers = append(closers, session.close)
			}
		}
	}
	c.mu.Unlock()
	for _, closeSession := range closers {
		closeSession()
	}

	// Hijacked WebSockets are outside net/http's Shutdown tracking. Give their bridge defers a
	// short bounded window to update LastSeen before the final durable client-state flush.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		active := 0
		for _, sessions := range c.active {
			active += len(sessions)
		}
		c.mu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clientFlush != nil {
		c.clientFlush.Stop()
		c.clientFlush = nil
	}
	if !c.clientDirty {
		return nil
	}
	if err := c.persistLocked(); err != nil {
		return err
	}
	c.clientDirty = false
	return nil
}

func (c *controlPlane) clientTokenActiveLocked(wanted config.Token) bool {
	wantedHash := config.NormalizeTokenHash(wanted.Hash)
	if wantedHash == "" || c.revokedLocked(wantedHash) {
		return false
	}
	for _, token := range c.allTokensLocked() {
		if constantHashEqual(token.Hash, wantedHash) {
			return true
		}
	}
	return false
}

func (c *controlPlane) overview() overviewResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	knownByToken := make(map[string]map[string]struct{})
	activeByClient := make(map[string]int)
	for _, byToken := range c.active {
		for _, session := range byToken {
			if session != nil {
				activeByClient[session.clientKey]++
			}
		}
	}
	clients := make([]clientView, 0, len(c.state.Clients))
	for _, client := range c.state.Clients {
		if client == nil {
			continue
		}
		if knownByToken[client.TokenID] == nil {
			knownByToken[client.TokenID] = make(map[string]struct{})
		}
		knownByToken[client.TokenID][client.DeviceID] = struct{}{}
		clients = append(clients, clientView{
			TokenID: client.TokenID, DeviceID: client.DeviceID,
			Manufacturer: client.Manufacturer, Model: client.Model,
			AppVersion: client.AppVersion, AppCode: client.AppCode,
			Android: client.Android, Country: client.Country, City: client.City,
			RemoteIP: client.RemoteIP, FirstSeen: client.FirstSeen, LastSeen: client.LastSeen,
			ActiveSessions: activeByClient[client.Key],
			Blocked:        c.state.BlockedDevices[client.Key] != "",
			BlockedAt:      c.state.BlockedDevices[client.Key],
		})
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].LastSeen > clients[j].LastSeen })
	tokens := make([]tokenView, 0)
	for _, token := range c.allTokensLocked() {
		id := config.StableTokenID(token)
		activeDevices := 0
		for key := range knownByToken[id] {
			if activeByClient[id+":"+key] > 0 {
				activeDevices++
			}
		}
		tokens = append(tokens, tokenView{
			ID: id, Name: token.Name, CreatedAt: token.CreatedAt,
			ActiveDevices: activeDevices, KnownDevices: len(knownByToken[id]),
		})
	}
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].CreatedAt == tokens[j].CreatedAt {
			return tokens[i].Name < tokens[j].Name
		}
		return tokens[i].CreatedAt < tokens[j].CreatedAt
	})
	return overviewResponse{Tokens: tokens, Clients: clients}
}

func (c *controlPlane) allTokensLocked() []config.Token {
	result := make([]config.Token, 0, len(c.baseTokens)+len(c.state.AddedTokens))
	seen := make(map[string]struct{})
	for _, group := range [][]config.Token{c.baseTokens, c.state.AddedTokens} {
		for _, token := range group {
			hash := config.NormalizeTokenHash(token.Hash)
			if hash == "" || c.revokedLocked(hash) {
				continue
			}
			if _, exists := seen[hash]; exists {
				continue
			}
			seen[hash] = struct{}{}
			token.Hash = hash
			token.ID = config.StableTokenID(token)
			result = append(result, token)
		}
	}
	return result
}

func (c *controlPlane) validateStateAgainstConfig() error {
	ids := make(map[string]struct{}, len(c.baseTokens)+len(c.state.AddedTokens))
	hashes := make(map[string]struct{}, len(c.baseTokens)+len(c.state.AddedTokens))
	for _, token := range append(cloneTokens(c.baseTokens), c.state.AddedTokens...) {
		id := config.StableTokenID(token)
		hash := config.NormalizeTokenHash(token.Hash)
		if _, exists := ids[id]; exists {
			return fmt.Errorf("control state conflicts with configured token id %q", id)
		}
		if _, exists := hashes[hash]; exists {
			return errors.New("control state conflicts with a configured token hash")
		}
		ids[id] = struct{}{}
		hashes[hash] = struct{}{}
	}
	return nil
}

func (c *controlPlane) revokedLocked(hash string) bool {
	return containsString(c.state.RevokedHashes, config.NormalizeTokenHash(hash))
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func (c *controlPlane) load() error {
	data, err := os.ReadFile(c.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load control state: %w", err)
	}
	if len(data) > 8*1024*1024 {
		return errors.New("control state is too large")
	}
	var state controlState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("parse control state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("parse control state: trailing data")
	}
	if state.Version != 1 && state.Version != controlStateVersion {
		return fmt.Errorf("unsupported control state version %d", state.Version)
	}
	if state.Version == 1 {
		state.Version = controlStateVersion
	}
	if state.Clients == nil {
		state.Clients = make(map[string]*trackedClient)
	}
	if state.BlockedDevices == nil {
		state.BlockedDevices = make(map[string]string)
	}
	if len(state.AddedTokens) > 1024 || len(state.RevokedHashes) > 2048 ||
		len(state.Clients) > maxTrackedClients || len(state.BlockedDevices) > maxTrackedClients {
		return errors.New("control state exceeds limits")
	}
	for _, token := range state.AddedTokens {
		if token.ID == "" || config.StableTokenID(token) != token.ID ||
			!constantHashEqual(token.Hash, config.NormalizeTokenHash(token.Hash)) {
			return errors.New("control state contains an invalid token")
		}
	}
	for _, hash := range state.RevokedHashes {
		if normalized := config.NormalizeTokenHash(hash); normalized == "" ||
			!constantHashEqual(hash, normalized) {
			return errors.New("control state contains an invalid revoked hash")
		}
	}
	for key, client := range state.Clients {
		if client == nil || key != client.Key || sanitizeIdentifier(client.DeviceID, 96) == "" ||
			sanitizeIdentifier(client.TokenID, 96) == "" || key != client.TokenID+":"+client.DeviceID {
			return errors.New("control state contains an invalid client")
		}
	}
	for key, blockedAt := range state.BlockedDevices {
		client := state.Clients[key]
		if client == nil || key != client.TokenID+":"+client.DeviceID {
			return errors.New("control state contains an invalid blocked device")
		}
		if _, err := time.Parse(time.RFC3339, blockedAt); err != nil {
			return errors.New("control state contains an invalid blocked timestamp")
		}
	}
	c.state = state
	return nil
}

func (c *controlPlane) persistLocked() error {
	if c.statePath == "" {
		return nil
	}
	c.state.Version = controlStateVersion
	data, err := json.MarshalIndent(c.state, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(c.statePath)
	if err := os.MkdirAll(directory, 0750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".tgproxy-state-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
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
		if err := os.Remove(c.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(temporaryName, c.statePath); err != nil {
		return err
	}
	// The file itself is fsynced above. Sync the parent directory too so the rename survives a
	// sudden VPS power loss. Windows does not support opening directories this way.
	if runtime.GOOS != "windows" {
		if directoryHandle, err := os.Open(directory); err == nil {
			if err := directoryHandle.Sync(); err != nil {
				log.Printf("owner state directory sync failed: %v", err)
			}
			_ = directoryHandle.Close()
		} else {
			log.Printf("owner state directory open failed: %v", err)
		}
	}
	return nil
}

// Token creation and revocation are durability boundaries: the API reports success only after
// the state file is safely replaced. Client presence/last-seen metadata is intentionally
// coalesced so a busy Relay does not fsync the complete state file for every MTProto session.
func (c *controlPlane) persistCriticalLocked() error {
	if err := c.persistLocked(); err != nil {
		return err
	}
	c.clientDirty = false
	if c.clientFlush != nil {
		c.clientFlush.Stop()
		c.clientFlush = nil
	}
	return nil
}

func (c *controlPlane) scheduleClientPersistLocked() {
	if c == nil || c.statePath == "" {
		return
	}
	c.clientDirty = true
	if c.shuttingDown {
		// shutdown() performs one final synchronous flush after closing hijacked sessions.
		return
	}
	if c.clientFlush != nil {
		return
	}
	c.clientFlush = time.AfterFunc(clientPersistDelay, c.flushClientState)
}

func (c *controlPlane) flushClientState() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.clientFlush = nil
	if !c.clientDirty {
		c.mu.Unlock()
		return
	}
	if err := c.persistLocked(); err != nil {
		log.Printf("owner client state persistence failed: %v", err)
		c.clientFlush = time.AfterFunc(clientPersistRetry, c.flushClientState)
		c.mu.Unlock()
		return
	}
	c.clientDirty = false
	c.mu.Unlock()
}

func (c *controlPlane) resolveLocation(clientKey, ip string) {
	defer func() {
		c.mu.Lock()
		delete(c.locationBusy, ip)
		c.mu.Unlock()
	}()
	c.mu.Lock()
	if cached, ok := c.locationCache[ip]; ok && time.Since(cached.At) < 24*time.Hour {
		if client := c.state.Clients[clientKey]; client != nil {
			client.Country = cached.Location.Country
			client.City = cached.Location.City
			c.scheduleClientPersistLocked()
		}
		c.mu.Unlock()
		return
	}
	resolver := c.resolver
	c.mu.Unlock()
	if resolver == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resolved, err := resolver.Resolve(ctx, ip)
	if err != nil || (resolved.Country == "" && resolved.City == "") {
		return
	}
	c.mu.Lock()
	if len(c.locationCache) >= maxLocationCache {
		for key := range c.locationCache {
			delete(c.locationCache, key)
			break
		}
	}
	c.locationCache[ip] = cachedLocation{Location: resolved, At: time.Now()}
	if client := c.state.Clients[clientKey]; client != nil && client.RemoteIP == ip {
		client.Country = sanitizeDisplay(resolved.Country, 128)
		client.City = sanitizeDisplay(resolved.City, 128)
		c.scheduleClientPersistLocked()
	}
	c.mu.Unlock()
}

func (r *httpGeoIPResolver) Resolve(ctx context.Context, ip string) (location, error) {
	if r == nil || r.client == nil || !publicIP(ip) {
		return location{}, errors.New("geoip unavailable")
	}
	endpoint := strings.Replace(r.template, "%s", url.PathEscape(ip), 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return location{}, err
	}
	req.Header.Set("Accept", "application/json")
	response, err := r.client.Do(req)
	if err != nil {
		return location{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return location{}, fmt.Errorf("geoip status %d", response.StatusCode)
	}
	var body struct {
		Success bool   `json:"success"`
		Country string `json:"country"`
		City    string `json:"city"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
	if err := decoder.Decode(&body); err != nil {
		return location{}, err
	}
	if !body.Success {
		return location{}, errors.New("geoip lookup failed")
	}
	return location{Country: sanitizeDisplay(body.Country, 128), City: sanitizeDisplay(body.City, 128)}, nil
}

func (c *controlPlane) pruneClientsLocked() {
	if len(c.state.Clients) < maxTrackedClients {
		return
	}
	type candidate struct {
		key      string
		lastSeen string
	}
	inactive := make([]candidate, 0, len(c.state.Clients))
	active := make([]candidate, 0)
	activeKeys := make(map[string]struct{})
	for _, sessions := range c.active {
		for _, session := range sessions {
			if session != nil {
				activeKeys[session.clientKey] = struct{}{}
			}
		}
	}
	for key, client := range c.state.Clients {
		if client == nil {
			continue
		}
		item := candidate{key: key, lastSeen: client.LastSeen}
		if _, isActive := activeKeys[key]; isActive {
			active = append(active, item)
		} else {
			inactive = append(inactive, item)
		}
	}
	sort.Slice(inactive, func(i, j int) bool { return inactive[i].lastSeen < inactive[j].lastSeen })
	sort.Slice(active, func(i, j int) bool { return active[i].lastSeen < active[j].lastSeen })
	items := append(inactive, active...)
	remove := len(c.state.Clients) - maxTrackedClients + 1
	for index := 0; index < remove && index < len(items); index++ {
		key := items[index].key
		delete(c.state.Clients, key)
		delete(c.state.BlockedDevices, key)
	}
}

func sanitizeDisplay(value string, max int) string {
	clean := strings.TrimSpace(value)
	clean = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, clean)
	runes := []rune(clean)
	if len(runes) > max {
		clean = string(runes[:max])
	}
	return clean
}

func sanitizeIdentifier(value string, max int) string {
	clean := strings.TrimSpace(value)
	if clean == "" || len(clean) > max {
		return ""
	}
	for _, ch := range clean {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return ""
	}
	return clean
}

func clientRemoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	parsedRemote := net.ParseIP(host)
	// Host reverse proxies arrive from loopback; a Docker reverse proxy arrives from its private
	// bridge. Client metadata is informational (the bearer token still authorizes the session),
	// so trusting forwarding headers only from local/private hops preserves the real location
	// without allowing a public direct client to spoof it.
	if parsedRemote != nil && (parsedRemote.IsLoopback() || parsedRemote.IsPrivate()) {
		if parsed := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); parsed != nil {
			return parsed.String()
		}
		forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for index := len(forwarded) - 1; index >= 0; index-- {
			if parsed := net.ParseIP(strings.TrimSpace(forwarded[index])); parsed != nil {
				return parsed.String()
			}
		}
	}
	if parsedRemote == nil {
		return ""
	}
	return parsedRemote.String()
}

func publicIP(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsMulticast()
}
