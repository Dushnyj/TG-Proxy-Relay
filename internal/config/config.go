package config

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxConfigBytes = 1 << 20

type Config struct {
	Listen    string          `json:"listen"`
	PublicURL string          `json:"publicUrl"`
	Tokens    []Token         `json:"tokens"`
	Admin     AdminConfig     `json:"admin"`
	Telegram  TelegramConfig  `json:"telegram"`
	WebSocket WebSocketConfig `json:"websocket"`
}

type Token struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Hash      string `json:"hash"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type AdminConfig struct {
	Tokens    []Token `json:"tokens,omitempty"`
	StatePath string  `json:"statePath,omitempty"`
	GeoIPURL  string  `json:"geoIpUrl,omitempty"`
}

type TelegramConfig struct {
	ConnectTimeoutMs int            `json:"connectTimeoutMs"`
	IdleTimeoutSec   int            `json:"idleTimeoutSec"`
	DCMap            map[int]string `json:"dcMap"`
	TestDCMap        map[int]string `json:"testDcMap"`
}

func Default() Config {
	return Config{
		Listen: "127.0.0.1:18080",
		Admin: AdminConfig{
			StatePath: "/var/lib/tgproxy-relay/state.json",
			GeoIPURL:  "https://ipwho.is/%s?lang=ru&fields=success,country,city",
		},
		Telegram: TelegramConfig{
			ConnectTimeoutMs: 7000,
			IdleTimeoutSec:   0,
			DCMap: map[int]string{
				1:   "149.154.175.50",
				2:   "149.154.167.51",
				3:   "149.154.175.100",
				4:   "149.154.167.91",
				5:   "149.154.171.5",
				203: "91.105.192.100",
			},
			TestDCMap: map[int]string{
				1: "149.154.175.10",
				2: "149.154.167.40",
				3: "149.154.175.117",
			},
		},
		WebSocket: WebSocketConfig{
			Path:            "/apiws",
			PingIntervalSec: 25,
			PongTimeoutSec:  12,
			WriteTimeoutSec: 15,
			MaxMessageBytes: 16 * 1024 * 1024,
		},
	}
}

type WebSocketConfig struct {
	Path            string `json:"path"`
	PingIntervalSec int    `json:"pingIntervalSec"`
	PongTimeoutSec  int    `json:"pongTimeoutSec"`
	WriteTimeoutSec int    `json:"writeTimeoutSec"`
	MaxMessageBytes int    `json:"maxMessageBytes"`
}

func LoadFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > maxConfigBytes {
		return Config{}, errors.New("config must not exceed 1048576 bytes")
	}
	cfg := Default()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, errors.New("config must contain exactly one JSON object")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Telegram.ConnectTimeoutMs < 0 || c.Telegram.IdleTimeoutSec < 0 ||
		c.WebSocket.PingIntervalSec < 0 || c.WebSocket.PongTimeoutSec < 0 ||
		c.WebSocket.WriteTimeoutSec < 0 || c.WebSocket.MaxMessageBytes < 0 {
		return errors.New("timeouts and size limits must not be negative")
	}
	c.applyDefaults()
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen is required")
	}
	listenHost, listenPort, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen must be host:port")
	}
	if listenHost != "" && !validHost(listenHost) {
		return errors.New("listen contains an invalid host")
	}
	port, err := strconv.Atoi(listenPort)
	if err != nil || port <= 0 || port > 65535 {
		return errors.New("listen port must be between 1 and 65535")
	}
	if strings.TrimSpace(c.PublicURL) != "" {
		publicURL, err := url.Parse(c.PublicURL)
		if err != nil || (publicURL.Scheme != "http" && publicURL.Scheme != "https") ||
			publicURL.Host == "" || !validHost(publicURL.Hostname()) ||
			publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" {
			return errors.New("publicUrl must be an absolute http or https URL")
		}
	}
	if len(c.Tokens) == 0 {
		return errors.New("at least one token is required")
	}
	if err := validateTokens(c.Tokens, "token", 1024); err != nil {
		return err
	}
	if err := validateTokens(c.Admin.Tokens, "admin token", 64); err != nil {
		return err
	}
	clientHashes := make(map[string]struct{}, len(c.Tokens))
	for _, token := range c.Tokens {
		clientHashes[normalizeHash(token.Hash)] = struct{}{}
	}
	for _, token := range c.Admin.Tokens {
		if _, reused := clientHashes[normalizeHash(token.Hash)]; reused {
			return errors.New("admin and client tokens must be different")
		}
	}
	if len(c.Admin.Tokens) > 0 {
		if !validStatePath(c.Admin.StatePath) {
			return errors.New("admin statePath must be an absolute safe path")
		}
		if !validGeoIPURL(c.Admin.GeoIPURL) {
			return errors.New("admin geoIpUrl must be empty or an https URL containing one %s placeholder")
		}
	}
	if c.Telegram.ConnectTimeoutMs > 60_000 {
		return errors.New("telegram connectTimeoutMs must not exceed 60000")
	}
	if c.Telegram.IdleTimeoutSec > 86_400 {
		return errors.New("telegram idleTimeoutSec must not exceed 86400")
	}
	if err := validateDCMap(c.Telegram, c.Telegram.DCMap, false); err != nil {
		return err
	}
	if err := validateDCMap(c.Telegram, c.Telegram.TestDCMap, true); err != nil {
		return err
	}
	if !validWebSocketPath(c.WebSocket.Path) {
		return errors.New("websocket path must be a canonical absolute path without escapes, query, or fragment")
	}
	if c.WebSocket.Path == "/healthz" || c.WebSocket.Path == "/version" ||
		c.WebSocket.Path == "/test-routes" || c.WebSocket.Path == "/connect" ||
		c.WebSocket.Path == "/admin" || strings.HasPrefix(c.WebSocket.Path, "/admin/") ||
		c.WebSocket.Path == "/apiws/connect" || c.WebSocket.Path == "/apiws/admin" ||
		strings.HasPrefix(c.WebSocket.Path, "/apiws/admin/") {
		return errors.New("websocket path conflicts with a management endpoint")
	}
	if c.WebSocket.PingIntervalSec <= 0 || c.WebSocket.PongTimeoutSec <= 0 {
		return errors.New("websocket ping and pong timeouts must be positive")
	}
	if c.WebSocket.PingIntervalSec > 3600 || c.WebSocket.PongTimeoutSec > 3600 {
		return errors.New("websocket ping and pong timeouts must not exceed 3600")
	}
	if c.WebSocket.WriteTimeoutSec <= 0 {
		return errors.New("websocket write timeout must be positive")
	}
	if c.WebSocket.WriteTimeoutSec > 3600 {
		return errors.New("websocket write timeout must not exceed 3600")
	}
	if c.WebSocket.MaxMessageBytes < 64*1024 || c.WebSocket.MaxMessageBytes > 64*1024*1024 {
		return errors.New("websocket maxMessageBytes must be between 65536 and 67108864")
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

func (c *Config) AuthorizeAdminBearer(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return authorizeToken(c.Admin.Tokens, strings.TrimSpace(strings.TrimPrefix(header, prefix)))
}

func (c *Config) AuthorizeToken(rawToken string) bool {
	return authorizeToken(c.Tokens, rawToken)
}

func authorizeToken(tokens []Token, rawToken string) bool {
	if strings.TrimSpace(rawToken) == "" {
		return false
	}
	hash := TokenHash(rawToken)
	for _, token := range tokens {
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
	if c.Telegram.IdleTimeoutSec < 0 {
		c.Telegram.IdleTimeoutSec = 0
	}
	if len(c.Telegram.DCMap) == 0 {
		c.Telegram.DCMap = Default().Telegram.DCMap
	}
	if len(c.Telegram.TestDCMap) == 0 {
		c.Telegram.TestDCMap = Default().Telegram.TestDCMap
	}
	if strings.TrimSpace(c.Admin.StatePath) == "" {
		c.Admin.StatePath = Default().Admin.StatePath
	}
	if strings.TrimSpace(c.WebSocket.Path) == "" {
		c.WebSocket.Path = Default().WebSocket.Path
	}
	if c.WebSocket.PingIntervalSec <= 0 {
		c.WebSocket.PingIntervalSec = Default().WebSocket.PingIntervalSec
	}
	if c.WebSocket.PongTimeoutSec <= 0 {
		c.WebSocket.PongTimeoutSec = Default().WebSocket.PongTimeoutSec
	}
	if c.WebSocket.WriteTimeoutSec <= 0 {
		c.WebSocket.WriteTimeoutSec = Default().WebSocket.WriteTimeoutSec
	}
	if c.WebSocket.MaxMessageBytes <= 0 {
		c.WebSocket.MaxMessageBytes = Default().WebSocket.MaxMessageBytes
	}
}

func (t TelegramConfig) AddressForRoute(dc int, test bool) string {
	if !test {
		return t.AddressForDC(dc)
	}
	host := ""
	if t.TestDCMap != nil {
		host = strings.TrimSpace(t.TestDCMap[dc])
	}
	if host == "" {
		host = strings.TrimSpace(Default().Telegram.TestDCMap[dc])
	}
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "443")
}

func validateDCMap(telegram TelegramConfig, routes map[int]string, test bool) error {
	if len(routes) > 256 {
		return errors.New("telegram dc map contains too many routes")
	}
	for dc, host := range routes {
		if dc <= 0 || dc > 32767 || strings.TrimSpace(host) == "" {
			return errors.New("telegram dc map contains an invalid route")
		}
		address := telegram.AddressForDC(dc)
		if test {
			address = telegram.AddressForRoute(dc, true)
		}
		addressHost, _, err := net.SplitHostPort(address)
		if err != nil || !validHost(addressHost) {
			return errors.New("telegram dc map contains an invalid address")
		}
	}
	return nil
}

func validWebSocketPath(value string) bool {
	path := strings.TrimSpace(value)
	if path == "" || len(path) > 256 || path[0] != '/' || len(path) == 1 ||
		strings.HasSuffix(path, "/") || strings.Contains(path, "//") ||
		strings.ContainsAny(path, "%?#") {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for index := 0; index < len(segment); index++ {
			ch := segment[index]
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
				(ch >= '0' && ch <= '9') || strings.ContainsRune("._~-", rune(ch)) {
				continue
			}
			return false
		}
	}
	return true
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

func NormalizeTokenHash(value string) string {
	return normalizeHash(value)
}

func StableTokenID(token Token) string {
	if validIdentifier(token.ID) {
		return strings.TrimSpace(token.ID)
	}
	hash := strings.TrimPrefix(normalizeHash(token.Hash), "sha256:")
	if len(hash) >= 16 {
		return "cfg_" + hash[:16]
	}
	return "cfg_unknown"
}

func validateTokens(tokens []Token, label string, max int) error {
	if len(tokens) > max {
		return fmt.Errorf("too many %s entries", label)
	}
	seenHashes := make(map[string]struct{}, len(tokens))
	seenIDs := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if len(token.Name) > 128 || strings.IndexFunc(token.Name, func(r rune) bool {
			return r < 0x20 || r == 0x7f
		}) >= 0 {
			return fmt.Errorf("%s name is invalid", label)
		}
		if token.ID != "" && !validIdentifier(token.ID) {
			return fmt.Errorf("%s id is invalid", label)
		}
		if len(token.CreatedAt) > 64 || strings.IndexFunc(token.CreatedAt, func(r rune) bool {
			return r < 0x20 || r == 0x7f
		}) >= 0 {
			return fmt.Errorf("%s createdAt is invalid", label)
		}
		normalized := normalizeHash(token.Hash)
		hexHash := strings.TrimPrefix(normalized, "sha256:")
		if !strings.HasPrefix(normalized, "sha256:") || len(hexHash) != sha256.Size*2 {
			return fmt.Errorf("%s hash must be sha256:<64 hex chars>", label)
		}
		if _, err := hex.DecodeString(hexHash); err != nil {
			return fmt.Errorf("%s hash must be sha256:<64 hex chars>", label)
		}
		if _, exists := seenHashes[normalized]; exists {
			return fmt.Errorf("duplicate %s hash", label)
		}
		seenHashes[normalized] = struct{}{}
		id := StableTokenID(token)
		if _, exists := seenIDs[id]; exists {
			return fmt.Errorf("duplicate %s id", label)
		}
		seenIDs[id] = struct{}{}
	}
	return nil
}

func validIdentifier(value string) bool {
	id := strings.TrimSpace(value)
	if id == "" || len(id) > 96 {
		return false
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func validStatePath(value string) bool {
	path := strings.TrimSpace(value)
	if path == "" || len(path) > 512 || !filepath.IsAbs(path) || strings.Contains(path, "\x00") {
		return false
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

func validGeoIPURL(value string) bool {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return true
	}
	if strings.Count(raw, "%s") != 1 || len(raw) > 1024 {
		return false
	}
	parsed, err := url.Parse(strings.Replace(raw, "%s", "127.0.0.1", 1))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func ParsePort(value string, fallback int) int {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port <= 0 || port > 65535 {
		return fallback
	}
	return port
}

func validHost(value string) bool {
	host := strings.TrimSuffix(strings.TrimSpace(value), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') &&
				(ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}
	return true
}
