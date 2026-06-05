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

func TestValidateRejectsMissingTokens(t *testing.T) {
	cfg := config.Default()
	cfg.Tokens = nil

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing tokens to be rejected")
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
