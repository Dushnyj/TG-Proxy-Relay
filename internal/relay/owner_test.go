package relay_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/relay"
)

func TestOwnerAPISeparatesRolesAndPersistsDynamicTokenDeletion(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := newOwnerServer(t, statePath, &fakeDialer{})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	if status := getStatus(t, ts.URL+"/admin/v1/overview", "Bearer client-token"); status != http.StatusUnauthorized {
		t.Fatalf("client credential reached owner API: %d", status)
	}
	if status := getStatus(t, ts.URL+"/healthz", "Bearer owner-token"); status != http.StatusUnauthorized {
		t.Fatalf("owner credential was accepted as client: %d", status)
	}

	created := createOwnerToken(t, ts.URL+"/apiws/admin/v1/tokens", "Семья")
	if !strings.HasPrefix(created.Secret, "tgpr_") || created.Token.ID == "" {
		t.Fatalf("unexpected created token: %+v", created)
	}
	persisted, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte(created.Secret)) {
		t.Fatal("raw client token was persisted in owner state")
	}
	if !bytes.Contains(persisted, []byte(config.TokenHash(created.Secret))) {
		t.Fatal("created token hash was not persisted")
	}
	if status := getStatus(t, ts.URL+"/healthz", "Bearer "+created.Secret); status != http.StatusOK {
		t.Fatalf("new token authorization status = %d", status)
	}

	request, _ := http.NewRequest(http.MethodDelete, ts.URL+"/admin/v1/tokens/"+url.PathEscape(created.Token.ID), nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	if status := getStatus(t, ts.URL+"/healthz", "Bearer "+created.Secret); status != http.StatusUnauthorized {
		t.Fatalf("deleted token authorization status = %d", status)
	}
	persisted, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte(config.TokenHash(created.Secret))) {
		t.Fatal("removed dynamic token hash was retained as an unnecessary revocation")
	}

	restarted := newOwnerServer(t, statePath, &fakeDialer{})
	restartedHTTP := httptest.NewServer(restarted.Handler())
	defer restartedHTTP.Close()
	if status := getStatus(t, restartedHTTP.URL+"/healthz", "Bearer "+created.Secret); status != http.StatusUnauthorized {
		t.Fatalf("revocation did not survive restart: %d", status)
	}
}

func TestOwnerAPIRevokesConfiguredTokenAcrossRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := newOwnerServer(t, statePath, &fakeDialer{})
	ts := httptest.NewServer(server.Handler())

	request, _ := http.NewRequest(http.MethodDelete, ts.URL+"/admin/v1/tokens/primary", nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	ts.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete configured token status = %d", response.StatusCode)
	}
	persisted, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(persisted, []byte(config.TokenHash("client-token"))) {
		t.Fatal("configured token revocation was not persisted")
	}

	restarted := newOwnerServer(t, statePath, &fakeDialer{})
	restartedHTTP := httptest.NewServer(restarted.Handler())
	defer restartedHTTP.Close()
	if status := getStatus(t, restartedHTTP.URL+"/healthz", "Bearer client-token"); status != http.StatusUnauthorized {
		t.Fatalf("configured token revocation did not survive restart: %d", status)
	}
}

func TestOwnerStateRejectsMalformedTokenHash(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := `{"version":1,"addedTokens":[{"id":"tok_bad","name":"bad","hash":"sha256:"}]}`
	if err := os.WriteFile(statePath, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tokens = []config.Token{{ID: "primary", Name: "primary", Hash: config.TokenHash("client-token")}}
	cfg.Admin.Tokens = []config.Token{{ID: "owner", Name: "owner", Hash: config.TokenHash("owner-token")}}
	cfg.Admin.StatePath = statePath
	cfg.Admin.GeoIPURL = ""
	if _, err := relay.NewServer(cfg, relay.WithDialer(&fakeDialer{})); err == nil {
		t.Fatal("malformed owner-state token hash was accepted")
	}
}

func TestOwnerOverviewTracksDeviceMetadataAndTrustsOnlyLocalProxyForwarding(t *testing.T) {
	telegramRelay, telegramPeer := net.Pipe()
	defer telegramPeer.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := newOwnerServer(t, statePath, singleUseDialer{conn: telegramRelay})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	conn := dialWebSocketHeaders(t, ts.URL, "/apiws?dc=2&media=0", "client-token", map[string]string{
		"X-TGProxy-Device-ID":    "device_42",
		"X-TGProxy-Manufacturer": "Xiaomi",
		"X-TGProxy-Model":        "Redmi Note 8 Pro",
		"X-TGProxy-App-Version":  "1.1.0",
		"X-TGProxy-App-Code":     "10100",
		"X-TGProxy-Android":      "11",
		"X-Forwarded-For":        "198.51.100.9, 203.0.113.44",
	})
	defer conn.Close()

	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/overview", nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var overview struct {
		Tokens []struct {
			ID            string `json:"id"`
			ActiveDevices int    `json:"activeDevices"`
		} `json:"tokens"`
		Clients []struct {
			DeviceID       string `json:"deviceId"`
			Manufacturer   string `json:"manufacturer"`
			Model          string `json:"model"`
			AppVersion     string `json:"appVersion"`
			RemoteIP       string `json:"remoteIp"`
			ActiveSessions int    `json:"activeSessions"`
		} `json:"clients"`
	}
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Clients) != 1 || overview.Clients[0].DeviceID != "device_42" ||
		overview.Clients[0].Manufacturer != "Xiaomi" || overview.Clients[0].Model != "Redmi Note 8 Pro" ||
		overview.Clients[0].AppVersion != "1.1.0" || overview.Clients[0].ActiveSessions != 1 {
		t.Fatalf("unexpected clients: %+v", overview.Clients)
	}
	if overview.Clients[0].RemoteIP != "203.0.113.44" {
		t.Fatalf("local reverse proxy address was not accepted: %+v", overview.Clients[0])
	}
	if len(overview.Tokens) != 1 || overview.Tokens[0].ActiveDevices != 1 {
		t.Fatalf("unexpected token summary: %+v", overview.Tokens)
	}

	_ = conn.Close()
	_ = telegramPeer.Close()
	waitForFileContains(t, statePath, `"deviceId": "device_42"`)

	restarted := newOwnerServer(t, statePath, &fakeDialer{})
	restartedHTTP := httptest.NewServer(restarted.Handler())
	defer restartedHTTP.Close()
	request, _ = http.NewRequest(http.MethodGet, restartedHTTP.URL+"/admin/v1/overview", nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var persistedOverview struct {
		Clients []struct {
			DeviceID       string `json:"deviceId"`
			Model          string `json:"model"`
			ActiveSessions int    `json:"activeSessions"`
		} `json:"clients"`
	}
	if err := json.NewDecoder(response.Body).Decode(&persistedOverview); err != nil {
		t.Fatal(err)
	}
	if len(persistedOverview.Clients) != 1 || persistedOverview.Clients[0].DeviceID != "device_42" ||
		persistedOverview.Clients[0].Model != "Redmi Note 8 Pro" || persistedOverview.Clients[0].ActiveSessions != 0 {
		t.Fatalf("client metadata did not survive restart: %+v", persistedOverview.Clients)
	}
}

func TestOwnerCanDisconnectBlockAndUnblockOneDevice(t *testing.T) {
	telegramRelay, telegramPeer := net.Pipe()
	defer telegramPeer.Close()
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := newOwnerServer(t, statePath, singleUseDialer{conn: telegramRelay})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	conn := dialWebSocketHeaders(t, ts.URL, "/apiws?dc=2&media=0", "client-token",
		map[string]string{"X-TGProxy-Device-ID": "device_42"})
	defer conn.Close()
	endpoint := ts.URL + "/admin/v1/tokens/primary/devices/device_42/block"
	if status := ownerMutationStatus(t, http.MethodPut, endpoint); status != http.StatusNoContent {
		t.Fatalf("block status = %d", status)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("blocking a device did not close its active session")
	}
	if status := webSocketUpgradeStatus(t, ts.URL, "/apiws?dc=2&media=0",
		"client-token", "device_42"); status != http.StatusForbidden {
		t.Fatalf("blocked reconnect status = %d", status)
	}
	if status := ownerMutationStatus(t, http.MethodDelete, endpoint); status != http.StatusNoContent {
		t.Fatalf("unblock status = %d", status)
	}

	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/overview", nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var overview struct {
		Clients []struct {
			DeviceID string `json:"deviceId"`
			Blocked  bool   `json:"blocked"`
		} `json:"clients"`
	}
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Clients) != 1 || overview.Clients[0].DeviceID != "device_42" || overview.Clients[0].Blocked {
		t.Fatalf("unexpected device state: %+v", overview.Clients)
	}
}

func TestOwnerCanDisconnectOneDeviceWithoutRevokingToken(t *testing.T) {
	telegramRelay, telegramPeer := net.Pipe()
	defer telegramPeer.Close()
	server := newOwnerServer(t, filepath.Join(t.TempDir(), "state.json"),
		singleUseDialer{conn: telegramRelay})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	conn := dialWebSocketHeaders(t, ts.URL, "/apiws?dc=2&media=0", "client-token",
		map[string]string{"X-TGProxy-Device-ID": "device_42"})
	defer conn.Close()
	endpoint := ts.URL + "/admin/v1/tokens/primary/devices/device_42/disconnect"
	if status := ownerMutationStatus(t, http.MethodPost, endpoint); status != http.StatusNoContent {
		t.Fatalf("disconnect status = %d", status)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("disconnect did not close the selected device session")
	}

	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/overview", nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var overview struct {
		Tokens []struct {
			ID string `json:"id"`
		} `json:"tokens"`
	}
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Tokens) != 1 || overview.Tokens[0].ID != "primary" {
		t.Fatalf("disconnect unexpectedly revoked token: %+v", overview.Tokens)
	}
}

func ownerMutationStatus(t *testing.T, method, endpoint string) int {
	t.Helper()
	request, _ := http.NewRequest(method, endpoint, nil)
	request.Header.Set("Authorization", "Bearer owner-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func webSocketUpgradeStatus(t *testing.T, rawURL, path, token, deviceID string) int {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdC10ZXN0LXRlc3Q=\r\n"+ // gitleaks:allow
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: tgproxy-relay.v2, binary\r\n"+
		"X-TGProxy-Device-ID: %s\r\n\r\n", path, parsed.Host, token, deviceID)
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	statusLine, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var status int
	if _, err := fmt.Sscanf(statusLine, "HTTP/1.1 %d", &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestOwnerStateRejectsTokenIDCollisionAfterConfigChange(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := newOwnerServer(t, statePath, &fakeDialer{})
	ts := httptest.NewServer(server.Handler())
	created := createOwnerToken(t, ts.URL+"/admin/v1/tokens", "dynamic")
	ts.Close()

	cfg := config.Default()
	cfg.Tokens = []config.Token{{
		ID: created.Token.ID, Name: "replacement", Hash: config.TokenHash("replacement-client"),
	}}
	cfg.Admin.Tokens = []config.Token{{ID: "owner", Name: "owner", Hash: config.TokenHash("owner-token")}}
	cfg.Admin.StatePath = statePath
	cfg.Admin.GeoIPURL = ""
	if _, err := relay.NewServer(cfg, relay.WithDialer(&fakeDialer{})); err == nil ||
		!strings.Contains(err.Error(), "token id") {
		t.Fatalf("state/config token id collision was accepted: %v", err)
	}
}

func TestConnectLandingAcceptsSharePayloadAndRejectsInjection(t *testing.T) {
	server := newTestServer(t, &fakeDialer{})
	request := httptest.NewRequest(http.MethodGet, "/apiws/connect", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Добавить подключение VPS Relay") ||
		!strings.Contains(response.Body.String(), "tgproxy://import?data=") ||
		!strings.Contains(response.Body.String(), "location.hash") {
		t.Fatalf("unexpected landing response: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "YWJjLTEyM19kZWY") {
		t.Fatal("fragment payload must not be reflected by the landing page")
	}
	request = httptest.NewRequest(http.MethodGet, "/apiws/connect?data=YWJjLTEyM19kZWY", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy query link status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/connect?data=%3Cscript%3E", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe payload status = %d", response.Code)
	}
}

type ownerCreatedToken struct {
	Token struct {
		ID string `json:"id"`
	} `json:"token"`
	Secret string `json:"secret"`
}

func createOwnerToken(t *testing.T, endpoint, name string) ownerCreatedToken {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	request, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer owner-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("create status = %d body=%s", response.StatusCode, data)
	}
	var created ownerCreatedToken
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func newOwnerServer(t *testing.T, statePath string, dialer relay.Dialer) *relay.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Tokens = []config.Token{{ID: "primary", Name: "Основной", Hash: config.TokenHash("client-token")}}
	cfg.Admin.Tokens = []config.Token{{ID: "owner", Name: "Владелец", Hash: config.TokenHash("owner-token")}}
	cfg.Admin.StatePath = statePath
	cfg.Admin.GeoIPURL = ""
	cfg.Telegram.DCMap = map[int]string{2: "149.154.167.220"}
	server, err := relay.NewServer(cfg, relay.WithDialer(dialer))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func waitForFileContains(t *testing.T, path, expected string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(expected)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	t.Fatalf("state did not contain %q: err=%v body=%s", expected, err, data)
}

type singleUseDialer struct{ conn net.Conn }

func (d singleUseDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

func dialWebSocketHeaders(t *testing.T, rawURL, path, token string, headers map[string]string) net.Conn {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	var request strings.Builder
	fmt.Fprintf(&request, "GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: binary\r\n", path, parsed.Host, token, key)
	for name, value := range headers {
		fmt.Fprintf(&request, "%s: %s\r\n", name, value)
	}
	request.WriteString("\r\n")
	if _, err := conn.Write([]byte(request.String())); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("websocket status = %q err=%v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	return conn
}
