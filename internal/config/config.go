package config

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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

const (
	maxConfigBytes       = 1 << 20
	maxTelegramDCs       = 32
	maxEndpointsPerDC    = 32
	maxTelegramEndpoints = maxTelegramDCs * maxEndpointsPerDC
)

type Config struct {
	InstanceID string          `json:"instanceId,omitempty"`
	Listen     string          `json:"listen"`
	PublicURL  string          `json:"publicUrl"`
	Tokens     []Token         `json:"tokens"`
	Admin      AdminConfig     `json:"admin"`
	Telegram   TelegramConfig  `json:"telegram"`
	WebSocket  WebSocketConfig `json:"websocket"`
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
	ConnectTimeoutMs int                        `json:"connectTimeoutMs"`
	IdleTimeoutSec   int                        `json:"idleTimeoutSec"`
	DCMap            map[int]string             `json:"dcMap"`
	TestDCMap        map[int]string             `json:"testDcMap"`
	DCOptions        map[int][]TelegramEndpoint `json:"dcOptions,omitempty"`
	TestDCOptions    map[int][]TelegramEndpoint `json:"testDcOptions,omitempty"`
	Topology         TopologyConfig             `json:"topology,omitempty"`
}

type TopologyConfig struct {
	URL                string `json:"url,omitempty"`
	PublicKey          string `json:"publicKey,omitempty"`
	StatePath          string `json:"statePath,omitempty"`
	RefreshIntervalSec int    `json:"refreshIntervalSec,omitempty"`
}

// TelegramEndpoint is one owner-controlled, allow-listed Telegram client access point.
// Multiple entries for the same DC preserve ports, address families and media/CDN roles.
type TelegramEndpoint struct {
	Address      string `json:"address"`
	Role         string `json:"role,omitempty"` // regular, media or cdn
	Static       bool   `json:"static,omitempty"`
	ThisPortOnly bool   `json:"thisPortOnly,omitempty"`
	TCPOOnly     bool   `json:"tcpoOnly,omitempty"`
	Secret       string `json:"secret,omitempty"`
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
	// encoding/json merges object members into an already non-nil map. Without clearing a
	// map that is explicitly present in the file, an owner-supplied partial dcMap silently
	// inherited unrelated embedded routes and advertised capabilities it did not configure.
	resetExplicitTelegramMaps(data, &cfg)
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

func resetExplicitTelegramMaps(data []byte, cfg *Config) {
	if cfg == nil {
		return
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return
	}
	var telegram map[string]json.RawMessage
	if raw, ok := root["telegram"]; !ok || json.Unmarshal(raw, &telegram) != nil {
		return
	}
	if _, ok := telegram["dcMap"]; ok {
		cfg.Telegram.DCMap = nil
	}
	if _, ok := telegram["testDcMap"]; ok {
		cfg.Telegram.TestDCMap = nil
	}
	if _, ok := telegram["dcOptions"]; ok {
		cfg.Telegram.DCOptions = nil
	}
	if _, ok := telegram["testDcOptions"]; ok {
		cfg.Telegram.TestDCOptions = nil
	}
}

func (c *Config) Validate() error {
	if c.Telegram.ConnectTimeoutMs < 0 || c.Telegram.IdleTimeoutSec < 0 ||
		c.WebSocket.PingIntervalSec < 0 || c.WebSocket.PongTimeoutSec < 0 ||
		c.WebSocket.WriteTimeoutSec < 0 || c.WebSocket.MaxMessageBytes < 0 {
		return errors.New("timeouts and size limits must not be negative")
	}
	c.applyDefaults()
	if strings.TrimSpace(c.InstanceID) != "" && !ValidInstanceID(c.InstanceID) {
		return errors.New("instanceId must use ri_ followed by 32 to 64 lowercase hexadecimal characters")
	}
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
	if err := ValidateTelegramDCOptions(c.Telegram.DCOptions); err != nil {
		return err
	}
	if err := ValidateTelegramDCOptions(c.Telegram.TestDCOptions); err != nil {
		return err
	}
	if len(c.Telegram.DCMap) == 0 && len(c.Telegram.DCOptions) == 0 {
		return errors.New("telegram requires at least one production bootstrap route")
	}
	if err := validateTopology(c.Telegram.Topology); err != nil {
		return err
	}
	if !validWebSocketPath(c.WebSocket.Path) {
		return errors.New("websocket path must be a canonical absolute path without escapes, query, or fragment")
	}
	if c.WebSocket.Path == "/healthz" || c.WebSocket.Path == "/version" ||
		c.WebSocket.Path == "/identity" ||
		c.WebSocket.Path == "/capabilities" || c.WebSocket.Path == "/test-routes" || c.WebSocket.Path == "/connect" ||
		c.WebSocket.Path == "/admin" || strings.HasPrefix(c.WebSocket.Path, "/admin/") ||
		c.WebSocket.Path == "/apiws/connect" || c.WebSocket.Path == "/apiws/identity" ||
		c.WebSocket.Path == "/apiws/admin" ||
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

// ValidInstanceID validates the persistent, non-secret identity assigned to one Relay
// installation. The value deliberately does not encode a public endpoint: domains, IPs and
// TLS certificates may change while the Relay instance remains the same.
func ValidInstanceID(value string) bool {
	clean := strings.TrimSpace(value)
	if !strings.HasPrefix(clean, "ri_") {
		return false
	}
	hexPart := strings.TrimPrefix(clean, "ri_")
	if len(hexPart) < 32 || len(hexPart) > 64 {
		return false
	}
	for _, ch := range hexPart {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
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
	return normalizeTelegramAddress(host)
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
	// A non-nil empty map is an explicit owner decision. Restoring embedded routes here
	// would reintroduce destinations the owner deliberately removed (for example when
	// migrating completely to dcOptions). Omitted/null maps remain nil after decoding.
	if c.Telegram.DCMap == nil {
		c.Telegram.DCMap = Default().Telegram.DCMap
	}
	if c.Telegram.TestDCMap == nil {
		c.Telegram.TestDCMap = Default().Telegram.TestDCMap
	}
	if strings.TrimSpace(c.Telegram.Topology.URL) != "" {
		if strings.TrimSpace(c.Telegram.Topology.StatePath) == "" {
			c.Telegram.Topology.StatePath = "/var/lib/tgproxy-relay/topology.json"
		}
		if c.Telegram.Topology.RefreshIntervalSec <= 0 {
			c.Telegram.Topology.RefreshIntervalSec = 900
		}
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
	addresses := t.AddressesForRoute(dc, test, false)
	if len(addresses) == 0 {
		return ""
	}
	return addresses[0]
}

func (t TelegramConfig) AddressesForRoute(dc int, test bool, media bool) []string {
	options := t.DCOptions[dc]
	legacy := t.DCMap[dc]
	if test {
		options = t.TestDCOptions[dc]
		legacy = t.TestDCMap[dc]
	}
	result := make([]string, 0, len(options)+1)
	appendRole := func(role string) {
		for _, endpoint := range options {
			endpointRole := strings.ToLower(strings.TrimSpace(endpoint.Role))
			if endpointRole == "" {
				endpointRole = "regular"
			}
			if endpointRole != role {
				continue
			}
			address := normalizeTelegramAddress(endpoint.Address)
			if address != "" && !containsStringValue(result, address) {
				result = append(result, address)
			}
		}
	}
	if media {
		appendRole("media")
		appendRole("cdn")
		appendRole("regular")
	} else {
		appendRole("regular")
		appendRole("cdn")
	}
	if len(result) == 0 {
		if address := normalizeTelegramAddress(legacy); address != "" {
			result = append(result, address)
		}
	}
	return result
}

func validateDCMap(_ TelegramConfig, routes map[int]string, _ bool) error {
	if len(routes) > maxTelegramDCs {
		return errors.New("telegram dc map contains too many routes")
	}
	for dc, host := range routes {
		if dc <= 0 || dc > 32767 || strings.TrimSpace(host) == "" {
			return errors.New("telegram dc map contains an invalid route")
		}
		address := normalizeTelegramAddress(host)
		if err := validateTelegramAddress(address); err != nil {
			return errors.New("telegram dc map contains an invalid address")
		}
	}
	return nil
}

func ValidateTelegramDCOptions(routes map[int][]TelegramEndpoint) error {
	if len(routes) > maxTelegramDCs {
		return errors.New("telegram dc options contain too many routes")
	}
	total := 0
	for dc, endpoints := range routes {
		if dc <= 0 || dc > 32767 || len(endpoints) == 0 ||
			len(endpoints) > maxEndpointsPerDC {
			return errors.New("telegram dc options contain an invalid route")
		}
		total += len(endpoints)
		seen := make(map[string]struct{}, len(endpoints))
		for _, endpoint := range endpoints {
			role := strings.ToLower(strings.TrimSpace(endpoint.Role))
			if role != "" && role != "regular" && role != "media" && role != "cdn" {
				return errors.New("telegram dc endpoint contains an invalid role")
			}
			if endpoint.Secret != "" {
				return errors.New("telegram dc endpoint secrets are not supported by raw relay")
			}
			if role == "" {
				role = "regular"
			}
			address := normalizeTelegramAddress(endpoint.Address)
			if err := validateTelegramAddress(address); err != nil {
				return errors.New("telegram dc endpoint contains an invalid address")
			}
			key := role + "|" + address
			if _, duplicate := seen[key]; duplicate {
				return errors.New("telegram dc endpoint is duplicated")
			}
			seen[key] = struct{}{}
		}
	}
	if total > maxTelegramEndpoints {
		return errors.New("telegram dc options contain too many endpoints")
	}
	return nil
}

func validateTopology(topology TopologyConfig) error {
	rawURL := strings.TrimSpace(topology.URL)
	if rawURL == "" {
		if strings.TrimSpace(topology.PublicKey) != "" ||
			strings.TrimSpace(topology.StatePath) != "" || topology.RefreshIntervalSec != 0 {
			return errors.New("telegram topology url is required when topology settings are present")
		}
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || !validHost(parsed.Hostname()) {
		return errors.New("telegram topology url must be an absolute https URL")
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(topology.PublicKey))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(topology.PublicKey))
	}
	if err != nil || len(key) != 32 {
		return errors.New("telegram topology publicKey must be a base64 Ed25519 public key")
	}
	if !validStatePath(topology.StatePath) {
		return errors.New("telegram topology statePath must be an absolute safe path")
	}
	if topology.RefreshIntervalSec < 300 || topology.RefreshIntervalSec > 86_400 {
		return errors.New("telegram topology refreshIntervalSec must be between 300 and 86400")
	}
	return nil
}

func normalizeTelegramAddress(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	if ip := net.ParseIP(strings.Trim(raw, "[]")); ip != nil {
		return net.JoinHostPort(ip.String(), "443")
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if host == "" || port == "" {
			return ""
		}
		return net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	if strings.Contains(raw, ":") {
		return ""
	}
	return net.JoinHostPort(raw, "443")
}

func validateTelegramAddress(address string) error {
	host, rawPort, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return errors.New("invalid host")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if !IsSafeTelegramIP(ip) {
		return errors.New("Telegram endpoint must be a public IP literal")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port <= 0 || port > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

// IsSafeTelegramIP is the common SSRF boundary for owner config, signed manifests and
// topology-source DNS. Telegram dc_options carry IP literals, so accepting hostnames here
// would add DNS-rebinding risk without adding a Telegram capability.
func IsSafeTelegramIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		for _, cidr := range []string{
			"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
			"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		} {
			_, block, _ := net.ParseCIDR(cidr)
			if block.Contains(v4) {
				return false
			}
		}
		return true
	}
	for _, cidr := range []string{"100::/64", "2001:db8::/32"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func containsStringValue(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
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
