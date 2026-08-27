package relay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

func TestClientRemoteIPTrustsOnlyLocalOrPrivateProxyHop(t *testing.T) {
	privateProxy := httptest.NewRequest("GET", "http://relay/apiws", nil)
	privateProxy.RemoteAddr = "172.18.0.2:43000"
	privateProxy.Header.Set("X-Real-IP", "203.0.113.44")
	privateProxy.Header.Set("X-Forwarded-For", "198.51.100.9, 203.0.113.44")
	if got := clientRemoteIP(privateProxy); got != "203.0.113.44" {
		t.Fatalf("private reverse proxy address = %q", got)
	}

	publicClient := httptest.NewRequest("GET", "http://relay/apiws", nil)
	publicClient.RemoteAddr = "198.51.100.22:43000"
	publicClient.Header.Set("X-Real-IP", "203.0.113.99")
	if got := clientRemoteIP(publicClient); got != "198.51.100.22" {
		t.Fatalf("public client spoofed forwarding address: %q", got)
	}
}

func TestControlPlaneShutdownClosesSessionsAndFlushesClientState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	token := config.Token{ID: "primary", Name: "primary", Hash: config.TokenHash("client")}
	cfg := config.Default()
	cfg.Tokens = []config.Token{token}
	cfg.Admin.Tokens = []config.Token{{ID: "owner", Name: "owner", Hash: config.TokenHash("admin")}}
	cfg.Admin.StatePath = statePath
	cfg.Admin.GeoIPURL = ""
	control, err := newControlPlane(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://relay/apiws", nil)
	request.RemoteAddr = "192.0.2.8:43000"
	request.Header.Set("X-TGProxy-Device-ID", "shutdown_device")
	var closed atomic.Bool
	var unregister func()
	unregister, accepted := control.registerSession(token, request, func() {
		closed.Store(true)
		unregister()
	})
	if !accepted {
		t.Fatal("session registration was rejected")
	}

	if err := control.shutdown(); err != nil {
		t.Fatal(err)
	}
	if !closed.Load() {
		t.Fatal("shutdown did not close a hijacked session")
	}
	if len(control.active) != 0 {
		t.Fatalf("active sessions remained after shutdown: %d", len(control.active))
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(state, []byte(`"deviceId": "shutdown_device"`)) {
		t.Fatalf("shutdown did not durably flush client state: %s", state)
	}
	_, accepted = control.registerSession(token, request, func() {})
	if accepted {
		t.Fatal("control plane accepted a new session after shutdown")
	}
}

func TestSessionRegistrationCannotRacePastTokenDeletion(t *testing.T) {
	cfg := config.Default()
	cfg.Tokens = []config.Token{{ID: "primary", Name: "primary", Hash: config.TokenHash("client")}}
	cfg.Admin.Tokens = []config.Token{{ID: "owner", Name: "owner", Hash: config.TokenHash("admin")}}
	cfg.Admin.StatePath = filepath.Join(t.TempDir(), "state.json")
	cfg.Admin.GeoIPURL = ""
	control, err := newControlPlane(cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := control.createToken("race")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.deleteToken(token.ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://relay/apiws", nil)
	unregister, accepted := control.registerSession(token, request, func() {})
	unregister()
	if accepted {
		t.Fatal("deleted token registered a session after revocation")
	}
	if len(control.state.Clients) != 0 || len(control.active) != 0 {
		t.Fatalf("rejected registration changed session state: clients=%d active=%d",
			len(control.state.Clients), len(control.active))
	}
}
