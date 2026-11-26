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
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"
	"github.com/maximhq/bifrost/core/schemas"
	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

const directConnectTimeout = time.Duration(MCPDefaultTimeoutSeconds) * time.Second

var errManagerNotInitialized = errors.New("MCP manager not initialized")

type directClient struct {
	cfg         *ToolsetConfig
	mu          sync.Mutex
	mcp         *mcpclient.Client
	transport   transport.Interface
	tools       map[string]mcpgo.Tool
	version     string
	authHandler *AuthHandler
	authFetch   authHeaderFetcher
}

type Manager struct {
	bifrost      bifrostMCP.MCPManagerInterface
	direct       map[string]*directClient
	authHandlers map[authSlot]*sharedAuthHandler
	logger       *slog.Logger
	mu           sync.RWMutex
	calls        sync.RWMutex
	clients      map[string]*sharedClient
	opening      map[string]chan struct{}

	bifrostRecovery  singleflight.Group
	recoveryMu       sync.Mutex
	failedReconnects map[string]failedReconnect

	stopMonitor context.CancelFunc
	monitorDone sync.WaitGroup
}

func NewManager(logger *slog.Logger) *Manager {
	mcpConfig := schemas.MCPConfig{}
	mgr := bifrostMCP.NewMCPManager(context.Background(), mcpConfig, nil, nil, nil)

	m := &Manager{
		bifrost:      mgr,
		direct:       make(map[string]*directClient),
		authHandlers: make(map[authSlot]*sharedAuthHandler),
		clients:      make(map[string]*sharedClient),
		opening:      make(map[string]chan struct{}),
		logger:       logger,
	}
	m.startSessionMonitor()
	return m
}

type authSlot struct {
	credentialKey string
	server        string
}

type sharedAuthHandler struct {
	handler  *AuthHandler
	identity authIdentity
}

func authSlotOf(cfg *ToolsetConfig) (authSlot, bool) {
	if cfg == nil || cfg.AuthConfig == nil {
		return authSlot{}, false
	}
	return authSlot{credentialKey: cfg.AuthConfig.CredentialKey, server: defaultCredentialKeyBase(cfg)}, true
}

func (m *Manager) checkAuthSlotLocked(cfg *ToolsetConfig) error {
	slot, ok := authSlotOf(cfg)
	if !ok {
		return nil
	}
	existing, ok := m.authHandlers[slot]
	if !ok {
		return nil
	}
	if diffs := authDifferences(existing.identity, *cfg.authIdentity()); len(diffs) > 0 {
		return credentialSlotConflict(cfg, diffs)
	}
	return nil
}

func (m *Manager) authHandlerForLocked(cfg *ToolsetConfig) *AuthHandler {
	slot, ok := authSlotOf(cfg)
	if !ok {
		return nil
	}
	if shared, ok := m.authHandlers[slot]; ok {
		return shared.handler
	}
	shared := &sharedAuthHandler{handler: NewAuthHandler(cfg.AuthConfig, m.logger), identity: *cfg.authIdentity()}
	m.authHandlers[slot] = shared
	return shared.handler
}

func (m *Manager) authHandlerFor(cfg *ToolsetConfig) *AuthHandler {
	slot, ok := authSlotOf(cfg)
	if !ok {
		return nil
	}
	if shared, ok := m.authHandlers[slot]; ok {
		return shared.handler
	}
	return nil
}

func (m *Manager) Start(_ context.Context) error {
	return nil
}

func (m *Manager) Stop() {
	m.stopSessionMonitor()

	m.calls.Lock()
	defer m.calls.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, dc := range m.direct {
		if dc.mcp == nil {
			continue
		}
		if err := dc.mcp.Close(); err != nil {
			m.logger.Warn("direct MCP client close error", "clientID", id, "error", err)
		}
	}
	m.direct = nil

	if m.bifrost == nil {
		return
	}

	if err := m.bifrost.Cleanup(); err != nil {
		m.logger.Warn("MCP manager cleanup error", "error", err)
	}
	m.bifrost = nil
	m.logger.Info("MCP manager stopped")
}

func (m *Manager) AddClient(cfg *ToolsetConfig) error {
	legacyVersion := m.legacyVersionServed(cfg)

	if cfg.RequiresManifestForAuth() {
		return fmt.Errorf("add MCP client %q: streamable-http server with auth requires a manifest; tools/list is blocked until the user completes OAuth, so the agent cannot discover tools at startup — either provide manifest/manifest_file or switch to an SSE transport", cfg.ClientID)
	}

	if cfg.ConnectionType == connTypeStreamableHTTP && cfg.TerminateOnClose != nil && !*cfg.TerminateOnClose {
		m.logger.Warn("mcp terminate_on_close=false is not honored by mcp-go; DELETE will still be sent on client close of legacy-protocol sessions",
			"clientID", cfg.ClientID,
			"url", logging.RedactURL(cfg.URL),
		)
	}

	switch {
	case cfg.SSLVerify != nil && !*cfg.SSLVerify:
		m.logger.Warn("mcp ssl_config.verify=false; TLS verification disabled — do not use in production",
			"clientID", cfg.ClientID,
			"url", logging.RedactURL(cfg.URL),
		)
	case cfg.SSLVerify == nil && !cfg.EffectiveSSLVerify():
		m.logger.Warn("mcp TLS verification disabled via SAM_MCP_CONNECTOR_TLS_VERIFY=false — do not use in production",
			"clientID", cfg.ClientID,
			"url", logging.RedactURL(cfg.URL),
		)
	}

	for {
		m.mu.Lock()
		if m.bifrost == nil {
			m.mu.Unlock()
			return errManagerNotInitialized
		}
		if err := m.checkAuthSlotLocked(cfg); err != nil {
			m.mu.Unlock()
			return err
		}
		if existing, shared := m.clients[cfg.ClientID]; shared {
			err := m.joinClientLocked(existing, cfg)
			m.mu.Unlock()
			return err
		}
		if opened, opening := m.opening[cfg.ClientID]; opening {
			m.mu.Unlock()
			<-opened
			continue
		}
		if cfg.Manifest != nil {
			err := m.addManifestClientLocked(cfg)
			m.mu.Unlock()
			return err
		}
		opened := make(chan struct{})
		m.opening[cfg.ClientID] = opened
		b := m.bifrost
		m.mu.Unlock()
		return m.openClient(b, cfg, legacyVersion, opened)
	}
}

type sharedClient struct {
	cfg     *ToolsetConfig
	entries int
}

func (m *Manager) joinClientLocked(existing *sharedClient, cfg *ToolsetConfig) error {
	if diffs := connectionDifferences(existing.cfg, cfg); len(diffs) > 0 {
		return connectionConflict(cfg, diffs)
	}
	existing.entries++
	m.logger.Debug("MCP client shared by a further entry",
		"clientID", cfg.ClientID,
		"entries", existing.entries)
	return nil
}

func (m *Manager) openClient(b bifrostMCP.MCPManagerInterface, cfg *ToolsetConfig, legacyVersion string, opened chan struct{}) error {
	dc, dialErr := m.dialClient(b, cfg, legacyVersion)

	m.mu.Lock()
	err := dialErr
	if dialErr == nil {
		err = m.publishClientLocked(cfg, dc)
	}
	delete(m.opening, cfg.ClientID)
	close(opened)
	m.mu.Unlock()

	if dialErr == nil && err != nil {
		closeUnpublished(b, cfg, dc)
	}
	return err
}

func (m *Manager) dialClient(b bifrostMCP.MCPManagerInterface, cfg *ToolsetConfig, legacyVersion string) (*directClient, error) {
	if connectsDirectly(cfg) {
		return m.dialDirectClient(cfg, "")
	}

	if legacyVersion != "" {
		m.logger.Info("MCP server serves only protocol versions before 2026-07-28; connecting it outside Bifrost",
			"clientID", cfg.ClientID, "protocolVersion", legacyVersion)
		return m.dialDirectClient(cfg, legacyVersion)
	}

	bc := cfg.ToBifrostClientConfig()
	if err := b.AddClient(context.Background(), bc); err != nil {
		return nil, fmt.Errorf("add MCP client %q: %w", cfg.ClientID, err)
	}
	return nil, nil
}

func (m *Manager) publishClientLocked(cfg *ToolsetConfig, dc *directClient) error {
	if m.bifrost == nil {
		return fmt.Errorf("add MCP client %q: MCP manager stopped while it connected", cfg.ClientID)
	}
	if err := m.checkAuthSlotLocked(cfg); err != nil {
		return err
	}
	authHandler := m.authHandlerForLocked(cfg)
	if dc != nil {
		dc.authHandler = authHandler
		m.direct[cfg.ClientID] = dc
		m.logger.Info("MCP client added (direct)",
			"clientID", cfg.ClientID,
			"type", cfg.ConnectionType,
			"tools", len(dc.tools),
			"protocolVersion", dc.mcp.ProtocolVersion())
	} else {
		m.logger.Info("MCP client added", "clientID", cfg.ClientID, "type", cfg.ConnectionType)
	}
	m.clients[cfg.ClientID] = &sharedClient{cfg: cfg, entries: 1}
	return nil
}

func closeUnpublished(b bifrostMCP.MCPManagerInterface, cfg *ToolsetConfig, dc *directClient) {
	if dc != nil {
		_ = dc.mcp.Close()
		return
	}
	_ = b.RemoveClient(cfg.ClientID)
}

func (m *Manager) addManifestClientLocked(cfg *ToolsetConfig) error {
	if cfg.ConnectionType == connTypeStdio {
		return fmt.Errorf("add MCP client %q: manifest is not supported for stdio connections; stdio servers start as local subprocesses and discovery does not block on user auth", cfg.ClientID)
	}
	tools := make(map[string]mcpgo.Tool, len(cfg.Manifest))
	for _, mt := range cfg.Manifest {
		tools[mt.Name] = manifestToMCPGoTool(mt)
	}
	dc := &directClient{
		cfg:         cfg,
		tools:       tools,
		authHandler: m.authHandlerForLocked(cfg),
	}
	m.direct[cfg.ClientID] = dc
	m.clients[cfg.ClientID] = &sharedClient{cfg: cfg, entries: 1}
	m.logger.Info("MCP manifest loaded",
		"clientID", cfg.ClientID,
		"type", cfg.ConnectionType,
		"tools", len(tools))
	return nil
}

func manifestToMCPGoTool(mt ManifestTool) mcpgo.Tool {
	schema := mcpgo.ToolInputSchema{
		Type:       "object",
		Properties: map[string]any{},
	}
	if t, ok := mt.InputSchema["type"].(string); ok && t != "" {
		schema.Type = t
	}
	if props, ok := mt.InputSchema["properties"].(map[string]any); ok {
		schema.Properties = props
	}
	if req, ok := mt.InputSchema["required"].([]any); ok {
		for _, r := range req {
			if rs, ok := r.(string); ok {
				schema.Required = append(schema.Required, rs)
			}
		}
	}
	if defs, ok := mt.InputSchema["$defs"].(map[string]any); ok {
		schema.Defs = defs
	}
	return mcpgo.Tool{
		Name:        mt.Name,
		Description: mt.Description,
		InputSchema: schema,
	}
}

func (dc *directClient) invalidate(failed transport.Interface) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.transport != failed {
		return
	}
	if dc.mcp != nil {
		_ = dc.mcp.Close()
	}
	dc.transport = nil
	dc.mcp = nil
	dc.authFetch = nil
}

func withSSEReadTimeout(ctx context.Context, cfg *ToolsetConfig) (context.Context, context.CancelFunc) {
	if cfg == nil {
		return ctx, func() {}
	}
	if cfg.ConnectionType != connTypeSSE && cfg.ConnectionType != connTypeStreamableHTTP {
		return ctx, func() {}
	}
	d := cfg.EffectiveSSEReadTimeout()
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func isTransportAuthError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrAuthRequired) ||
		errors.Is(err, transport.ErrAuthorizationRequired) ||
		errors.Is(err, transport.ErrOAuthAuthorizationRequired) {
		return true
	}
	s := strings.ToLower(err.Error())
	return (strings.Contains(s, "401") && strings.Contains(s, "unauthorized")) ||
		(strings.Contains(s, "403") && strings.Contains(s, "forbidden")) ||
		strings.Contains(s, "status code: 401") ||
		strings.Contains(s, "status code: 403")
}

func (m *Manager) ensureConnected(ctx context.Context, dc *directClient, toolCtx *tool.Context) (*mcpclient.Client, error) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if dc.transport != nil {
		return dc.mcp, nil
	}

	if dc.authFetch == nil && dc.authHandler != nil && toolCtx != nil {
		ah := dc.authHandler
		tc := toolCtx
		dc.authFetch = func(rctx context.Context) (http.Header, error) {
			return ah.GetAuthHeaders(rctx, tc)
		}
	}

	var baseTransport http.RoundTripper
	if dc.cfg.HasSSLConfig() {
		sslClient, err := dc.cfg.BuildHTTPClient()
		if err != nil {
			dc.authFetch = nil
			return nil, fmt.Errorf("lazy connect %q: build HTTP client: %w", dc.cfg.ClientID, err)
		}
		baseTransport = sslClient.Transport
	}
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}

	var httpClient *http.Client
	switch {
	case dc.authFetch != nil:
		httpClient = &http.Client{
			Transport: &bearerRoundTripper{
				fetch: dc.authFetch,
				base:  baseTransport,
			},
		}
	case dc.cfg.HasSSLConfig():
		httpClient = &http.Client{Transport: baseTransport}
	default:
		httpClient = &http.Client{Transport: http.DefaultTransport}
	}

	httpClient.Transport = wrapWithByteCap(httpClient.Transport, dc.cfg, m.logger)

	var tr transport.Interface
	var err error
	switch dc.cfg.ConnectionType {
	case connTypeStreamableHTTP:
		opts := []transport.StreamableHTTPCOption{
			transport.WithHTTPBasicClient(httpClient),
			transport.WithHTTPMaxMessageBytes(unboundedTransportMessage),
		}
		if len(dc.cfg.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(dc.cfg.Headers))
		}
		tr, err = transport.NewStreamableHTTP(dc.cfg.URL, opts...)
	case connTypeSSE:
		opts := []transport.ClientOption{
			transport.WithHTTPClient(httpClient),
			transport.WithMaxMessageBytes(unboundedTransportMessage),
		}
		if len(dc.cfg.Headers) > 0 {
			opts = append(opts, transport.WithHeaders(dc.cfg.Headers))
		}
		tr, err = transport.NewSSE(dc.cfg.URL, opts...)
	default:
		return nil, fmt.Errorf("lazy connect %q: unsupported connection type %q", dc.cfg.ClientID, dc.cfg.ConnectionType)
	}
	if err != nil {
		return nil, fmt.Errorf("lazy connect %q: create transport: %w", dc.cfg.ClientID, err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, directConnectTimeout)
	defer cancel()

	transportCtx := context.WithoutCancel(ctx)

	client := mcpclient.NewClient(tr)
	if startErr := client.Start(transportCtx); startErr != nil {
		_ = client.Close()
		dc.authFetch = nil
		return nil, fmt.Errorf("lazy connect %q: start transport: %w", dc.cfg.ClientID, startErr)
	}

	if initErr := initializeDirect(connectCtx, client, dc.cfg, dc.version); initErr != nil {
		_ = client.Close()
		dc.authFetch = nil
		return nil, fmt.Errorf("lazy connect %q: initialize: %w", dc.cfg.ClientID, initErr)
	}

	dc.transport = tr
	dc.mcp = client

	m.logger.Info("MCP lazy connect succeeded",
		"clientID", dc.cfg.ClientID,
		"type", dc.cfg.ConnectionType,
		"tools", len(dc.tools),
		"protocolVersion", client.ProtocolVersion())
	return client, nil
}

func (m *Manager) dialDirectClient(cfg *ToolsetConfig, version string) (*directClient, error) {
	tr, err := m.newDirectTransport(cfg)
	if err != nil {
		return nil, fmt.Errorf("add MCP client %q: %w", cfg.ClientID, err)
	}

	client := mcpclient.NewClient(tr)

	ctx, cancel := context.WithTimeout(context.Background(), directConnectTimeout)
	defer cancel()

	if startErr := client.Start(ctx); startErr != nil {
		_ = client.Close()
		return nil, fmt.Errorf("add MCP client %q: start transport: %w", cfg.ClientID, startErr)
	}

	if initErr := initializeDirect(ctx, client, cfg, version); initErr != nil {
		_ = client.Close()
		return nil, fmt.Errorf("add MCP client %q: initialize: %w", cfg.ClientID, initErr)
	}

	listResp, err := client.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("add MCP client %q: list tools: %w", cfg.ClientID, err)
	}

	tools := make(map[string]mcpgo.Tool, len(listResp.Tools))
	for _, t := range listResp.Tools {
		tools[t.Name] = t
	}

	return &directClient{
		cfg:       cfg,
		mcp:       client,
		transport: tr,
		tools:     tools,
		version:   version,
	}, nil
}

func (m *Manager) newDirectTransport(cfg *ToolsetConfig) (transport.Interface, error) {
	httpClient, err := m.directHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	return newTransportOn(cfg, httpClient)
}

func (m *Manager) directHTTPClient(cfg *ToolsetConfig) (*http.Client, error) {
	httpClient, err := cfg.BuildHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("build HTTP client: %w", err)
	}
	httpClient.Transport = wrapWithByteCap(httpClient.Transport, cfg, m.logger)
	return httpClient, nil
}

func newTransportOn(cfg *ToolsetConfig, httpClient *http.Client) (transport.Interface, error) {
	var tr transport.Interface
	var err error
	switch cfg.ConnectionType {
	case connTypeStreamableHTTP:
		opts := []transport.StreamableHTTPCOption{
			transport.WithHTTPBasicClient(httpClient),
			transport.WithHTTPMaxMessageBytes(unboundedTransportMessage),
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(cfg.Headers))
		}
		tr, err = transport.NewStreamableHTTP(cfg.URL, opts...)
	case connTypeSSE:
		opts := []transport.ClientOption{
			transport.WithHTTPClient(httpClient),
			transport.WithMaxMessageBytes(unboundedTransportMessage),
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHeaders(cfg.Headers))
		}
		tr, err = transport.NewSSE(cfg.URL, opts...)
	default:
		return nil, fmt.Errorf("direct path does not support connection type %q", cfg.ConnectionType)
	}
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}
	return tr, nil
}

func connectsDirectly(cfg *ToolsetConfig) bool {
	return cfg.HasSSLConfig() && (cfg.ConnectionType == connTypeSSE || cfg.ConnectionType == connTypeStreamableHTTP)
}

func (m *Manager) legacyVersionServed(cfg *ToolsetConfig) string {
	if cfg.ConnectionType != connTypeStreamableHTTP || cfg.Manifest != nil || connectsDirectly(cfg) || cfg.RequiresManifestForAuth() {
		return ""
	}
	m.mu.RLock()
	_, shared := m.clients[cfg.ClientID]
	m.mu.RUnlock()
	if shared {
		return ""
	}

	version, err := m.probeLegacyVersion(cfg)
	if err != nil {
		m.logger.Debug("MCP protocol probe failed", "clientID", cfg.ClientID, "error", logging.UnwrapURLError(err))
	}
	return version
}

func (m *Manager) probeLegacyVersion(cfg *ToolsetConfig) (string, error) {
	httpClient, err := m.directHTTPClient(cfg)
	if err != nil {
		return "", err
	}
	defer httpClient.CloseIdleConnections()
	tr, err := newTransportOn(cfg, httpClient)
	if err != nil {
		return "", err
	}
	defer func() { _ = tr.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), discoverProbeTimeout)
	defer cancel()
	if err := tr.Start(ctx); err != nil {
		return "", fmt.Errorf("start transport: %w", err)
	}
	return legacyVersionServedOn(ctx, tr, directClientInfo(cfg))
}

func (m *Manager) CanConnect(ctx context.Context, cfg *ToolsetConfig) error {
	if cfg.ConnectionType != connTypeStreamableHTTP && cfg.ConnectionType != connTypeSSE {
		return nil
	}
	httpClient, err := m.directHTTPClient(cfg)
	if err != nil {
		return err
	}
	defer httpClient.CloseIdleConnections()
	tr, err := newTransportOn(cfg, httpClient)
	if err != nil {
		return err
	}
	client := mcpclient.NewClient(tr)
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(ctx, directConnectTimeout)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start transport: %w", err)
	}
	return InitializeServed(ctx, client, directClientInfo(cfg))
}

func IsPermanentConnectError(cfg *ToolsetConfig, err error) bool {
	return errors.Is(err, ErrConnectionConflict) ||
		isTransportAuthError(err) ||
		refusedOnAuthGroundsThroughBifrost(err) ||
		cfg.RequiresManifestForAuth() ||
		cfg.Manifest != nil ||
		cfg.ConnectionType == connTypeStdio
}

func refusedOnAuthGroundsThroughBifrost(err error) bool {
	return err != nil && strings.Contains(err.Error(), transport.ErrAuthorizationRequired.Error())
}

func (m *Manager) ProtocolVersion(cfg *ToolsetConfig) string {
	m.mu.RLock()
	dc, direct := m.direct[cfg.ClientID]
	var conn *mcpclient.Client
	if !direct && m.bifrost != nil {
		conn = bifrostConn(m.bifrost, cfg.ClientID)
	}
	m.mu.RUnlock()

	if direct {
		dc.mu.Lock()
		conn = dc.mcp
		dc.mu.Unlock()
	}
	if conn == nil {
		return ""
	}
	return conn.ProtocolVersion()
}

func (m *Manager) DiscoverTools(cfg *ToolsetConfig) ([]*MCPTool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.bifrost == nil {
		return nil, errManagerNotInitialized
	}

	var tools []*MCPTool

	sharedAuth := m.authHandlerFor(cfg)

	if dc, ok := m.direct[cfg.ClientID]; ok {
		return m.discoverDirectTools(dc, cfg, sharedAuth), nil
	}

	allTools := m.bifrost.GetAvailableTools(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline))

	var available []string
	for _, ct := range allTools {
		if ct.Function == nil {
			continue
		}

		bifrostName := ct.Function.Name
		if !strings.HasPrefix(bifrostName, cfg.ClientID+"-") {
			continue
		}

		originalName := strings.TrimPrefix(bifrostName, cfg.ClientID+"-")
		available = append(available, originalName)

		if !cfg.ShouldIncludeTool(originalName) {
			continue
		}

		samName := buildSAMToolName(originalName, cfg.ToolNamePrefix)

		tool := newMCPTool(samName, bifrostName, &ct, m, cfg, sharedAuth, m.logger)
		tools = append(tools, tool)
	}

	m.logger.Info("discovered MCP tools",
		"clientID", cfg.ClientID,
		"total", len(allTools),
		"filtered", len(tools))
	if len(tools) == 0 {
		m.warnNoTools(cfg, available)
	}

	return tools, nil
}

func (m *Manager) discoverDirectTools(dc *directClient, cfg *ToolsetConfig, sharedAuth *AuthHandler) []*MCPTool {
	var tools []*MCPTool
	var available []string
	source := "direct"
	if cfg.Manifest != nil {
		source = "manifest"
		for _, mt := range cfg.Manifest {
			available = append(available, mt.Name)
			if !cfg.ShouldIncludeTool(mt.Name) {
				continue
			}
			samName := buildSAMToolName(mt.Name, cfg.ToolNamePrefix)
			tools = append(tools, newManifestMCPTool(samName, cfg.ClientID+"-"+mt.Name, mt, m, cfg, sharedAuth, m.logger))
		}
	} else {
		available = slices.Sorted(maps.Keys(dc.tools))
		for _, originalName := range available {
			if !cfg.ShouldIncludeTool(originalName) {
				continue
			}
			samName := buildSAMToolName(originalName, cfg.ToolNamePrefix)
			tools = append(tools, newDirectMCPTool(samName, cfg.ClientID+"-"+originalName, dc.tools[originalName], m, cfg, sharedAuth, m.logger))
		}
	}
	m.logger.Info("discovered MCP tools",
		"clientID", cfg.ClientID,
		"source", source,
		"total", len(available),
		"filtered", len(tools))
	if len(tools) == 0 {
		m.warnNoTools(cfg, available)
	}
	return tools
}

const maxLoggedAvailableTools = 20

func (m *Manager) warnNoTools(cfg *ToolsetConfig, available []string) {
	entries := 0
	if shared, ok := m.clients[cfg.ClientID]; ok {
		entries = shared.entries
	}
	if len(available) == 0 {
		m.logger.Warn("MCP server offers no tools; the entry registered none",
			"clientID", cfg.ClientID,
			"server", serverForLog(cfg),
			"entriesOnServer", entries)
		return
	}
	slices.Sort(available)
	listed := available
	if len(listed) > maxLoggedAvailableTools {
		listed = listed[:maxLoggedAvailableTools]
	}
	m.logger.Warn("MCP entry's filter matched none of its server's tools; the entry registered none",
		"clientID", cfg.ClientID,
		"server", serverForLog(cfg),
		"toolName", cfg.ToolName,
		"allowList", cfg.AllowList,
		"denyList", cfg.DenyList,
		"toolNamePrefix", cfg.ToolNamePrefix,
		"entriesOnServer", entries,
		"available", len(available),
		"availableTools", listed)
}

func serverForLog(cfg *ToolsetConfig) string {
	if cfg.URL != "" {
		return logging.RedactURL(cfg.URL)
	}
	return cfg.Command
}

var requestIDCounter atomic.Int64

func (m *Manager) ExecuteToolRaw(ctx context.Context, bifrostName string, args map[string]any) (*rawCallToolResult, error) {
	return m.ExecuteToolRawWithHeaders(ctx, bifrostName, args, nil, nil)
}

func (m *Manager) ExecuteToolRawWithHeaders(ctx context.Context, bifrostName string, args map[string]any, headers http.Header, toolCtx *tool.Context) (*rawCallToolResult, error) {
	m.calls.RLock()
	defer m.calls.RUnlock()

	m.mu.RLock()
	b := m.bifrost
	directID, dc := m.directClientForLocked(bifrostName)
	m.mu.RUnlock()

	if b == nil {
		return nil, errManagerNotInitialized
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal tool args: %w", err)
	}
	var arguments map[string]any
	if unmarshalErr := json.Unmarshal(argsJSON, &arguments); unmarshalErr != nil {
		return nil, fmt.Errorf("unmarshal tool args: %w", unmarshalErr)
	}

	if dc != nil {
		return m.executeDirect(ctx, directID, dc, toolCtx, bifrostName, arguments, headers)
	}

	clients := b.GetClients()
	var client *schemas.MCPClientState
	var toolDef schemas.ChatTool
	for i := range clients {
		if clients[i].ExecutionConfig == nil {
			continue
		}
		if def, ok := b.GetToolsForClient(ctx, clients[i].ExecutionConfig.ID)[bifrostName]; ok {
			client = &clients[i]
			toolDef = def
			break
		}
	}
	if client == nil {
		return nil, fmt.Errorf("no MCP client found for tool %q", bifrostName)
	}
	conn := client.Conn
	if conn == nil && client.ExecutionConfig != nil {
		recovered, err := m.reconnectBifrostClient(ctx, b, client.ExecutionConfig.ID, nil)
		if err != nil {
			return nil, fmt.Errorf("MCP client for tool %q has no connection: %w", bifrostName, err)
		}
		conn = recovered
	}
	if conn == nil {
		return nil, fmt.Errorf("MCP client for tool %q has no connection", bifrostName)
	}

	originalName := strings.TrimPrefix(bifrostName, client.Name+"-")

	call := callInfo{def: bifrostToolDef(toolDef)}
	result, callErr := m.sendRawToolCall(ctx, conn, bifrostName, originalName, arguments, headers, call)
	failure := classifySessionFailure(callErr)
	if failure == sessionAlive || client.ExecutionConfig == nil {
		return result, callErr
	}

	clientID := client.ExecutionConfig.ID
	replacement, reconnectErr := m.reconnectBifrostClient(ctx, b, clientID, conn)
	if reconnectErr != nil {
		m.logger.Warn("MCP session lost and reconnect failed", "clientID", clientID, "error", reconnectErr)
		return nil, fmt.Errorf("%w (reconnect failed: %w)", callErr, reconnectErr)
	}
	m.logger.Warn("MCP session lost; reconnected", "clientID", clientID, "replayed", failure == sessionLostBeforeSend)
	if failure == sessionLostAfterSend {
		return nil, callErr
	}
	return m.sendRawToolCall(ctx, replacement, bifrostName, originalName, arguments, headers, call)
}

func (m *Manager) executeDirect(
	ctx context.Context,
	clientID string,
	dc *directClient,
	toolCtx *tool.Context,
	bifrostName string,
	arguments map[string]any,
	headers http.Header,
) (*rawCallToolResult, error) {
	originalName := strings.TrimPrefix(bifrostName, clientID+"-")
	clientInfo := directClientInfo(dc.cfg)
	call := callInfo{clientInfo: &clientInfo}
	if def, ok := dc.tools[originalName]; ok {
		call.def = &def
	}
	var lostCallErr error
	for {
		conn, err := m.ensureConnected(ctx, dc, toolCtx)
		if err != nil {
			if isTransportAuthError(err) {
				return nil, requireAuth(err)
			}
			if lostCallErr != nil {
				m.logger.Warn("MCP session lost and reconnect failed", "clientID", clientID, "error", err)
				return nil, fmt.Errorf("%w (reconnect failed: %w)", lostCallErr, err)
			}
			return nil, err
		}
		callCtx, cancel := withSSEReadTimeout(ctx, dc.cfg)
		tr := conn.GetTransport()
		result, err := m.sendRawToolCall(callCtx, conn, bifrostName, originalName, arguments, headers, call)
		cancel()
		if err != nil && isTransportAuthError(err) {
			dc.invalidate(tr)
			return result, requireAuth(err)
		}
		if result != nil && IsAuthError(result) {
			dc.invalidate(tr)
			return result, err
		}
		failure := classifySessionFailure(err)
		if failure == sessionAlive {
			return result, err
		}
		dc.invalidate(tr)
		replay := failure == sessionLostBeforeSend && lostCallErr == nil
		m.logger.Warn("MCP session lost; reconnecting", "clientID", clientID, "replayed", replay)
		if !replay {
			return nil, err
		}
		lostCallErr = err
	}
}

func (m *Manager) directClientForLocked(bifrostName string) (string, *directClient) {
	for id, dc := range m.direct {
		if strings.HasPrefix(bifrostName, id+"-") {
			return id, dc
		}
	}
	return "", nil
}

const toolCallFailedFormat = "MCP tool %q call failed: %w"

func requireAuth(err error) error {
	return fmt.Errorf("%w: %w", ErrAuthRequired, err)
}

func (m *Manager) sendRawToolCall(
	ctx context.Context,
	conn *mcpclient.Client,
	bifrostName, originalName string,
	arguments map[string]any,
	headers http.Header,
	call callInfo,
) (*rawCallToolResult, error) {
	tr := conn.GetTransport()
	params := mcpgo.CallToolParams{
		Name:      originalName,
		Arguments: arguments,
	}
	headers, err := addProtocolMeta(conn, &params, headers, call)
	if err != nil {
		return nil, fmt.Errorf(toolCallFailedFormat, bifrostName, err)
	}
	reqID := requestIDCounter.Add(1)
	rpcReq := transport.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcpgo.NewRequestId(reqID),
		Method:  "tools/call",
		Params:  params,
		Header:  headers,
	}

	rpcResp, err := tr.SendRequest(ctx, rpcReq)
	if err != nil {
		if tle, ok := errors.AsType[*responseTooLargeError](err); ok {
			return nil, fmt.Errorf(toolCallFailedFormat, bifrostName, tle)
		}
		if rejectsMissingSessionAsText(tr, err) {
			err = fmt.Errorf("%w: %w", errSessionMissing, err)
		}
		return nil, fmt.Errorf(toolCallFailedFormat, bifrostName, &transportError{err: err})
	}
	if rpcResp.Error != nil {
		if rejectsMissingSession(tr, rpcReq.ID, rpcResp) {
			return nil, fmt.Errorf(toolCallFailedFormat, bifrostName,
				&transportError{err: fmt.Errorf("%w: %s", errSessionMissing, rpcResp.Error.Message)})
		}
		return nil, fmt.Errorf("MCP tool %q RPC error: code=%d %s", bifrostName, rpcResp.Error.Code, rpcResp.Error.Message)
	}

	result, err := parseCallToolResultRaw(rpcResp.Result)
	if errors.Is(err, errInputRequired) {
		return nil, fmt.Errorf("MCP tool %q: %w", bifrostName, err)
	}
	if err != nil {
		return nil, fmt.Errorf("parse MCP tool %q result: %w", bifrostName, err)
	}
	return result, nil
}

var errInputRequired = errors.New("the server asked for more input before it can finish the call (resultType input_required), which this client does not support")

type rawCallToolResult struct {
	Content []json.RawMessage
	IsError bool

	StructuredContent json.RawMessage
}

func parseCallToolResultRaw(raw json.RawMessage) (*rawCallToolResult, error) {
	if raw == nil {
		return nil, fmt.Errorf("response is nil")
	}

	var envelope struct {
		Content           []json.RawMessage `json:"content"`
		IsError           bool              `json:"isError"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		ResultType        mcpgo.ResultType  `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}
	if envelope.ResultType == mcpgo.ResultTypeInputRequired {
		return nil, errInputRequired
	}

	return &rawCallToolResult{
		Content:           envelope.Content,
		IsError:           envelope.IsError,
		StructuredContent: envelope.StructuredContent,
	}, nil
}

func SAMToolName(originalName, prefix string) string {
	return buildSAMToolName(originalName, prefix)
}

func buildSAMToolName(originalName, prefix string) string {
	name := sanitizeName(originalName)
	if prefix != "" {
		if !strings.HasSuffix(prefix, "_") {
			prefix += "_"
		}
		name = prefix + name
	}
	return name
}
