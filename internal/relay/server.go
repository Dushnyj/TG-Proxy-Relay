package relay

import (
	"context"
	"encoding/json"
	"errors"
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
	MaxRelayProtocol  = 2
	MinAppProtocol    = 1
	OwnerAPIProtocol  = 1
	maxTestRoutesBody = 64 * 1024
	maxTestRouteDCs   = 32
	maxTestRoutesTime = 15 * time.Second
)

var Version = "1.2.0"

type Dialer interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
}

type Server struct {
	cfg       config.Config
	dialer    Dialer
	control   *controlPlane
	upstreams *upstreamPool
	sessions  *sessionLimiter
	topology  *topologyManager
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
		cfg:       cfg,
		upstreams: newUpstreamPool(),
		sessions:  newSessionLimiter(),
		dialer: &net.Dialer{
			Timeout: time.Duration(cfg.Telegram.ConnectTimeoutMs) * time.Millisecond,
		},
	}
	for _, opt := range opts {
		opt(server)
	}
	topology, err := newTopologyManager(cfg.Telegram)
	if err != nil {
		return nil, fmt.Errorf("initialize Telegram topology: %w", err)
	}
	server.topology = topology
	control, err := newControlPlane(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize owner control plane: %w", err)
	}
	server.control = control
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.withClientAuth(s.ignoreClientToken(s.handleHealthz)))
	mux.HandleFunc("/version", s.withClientAuth(s.ignoreClientToken(s.handleVersion)))
	mux.HandleFunc("/capabilities", s.withClientAuth(s.ignoreClientToken(s.handleCapabilities)))
	mux.HandleFunc("/test-routes", s.withClientAuth(s.ignoreClientToken(s.handleTestRoutes)))
	mux.HandleFunc(s.cfg.WebSocket.Path, s.withClientAuth(s.handleWebSocket))
	s.registerOwnerRoutes(mux, "")
	s.registerOwnerRoutes(mux, s.cfg.WebSocket.Path)
	mux.HandleFunc("/connect", s.handleConnectLanding)
	mux.HandleFunc(s.cfg.WebSocket.Path+"/connect", s.handleConnectLanding)
	// Keep the original endpoint as a compatibility alias when an installation moves to a
	// private path. Existing clients then survive a staged server/client configuration update.
	if s.cfg.WebSocket.Path != "/apiws" {
		mux.HandleFunc("/apiws", s.withClientAuth(s.handleWebSocket))
		s.registerOwnerRoutes(mux, "/apiws")
		mux.HandleFunc("/apiws/connect", s.handleConnectLanding)
	}
	return mux
}

func (s *Server) ListenAndServe() error {
	return s.ListenAndServeContext(context.Background())
}

func (s *Server) ListenAndServeContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.topology.start(ctx)
	httpServer := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- httpServer.ListenAndServe() }()
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownError := httpServer.Shutdown(shutdownContext)
		if shutdownError != nil {
			// Shutdown can time out while a regular HTTP handler is still running. Ensure the
			// serving goroutine is released before waiting for its result below.
			_ = httpServer.Close()
		}
		controlError := s.control.shutdown()
		serveError := <-serveResult
		if shutdownError != nil {
			return shutdownError
		}
		if controlError != nil {
			return fmt.Errorf("flush owner state: %w", controlError)
		}
		if serveError != nil && !errors.Is(serveError, http.ErrServerClosed) {
			return serveError
		}
		return nil
	}
}

func (s *Server) withClientAuth(next func(http.ResponseWriter, *http.Request, config.Token)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, authorized := s.control.authorizeClient(r.Header.Get("Authorization"))
		if !authorized {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r, token)
	}
}

func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.control == nil || !s.control.authorizeAdmin(r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) ignoreClientToken(next http.HandlerFunc) func(http.ResponseWriter, *http.Request, config.Token) {
	return func(w http.ResponseWriter, r *http.Request, _ config.Token) {
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
		OwnerProtocol  int    `json:"ownerProtocol"`
	}{
		Name:           Name,
		Version:        Version,
		Protocol:       Protocol,
		MinAppProtocol: MinAppProtocol,
		OwnerProtocol:  OwnerAPIProtocol,
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type protocolRange struct {
		Min int `json:"min"`
		Max int `json:"max"`
	}
	type topologyView struct {
		Source        string `json:"source"`
		Dynamic       bool   `json:"dynamic"`
		Revision      uint64 `json:"revision"`
		ProductionDCs []int  `json:"productionDcs"`
		TestDCs       []int  `json:"testDcs"`
	}
	writeJSON(w, http.StatusOK, struct {
		Name             string        `json:"name"`
		Version          string        `json:"version"`
		Protocol         protocolRange `json:"protocol"`
		WebSocket        []string      `json:"websocketSubprotocols"`
		Features         []string      `json:"features"`
		RouteDiagnostics string        `json:"routeDiagnostics"`
		Topology         topologyView  `json:"topology"`
	}{
		Name: Name, Version: Version,
		Protocol:  protocolRange{Min: MinAppProtocol, Max: MaxRelayProtocol},
		WebSocket: []string{"tgproxy-relay.v2", "binary"},
		Features: []string{"endpoint-fallback", "media-endpoints", "ipv4-ipv6",
			"signed-topology-lkg", "owner-tokens", "owner-devices", "device-blocking"},
		RouteDiagnostics: "tcp-preflight-only-client-must-prove-mtproto",
		Topology: topologyView{Source: s.topology.source(), Dynamic: s.topology.dynamic(),
			Revision:      s.topology.revision(),
			ProductionDCs: s.topology.configuredDCs(false),
			TestDCs:       s.topology.configuredDCs(true)},
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
	errorsByIndex := make([]routeCheckErrors, len(dcs))
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
					errorsByIndex[index].main = s.checkRouteAddress(
						ctx, dcs[index].DC, false)
					errorsByIndex[index].media = s.checkRouteAddress(
						ctx, dcs[index].DC, true)
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
				errorsByIndex[pending] = routeCheckErrors{main: ctx.Err(), media: ctx.Err()}
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
				errorsByIndex[index] = routeCheckErrors{main: ctx.Err(), media: ctx.Err()}
			}
		}
	}
	return formatRouteReport(dcs, errorsByIndex)
}

type routeCheckErrors struct{ main, media error }

func formatRouteReport(dcs []testDC, errorsByIndex []routeCheckErrors) (string, bool) {
	var out strings.Builder
	allAvailable := true
	for index, dc := range dcs {
		for _, check := range []struct {
			scope string
			err   error
		}{
			{scope: "main", err: errorsByIndex[index].main},
			{scope: "media", err: errorsByIndex[index].media},
		} {
			err := check.err
			if err != nil {
				allAvailable = false
				fmt.Fprintf(&out, "DC%d %s ERROR %s (TCP_ONLY)\n", dc.DC, check.scope, err.Error())
			} else {
				fmt.Fprintf(&out, "DC%d %s OK TCP_ONLY\n", dc.DC, check.scope)
			}
		}
	}
	return out.String(), allAvailable
}

func (s *Server) checkRouteAddress(parent context.Context, dc int, media bool) error {
	addresses := s.topology.addresses(dc, false, media)
	if len(addresses) == 0 {
		return fmt.Errorf("unknown dc")
	}
	var last error
	for _, address := range addresses {
		if err := s.checkAddress(parent, address); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return last
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
