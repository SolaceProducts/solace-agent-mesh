// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/egressguard"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	adapterType     = "agent_link"
	pingInterval    = 20 * time.Second
	peersRefresh    = 30 * time.Second
	idleTimeout     = 60 * time.Second
	sweepInterval   = time.Minute
	closedTaskGrace = 10 * time.Minute
	cancelGrace     = 2 * time.Minute
	outboxSize      = 256
)

func init() {
	entrypoint.RegisterAdapterFactory(adapterType, newOrRefusing)
}

var errFeatureDisabled = fmt.Errorf("agent link: the agent link entrypoint is disabled; set %s=true to enable it",
	features.EnvVarName(features.KeyAgentLinkEntrypoint))

func featureEnabled(svc runtime.Services) bool {
	if svc == nil {
		return false
	}
	reg := svc.FeatureRegistry()
	return reg != nil && reg.IsEnabled(features.KeyAgentLinkEntrypoint)
}

func newOrRefusing(raw map[string]any) (entrypoint.GatewayAdapter, error) {
	a, err := New(raw)
	if err != nil {
		return &Adapter{configErr: err}, nil
	}
	return a, nil
}

type Adapter struct {
	configErr        error
	cfg              *Config
	core             *entrypoint.GatewayCore
	logger           *slog.Logger
	onHandlerReady   func(http.Handler)
	roster           agentRoster
	upgrader         websocket.Upgrader
	httpClient       *http.Client
	now              func() time.Time
	controlWait      time.Duration
	publishWait      time.Duration
	cardRepublish    time.Duration
	describeInterval time.Duration
	leaseSettle      time.Duration
	startWakeRetry   time.Duration
	settleWait       time.Duration
	invocations      *structured.Handler
	unknownDigest    [32]byte

	ctx      context.Context
	cancel   context.CancelFunc
	started  atomic.Bool
	wg       sync.WaitGroup
	handlers sync.WaitGroup
	leading  atomic.Pointer[leaderTerm]

	runMu     sync.Mutex
	runClosed bool

	termMu   sync.Mutex
	term     *leaderTerm
	stopping bool
}

func New(raw map[string]any) (*Adapter, error) {
	cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("agent_link config: %w", err)
	}
	a := &Adapter{
		cfg:              cfg,
		roster:           newAgentRoster(),
		httpClient:       wakeHTTPClient(cfg.PlatformManaged),
		now:              time.Now,
		controlWait:      controlLeaseWait,
		publishWait:      defaultPublishWait,
		cardRepublish:    cardRepublishInterval,
		describeInterval: describePublishInterval,
		leaseSettle:      defaultLeaseSettle,
		startWakeRetry:   wakeInterval,
		settleWait:       defaultSettleWait,
	}
	a.upgrader = websocket.Upgrader{
		Subprotocols:    []string{subprotocol},
		ReadBufferSize:  32 << 10,
		WriteBufferSize: 32 << 10,
		CheckOrigin:     func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
	}
	a.unknownDigest = sha256.Sum256([]byte(idgen.New()))
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx, a.cancel = ctx, cancel
	return a, nil
}

func (a *Adapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
}

func (a *Adapter) SetOnHandlerReady(cb func(http.Handler)) { a.onHandlerReady = cb }

func (a *Adapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	if a.configErr != nil {
		return a.configErr
	}
	if !featureEnabled(core.Services()) {
		a.cancel()
		a.configErr = errFeatureDisabled
		return a.configErr
	}
	a.core = core
	a.logger = core.Logger().With("logger_name", "agent-link")
	invocations, err := structured.NewHandler(structured.HandlerConfig{Logger: a.logger})
	if err != nil {
		return fmt.Errorf("agent link: structured invocations: %w", err)
	}
	a.invocations = invocations
	for _, cfg := range a.cfg.Agents {
		a.roster.put(newAgentState(a, cfg))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/connect", a.handleConnect)
	a.logger.Info("agent link initialized", "gatewayID", core.GatewayID(), "agents", a.roster.size())
	if a.onHandlerReady != nil {
		a.onHandlerReady(mux)
	}
	return nil
}

func (a *Adapter) Start(_ context.Context) error {
	if a.configErr != nil {
		return a.configErr
	}
	if !a.started.CompareAndSwap(false, true) {
		return errRestartInPlace
	}
	if a.cfg.PlatformManaged {
		a.startEachAgentAlone()
	} else if err := a.startEveryAgent(); err != nil {
		a.cancel()
		return err
	}
	a.warnOfIgnoredProxy()
	a.goRun(a.sweep)
	if a.cfg.PlatformManaged {
		a.goRun(a.retryFailedStarts)
	}
	if a.leadsOnly() {
		a.logger.Info("agent link started; the leader serves agent connections", "agents", a.roster.size())
	}
	return nil
}

func (a *Adapter) startEveryAgent() error {
	for _, s := range a.roster.all() {
		if err := s.start(a.ctx); err != nil {
			return fmt.Errorf("agent link: start %s: %w", s.cfg.Name, err)
		}
	}
	return nil
}

func (a *Adapter) startEachAgentAlone() {
	a.roster.beginChange()
	defer a.roster.endChange()
	for _, s := range a.roster.all() {
		if err := a.bringUp(a.ctx, s); err != nil {
			a.warnOfFailedStart(s.cfg.Name, err)
		}
	}
}

const stoppedWhileAgentsChanged = "agent link stopping before a change to its agents finished; agents that change started are closed as they are"

const stoppingWithOpenTasks = "agent link stopping with tasks its agents had not answered"

var errRestartInPlace = errors.New("agent link: a stopped agent link cannot start again in place; restart the gateway process")

func (a *Adapter) Stop(ctx context.Context) error {
	if a.configErr != nil {
		return nil
	}
	openBeforeStopping := a.openTasks()
	a.endTermBeforeStopping(ctx)
	a.started.Store(true)
	a.cancel()
	agents, settled := a.roster.settled(ctx)
	if !settled {
		a.logger.Warn(stoppedWhileAgentsChanged, "agents", len(agents))
	}
	for _, s := range agents {
		for _, c := range s.connections() {
			c.close(websocket.CloseGoingAway, "gateway stopping")
		}
		s.router.CancelAllWithPropagation(s.ns())
	}
	if openBeforeStopping > 0 {
		a.logger.Warn(stoppingWithOpenTasks, "openTasks", openBeforeStopping)
	}
	done := make(chan struct{})
	go func() {
		a.handlers.Wait()
		a.refuseNewRuns()
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		a.logger.Warn("agent link stopped before its work drained", "openTasks", a.openTasks())
	}
	return nil
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

const (
	allowPrivateWakeEnv = "SAM_AGENT_LINK_ALLOW_PRIVATE_WAKE"
	wakeDialTimeout     = 15 * time.Second
	wakeDialKeepAlive   = 30 * time.Second
	wakeTLSTimeout      = 10 * time.Second
)

func allowPrivateWakeTargets() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(allowPrivateWakeEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

const wakeProxyIgnored = "agent link wake calls of platform-managed agents do not use the HTTP proxy set in the environment, because a proxy would bypass the address check"

var proxyEnvVars = []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"}

func (a *Adapter) warnOfIgnoredProxy() {
	if !a.cfg.PlatformManaged {
		return
	}
	var set []string
	for _, name := range proxyEnvVars {
		if os.Getenv(name) != "" {
			set = append(set, name)
		}
	}
	if len(set) > 0 {
		a.logger.Warn(wakeProxyIgnored, "variables", set)
	}
}

func wakeHTTPClient(platformManaged bool) *http.Client {
	client := &http.Client{CheckRedirect: refuseRedirect}
	if !platformManaged {
		return client
	}
	client.Transport = &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   wakeDialTimeout,
			KeepAlive: wakeDialKeepAlive,
			Control:   egressguard.DialControl(allowPrivateWakeTargets()),
		}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: wakeTLSTimeout,
	}
	return client
}

const headerRetryAfter = "Retry-After"

const maxLoggedAddress = 256

const refusedTooManyConnections = "too many connections"

func (a *Adapter) warnRefused(r *http.Request, agentName, reason string, more ...any) {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peer = host
	}
	client := peer
	if first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); strings.TrimSpace(first) != "" {
		client = strings.TrimSpace(first)
		if len(client) > maxLoggedAddress {
			client = client[:maxLoggedAddress]
		}
	}
	a.logger.Warn("agent link connection refused", append([]any{"agentName", agentName, "reason", reason,
		"clientIP", client, "peerIP", peer}, more...)...)
}

func (a *Adapter) openTasks() int {
	open := 0
	for _, s := range a.roster.all() {
		open += s.openTasks()
	}
	return open
}

func (a *Adapter) goRun(fn func(context.Context)) bool {
	a.runMu.Lock()
	if a.runClosed {
		a.runMu.Unlock()
		return false
	}
	a.wg.Add(1)
	a.runMu.Unlock()
	go func() {
		defer a.wg.Done()
		fn(a.ctx)
	}()
	return true
}

func (a *Adapter) refuseNewRuns() {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.runClosed = true
}

func (a *Adapter) sweep(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sweepOnce(a.now())
		}
	}
}

func (a *Adapter) sweepOnce(now time.Time) {
	for _, s := range a.roster.all() {
		s.sweep(now)
		s.warnOfSharedName()
		s.goRun(s.recheckStopLease)
	}
}

func (a *Adapter) OnAgentDiscovered(card *a2a.AgentCard) {
	if card == nil {
		return
	}
	a.peerChanged(card.Name)
}

func (a *Adapter) OnAgentRemoved(name string) { a.peerChanged(name) }

func (a *Adapter) peerChanged(name string) {
	for _, s := range a.roster.all() {
		if s.cfg.allowsPeer(name) {
			s.pushPeers()
		}
	}
}

func (a *Adapter) OnStatusUpdate(_ context.Context, taskID string, _ *a2a.TaskStatusUpdateEvent, _ []byte) {
	a.ignoreUnaskedReply("status", taskID)
}

func (a *Adapter) OnArtifactUpdate(_ context.Context, taskID string, _ *a2a.TaskArtifactUpdateEvent, _ []byte) {
	a.ignoreUnaskedReply("artifact", taskID)
}

func (a *Adapter) OnFinalResponse(_ context.Context, taskID string, _ *a2a.Task, _ []byte) {
	a.ignoreUnaskedReply("final response", taskID)
}

func (a *Adapter) OnError(_ context.Context, taskID string, _ *a2a.Error, _ []byte) {
	a.ignoreUnaskedReply("error", taskID)
}

func (a *Adapter) ignoreUnaskedReply(kind, taskID string) {
	if a.logger == nil {
		return
	}
	a.logger.Debug("agent link ignored a reply to its gateway id, which submits no tasks", "kind", kind, "taskID", taskID)
}

func (a *Adapter) handleConnect(w http.ResponseWriter, r *http.Request) {
	if a.ctx.Err() != nil {
		http.Error(w, "gateway stopping", http.StatusServiceUnavailable)
		return
	}
	if !a.serves() {
		a.logger.Debug("agent link refused a connection: this replica is not the leader")
		w.Header().Set(headerRetryAfter, followerRetryAfterSeconds)
		http.Error(w, msgNotLeading, http.StatusServiceUnavailable)
		return
	}
	a.handlers.Add(1)
	defer a.handlers.Done()
	name := r.Header.Get(headerAgent)
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s := a.agent(name)
	want := a.unknownDigest[:]
	if s != nil {
		want = s.cfg.TokenSHA256
	}
	got := sha256.Sum256([]byte(token))
	matched := subtle.ConstantTimeCompare(got[:], want) == 1
	if s == nil && token != "" && a.roster.isStarting(name, got[:]) {
		a.logger.Debug("agent link refused a connection: the agent has not started with its new settings yet", "agentName", name)
		w.Header().Set(headerRetryAfter, followerRetryAfterSeconds)
		http.Error(w, msgAgentRestarting, http.StatusServiceUnavailable)
		return
	}
	if s == nil || token == "" || !matched {
		claimed := name
		if !agentNamePattern.MatchString(claimed) {
			claimed = "(invalid name)"
		}
		a.auditConnect(r.Context(), claimed, false, "invalid agent credentials")
		a.warnRefused(r, claimed, "invalid credentials")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.retired() {
		a.logger.Debug("agent link refused a connection: the agent is restarting with new settings", "agentName", name)
		w.Header().Set(headerRetryAfter, followerRetryAfterSeconds)
		http.Error(w, msgAgentRestarting, http.StatusServiceUnavailable)
		return
	}
	if !s.cfg.Enabled {
		a.auditConnect(r.Context(), name, false, "agent disabled")
		a.warnRefused(r, name, "agent disabled")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.admitConnect() {
		a.auditConnect(r.Context(), name, false, "connection rate exceeded")
		a.warnRefused(r, name, "connection rate exceeded", "maxConnectsPerMinute", a.cfg.MaxConnectsPerMinute)
		http.Error(w, refusedTooManyConnections, http.StatusTooManyRequests)
		return
	}
	ws, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		a.warnRefused(r, name, "upgrade failed", "error", err)
		return
	}
	if ws.Subprotocol() != subprotocol {
		a.auditConnect(r.Context(), name, false, "unsupported subprotocol")
		a.warnRefused(r, name, "unsupported subprotocol")
		closeNow(ws, closeProtocolError, "subprotocol "+subprotocol+" required")
		return
	}
	c := newConn(a, s, ws)
	if !s.work.enter() {
		closeNow(ws, websocket.CloseServiceRestart, closedForRedeploy)
		return
	}
	defer s.work.leave()
	switch s.join(c) {
	case joinRefusedRetired:
		a.logger.Debug("agent link closed a connection opened as its agent was removed or restarted", "agentName", name)
		closeNow(ws, websocket.CloseServiceRestart, closedForRedeploy)
		return
	case joinRefusedNotLeading:
		a.logger.Debug("agent link closed a connection opened as this replica stopped leading", "agentName", name)
		closeNow(ws, websocket.CloseServiceRestart, closedForHandover)
		return
	case joinRefusedFull:
		a.auditConnect(r.Context(), name, false, refusedTooManyConnections)
		a.warnRefused(r, name, refusedTooManyConnections, "maxConnectionsPerAgent", a.cfg.MaxConnectionsPerAgent)
		closeNow(ws, closeTooManyConns, "too many connections for this agent")
		return
	}
	a.auditConnect(r.Context(), name, true, "")
	a.logger.Debug("agent connected", "agentName", name, "connectionID", c.id)
	go c.writeLoop()
	s.relistOnConnect(s.ctx)
	go s.refreshPeers(c)
	c.readLoop()
	s.removeConn(c)
	a.logger.Debug("agent disconnected", "agentName", name, "connectionID", c.id, "closeReason", c.closedFor)
}

func (a *Adapter) limits() *linkLimits {
	return &linkLimits{
		MaxFrameBytes:            a.cfg.MaxFrameBytes,
		MaxStringBytes:           maxStringBytes,
		MaxIDBytes:               maxIDBytes,
		MaxNameBytes:             maxNameBytes,
		MaxToolCallsPerModelCall: maxToolCallsPerModelCall,
		MaxModelsPerTask:         maxModelsPerTask,
		MaxOpenPeerCalls:         maxOpenSubtasks,
		MaxOpenPrompts:           maxOpenPrompts,
		MaxToolScopes:            maxToolScopes,
		MaxFailureMessageBytes:   maxFailureMessageBytes,
		MaxFrameValues:           maxFrameValues,
		MaxPromptQuestions:       maxPromptQuestions,
		MaxPromptOptions:         maxPromptOptions,
		MaxPromptTextBytes:       maxPromptTextBytes,
		MaxQuestionHeaderChars:   maxQuestionHeaderChars,
		MaxApprovalArgs:          maxApprovalArgs,
		MaxApprovalArgsBytes:     maxApprovalArgsBytes,
		MaxDescriptionBytes:      maxDescriptionBytes,
		MaxSkills:                maxSkills,
		MaxSkillDescriptionBytes: maxSkillDescriptionBytes,
		TaskTTLSeconds:           int(a.cfg.TaskTTL / time.Second),
		MaxCallDepth:             maxCallDepth,
		MaxConnectionsPerAgent:   a.cfg.MaxConnectionsPerAgent,
		MaxToolPayloadBytes:      maxToolPayloadBytes,
	}
}

func (a *Adapter) auditConnect(ctx context.Context, agentName string, ok bool, reason string) {
	a.core.Services().AuditLogger().LogAuth(ctx, auth.AuthEvent{
		UserID:  "agent:" + agentName,
		Success: ok,
		Reason:  reason,
	})
}

func closeNow(ws *websocket.Conn, code int, reason string) {
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = ws.Close()
}

var (
	_ entrypoint.GatewayAdapter      = (*Adapter)(nil)
	_ entrypoint.DeferredHTTPAdapter = (*Adapter)(nil)
	_ entrypoint.Reconfigurer        = (*Adapter)(nil)
)
