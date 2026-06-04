package relay_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/relay"
)

func TestHealthAndVersionRequireTokenAndReturnProtocol(t *testing.T) {
	server := newTestServer(t, fakeDialer{})
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
	dialer := fakeDialer{
		fail: map[string]bool{"149.154.167.220:443": true},
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

	if res.StatusCode != http.StatusOK {
		t.Fatalf("test-routes status = %d body=%s", res.StatusCode, report)
	}
	for _, want := range []string{
		"DC2 main ERROR",
		"DC2 media ERROR",
		"DC4 main OK",
		"DC4 media OK",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func newTestServer(t *testing.T, dialer fakeDialer) *relay.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
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
	fail map[string]bool
	conn net.Conn
}

func (d fakeDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
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

type errDialFailed string

func (e errDialFailed) Error() string {
	return "dial failed: " + string(e)
}
