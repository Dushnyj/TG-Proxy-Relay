package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

const (
	maxRelaySessions         = 1024
	maxRelaySessionsPerToken = 128
	maxPendingUpstreamDials  = 128
	endpointRaceWidth        = 3
	endpointRaceDelay        = 200 * time.Millisecond
)

type endpointState struct {
	failures        int
	retryAt         time.Time
	probeLeaseUntil time.Time
}

type upstreamPool struct {
	mu     sync.Mutex
	health map[string]endpointState
	cursor uint64
}

func newUpstreamPool() *upstreamPool {
	return &upstreamPool{health: make(map[string]endpointState)}
}

func (p *upstreamPool) ordered(addresses []string, now time.Time) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(addresses) == 0 {
		return nil
	}
	start := int(p.cursor % uint64(len(addresses)))
	p.cursor++
	rotated := append(append([]string(nil), addresses[start:]...), addresses[:start]...)
	eligible := make([]string, 0, len(rotated))
	for _, address := range rotated {
		state, tracked := p.health[address]
		if !tracked {
			eligible = append(eligible, address)
			continue
		}
		if state.retryAt.After(now) || state.probeLeaseUntil.After(now) {
			continue
		}
		state.probeLeaseUntil = now.Add(5 * time.Second)
		p.health[address] = state
		eligible = append(eligible, address)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left := p.health[eligible[i]].retryAt
		right := p.health[eligible[j]].retryAt
		leftReady := left.IsZero() || !left.After(now)
		rightReady := right.IsZero() || !right.After(now)
		if leftReady != rightReady {
			return leftReady
		}
		return left.Before(right)
	})
	return eligible
}

func (p *upstreamPool) succeeded(address string) {
	p.mu.Lock()
	delete(p.health, address)
	p.mu.Unlock()
}

func (p *upstreamPool) failed(address string) {
	p.mu.Lock()
	state := p.health[address]
	state.failures++
	if state.failures > 6 {
		state.failures = 6
	}
	delay := time.Second * time.Duration(1<<(state.failures-1))
	state.retryAt = time.Now().Add(delay)
	state.probeLeaseUntil = time.Time{}
	p.health[address] = state
	p.mu.Unlock()
}

func (s *Server) dialTelegramRoute(ctx context.Context, dc int, test, media bool) (net.Conn, string, error) {
	addresses := s.topology.addresses(dc, test, media)
	if len(addresses) == 0 {
		return nil, "", errors.New("unknown dc")
	}
	ordered := s.upstreams.ordered(addresses, time.Now())
	if len(ordered) == 0 {
		return nil, "", errors.New("all Telegram endpoints are cooling down")
	}
	var failures []error
	for start := 0; start < len(ordered); start += endpointRaceWidth {
		end := start + endpointRaceWidth
		if end > len(ordered) {
			end = len(ordered)
		}
		connection, address, waveFailures := s.dialEndpointWave(ctx, ordered[start:end])
		failures = append(failures, waveFailures...)
		if connection != nil {
			return connection, address, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
	}
	return nil, "", errors.Join(failures...)
}

type endpointDialResult struct {
	connection net.Conn
	address    string
	err        error
}

func (s *Server) dialEndpointWave(parent context.Context, addresses []string) (
	net.Conn, string, []error) {
	if len(addresses) == 0 {
		return nil, "", nil
	}
	ctx, cancel := context.WithCancel(parent)
	results := make(chan endpointDialResult, len(addresses))
	for index, address := range addresses {
		delay := time.Duration(index) * endpointRaceDelay
		go func(target string, wait time.Duration) {
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					results <- endpointDialResult{address: target, err: ctx.Err()}
					return
				case <-timer.C:
				}
			}
			connection, err := s.dialer.DialContext(ctx, "tcp", target)
			if err == nil && ctx.Err() != nil {
				_ = connection.Close()
				connection = nil
				err = ctx.Err()
			}
			results <- endpointDialResult{connection: connection, address: target, err: err}
		}(address, delay)
	}

	failures := make([]error, 0, len(addresses))
	for received := 0; received < len(addresses); received++ {
		select {
		case <-parent.Done():
			cancel()
			go closeLateEndpointConnections(results, len(addresses)-received)
			return nil, "", append(failures, parent.Err())
		case result := <-results:
			if result.err == nil && result.connection != nil {
				s.upstreams.succeeded(result.address)
				cancel()
				go closeLateEndpointConnections(results, len(addresses)-received-1)
				return result.connection, result.address, failures
			}
			if !errors.Is(result.err, context.Canceled) {
				s.upstreams.failed(result.address)
				failures = append(failures, fmt.Errorf("%s: %w", result.address, result.err))
			}
		}
	}
	cancel()
	return nil, "", failures
}

func closeLateEndpointConnections(results <-chan endpointDialResult, count int) {
	for index := 0; index < count; index++ {
		result := <-results
		if result.connection != nil {
			_ = result.connection.Close()
		}
	}
}

type sessionLimiter struct {
	mu      sync.Mutex
	active  int
	pending int
	byToken map[string]int
}

type sessionReservation struct {
	limiter  *sessionLimiter
	tokenID  string
	dialDone bool
	released bool
}

func newSessionLimiter() *sessionLimiter {
	return &sessionLimiter{byToken: make(map[string]int)}
}

func (l *sessionLimiter) reserve(tokenID string) (*sessionReservation, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending >= maxPendingUpstreamDials {
		return nil, 503
	}
	if l.active >= maxRelaySessions || l.byToken[tokenID] >= maxRelaySessionsPerToken {
		return nil, 429
	}
	l.active++
	l.pending++
	l.byToken[tokenID]++
	return &sessionReservation{limiter: l, tokenID: tokenID}, 0
}

func (r *sessionReservation) upstreamDialDone() {
	if r == nil || r.limiter == nil {
		return
	}
	r.limiter.mu.Lock()
	if !r.dialDone {
		r.dialDone = true
		r.limiter.pending--
	}
	r.limiter.mu.Unlock()
}

func (r *sessionReservation) release() {
	if r == nil || r.limiter == nil {
		return
	}
	r.limiter.mu.Lock()
	defer r.limiter.mu.Unlock()
	if r.released {
		return
	}
	if !r.dialDone {
		r.dialDone = true
		r.limiter.pending--
	}
	r.released = true
	r.limiter.active--
	r.limiter.byToken[r.tokenID]--
	if r.limiter.byToken[r.tokenID] <= 0 {
		delete(r.limiter.byToken, r.tokenID)
	}
}
