package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

func TestLoadFileNormalizesConfigAndAuthorizesHashedTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	tokenHash := config.TokenHash("phone-token")
	raw := `{
  "listen": "127.0.0.1:18080",
  "publicUrl": "https://relay.example.com/apiws",
  "tokens": [{"name": "phone", "hash": "` + tokenHash + `"}],
  "telegram": {
    "connectTimeoutMs": 1200,
    "idleTimeoutSec": 45,
    "dcMap": {"2": "149.154.167.220"}
  }
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if cfg.Listen != "127.0.0.1:18080" {
		t.Fatalf("listen mismatch: %q", cfg.Listen)
	}
	if cfg.Telegram.ConnectTimeoutMs != 1200 {
		t.Fatalf("connect timeout mismatch: %d", cfg.Telegram.ConnectTimeoutMs)
	}
	if got := cfg.Telegram.AddressForDC(2); got != "149.154.167.220:443" {
		t.Fatalf("dc address mismatch: %q", got)
	}
	if !cfg.AuthorizeBearer("Bearer phone-token") {
		t.Fatal("expected bearer token to be authorized")
	}
	if cfg.AuthorizeBearer("Bearer wrong-token") {
		t.Fatal("wrong token was authorized")
	}
}

func TestDefaultIncludesWebSocketPathAndTestDCs(t *testing.T) {
	cfg := config.Default()
	if cfg.WebSocket.Path != "/apiws" {
		t.Fatalf("default websocket path = %q", cfg.WebSocket.Path)
	}
	if got := cfg.Telegram.AddressForRoute(2, true); got != "149.154.167.40:443" {
		t.Fatalf("test DC2 address = %q", got)
	}
}

func TestValidateRejectsUnsafeOrReservedWebSocketPath(t *testing.T) {
	cfg := config.Default()
	cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("secret")}}
	for _, path := range []string{"relative", "/apiws?x=1", "/healthz", "/bad path"} {
		cfg.WebSocket.Path = path
		if err := cfg.Validate(); err == nil {
			t.Fatalf("unsafe websocket path %q accepted", path)
		}
	}
}

func TestValidateRejectsMissingTokens(t *testing.T) {
	cfg := config.Default()
	cfg.Tokens = nil

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing tokens to be rejected")
	}
}

func TestValidateRejectsRawTokenAndMalformedListenAddress(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = "not-a-listen-address"
	cfg.Tokens = []config.Token{{Name: "phone", Hash: "raw-secret"}}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected malformed listen address to be rejected")
	}

	cfg.Listen = "127.0.0.1:18080"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected raw token in hash field to be rejected")
	}

	cfg.Tokens[0].Hash = config.TokenHash("phone-token")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid token hash was rejected: %v", err)
	}
}

func TestDefaultTelegramDCMapUsesRawMtProtoAddresses(t *testing.T) {
	cfg := config.Default()

	if got := cfg.Telegram.AddressForDC(2); got != "149.154.167.51:443" {
		t.Fatalf("dc2 address mismatch: %q", got)
	}
	if got := cfg.Telegram.AddressForDC(4); got != "149.154.167.91:443" {
		t.Fatalf("dc4 address mismatch: %q", got)
	}
	if got := cfg.Telegram.AddressForDC(203); got != "91.105.192.100:443" {
		t.Fatalf("dc203 address mismatch: %q", got)
	}
}

func TestDefaultWebSocketLivenessDoesNotUseApplicationIdleAsTransportTimeout(t *testing.T) {
	cfg := config.Default()

	if cfg.Telegram.IdleTimeoutSec != 0 {
		t.Fatalf("default application idle timeout = %d", cfg.Telegram.IdleTimeoutSec)
	}
	if cfg.WebSocket.PingIntervalSec != 25 || cfg.WebSocket.PongTimeoutSec != 12 {
		t.Fatalf("unexpected heartbeat defaults: %+v", cfg.WebSocket)
	}
	if cfg.WebSocket.MaxMessageBytes != 16*1024*1024 {
		t.Fatalf("unexpected max message size: %d", cfg.WebSocket.MaxMessageBytes)
	}
}

func TestLoadFileRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	tokenHash := config.TokenHash("phone-token")
	base := `{"listen":"127.0.0.1:18080","tokens":[{"hash":"` + tokenHash + `"}]`

	if err := os.WriteFile(path, []byte(base+`,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFile(path); err == nil {
		t.Fatal("unknown config field was accepted")
	}

	if err := os.WriteFile(path, []byte(base+`} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFile(path); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}

func TestValidateRejectsUnsafeBoundsAndPublicURL(t *testing.T) {
	valid := func() config.Config {
		cfg := config.Default()
		cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("phone-token")}}
		return cfg
	}

	cfg := valid()
	cfg.Listen = "127.0.0.1:0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero listen port was accepted")
	}

	cfg = valid()
	cfg.PublicURL = "file:///etc/passwd"
	if err := cfg.Validate(); err == nil {
		t.Fatal("non-HTTP publicUrl was accepted")
	}

	cfg = valid()
	cfg.PublicURL = "https://user:pass@relay.example.com/apiws"
	if err := cfg.Validate(); err == nil {
		t.Fatal("publicUrl credentials were accepted")
	}

	cfg = valid()
	cfg.PublicURL = "https://relay.example.com/apiws?token=secret"
	if err := cfg.Validate(); err == nil {
		t.Fatal("publicUrl query was accepted")
	}

	cfg = valid()
	cfg.Telegram.IdleTimeoutSec = 86_401
	if err := cfg.Validate(); err == nil {
		t.Fatal("oversized idle timeout was accepted")
	}

	cfg = valid()
	cfg.WebSocket.PingIntervalSec = 3601
	if err := cfg.Validate(); err == nil {
		t.Fatal("oversized websocket timeout was accepted")
	}

	cfg = valid()
	cfg.Telegram.IdleTimeoutSec = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative idle timeout was accepted")
	}

	cfg = valid()
	cfg.Telegram.DCMap = map[int]string{2: "bad/host"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid Telegram host was accepted")
	}

	cfg = valid()
	cfg.Tokens = append(cfg.Tokens, cfg.Tokens[0])
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate token hash was accepted")
	}
}

func TestLoadFileRejectsOversizedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := make([]byte, 1<<20+1)
	for i := range data {
		data[i] = ' '
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFile(path); err == nil {
		t.Fatal("oversized config was accepted")
	}
}
