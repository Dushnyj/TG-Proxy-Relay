package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen    string         `json:"listen"`
	PublicURL string         `json:"publicUrl"`
	Tokens    []Token        `json:"tokens"`
	Telegram  TelegramConfig `json:"telegram"`
}

type Token struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type TelegramConfig struct {
	ConnectTimeoutMs int            `json:"connectTimeoutMs"`
	IdleTimeoutSec   int            `json:"idleTimeoutSec"`
	DCMap            map[int]string `json:"dcMap"`
}

func Default() Config {
	return Config{
		Listen: "127.0.0.1:18080",
		Telegram: TelegramConfig{
			ConnectTimeoutMs: 7000,
			IdleTimeoutSec:   125,
			DCMap: map[int]string{
				1: "149.154.175.50",
				2: "149.154.167.51",
				3: "149.154.175.100",
				4: "149.154.167.91",
				5: "149.154.171.5",
				203: "91.105.192.100",
			},
		},
	}
}

func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	c.applyDefaults()
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen is required")
	}
	if len(c.Tokens) == 0 {
		return errors.New("at least one token is required")
	}
	for _, token := range c.Tokens {
		if strings.TrimSpace(token.Hash) == "" {
			return errors.New("token hash is required")
		}
	}
	return nil
}

func (c *Config) AuthorizeBearer(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return c.AuthorizeToken(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
}

func (c *Config) AuthorizeToken(rawToken string) bool {
	if strings.TrimSpace(rawToken) == "" {
		return false
	}
	hash := TokenHash(rawToken)
	for _, token := range c.Tokens {
		stored := normalizeHash(token.Hash)
		if stored == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hash), []byte(stored)) == 1 {
			return true
		}
	}
	return false
}

func (t TelegramConfig) AddressForDC(dc int) string {
	host := ""
	if t.DCMap != nil {
		host = strings.TrimSpace(t.DCMap[dc])
	}
	if host == "" {
		host = strings.TrimSpace(Default().Telegram.DCMap[dc])
	}
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "443")
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = Default().Listen
	}
	if c.Telegram.ConnectTimeoutMs <= 0 {
		c.Telegram.ConnectTimeoutMs = Default().Telegram.ConnectTimeoutMs
	}
	if c.Telegram.IdleTimeoutSec <= 0 {
		c.Telegram.IdleTimeoutSec = Default().Telegram.IdleTimeoutSec
	}
	if len(c.Telegram.DCMap) == 0 {
		c.Telegram.DCMap = Default().Telegram.DCMap
	}
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeHash(value string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if normalized == "" {
		return ""
	}
	if strings.HasPrefix(normalized, "sha256:") {
		return normalized
	}
	if len(normalized) == sha256.Size*2 {
		if _, err := hex.DecodeString(normalized); err == nil {
			return "sha256:" + normalized
		}
	}
	return normalized
}

func ParsePort(value string, fallback int) int {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
