// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package egressproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sandbox/hostmatch"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

type Policy struct {
	Allowlist []string

	PolicyName string
}

func (p Policy) MatchHost(host string) bool {
	return hostmatch.Match(host, p.Allowlist)
}

type Proxy struct {
	policy    Policy
	logger    *slog.Logger
	listener  net.Listener
	server    *http.Server
	fwdClient *http.Client

	mu       sync.Mutex
	allowed  int
	denied   int
	upstream int
	conns    map[net.Conn]struct{}
}

func Start(policy Policy, logger *slog.Logger) (*Proxy, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("egressproxy listen: %w", err)
	}
	p := &Proxy{
		policy:   policy,
		logger:   logger,
		listener: ln,
		conns:    make(map[net.Conn]struct{}),
		fwdClient: &http.Client{
			Timeout:       60 * time.Second,
			Transport:     &http.Transport{Proxy: nil},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	p.server = &http.Server{
		Handler:      http.HandlerFunc(p.handle),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		if err := p.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("egress proxy serve error", "error", err)
		}
	}()
	return p, nil
}

func (p *Proxy) Address() string {
	return "http://" + p.listener.Addr().String()
}

func (p *Proxy) Stop() error {
	if p.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	p.mu.Lock()
	for c := range p.conns {
		_ = c.Close()
		delete(p.conns, c)
	}
	p.mu.Unlock()
	return nil
}

func (p *Proxy) trackConns(conns ...net.Conn) {
	p.mu.Lock()
	for _, c := range conns {
		p.conns[c] = struct{}{}
	}
	p.mu.Unlock()
}

func (p *Proxy) untrackConns(conns ...net.Conn) {
	p.mu.Lock()
	for _, c := range conns {
		delete(p.conns, c)
	}
	p.mu.Unlock()
}

type Stats struct {
	Allowed  int
	Denied   int
	Upstream int
}

func (p *Proxy) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{Allowed: p.allowed, Denied: p.denied, Upstream: p.upstream}
}

func (p *Proxy) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleForward(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if !p.policy.MatchHost(host) {
		p.deny(w, host, r.Method, r.URL.String())
		return
	}

	upstream, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		p.recordUpstream()
		p.logger.Warn("egress proxy upstream dial failed",
			"host", host, "error", err)
		http.Error(w, "upstream dial failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = clientConn.Close() }()

	p.trackConns(clientConn, upstream)
	defer p.untrackConns(clientConn, upstream)

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	p.recordAllowed()
	p.logger.Debug("egress proxy connect", "host", host)

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, clientConn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(clientConn, upstream); done <- struct{}{} }()
	<-done
}

func (p *Proxy) handleForward(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || r.URL.Host == "" {
		http.Error(w, "forward proxy requires absolute URL", http.StatusBadRequest)
		return
	}
	host := r.URL.Hostname()
	if !p.policy.MatchHost(host) {
		p.deny(w, host, r.Method, r.URL.String())
		return
	}

	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "build upstream request: "+err.Error(), http.StatusBadGateway)
		return
	}
	copyForwardedHeaders(upReq.Header, r.Header)

	resp, err := p.fwdClient.Do(upReq)
	if err != nil {
		p.recordUpstream()
		p.logger.Warn("egress proxy upstream request failed",
			"host", host, "method", r.Method,
			"error", logging.RedactURLQueries(err.Error()))
		http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	p.recordAllowed()
	p.logger.Debug("egress proxy forward", "host", host, "method", r.Method, "httpStatus", resp.StatusCode)

	for k, vv := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *Proxy) deny(w http.ResponseWriter, host, method, url string) {
	p.recordDenied()
	body := fmt.Sprintf(
		"denied by SAM network policy: host=%s policy=%s method=%s url=%s "+
			"(host not in allowlist; ask the operator to add it to the bound network policy)\n",
		host, p.policy.PolicyName, method, url)
	p.logger.Warn("egress proxy deny",
		"host", host, "policy", p.policy.PolicyName, "method", method)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(body))
}

func (p *Proxy) recordAllowed() {
	p.mu.Lock()
	p.allowed++
	p.mu.Unlock()
}

func (p *Proxy) recordDenied() {
	p.mu.Lock()
	p.denied++
	p.mu.Unlock()
}

func (p *Proxy) recordUpstream() {
	p.mu.Lock()
	p.upstream++
	p.mu.Unlock()
}

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"proxy-connection":    {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func isHopByHop(h string) bool {
	_, ok := hopByHopHeaders[strings.ToLower(h)]
	return ok
}

func copyForwardedHeaders(dst, src http.Header) {
	for k, vv := range src {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
