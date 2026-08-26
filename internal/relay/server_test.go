package relay_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/relay"
)

func TestHealthAndVersionRequireTokenAndReturnProtocol(t *testing.T) {
	server := newTestServer(t, &fakeDialer{})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	if status := getStatus(t, ts.URL+"/healthz", ""); status != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", status)
	}
	if status := getStatus(t, ts.URL+"/healthz", "Bearer wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", status)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/version", nil)
	req.Header.Set("Authorization", "Bearer phone-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("version status = %d body=%s", res.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`"name":"tgproxy-relay"`)) {
		t.Fatalf("version body missing name: %s", body)
	}
	if !bytes.Contains(body, []byte(`"protocol":1`)) {
		t.Fatalf("version body missing protocol: %s", body)
	}
}

func TestTestRoutesChecksMainAndMediaForEachRequestedDC(t *testing.T) {
	dialer := &fakeDialer{
		fail: map[string]bool{"149.154.167.221:443": true},
	}
	server := newTestServer(t, dialer)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"dcs":[{"dc":2,"ip":"149.154.167.220"},{"dc":4,"ip":"149.154.167.221"}]}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/test-routes", body)
	req.Header.Set("Authorization", "Bearer phone-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reportBytes, _ := io.ReadAll(res.Body)
	report := string(reportBytes)

	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("test-routes status = %d body=%s", res.StatusCode, report)
	}
	for _, want := range []string{
		"DC2 main OK",
		"DC2 media OK",
		"DC4 main ERROR",
		"DC4 media ERROR",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestTestRoutesUsesOnlyConfiguredTelegramDCAddresses(t *testing.T) {
	dialer := &fakeDialer{}
	server := newTestServer(t, dialer)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"dcs":[{"dc":2,"ip":"10.0.0.1"},{"dc":99,"ip":"203.0.113.10"}]}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/test-routes", body)
	req.Header.Set("Authorization", "Bearer phone-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reportBytes, _ := io.ReadAll(res.Body)
	report := string(reportBytes)

	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("test-routes status = %d body=%s", res.StatusCode, report)
	}
	for _, forbidden := range []string{"10.0.0.1:443", "203.0.113.10:443"} {
		if dialer.called(forbidden) {
			t.Fatalf("test-routes dialed client supplied address %s", forbidden)
		}
	}
	if !strings.Contains(report, "DC2 main OK") {
		t.Fatalf("configured DC was not checked:\n%s", report)
	}
	if !strings.Contains(report, "DC99 main ERROR unknown dc") {
		t.Fatalf("unknown DC was not rejected clearly:\n%s", report)
	}
}

func TestTestRoutesRejectsUnboundedRouteLists(t *testing.T) {
	server := newTestServer(t, &fakeDialer{})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	var body strings.Builder
	body.WriteString(`{"dcs":[`)
	for i := 0; i < 33; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		body.WriteString(`{"dc":2}`)
	}
	body.WriteString(`]}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/test-routes", strings.NewReader(body.String()))
	req.Header.Set("Authorization", "Bearer phone-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("too many routes status = %d", res.StatusCode)
	}
}

func newTestServer(t *testing.T, dialer *fakeDialer) *relay.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:18080"
	cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("phone-token")}}
	cfg.Telegram.DCMap = map[int]string{
		2: "149.154.167.220",
		4: "149.154.167.221",
	}
	server, err := relay.NewServer(cfg, relay.WithDialer(dialer))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestCustomWebSocketPathIsRegisteredWithCompatibilityAlias(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:18080"
	cfg.WebSocket.Path = "/private-relay"
	cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("phone-token")}}
	server, err := relay.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/private-relay", "/apiws"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer phone-token")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want websocket validation 400", path, response.Code)
		}
	}
}

func getStatus(t *testing.T, url string, auth string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

type fakeDialer struct {
	mu        sync.Mutex
	fail      map[string]bool
	conn      net.Conn
	addresses []string
}

func (d *fakeDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	if d.fail[address] {
		return nil, errDialFailed(address)
	}
	if d.conn != nil {
		return d.conn, nil
	}
	client, server := net.Pipe()
	go func() {
		_ = server.Close()
	}()
	return client, nil
}

func (d *fakeDialer) called(address string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, item := range d.addresses {
		if item == address {
			return true
		}
	}
	return false
}

type errDialFailed string

func (e errDialFailed) Error() string {
	return "dial failed: " + string(e)
}
