package topology

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

const Schema = 1

type Payload struct {
	Schema     int                               `json:"schema"`
	Generation uint64                            `json:"generation"`
	NotBefore  string                            `json:"notBefore"`
	ExpiresAt  string                            `json:"expiresAt"`
	Production map[int][]config.TelegramEndpoint `json:"production"`
	Test       map[int][]config.TelegramEndpoint `json:"test,omitempty"`
}

type Bundle struct {
	Schema     int                               `json:"schema"`
	Generation uint64                            `json:"generation"`
	NotBefore  string                            `json:"notBefore"`
	ExpiresAt  string                            `json:"expiresAt"`
	Production map[int][]config.TelegramEndpoint `json:"production"`
	Test       map[int][]config.TelegramEndpoint `json:"test,omitempty"`
	Signature  string                            `json:"signature"`
}

type Snapshot struct {
	Generation uint64
	NotBefore  time.Time
	ExpiresAt  time.Time
	Production map[int][]config.TelegramEndpoint
	Test       map[int][]config.TelegramEndpoint
	Raw        []byte
}

func Verify(data []byte, publicKey ed25519.PublicKey, now time.Time,
	allowExpired bool) (Snapshot, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return Snapshot{}, errors.New("topology bundle size is invalid")
	}
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Snapshot{}, fmt.Errorf("parse topology bundle: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Snapshot{}, errors.New("topology bundle must contain exactly one object")
	}
	payload := Payload{Schema: bundle.Schema, Generation: bundle.Generation,
		NotBefore: bundle.NotBefore, ExpiresAt: bundle.ExpiresAt,
		Production: bundle.Production, Test: bundle.Test}
	if err := validatePayload(payload, now, allowExpired); err != nil {
		return Snapshot{}, err
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return Snapshot{}, err
	}
	signature, err := decodeBase64(bundle.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, canonical, signature) {
		return Snapshot{}, errors.New("topology bundle signature is invalid")
	}
	notBefore, _ := time.Parse(time.RFC3339, payload.NotBefore)
	expiresAt, _ := time.Parse(time.RFC3339, payload.ExpiresAt)
	return Snapshot{Generation: payload.Generation, NotBefore: notBefore, ExpiresAt: expiresAt,
		Production: cloneRoutes(payload.Production), Test: cloneRoutes(payload.Test),
		Raw: append([]byte(nil), data...)}, nil
}

func Sign(payload Payload, privateKey ed25519.PrivateKey, now time.Time) ([]byte, error) {
	if err := validatePayload(payload, now, false); err != nil {
		return nil, err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key")
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	bundle := Bundle{Schema: payload.Schema, Generation: payload.Generation,
		NotBefore: payload.NotBefore, ExpiresAt: payload.ExpiresAt,
		Production: payload.Production, Test: payload.Test,
		Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))}
	return json.MarshalIndent(bundle, "", "  ")
}

func validatePayload(payload Payload, now time.Time, allowExpired bool) error {
	if payload.Schema != Schema || payload.Generation == 0 {
		return errors.New("topology bundle schema or generation is invalid")
	}
	notBefore, err := time.Parse(time.RFC3339, payload.NotBefore)
	if err != nil {
		return errors.New("topology notBefore is invalid")
	}
	expiresAt, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil || !expiresAt.After(notBefore) {
		return errors.New("topology expiresAt is invalid")
	}
	if notBefore.After(now.Add(5 * time.Minute)) {
		return errors.New("topology bundle is not active yet")
	}
	if !allowExpired && !expiresAt.After(now) {
		return errors.New("topology bundle is expired")
	}
	if expiresAt.Sub(notBefore) > 90*24*time.Hour {
		return errors.New("topology validity interval is too long")
	}
	if len(payload.Production) == 0 {
		return errors.New("topology production routes are empty")
	}
	if err := config.ValidateTelegramDCOptions(payload.Production); err != nil {
		return err
	}
	if err := config.ValidateTelegramDCOptions(payload.Test); err != nil {
		return err
	}
	return nil
}

func decodeBase64(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

func cloneRoutes(source map[int][]config.TelegramEndpoint) map[int][]config.TelegramEndpoint {
	result := make(map[int][]config.TelegramEndpoint, len(source))
	for dc, endpoints := range source {
		result[dc] = append([]config.TelegramEndpoint(nil), endpoints...)
	}
	return result
}
