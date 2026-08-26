package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

const (
	Name              = "tgproxy-relay"
	Protocol          = 1
	MinAppProtocol    = 1
	maxTestRoutesBody = 64 * 1024
	maxTestRouteDCs   = 32
	maxTestRoutesTime = 15 * time.Second
)

var Version = "1.0.5"

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
	mux.HandleFunc(s.cfg.WebSocket.Path, s.withAuth(s.handleWebSocket))
	// Keep the original endpoint as a compatibility alias when an installation moves to a
	// private path. Existing clients then survive a staged server/client configuration update.
	if s.cfg.WebSocket.Path != "/apiws" {
		mux.HandleFunc("/apiws", s.withAuth(s.handleWebSocket))
	}
	return mux
}

func (s *Server) ListenAndServe() error {
	server := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
	return server.ListenAndServe()
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
	r.Body = http.MaxBytesReader(w, r.Body, maxTestRoutesBody)
	var req testRoutesRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(req.DCS) == 0 {
		http.Error(w, "no dc routes", http.StatusBadRequest)
		return
	}
	if len(req.DCS) > maxTestRouteDCs {
		http.Error(w, "too many dc routes", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), maxTestRoutesTime)
	defer cancel()
	report, allAvailable := s.checkRoutes(ctx, req)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !allAvailable {
		w.WriteHeader(http.StatusBadGateway)
	}
	_, _ = w.Write([]byte(report))
}

func (s *Server) checkRoutes(ctx context.Context, req testRoutesRequest) (string, bool) {
	dcs := sortedDCS(req.DCS)
	errorsByIndex := make([]error, len(dcs))
	completedByIndex := make([]bool, len(dcs))
	jobs := make(chan int)
	workerCount := len(dcs)
	if workerCount > 6 {
		workerCount = 6
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					errorsByIndex[index] = s.checkAddress(
						ctx, s.cfg.Telegram.AddressForDC(dcs[index].DC))
					completedByIndex[index] = true
				}
			}
		}()
	}
	for index := range dcs {
		select {
		case jobs <- index:
		case <-ctx.Done():
			for pending := index; pending < len(dcs); pending++ {
				errorsByIndex[pending] = ctx.Err()
				completedByIndex[pending] = true
			}
			close(jobs)
			workers.Wait()
			return formatRouteReport(dcs, errorsByIndex)
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		for index := range errorsByIndex {
			if !completedByIndex[index] {
				errorsByIndex[index] = ctx.Err()
			}
		}
	}
	return formatRouteReport(dcs, errorsByIndex)
}

func formatRouteReport(dcs []testDC, errorsByIndex []error) (string, bool) {
	var out strings.Builder
	allAvailable := true
	for index, dc := range dcs {
		err := errorsByIndex[index]
		for _, scope := range []string{"main", "media"} {
			if err != nil {
				allAvailable = false
				fmt.Fprintf(&out, "DC%d %s ERROR %s\n", dc.DC, scope, err.Error())
			} else {
				fmt.Fprintf(&out, "DC%d %s OK\n", dc.DC, scope)
			}
		}
	}
	return out.String(), allAvailable
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
	seen := make(map[int]struct{}, len(items))
	copyItems := make([]testDC, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item.DC]; exists {
			continue
		}
		seen[item.DC] = struct{}{}
		copyItems = append(copyItems, item)
	}
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
