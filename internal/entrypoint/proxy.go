// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/tlsutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type ProxyTLSConfig struct {
	CertFile string
	KeyFile  string
	Password string
}

type GatewayProxy struct {
	prefixHandlers atomic.Pointer[[]prefixRoute]
	prefixMu       sync.Mutex
	namedHandlers  sync.Map

	mcpMetadataRoutes sync.Map

	listener      net.Listener
	server        *http.Server
	tlsServer     *http.Server
	logger        *slog.Logger
	listenAddr    string
	actualAddr    string
	actualTLSAddr string
	tlsCfg        *ProxyTLSConfig
	tlsAddr       string

	staticFS   fs.FS
	spaHandler http.Handler

	requestGate func(http.Handler) http.Handler

	draining  chan struct{}
	drainOnce sync.Once

	timeouts ServerTimeouts

	OnServeFailure func(error)
}

type ServerTimeouts struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

var DefaultServerTimeouts = ServerTimeouts{
	ReadHeaderTimeout: 10 * time.Second,
	ReadTimeout:       30 * time.Second,
	IdleTimeout:       120 * time.Second,
	ShutdownTimeout:   5 * time.Second,
}

const ShutdownTimeoutEnv = "SAM_PROXY_SHUTDOWN_TIMEOUT_SECONDS"

func ShutdownTimeoutFromEnv(logger *slog.Logger) time.Duration {
	raw := os.Getenv(ShutdownTimeoutEnv)
	if raw == "" {
		return DefaultServerTimeouts.ShutdownTimeout
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		logger.Warn("invalid proxy shutdown timeout; using the default",
			"error", fmt.Errorf("%s=%q is not a positive whole number of seconds", ShutdownTimeoutEnv, raw),
			"default", DefaultServerTimeouts.ShutdownTimeout)
		return DefaultServerTimeouts.ShutdownTimeout
	}
	return time.Duration(n) * time.Second
}

var mcpMetadataRewrites = [...]struct {
	prefix string
	serve  string
}{
	{"/.well-known/oauth-protected-resource", mcpauth.PathProtectedResource},
	{"/.well-known/oauth-authorization-server", mcpauth.PathWellKnown},
	{"/.well-known/openid-configuration", mcpauth.PathWellKnown},
}

type prefixRoute struct {
	prefix  string
	handler http.Handler
}

var _ runtime.Component = (*GatewayProxy)(nil)

func NewGatewayProxy(addr string, logger *slog.Logger) *GatewayProxy {
	if logger == nil {
		logger = slog.Default()
	}
	scoped := logger.With("logger_name", "proxy")
	p := &GatewayProxy{
		listenAddr: addr,
		logger:     scoped,
		timeouts:   DefaultServerTimeouts,
	}
	p.OnServeFailure = func(err error) {
		scoped.Error("gateway proxy serve failed, terminating process", "error", err)
		os.Exit(1)
	}
	return p
}

func (p *GatewayProxy) SetTimeouts(t ServerTimeouts) {
	p.timeouts = t
}

func (p *GatewayProxy) RegisterPrefixHandler(prefix string, h http.Handler) {
	p.prefixMu.Lock()
	defer p.prefixMu.Unlock()

	var current []prefixRoute
	if cur := p.prefixHandlers.Load(); cur != nil {
		current = *cur
	}

	next := make([]prefixRoute, 0, len(current)+1)
	for _, r := range current {
		if r.prefix == prefix {
			continue
		}
		next = append(next, r)
	}
	next = append(next, prefixRoute{prefix: prefix, handler: h})
	sortPrefixRoutesDesc(next)
	p.prefixHandlers.Store(&next)
	p.logger.Debug("prefix handler registered", "prefix", prefix)
}

func (p *GatewayProxy) UnregisterPrefixHandler(prefix string) {
	p.prefixMu.Lock()
	defer p.prefixMu.Unlock()

	cur := p.prefixHandlers.Load()
	if cur == nil {
		return
	}
	current := *cur
	next := make([]prefixRoute, 0, len(current))
	removed := false
	for _, r := range current {
		if r.prefix == prefix {
			removed = true
			continue
		}
		next = append(next, r)
	}
	if !removed {
		return
	}
	p.prefixHandlers.Store(&next)
	p.logger.Info("prefix handler unregistered", "prefix", prefix)
}

func (p *GatewayProxy) RegisterHandler(name string, h http.Handler) {
	p.namedHandlers.Store(name, h)
	p.logger.Info("gateway handler registered", "name", name)
}

func (p *GatewayProxy) UnregisterHandler(name string) {
	p.namedHandlers.Delete(name)
	p.logger.Info("gateway handler unregistered", "name", name)
}

func (p *GatewayProxy) RegisterMCPMetadataRoutes(gatewayID string, handler http.Handler) {
	if gatewayID == "" || handler == nil {
		return
	}
	p.mcpMetadataRoutes.Store(gatewayID, handler)
	p.logger.Info("mcp metadata routes registered", "gatewayID", gatewayID)
}

func (p *GatewayProxy) UnregisterMCPMetadataRoutes(gatewayID string) {
	p.mcpMetadataRoutes.Delete(gatewayID)
	p.logger.Info("mcp metadata routes unregistered", "gatewayID", gatewayID)
}

func (p *GatewayProxy) HasMCPMetadataRoute(gatewayID string) bool {
	_, ok := p.mcpMetadataRoutes.Load(gatewayID)
	return ok
}

func (p *GatewayProxy) SetTLS(cfg *ProxyTLSConfig, tlsListenAddr string) {
	p.tlsCfg = cfg
	p.tlsAddr = tlsListenAddr
}

func (p *GatewayProxy) SetStaticFS(fsys fs.FS) {
	p.staticFS = fsys
	if fsys != nil {
		p.spaHandler = SPAHandler(fsys)
	} else {
		p.spaHandler = nil
	}
}

func (p *GatewayProxy) Handler() http.Handler {
	return p.buildHandler()
}

func (p *GatewayProxy) Addr() string {
	return p.actualAddr
}

func (p *GatewayProxy) Init(_ context.Context, _ config.Config) error {
	return nil
}

func (p *GatewayProxy) Start(_ context.Context) error {
	p.draining = make(chan struct{})
	handler := withDrainSignal(p.buildHandler(), p.draining)

	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("proxy listen %s: %w", p.listenAddr, err)
	}
	p.listener = ln
	p.actualAddr = ln.Addr().String()

	p.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: p.timeouts.ReadHeaderTimeout,
		ReadTimeout:       p.timeouts.ReadTimeout,
		IdleTimeout:       p.timeouts.IdleTimeout,
		ErrorLog:          httpServerErrorLog(p.logger),
	}

	if p.tlsCfg != nil && p.tlsCfg.CertFile != "" && p.tlsCfg.KeyFile != "" && p.tlsAddr != "" {
		return p.startDualPort(handler, ln)
	}
	if p.tlsCfg != nil && p.tlsCfg.CertFile != "" && p.tlsCfg.KeyFile != "" {
		return p.startSinglePortTLS(ln)
	}

	go func() {
		if serveErr := p.server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			p.logger.Error("proxy server error", "error", serveErr)
			p.OnServeFailure(serveErr)
		}
	}()
	p.logger.Info("HTTP server started", "addr", p.actualAddr)
	return nil
}

func httpServerErrorLog(logger *slog.Logger) *log.Logger {
	return log.New(&slogServerErrorWriter{logger: logger}, "", 0)
}

type slogServerErrorWriter struct{ logger *slog.Logger }

func (w *slogServerErrorWriter) Write(p []byte) (int, error) {
	detail := strings.TrimRight(string(p), "\n")
	if strings.Contains(detail, "TLS handshake error") {
		w.logger.Debug("http server transport error", "detail", detail)
		return len(p), nil
	}
	if strings.HasPrefix(detail, "http: panic serving ") {
		if _, rest, found := strings.Cut(strings.TrimPrefix(detail, "http: panic serving "), ": "); found {
			detail = "http: panic serving <redacted>: " + rest
		}
	}
	w.logger.Warn("http server transport error", "detail", detail)
	return len(p), nil
}

func (p *GatewayProxy) startSinglePortTLS(ln net.Listener) error {
	tlsCfg, err := p.loadTLSConfig()
	if err != nil {
		_ = ln.Close()
		return err
	}
	p.server.TLSConfig = tlsCfg
	go func() {
		if serveErr := p.server.ServeTLS(ln, "", ""); serveErr != nil && serveErr != http.ErrServerClosed {
			p.logger.Error("proxy HTTPS error", "error", serveErr)
			p.OnServeFailure(serveErr)
		}
	}()
	p.logger.Info("HTTP server started (HTTPS)", "addr", p.actualAddr)
	return nil
}

func (p *GatewayProxy) startDualPort(handler http.Handler, httpLn net.Listener) error {
	tlsLn, err := net.Listen("tcp", p.tlsAddr)
	if err != nil {
		_ = httpLn.Close()
		return fmt.Errorf("proxy TLS listen %s: %w", p.tlsAddr, err)
	}

	tlsCfg, err := p.loadTLSConfig()
	if err != nil {
		_ = httpLn.Close()
		_ = tlsLn.Close()
		return err
	}

	p.tlsServer = &http.Server{
		Handler:           handler,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: p.timeouts.ReadHeaderTimeout,
		ReadTimeout:       p.timeouts.ReadTimeout,
		IdleTimeout:       p.timeouts.IdleTimeout,
		ErrorLog:          httpServerErrorLog(p.logger),
	}
	p.actualTLSAddr = tlsLn.Addr().String()

	go func() {
		if serveErr := p.server.Serve(httpLn); serveErr != nil && serveErr != http.ErrServerClosed {
			p.logger.Error("proxy HTTP error", "error", serveErr)
			p.OnServeFailure(serveErr)
		}
	}()
	go func() {
		if serveErr := p.tlsServer.ServeTLS(tlsLn, "", ""); serveErr != nil && serveErr != http.ErrServerClosed {
			p.logger.Error("proxy HTTPS error", "error", serveErr)
			p.OnServeFailure(serveErr)
		}
	}()

	p.logger.Info("HTTP server started (dual-port)", "addr", p.actualAddr, "https", p.actualTLSAddr)
	return nil
}

func (p *GatewayProxy) TLSAddr() string {
	return p.actualTLSAddr
}

var cbcCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
}

func filterInsecureCiphers() []uint16 {
	var ids []uint16
	for _, s := range tls.CipherSuites() {
		if !slices.Contains(cbcCipherSuites, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

func (p *GatewayProxy) loadTLSConfig() (*tls.Config, error) {
	cert, err := tlsutil.LoadX509KeyPair(p.tlsCfg.CertFile, p.tlsCfg.KeyFile, p.tlsCfg.Password)
	if err != nil {
		return nil, fmt.Errorf("proxy load TLS certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: filterInsecureCiphers(),
	}, nil
}

func (p *GatewayProxy) Stop(ctx context.Context) error {
	if p.server == nil {
		return nil
	}
	p.logger.Info("proxy stopping")
	if p.draining != nil {
		p.drainOnce.Do(func() { close(p.draining) })
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, cmp.Or(p.timeouts.ShutdownTimeout, DefaultServerTimeouts.ShutdownTimeout))
	defer cancel()
	servers := []*http.Server{p.server}
	if p.tlsServer != nil {
		servers = append(servers, p.tlsServer)
	}
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Go(func() {
			if s.Shutdown(shutdownCtx) != nil {
				errs[i] = s.Close()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (p *GatewayProxy) Health() error {
	return nil
}

func (p *GatewayProxy) SetRequestGate(gate func(http.Handler) http.Handler) {
	p.requestGate = gate
}

func (p *GatewayProxy) buildHandler() http.Handler {
	var router http.Handler = http.HandlerFunc(p.serveHTTP)
	if p.requestGate != nil {
		router = p.requestGate(router)
	}

	chain := httputil.Chain(
		httputil.SecurityHeadersMiddleware,
		httputil.HSTSMiddleware,
		httputil.NoStoreAPIMiddleware,
		httputil.RequestIDMiddleware,
	)
	return chain(router)
}

func (p *GatewayProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if p.logger != nil && path != "/health" {
		p.logger.Debug("proxy request",
			"method", r.Method, "path", path, "origin", r.Header.Get("Origin"),
			"remote", r.RemoteAddr)
	}

	if path == "/health" {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
		return
	}

	if h, rewritten, ok := p.matchMCPMetadata(path); ok {
		r2 := r.Clone(r.Context())
		r2.URL.Path = rewritten
		r2.URL.RawPath = ""
		h.ServeHTTP(w, r2)
		return
	}

	if strings.HasPrefix(path, "/gw/") {
		rest := path[4:]
		slashIdx := strings.IndexByte(rest, '/')
		var name, subPath string
		if slashIdx >= 0 {
			name = rest[:slashIdx]
			subPath = rest[slashIdx:]
		} else {
			name = rest
			subPath = "/"
		}

		if h, ok := p.namedHandlers.Load(name); ok {
			r2 := r.Clone(r.Context())
			r2.URL.Path = subPath
			r2.URL.RawPath = ""
			h.(http.Handler).ServeHTTP(w, r2)
			return
		}
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("gateway %q not found", name))
		return
	}

	var rootHandler http.Handler
	if routes := p.prefixHandlers.Load(); routes != nil {
		for _, r2 := range *routes {
			if !matchPrefix(path, r2.prefix) {
				continue
			}
			if r2.prefix == "/" {
				rootHandler = r2.handler
				break
			}
			r2.handler.ServeHTTP(w, r)
			return
		}
	}

	spaEligible := p.spaHandler != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasPrefix(path, "/api/")

	if rootHandler != nil {
		if spaEligible {
			capture := &notFoundCapturingWriter{ResponseWriter: w}
			rootHandler.ServeHTTP(capture, r)
			if capture.wasNotFound() {
				p.spaHandler.ServeHTTP(w, r)
			}
			return
		}
		rootHandler.ServeHTTP(w, r)
		return
	}

	if spaEligible {
		p.spaHandler.ServeHTTP(w, r)
		return
	}

	writeJSONError(w, http.StatusServiceUnavailable, "gateway not ready")
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message": message,
		"errorId": idgen.New(),
	})
}

func (p *GatewayProxy) matchMCPMetadata(path string) (http.Handler, string, bool) {
	for _, rw := range mcpMetadataRewrites {
		if path == rw.prefix {
			if h, ok := p.singleMCPMetadataHandler(); ok {
				return h, rw.serve, true
			}
			continue
		}
		rest, ok := strings.CutPrefix(path, rw.prefix+"/")
		if !ok {
			continue
		}
		gwID, resourcePath, _ := extractGatewayIDFromMetadataSuffix(rest)
		if gwID == "" {
			continue
		}
		_ = resourcePath
		v, ok := p.mcpMetadataRoutes.Load(gwID)
		if !ok {
			continue
		}
		return v.(http.Handler), rw.serve, true
	}
	return nil, "", false
}

func extractGatewayIDFromMetadataSuffix(s string) (gatewayID, resourcePath string, hasResourceSuffix bool) {
	const prefix = "gw/"
	if !strings.HasPrefix(s, prefix) {
		return "", "", false
	}
	s = s[len(prefix):]
	idx := strings.IndexByte(s, '/')
	if idx < 0 {
		return s, "", false
	}
	return s[:idx], s[idx:], true
}

func (p *GatewayProxy) singleMCPMetadataHandler() (http.Handler, bool) {
	var (
		count  int
		single http.Handler
	)
	p.mcpMetadataRoutes.Range(func(_, v any) bool {
		count++
		single = v.(http.Handler)
		return count <= 1
	})
	if count == 1 {
		return single, true
	}
	return nil, false
}

type notFoundCapturingWriter struct {
	http.ResponseWriter
	hdr   http.Header
	state captureState
}

type captureState uint8

const (
	captureStart captureState = iota
	captureDrop
	capturePass
)

func (w *notFoundCapturingWriter) Header() http.Header {
	if w.state == capturePass {
		return w.ResponseWriter.Header()
	}
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}

func (w *notFoundCapturingWriter) WriteHeader(code int) {
	switch w.state {
	case captureStart:
		if code == http.StatusNotFound {
			w.state = captureDrop
			return
		}
		w.flushHeaders()
		w.state = capturePass
		w.ResponseWriter.WriteHeader(code)
	case captureDrop:
	case capturePass:
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *notFoundCapturingWriter) Write(p []byte) (int, error) {
	switch w.state {
	case captureStart:
		w.flushHeaders()
		w.state = capturePass
		return w.ResponseWriter.Write(p)
	case captureDrop:
		return len(p), nil
	default:
		return w.ResponseWriter.Write(p)
	}
}

func (w *notFoundCapturingWriter) Flush() {
	if w.state != capturePass {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *notFoundCapturingWriter) flushHeaders() {
	if w.hdr == nil {
		return
	}
	dst := w.ResponseWriter.Header()
	for k, vs := range w.hdr {
		dst[k] = vs
	}
	w.hdr = nil
}

func (w *notFoundCapturingWriter) wasNotFound() bool {
	return w.state == captureDrop
}

func matchPrefix(path, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == '/'
}

func sortPrefixRoutesDesc(routes []prefixRoute) {
	slices.SortStableFunc(routes, func(a, b prefixRoute) int {
		return cmp.Compare(len(b.prefix), len(a.prefix))
	})
}
