package relay

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

func TestDeadFirstEndpointRacesToWorkingAlternative(t *testing.T) {
	dialer := &failureInjectionDialer{blocked: "1.1.1.1:443"}
	server := &Server{
		dialer:    dialer,
		upstreams: newUpstreamPool(),
		topology: &topologyManager{base: config.TelegramConfig{
			DCOptions: map[int][]config.TelegramEndpoint{204: {
				{Address: "1.1.1.1:443", Role: "regular"},
				{Address: "8.8.8.8:443", Role: "regular"},
			}},
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()

	connection, address, err := server.dialTelegramRoute(ctx, 204, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if address != "8.8.8.8:443" {
		t.Fatalf("selected endpoint = %q", address)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("fallback waited for dead endpoint timeout: %v", elapsed)
	}
	dialer.closePeers()
}

func TestFailedEndpointIsNotRedialedDuringCooldown(t *testing.T) {
	pool := newUpstreamPool()
	now := time.Now()
	addresses := []string{"1.1.1.1:443", "8.8.8.8:443"}
	if got := pool.ordered(addresses, now); len(got) != 2 {
		t.Fatalf("initial endpoints = %v", got)
	}
	pool.failed("1.1.1.1:443")

	got := pool.ordered(addresses, time.Now())
	if len(got) != 1 || got[0] != "8.8.8.8:443" {
		t.Fatalf("cooling endpoint was returned: %v", got)
	}
}

type failureInjectionDialer struct {
	blocked string
	mu      sync.Mutex
	peers   []net.Conn
}

func (d *failureInjectionDialer) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	if address == d.blocked {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	client, peer := net.Pipe()
	d.mu.Lock()
	d.peers = append(d.peers, peer)
	d.mu.Unlock()
	return client, nil
}

func (d *failureInjectionDialer) closePeers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, peer := range d.peers {
		if err := peer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			panic(err)
		}
	}
}
