package topology

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

func TestSignedBundlePreservesFutureDCMediaIPv6AndNonDefaultPort(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	payload := Payload{
		Schema: Schema, Generation: 42,
		NotBefore: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Production: map[int][]config.TelegramEndpoint{
			204: {
				{Address: "8.8.8.8:8443", Role: "regular", ThisPortOnly: true},
				{Address: "[2606:4700:4700::1111]:9443", Role: "media", Static: true},
			},
		},
	}
	bundle, err := Sign(payload, privateKey, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Verify(bundle, publicKey, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != 42 || len(snapshot.Production[204]) != 2 ||
		snapshot.Production[204][1].Role != "media" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	tampered := bytes.Replace(bundle, []byte("8443"), []byte("8444"), 1)
	if _, err := Verify(tampered, publicKey, now, false); err == nil {
		t.Fatal("tampered topology was accepted")
	}
}

func TestExpiredBundleIsOnlyAcceptedAsLastKnownGood(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	payload := Payload{Schema: Schema, Generation: 7,
		NotBefore: now.Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339),
		Production: map[int][]config.TelegramEndpoint{
			2: {{Address: "149.154.167.51:443", Role: "regular"}},
		}}
	// Sign validates freshness, so sign at a point when the payload was active.
	bundle, err := Sign(payload, privateKey, now.Add(-90*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(bundle, publicKey, now, false); err == nil {
		t.Fatal("expired remote update was accepted")
	}
	if _, err := Verify(bundle, publicKey, now, true); err != nil {
		t.Fatalf("signed last-known-good was not available during outage: %v", err)
	}
}
