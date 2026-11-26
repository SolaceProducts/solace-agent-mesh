// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yosida95/uritemplate/v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const headerWWWAuthenticate = "WWW-Authenticate"

var mcpSessionNamespace = uuid.MustParse("a3b66c0a-9f1f-4d78-9b2c-90fb0c3a7c6e")

func mcpSessionToUUID(mcpSessionID string) string {
	stripped := strings.TrimPrefix(mcpSessionID, "mcp-session-")
	if _, err := uuid.Parse(stripped); err == nil {
		return stripped
	}
	//nolint:forbidigo // v5 required for stable session→ID mapping; v7 would change every call
	return uuid.NewSHA1(mcpSessionNamespace, []byte(mcpSessionID)).String()
}

func init() {
	entrypoint.RegisterAdapterFactory("mcp", func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewAdapter(cfg)
	})
}

type MCPAuthDeps struct {
	IDPClient     mcpauth.IDPClient
	SAMMinter     mcpauth.SAMTokenMinter
	AuthzResolver mcpauth.AuthzResolver

	StateSigner   *trust.StateSigner
	StateVerifier *trust.StateVerifier
}

type taskResult struct {
	text         string
	contents     []mcp.Content
	structured   map[string]any
	unsuccessful bool
	err          *a2a.Error
}

type taskState struct {
	toolName       string
	isWorkflow     bool
	done           chan taskResult
	agentName      string
	userID         string
	sessionID      string
	mcpSessionID   string
	modern         bool
	handle         string
	mu             sync.Mutex
	textBuf        strings.Builder
	contents       []mcp.Content
	processedFiles map[string]struct{}
}

type Adapter struct {
	core   *entrypoint.GatewayCore
	cfg    *Config
	rawCfg map[string]any
	logger *slog.Logger

	mcpServer        *server.MCPServer
	httpServer       *server.StreamableHTTPServer
	sessKeys         *store.GatewaySessionKeyRepo
	sessions         *store.SessionRepo
	links            *store.MCPArtifactLinkRepo
	artifactTemplate *uritemplate.Template

	toolMap    map[string]toolMapping
	agentTools map[string][]string
	toolMu     sync.RWMutex

	tasks   map[string]*taskState
	tasksMu sync.Mutex

	cancel context.CancelFunc

	onHandlerReady func(http.Handler)

	coreTasksOverride entrypoint.TaskRegistry

	validator auth.TokenValidator

	authDeps *MCPAuthDeps

	mcpauthHandler *mcpauth.Handler

	metadataMu  sync.Mutex
	metadataMux http.Handler
}

type toolMapping struct {
	agentName  string
	skillID    string
	isWorkflow bool
}

var _ entrypoint.GatewayAdapter = (*Adapter)(nil)

func NewAdapter(rawCfg map[string]any) (*Adapter, error) {
	return &Adapter{
		rawCfg:     rawCfg,
		toolMap:    make(map[string]toolMapping),
		agentTools: make(map[string][]string),
		tasks:      make(map[string]*taskState),
	}, nil
}

func (a *Adapter) Handler() http.Handler {
	if a.httpServer == nil {
		return nil
	}
	return a.buildHTTPHandler()
}

func (a *Adapter) SetOnHandlerReady(cb func(http.Handler)) {
	a.onHandlerReady = cb
}

func (a *Adapter) oauthRecords(core *entrypoint.GatewayCore) (mcpauth.RecordRepo, *atrest.Box, error) {
	db := core.Store()
	if db == nil {
		return nil, nil, nil
	}
	secret := ""
	if cfg := core.Config(); cfg != nil {
		secret = cfg.SessionSecretKey
	}
	secret = atrest.SessionSecret(secret)
	if secret == "" {
		a.logger.Warn(atrest.NoSecretWarning)
	}
	box, err := atrest.New(secret, "mcp-oauth")
	if err != nil {
		return nil, nil, fmt.Errorf("MCP adapter: login record key: %w", err)
	}
	return store.NewMCPOAuthRepo(db), box, nil
}

func (a *Adapter) SetAuthDeps(deps *MCPAuthDeps) { a.authDeps = deps }

func (a *Adapter) AuthDeps() *MCPAuthDeps { return a.authDeps }

func (a *Adapter) Cfg() *Config { return a.cfg }

func (a *Adapter) MountIntent() entrypoint.MountSpec {
	if a.rawCfg != nil {
		if t, _ := a.rawCfg["transport"].(string); t == "stdio" {
			return entrypoint.MountSpec{Kind: entrypoint.MountNone}
		}
	}
	return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
}

func (a *Adapter) MachineIngress() bool { return true }

func (a *Adapter) EntrypointType() string { return "mcp" }

func (a *Adapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: false}
}

func (a *Adapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.logger = core.Logger().With("logger_name", "mcp-adapter")

	adapterCfg := a.rawCfg
	if adapterCfg == nil {
		if raw, ok := core.RawConfig()["adapter_config"].(map[string]any); ok {
			adapterCfg = raw
		}
	}

	cfg, err := parseConfig(adapterCfg)
	if err != nil {
		return err
	}
	a.cfg = cfg

	if db := core.Store(); db != nil {
		a.sessKeys = store.NewGatewaySessionKeyRepo(db)
		a.sessions = store.NewSessionRepo(db)
		a.links = store.NewMCPArtifactLinkRepo(db)
	} else {
		a.logger.Warn("mcp adapter: no gateway store configured — session reuse disabled, every MCP call will create a new ephemeral session")
	}

	if cfg.EnableAuth && cfg.Transport == "stdio" {
		a.logger.Warn("mcp adapter: enable_auth=true ignored for stdio transport (no HTTP surface)")
	}
	if cfg.EnableAuth && cfg.Transport != "stdio" {
		validator, err := resolveValidator(core.GatewayID(), cfg.ExternalAuthProvider, core.RawConfig())
		if err != nil {
			return err
		}
		core.SetTokenValidator(validator)
		a.validator = validator

		if a.authDeps == nil {
			return fmt.Errorf("MCP adapter: enable_auth=true requires SetAuthDeps to be called before Init (injected when auth is configured)")
		}
		records, box, err := a.oauthRecords(core)
		if err != nil {
			return err
		}
		a.mcpauthHandler = mcpauth.New(mcpauth.Config{
			EnableAuth:              true,
			Issuer:                  cfg.Issuer,
			GatewayID:               core.GatewayID(),
			ExternalAuthProvider:    cfg.ExternalAuthProvider,
			UserIDClaim:             cfg.UserIDClaim,
			Host:                    cfg.Host,
			Port:                    cfg.Port,
			RequirePKCE:             cfg.RequirePKCE,
			OAuthStateTTLSeconds:    cfg.OAuthStateTTLSeconds,
			OAuthCodeTTLSeconds:     cfg.OAuthCodeTTLSeconds,
			AllowedRedirectURIs:     cfg.AllowedRedirectURIs,
			IDPClient:               a.authDeps.IDPClient,
			SAMTokenMinter:          a.authDeps.SAMMinter,
			AuthzResolver:           a.authDeps.AuthzResolver,
			Logger:                  a.logger,
			StateRedirectionEnabled: cfg.StateRedirectionEnabled,
			OAuthDispatcherURL:      cfg.OAuthDispatcherURL,
			StateSigner:             a.authDeps.StateSigner,
			StateVerifier:           a.authDeps.StateVerifier,
			Records:                 records,
			RecordBox:               box,
		})
	}

	a.warnIfAuthnOnly()
	a.warnIfDevMode()
	a.warnIfPermissiveSystemUser()

	a.buildMCPServer()

	for _, card := range core.AgentRegistry().List() {
		if a2a.IsEphemeralCard(card) {
			continue
		}
		a.registerAgentTools(&card)
	}

	if cfg.Transport == "sse" {
		a.httpServer = newStreamableHTTPServer(a.mcpServer, cfg.streamableRequestBodyLimit())
	}

	a.logger.Info("mcp adapter initialized",
		"transport", cfg.Transport,
		"host", cfg.Host,
		"port", cfg.Port,
		"tools", len(a.toolMap),
	)

	if cfg.Transport == "sse" && a.onHandlerReady != nil {
		a.onHandlerReady(a.buildHTTPHandler())
	}
	return nil
}

func (a *Adapter) buildMCPServer() {
	hooks := &server.Hooks{}
	hooks.AddOnError(a.reportFilteredToolCall)
	if a.cfg.EnableArtifactResources {
		hooks.AddBeforeListResources(func(ctx context.Context, _ any, _ *mcp.ListResourcesRequest) { a.loadSessionLinks(ctx) })
		hooks.AddBeforeReadResource(func(ctx context.Context, _ any, _ *mcp.ReadResourceRequest) { a.loadSessionLinks(ctx) })
	}
	serverOpts := []server.ServerOption{
		server.WithHooks(hooks),
		server.WithToolCapabilities(true),
		server.WithLogging(),
		server.WithInstructions(a.cfg.ServerDescription),
		server.WithToolFilter(a.toolListFilter),
		server.WithToolFilter(offerSessionIDArg),
		server.WithToolHandlerMiddleware(a.plantDefaultIdentity),
	}
	if a.cfg.EnableArtifactResources {
		serverOpts = append(serverOpts, server.WithResourceCapabilities(true, true))
	}
	a.mcpServer = server.NewMCPServer(
		a.cfg.ServerName,
		"1.0.0",
		serverOpts...,
	)
	if a.cfg.EnableArtifactResources {
		a.registerArtifactTemplate()
	}
}

const maxRequestBodyBytes = server.DefaultMaxRequestBodyBytes

func (c *Config) streamableRequestBodyLimit() int64 {
	if c.EnableFileInput {
		return c.fileInputBodyLimit()
	}
	return maxRequestBodyBytes
}

func newStreamableHTTPServer(mcpServer *server.MCPServer, bodyLimit int64) *server.StreamableHTTPServer {
	return server.NewStreamableHTTPServer(mcpServer, server.WithMaxRequestBodyBytes(bodyLimit))
}

func (a *Adapter) buildHTTPHandler() http.Handler {
	return a.assembleHTTPHandler(a.httpServer)
}

func (a *Adapter) assembleHTTPHandler(inner http.Handler) http.Handler {
	if inner != nil {
		inner = a.logRequestedProtocolVersion(passStreamsThroughProxies(inner))
	}
	limitBody := a.fileInputBodyLimit()
	if a.mcpauthHandler == nil {
		return a.wrapCORS(limitBody(a.authMiddleware(inner)))
	}
	mux := http.NewServeMux()
	if inner != nil {
		mux.Handle("/", a.authMiddleware(inner))
	}
	a.mcpauthHandler.RegisterRoutes(mux)
	return a.wrapCORS(limitBody(mux))
}

const logRequestBodyTooLarge = "mcp request body over the file input limit"

func (a *Adapter) fileInputBodyLimit() func(http.Handler) http.Handler {
	if !a.cfg.EnableFileInput {
		return func(next http.Handler) http.Handler { return next }
	}
	limit := a.cfg.fileInputBodyLimit()
	gatewayID := a.gatewayID()
	return httputil.MaxBytesMiddleware(limit, func(r *http.Request, status int) {
		observability.RecordGatewayReject(gatewayID, observability.RejectRouteMaxBytes, r.Method, status)
		a.logger.Warn(logRequestBodyTooLarge, "gatewayID", gatewayID, "contentLength", r.ContentLength, "limit", limit)
	})
}

const logRequestProtocolVersion = "mcp request protocol version"

func (a *Adapter) logRequestedProtocolVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.logger.Debug(logRequestProtocolVersion,
			"requestedProtocolVersion", knownProtocolVersion(r.Header.Get(mcp.HeaderProtocolVersion)))
		next.ServeHTTP(w, r)
	})
}

func knownProtocolVersion(version string) string {
	if version == "" || mcp.IsValidProtocolVersion(version) {
		return version
	}
	return "unknown"
}

func (a *Adapter) wrapCORS(h http.Handler) http.Handler {
	var origins []string
	var re *regexp.Regexp
	if a.cfg != nil {
		origins = a.cfg.CORSAllowedOrigins
		re = a.cfg.compiledCORSRegex
	}
	return httputil.CORSMiddlewareWithExposedHeaders(origins, re, mcpExposedHeaders)(h)
}

var mcpExposedHeaders = []string{server.HeaderKeySessionID, server.HeaderKeyProtocolVersion}

func (a *Adapter) MetadataHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.metadataMu.Lock()
		if a.metadataMux == nil && a.mcpauthHandler != nil {
			mux := http.NewServeMux()
			a.mcpauthHandler.RegisterRoutes(mux)
			a.metadataMux = a.wrapCORS(mux)
		}
		mux := a.metadataMux
		a.metadataMu.Unlock()
		if mux == nil {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func requestClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(xff) != "" {
		first := xff
		if i := strings.IndexByte(xff, ','); i >= 0 {
			first = xff[:i]
		}
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *Adapter) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if a.validator == nil || (a.cfg != nil && a.cfg.DevMode) {
			ctx := auth.ContextWithSubject(r.Context(), a.defaultIdentity())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		token, extractErr := extractBearer(r)
		if extractErr != nil {
			a.logger.Warn("MCP tool call: malformed Authorization header",
				"error", extractErr, "path", r.URL.Path, "clientIP", requestClientIP(r))
			w.Header().Set(headerWWWAuthenticate,
				fmt.Sprintf(`Bearer realm=%q, error="invalid_request", resource_metadata=%q`,
					a.gatewayID(), a.resourceMetadataURL(r)))
			auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorInvalidToken, extractErr.Error())
			return
		}
		if token == "" {
			w.Header().Set(headerWWWAuthenticate,
				fmt.Sprintf(`Bearer realm=%q, resource_metadata=%q`,
					a.gatewayID(), a.resourceMetadataURL(r)))
			auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, "Missing Bearer token")
			return
		}
		identity, err := a.validator.Validate(r.Context(), token)
		if err != nil {
			a.logger.Warn("MCP tool call: token validation failed",
				"error", err, "path", r.URL.Path, "clientIP", requestClientIP(r))
			w.Header().Set(headerWWWAuthenticate,
				fmt.Sprintf(`Bearer realm=%q, error="invalid_token", resource_metadata=%q`,
					a.gatewayID(), a.resourceMetadataURL(r)))
			auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorInvalidToken, "Invalid or expired token")
			return
		}
		ctx := auth.ContextWithSubject(r.Context(), identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Adapter) gatewayID() string {
	if a.core != nil {
		return a.core.GatewayID()
	}
	return ""
}

func (a *Adapter) warnIfAuthnOnly() {
	if a.cfg == nil || !a.cfg.EnableAuth || a.cfg.Transport == "stdio" {
		return
	}
	if a.authzEnforced() {
		return
	}
	a.logger.Warn("MCP gateway running in authentication-only mode — no authorization_service configured; all authenticated callers will pass the delegation gate",
		"gatewayID", a.gatewayID(),
	)
}

func (a *Adapter) warnIfDevMode() {
	if a.cfg == nil || !a.cfg.DevMode || a.cfg.Transport == "stdio" {
		return
	}
	a.logger.Warn("DEV MODE ENABLED: Authentication is bypassed and tools/list filtering is disabled. This is insecure and must never be used in production",
		"gatewayID", a.gatewayID(),
	)
}

func (a *Adapter) warnIfPermissiveSystemUser() {
	if a.cfg == nil {
		return
	}
	if a.cfg.EnableAuth && a.cfg.Transport != "stdio" {
		return
	}
	if a.cfg.DefaultUserIdentity != "" || a.cfg.RunAs != "" {
		return
	}
	if !a.authzEnforced() {
		return
	}
	a.logger.Warn("MCP entrypoint resolves unauthenticated calls to the default service account, which can invoke any agent or workflow that declares no required_scopes; set run_as to a scoped service account to narrow what it reaches",
		"gatewayID", a.gatewayID(),
	)
}

func trimTrailingSlashToMatchResourceByteForByte(mountPath string) string {
	return strings.TrimRight(mountPath, "/")
}

func (a *Adapter) resourceMetadataURL(r *http.Request) string {
	mountPath := r.Header.Get("X-Original-Path")
	if mountPath == "" {
		mountPath = "/gw/" + a.gatewayID() + r.URL.Path
	}
	mountPath = trimTrailingSlashToMatchResourceByteForByte(mountPath)
	if a.cfg != nil && a.cfg.Issuer != "" {
		if u, err := url.Parse(a.cfg.Issuer); err == nil && u.Host != "" {
			scheme := u.Scheme
			if scheme == "" {
				scheme = "https"
			}
			return scheme + "://" + u.Host + mcpauth.PathProtectedResource + mountPath
		}
	}
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s%s%s", scheme, r.Host, mcpauth.PathProtectedResource, mountPath)
}

func extractBearer(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", nil
	}
	token, isBearer := apikey.BearerCredential(h)
	if !isBearer {
		scheme := h
		if i := strings.IndexByte(h, ' '); i > 0 {
			scheme = h[:i]
		}
		return "", fmt.Errorf("Authorization header uses unsupported scheme %q (expected Bearer)", scheme)
	}
	return token, nil
}

func (a *Adapter) Start(ctx context.Context) error {
	if a.cancel != nil {
		return fmt.Errorf("mcp adapter: already started; call Stop before Start again")
	}
	ctx, a.cancel = context.WithCancel(ctx)

	var startErr error
	switch a.cfg.Transport {
	case "sse":
		startErr = a.startSSE(ctx)
	case "stdio":
		startErr = a.startStdio(ctx)
	default:
		startErr = fmt.Errorf("mcp adapter: unsupported transport %q", a.cfg.Transport)
	}
	if startErr != nil {
		a.cancel()
		a.cancel = nil
		return startErr
	}

	return nil
}

func (a *Adapter) Stop(_ context.Context) error {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}

	a.tasksMu.Lock()
	for taskID, ts := range a.tasks {
		select {
		case ts.done <- taskResult{err: &a2a.Error{Code: -1, Message: "gateway shutting down"}}:
		default:
		}
		delete(a.tasks, taskID)
	}
	a.tasksMu.Unlock()

	a.logger.Info("mcp adapter stopped")
	return nil
}

func (a *Adapter) startSSE(_ context.Context) error {
	a.logger.Info("mcp adapter ready")
	return nil
}

func (a *Adapter) startStdio(ctx context.Context) error {
	stdioServer := server.NewStdioServer(a.mcpServer)

	go func() {
		a.logger.Info("mcp server listening on stdio")
		if err := stdioServer.Listen(ctx, os.Stdin, os.Stdout); err != nil {
			a.logger.Error("mcp stdio server error", "error", err)
		}
	}()

	return nil
}

func (a *Adapter) OnAgentDiscovered(card *a2a.AgentCard) {
	if a2a.IsEphemeralCard(*card) {
		return
	}
	a.registerAgentTools(card)
}

func (a *Adapter) OnAgentRemoved(agentName string) {
	a.deregisterAgentTools(agentName)
}

func (a *Adapter) OnStatusUpdate(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	if event.Status.Message == nil {
		return
	}

	a.tasksMu.Lock()
	ts, ok := a.tasks[taskID]
	a.tasksMu.Unlock()
	if !ok {
		return
	}

	for _, part := range event.Status.Message.Parts {
		switch part.Kind {
		case "text":
			if part.Text != "" {
				ts.mu.Lock()
				ts.textBuf.WriteString(part.Text)
				ts.mu.Unlock()
			}
		case "data":
			if a.handleAuthenticationRequired(taskID, part) {
				return
			}
			a.handleArtifactSignal(ctx, taskID, ts, part)
		case "file":
			a.handleFilePart(ctx, taskID, ts, part)
		}
	}
}

func (a *Adapter) handleAuthenticationRequired(taskID string, part a2a.Part) bool {
	dataMap, ok := part.Data.(map[string]any)
	if !ok {
		return false
	}
	if dataMap["type"] != "authentication_required" {
		return false
	}
	authURI, _ := dataMap["auth_uri"].(string)
	if authURI == "" {
		a.logger.Warn("authentication_required: missing auth_uri; ignoring signal", "taskID", taskID)
		return false
	}

	text := "Authentication required (copy the URL, remove empty spaces, " +
		"and paste it in your browser):\n\n" + authURI +
		"\n\nAfter authenticating, re-request your last operation."

	ts := a.removeTask(taskID)
	if ts == nil {
		return true
	}
	select {
	case ts.done <- taskResult{text: text}:
	default:
	}
	return true
}

func (a *Adapter) OnArtifactUpdate(ctx context.Context, taskID string, event *a2a.TaskArtifactUpdateEvent, _ []byte) {
	a.tasksMu.Lock()
	ts, ok := a.tasks[taskID]
	a.tasksMu.Unlock()
	if !ok {
		return
	}
	for _, part := range event.Artifact.Parts {
		if part.Kind == "file" {
			a.handleFilePart(ctx, taskID, ts, part)
		}
	}
}

func (a *Adapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, _ []byte) {
	a.tasksMu.Lock()
	ts, ok := a.tasks[taskID]
	a.tasksMu.Unlock()
	if !ok {
		return
	}

	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			if part.Kind == "file" {
				a.handleFilePart(ctx, taskID, ts, part)
			}
		}
	}

	if a.removeTask(taskID) == nil {
		return
	}

	var text string
	if task.Status.Message != nil {
		var sb strings.Builder
		for _, part := range task.Status.Message.Parts {
			if part.Kind == "text" {
				sb.WriteString(part.Text)
			}
		}
		text = sb.String()
	}
	if text == "" {
		ts.mu.Lock()
		text = ts.textBuf.String()
		ts.mu.Unlock()
	}

	text = a.resolveFinalText(ctx, taskID, text, ts)

	var structured map[string]any
	if ts.isWorkflow && task.Status.State == a2a.TaskStateCompleted {
		structured, text = a.withWorkflowOutput(taskID, task.Status.Message, text)
	}

	ts.mu.Lock()
	contents := ts.contents
	ts.mu.Unlock()

	if text == "" && len(contents) == 0 {
		text = "(no response)"
	}

	select {
	case ts.done <- taskResult{
		text:         text,
		contents:     contents,
		structured:   structured,
		unsuccessful: endedUnsuccessfully(task.Status.State),
	}:
	default:
	}
}

func endedUnsuccessfully(state a2a.TaskState) bool {
	switch state {
	case a2a.TaskStateFailed, a2a.TaskStateCancelled, a2a.TaskStateRejected:
		return true
	}
	return false
}

func (a *Adapter) withWorkflowOutput(taskID string, msg *a2a.Message, text string) (map[string]any, string) {
	output := finalResultData(msg)
	if output == nil {
		return nil, text
	}
	rendered, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		a.logger.Warn("mcp adapter: workflow output not serializable", "taskID", taskID, "error", err)
		return nil, text
	}
	if text != "" {
		text += "\n\n"
	}
	return output, text + string(rendered)
}

var controlDataTypes = map[string]bool{
	string(a2a.SignalAuthenticationRequired):   true,
	string(a2a.SignalArtifactCreationProgress): true,
	string(a2a.SignalStructuredInvocationRes):  true,
}

func finalResultData(msg *a2a.Message) map[string]any {
	if msg == nil {
		return nil
	}
	var results []any
	for _, part := range msg.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		if m, ok := part.Data.(map[string]any); ok {
			if t, _ := m["type"].(string); controlDataTypes[t] || len(m) == 0 {
				continue
			}
		}
		results = append(results, part.Data)
	}
	if len(results) == 0 {
		return nil
	}
	if structuredObject, ok := results[0].(map[string]any); ok && len(results) == 1 {
		return structuredObject
	}
	return map[string]any{"results": results}
}

func (a *Adapter) OnError(_ context.Context, taskID string, taskErr *a2a.Error, _ []byte) {
	ts := a.removeTask(taskID)
	if ts == nil {
		return
	}

	select {
	case ts.done <- taskResult{err: taskErr}:
	default:
	}
}

func (a *Adapter) HoldsTask(taskID string) bool {
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	_, ok := a.tasks[taskID]
	return ok
}

func (a *Adapter) removeTask(taskID string) *taskState {
	a.tasksMu.Lock()
	ts, ok := a.tasks[taskID]
	if ok {
		delete(a.tasks, taskID)
	}
	a.tasksMu.Unlock()
	if !ok {
		return nil
	}
	a.taskRegistry().Remove(taskID)
	return ts
}

func (a *Adapter) taskRegistry() entrypoint.TaskRegistry {
	if a.coreTasksOverride != nil {
		return a.coreTasksOverride
	}
	return a.core.Tasks()
}

func (a *Adapter) resolveSessionID(ctx context.Context, externalKey, agentName, userID string) (string, error) {
	if a.sessKeys == nil {
		return idgen.New(), nil
	}
	return a.sessKeys.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeMCP,
		ExternalKey: externalKey,
		UserID:      userID,
		AgentID:     &agentName,
	})
}

func (a *Adapter) handleToolCall(ctx context.Context, toolName string, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, refusal, err := a.callTool(ctx, toolName, request)
	a.core.ReportSubmitRefusal(refusal)
	return result, err
}

const mcpNoIdentityUserID = "mcp_user"

func toolCallCaller(ctx context.Context) *auth.Subject {
	if caller, ok := auth.SubjectFromContext(ctx); ok && caller != nil {
		return caller
	}
	return auth.ForUserIDOnly(mcpNoIdentityUserID)
}

func toolCallAttribution(caller *auth.Subject) string {
	if caller.UserID() != "" {
		return caller.UserID()
	}
	return mcpNoIdentityUserID
}

func (a *Adapter) callTool(ctx context.Context, toolName string, request mcp.CallToolRequest) (*mcp.CallToolResult, string, error) {
	message := request.GetString("message", "")
	if message == "" {
		return mcp.NewToolResultError("message parameter is required"), taxonomy.RejectMalformed, nil
	}
	files, err := decodeInputFiles(request, a.cfg)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), taxonomy.RejectMalformed, nil
	}

	a.toolMu.RLock()
	mapping, ok := a.toolMap[toolName]
	a.toolMu.RUnlock()
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("unknown tool %q", toolName)), taxonomy.RejectAgentNotFound, nil
	}

	taskID := idgen.New()
	caller := toolCallCaller(ctx)
	userID := toolCallAttribution(caller)

	conv, err := a.resolveConversation(ctx, request, mapping.agentName, userID)
	if errors.Is(err, errSessionIDNotOwned) {
		return mcp.NewToolResultError(unknownSessionIDMessage), taxonomy.RejectForbidden, nil
	}
	if errors.Is(err, errUnknownSessionID) {
		return mcp.NewToolResultError(unknownSessionIDMessage), taxonomy.RejectMalformed, nil
	}
	if err != nil {
		return nil, taxonomy.RejectInternal, err
	}
	fileParts, err := a.storeInputFiles(ctx, files, userID, conv.sessionID)
	if err != nil {
		return withSessionID(mcp.NewToolResultError(err.Error()), conv.handle), taxonomy.RejectInternal, nil
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      "user",
		Parts:     append([]a2a.Part{a2a.NewTextPart(message)}, fileParts...),
		TaskID:    taskID,
		ContextID: conv.sessionID,
	}

	ts := &taskState{
		toolName:     toolName,
		isWorkflow:   mapping.isWorkflow,
		done:         make(chan taskResult, 1),
		agentName:    mapping.agentName,
		userID:       userID,
		sessionID:    conv.sessionID,
		mcpSessionID: conv.mcpSessionID,
		handle:       conv.handle,
		modern:       server.IsModernRequest(ctx),
	}
	a.tasksMu.Lock()
	a.tasks[taskID] = ts
	a.tasksMu.Unlock()

	if err := a.taskRegistry().Register(taskID, mapping.agentName, userID, entrypoint.TaskIdentity{}); err != nil {
		a.removeTask(taskID)
		return nil, entrypoint.SubmitFailureReason(err), fmt.Errorf("record task: %w", err)
	}

	if err := a.core.SubmitTaskAttributedTo(ctx, mapping.agentName, taskID, msg, caller, userID); err != nil {
		a.removeTask(taskID)
		if entrypoint.IsBudgetRefusal(err) || entrypoint.IsBudgetUnconfirmed(err) || entrypoint.IsAgentNotAvailable(err) {
			return mcp.NewToolResultError(a.core.SubmitFailureText(err, false)), entrypoint.SubmitFailureReason(err), nil
		}
		return nil, entrypoint.SubmitFailureReason(err), fmt.Errorf("submit task: %w", err)
	}

	timer := time.NewTimer(time.Duration(a.cfg.TaskTimeoutSeconds) * time.Second)
	defer timer.Stop()

	select {
	case result := <-ts.done:
		if result.err != nil {
			return withSessionID(mcp.NewToolResultError(result.err.Message), conv.handle), "", nil
		}
		res := buildToolResult(result.text, result.contents)
		res.IsError = result.unsuccessful
		if result.structured != nil {
			res.StructuredContent = result.structured
		}
		return withSessionID(res, conv.handle), "", nil
	case <-timer.C:
		a.removeTask(taskID)
		return withSessionID(mcp.NewToolResultError(fmt.Sprintf("task timed out after %ds", a.cfg.TaskTimeoutSeconds)), conv.handle), "", nil
	case <-ctx.Done():
		a.removeTask(taskID)
		return nil, "", ctx.Err()
	}
}

func (a *Adapter) defaultIdentity() *auth.Subject {
	var defaults *auth.McpDefaults
	if a.cfg != nil {
		defaults = &auth.McpDefaults{
			DefaultUserIdentity: a.cfg.DefaultUserIdentity,
			RunAs:               a.cfg.RunAs,
		}
	}
	var scopes auth.ScopeExpander
	if resolver := a.scopeResolverOrNil(); resolver != nil {
		scopes = resolver
	}
	return auth.ForMcpDefaultCaller(defaults, scopes, a.authzEnforced())
}

func (a *Adapter) plantDefaultIdentity(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if id, ok := auth.SubjectFromContext(ctx); !ok || id == nil {
			ctx = auth.ContextWithSubject(ctx, a.defaultIdentity())
		}
		return next(ctx, req)
	}
}

func resolveValidator(gatewayID, provider string, rawCfg map[string]any) (auth.TokenValidator, error) {
	factoryName := fmt.Sprintf("%s:%s", gatewayID, provider)
	validator, err := auth.CreateValidator(factoryName, rawCfg)
	if err != nil {
		return nil, fmt.Errorf("MCP adapter: enable_auth=true but no validator factory registered for %q (configure an auth provider to enable OAuth): %w", factoryName, err)
	}
	return validator, nil
}
