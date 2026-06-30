package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

const (
	Name           = "tgproxy-relay"
	Protocol       = 1
	MinAppProtocol = 1
)

var Version = "1.0.3"

type Dialer interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
}

type Server struct {
	cfg    config.Config
	dialer Dialer
}

type Option func(*Server)

func WithDialer(dialer Dialer) Option {
	return func(server *Server) {
		if dialer != nil {
			server.dialer = dialer
		}
	}
}

func NewServer(cfg config.Config, opts ...Option) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	server := &Server{
		cfg: cfg,
		dialer: &net.Dialer{
			Timeout: time.Duration(cfg.Telegram.ConnectTimeoutMs) * time.Millisecond,
		},
	}
	for _, opt := range opts {
		opt(server)
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.withAuth(s.handleHealthz))
	mux.HandleFunc("/version", s.withAuth(s.handleVersion))
	mux.HandleFunc("/test-routes", s.withAuth(s.handleTestRoutes))
	mux.HandleFunc("/apiws", s.withAuth(s.handleWebSocket))
	return mux
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.cfg.Listen, s.Handler())
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthorizeBearer(r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		Protocol       int    `json:"protocol"`
		MinAppProtocol int    `json:"minAppProtocol"`
	}{
		Name:           Name,
		Version:        Version,
		Protocol:       Protocol,
		MinAppProtocol: MinAppProtocol,
	})
}

func (s *Server) handleTestRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req testRoutesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(req.DCS) == 0 {
		http.Error(w, "no dc routes", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	report := s.checkRoutes(r.Context(), req)
	_, _ = w.Write([]byte(report))
}

func (s *Server) checkRoutes(ctx context.Context, req testRoutesRequest) string {
	var out strings.Builder
	for _, dc := range sortedDCS(req.DCS) {
		address := s.cfg.Telegram.AddressForDC(dc.DC)
		for _, scope := range []string{"main", "media"} {
			err := s.checkAddress(ctx, address)
			if err != nil {
				fmt.Fprintf(&out, "DC%d %s ERROR %s\n", dc.DC, scope, err.Error())
			} else {
				fmt.Fprintf(&out, "DC%d %s OK\n", dc.DC, scope)
			}
		}
	}
	return out.String()
}

func (s *Server) checkAddress(parent context.Context, address string) error {
	if address == "" {
		return fmt.Errorf("unknown dc")
	}
	timeout := time.Duration(s.cfg.Telegram.ConnectTimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	conn, err := s.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

type testRoutesRequest struct {
	DCS []testDC `json:"dcs"`
}

type testDC struct {
	DC int    `json:"dc"`
	IP string `json:"ip"`
}

func sortedDCS(items []testDC) []testDC {
	copyItems := append([]testDC(nil), items...)
	sort.SliceStable(copyItems, func(i, j int) bool {
		return copyItems[i].DC < copyItems[j].DC
	})
	return copyItems
}

func routeAddress(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "443")
}
