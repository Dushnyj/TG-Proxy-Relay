package relay_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/relay"
)

func TestCapabilitiesAdvertiseDynamicSignedTopologyAndExactRevision(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tokens = []config.Token{{ID: "phone", Name: "phone",
		Hash: config.TokenHash("phone-token")}}
	cfg.Telegram.Topology = config.TopologyConfig{
		URL:       "https://updates.example/topology.json",
		PublicKey: base64.RawStdEncoding.EncodeToString(publicKey),
		StatePath: filepath.Join(t.TempDir(), "topology.json"), RefreshIntervalSec: 900,
	}
	server, err := relay.NewServer(cfg, relay.WithDialer(&fakeDialer{}))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/capabilities", nil)
	request.Header.Set("Authorization", "Bearer phone-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Protocol struct{ Min, Max int } `json:"protocol"`
		Features []string               `json:"features"`
		Topology struct {
			Dynamic    bool   `json:"dynamic"`
			Revision   uint64 `json:"revision"`
			Production []int  `json:"productionDcs"`
		} `json:"topology"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Topology.Dynamic || body.Topology.Revision != 0 || len(body.Topology.Production) == 0 {
		t.Fatalf("unexpected topology capability: %+v", body.Topology)
	}
	if body.Protocol.Min != 1 || body.Protocol.Max != 2 {
		t.Fatalf("unexpected protocol range: %+v", body.Protocol)
	}
}
