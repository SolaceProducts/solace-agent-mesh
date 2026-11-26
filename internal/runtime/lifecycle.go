// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type Runtime struct {
	mu             sync.Mutex
	components     []namedComponent
	startedCount   int
	services       *runtimeServices
	logger         *slog.Logger
	phase          Phase
	gracePeriod    time.Duration
	gracePeriodSet bool
	draining       atomic.Bool

	startupAbortTimeout time.Duration
	budgetWatcher       Component

	startedHooks []func()
}

type ReadinessGate interface {
	ReadinessOpen() bool
}

func (r *Runtime) OnStarted(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startedHooks = append(r.startedHooks, fn)
}

type namedComponent struct {
	name string
	comp Component
}

type Phase int

const (
	PhaseCreated Phase = iota
	PhaseInitialized
	PhaseRunning
	PhaseStopping
	PhaseStopped
)

func (p Phase) String() string {
	switch p {
	case PhaseCreated:
		return "created"
	case PhaseInitialized:
		return "initialized"
	case PhaseRunning:
		return "running"
	case PhaseStopping:
		return "stopping"
	case PhaseStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

type Option func(*Runtime)

func WithGracePeriod(d time.Duration) Option {
	return func(r *Runtime) {
		r.gracePeriod = d
		r.gracePeriodSet = true
	}
}

const (
	graceSecondsEnv        = "SAM_TERMINATION_GRACE_SECONDS"
	preStopSleepSecondsEnv = "SAM_PRESTOP_SLEEP_SECONDS"

	defaultGracePeriod = 30 * time.Second
	gracePeriodMargin  = 5 * time.Second
	stopTail           = 5 * time.Second
)

func gracePeriodFromEnv(logger *slog.Logger) time.Duration {
	grace, graceSet, err := envSeconds(graceSecondsEnv)
	if err != nil {
		logger.Warn("invalid termination grace period; using the default", "error", err, "default", defaultGracePeriod)
	}
	preStop, preStopSet, err := envSeconds(preStopSleepSecondsEnv)
	if err != nil {
		logger.Warn("invalid preStop sleep; counting none against the grace period", "error", err)
	}
	if !graceSet && !preStopSet {
		return defaultGracePeriod
	}
	if !graceSet {
		grace = defaultGracePeriod
	}
	left := grace - preStop
	if left <= 0 {
		logger.Warn("preStop sleep leaves no termination grace period; shutdown will not drain", "grace", grace, "preStop", preStop)
		return 0
	}
	return max(left-gracePeriodMargin, left/2)
}

func envSeconds(name string) (d time.Duration, ok bool, err error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("%s=%q is not a non-negative whole number of seconds", name, raw)
	}
	return time.Duration(n) * time.Second, true, nil
}

func WithLogger(logger *slog.Logger) Option {
	return func(r *Runtime) {
		r.logger = logger
	}
}

func WithLLM(model string, client llm.Client) Option {
	return func(r *Runtime) {
		r.services.llmClients[model] = client
		if r.services.defaultLLM == nil {
			r.services.defaultLLM = client
		}
	}
}

func WithDefaultLLM(client llm.Client) Option {
	return func(r *Runtime) {
		r.services.defaultLLM = client
	}
}

func WithArtifacts(svc *artifact.ScopedService) Option {
	return func(r *Runtime) {
		r.services.artifacts = svc
	}
}

func WithConnectorSpecStore(s ConnectorSpecStore) Option {
	return func(r *Runtime) {
		r.services.connectorSpecStore = s
	}
}

func WithSessions(store session.Store) Option {
	return func(r *Runtime) {
		r.services.sessions = store
		if c, ok := store.(interface{ Close() error }); ok {
			r.Register("session_store_close", shutdownComponent{
				shutdown: func(context.Context) error { return c.Close() },
			})
		}
	}
}

func WithFeatureRegistry(r *features.Registry) Option {
	return func(rt *Runtime) {
		rt.services.featureRegistry = r
	}
}

func WithVolumes(svc volume.Service) Option {
	return func(r *Runtime) {
		r.services.volumes = svc
	}
}

func WithModelResolver(resolver ModelAliasResolver) Option {
	return func(r *Runtime) {
		r.services.modelResolver = resolver
	}
}

func WithBudgetEnforcement(gate *budget.Gate, watcher Component) Option {
	return func(r *Runtime) {
		r.services.budgetGate = gate
		r.budgetWatcher = watcher
	}
}

func WithBudgetGate(gate *budget.Gate) Option {
	return func(r *Runtime) { r.services.budgetGate = gate }
}

func WithBindings(b ModelBindings) Option {
	return func(r *Runtime) {
		r.services.bindings = b
	}
}

func WithUsageReporter(svc Component) Option {
	return func(r *Runtime) {
		if isNilComponent(svc) {
			return
		}
		r.services.usageReporter = svc
		r.Register("usage_reporter", svc)
	}
}

func isNilComponent(c Component) bool {
	if c == nil {
		return true
	}
	switch v := reflect.ValueOf(c); v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

func WithAuthenticator(a auth.Authenticator) Option {
	return func(r *Runtime) {
		r.services.authenticator = a
	}
}

func WithToolAuthorizer(a auth.ToolAuthorizer) Option {
	return func(r *Runtime) {
		r.services.toolAuthorizer = a
	}
}

func WithAgentAuthorizer(a auth.AgentAuthorizer) Option {
	return func(r *Runtime) {
		r.services.agentAuthorizer = a
	}
}

func WithAuditLogger(a auth.AuditLogger) Option {
	return func(r *Runtime) {
		r.services.auditLogger = a
	}
}

func WithCredentialService(cs auth.CredentialService) Option {
	return func(r *Runtime) {
		r.services.credentialService = cs
	}
}

func WithHTTPClient(c *http.Client) Option {
	return func(r *Runtime) {
		r.services.httpClient = c
	}
}

func WithControlAuthorizer(a auth.ControlAuthorizer) Option {
	return func(r *Runtime) {
		r.services.controlAuthorizer = a
	}
}

func WithMetrics(m Metrics) Option {
	return func(r *Runtime) {
		r.services.metrics = m
		Init(m)
		if s, ok := m.(interface {
			Shutdown(context.Context) error
		}); ok {
			r.Register("metrics_flush", shutdownComponent{shutdown: s.Shutdown})
		}
	}
}

func NewRuntime(b broker.Broker, cfg config.Config, opts ...Option) *Runtime {
	r := &Runtime{
		services: &runtimeServices{
			broker:     b,
			config:     cfg,
			kvStore:    NewMapKVStore(),
			cache:      NewMapCache(),
			metrics:    NoopMetrics{},
			tracer:     NoopTracer{},
			llmClients: make(map[string]llm.Client),
		},
		logger:              slog.Default(),
		startupAbortTimeout: defaultStartupAbortTimeout,
	}
	for _, opt := range opts {
		opt(r)
	}
	if !r.gracePeriodSet {
		r.gracePeriod = gracePeriodFromEnv(r.logger)
	}
	if r.services.budgetGate == nil {
		r.services.budgetGate = budget.NewGate(r.logger)
	}
	if r.budgetWatcher != nil {
		r.Register("budget-watch", r.budgetWatcher)
	}
	r.services.logger = r.logger
	if r.services.broker != nil {
		if mc, ok := r.services.broker.(interface {
			SetMetrics(broker.MetricsRecorder)
		}); ok {
			mc.SetMetrics(r.services.metrics)
		}
	}
	return r
}

func (r *Runtime) Register(name string, comp Component) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = append(r.components, namedComponent{name: name, comp: comp})
}

func (r *Runtime) Init(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	start := time.Now()
	for _, nc := range r.components {
		r.logger.Info("initializing component", "name", nc.name)
		if err := nc.comp.Init(ctx, r.services.config); err != nil {
			r.logger.Error("component init failed", "name", nc.name, "error", err)
			return fmt.Errorf("init %s: %w", nc.name, err)
		}
	}
	r.phase = PhaseInitialized
	r.logger.Info("runtime initialized", "components", len(r.components), "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

func (r *Runtime) Start(ctx context.Context) error {
	if err := r.start(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	hooks := slices.Clone(r.startedHooks)
	r.mu.Unlock()
	for _, fn := range hooks {
		go fn()
	}
	return nil
}

func (r *Runtime) start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	start := time.Now()
	for i, nc := range r.components {
		r.logger.Info("starting component", "name", nc.name)
		if err := nc.comp.Start(ctx); err != nil {
			r.startedCount = i
			r.logger.Error("component start failed", "name", nc.name, "error", err)
			return fmt.Errorf("start %s: %w", nc.name, err)
		}
	}
	r.startedCount = len(r.components)
	r.phase = PhaseRunning
	r.logger.Info("runtime started", "components", len(r.components), "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.draining.Store(true)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.phase = PhaseStopping

	graceCtx, cancel := context.WithTimeout(ctx, r.gracePeriod)
	defer cancel()
	waitCtx, cancelWait := context.WithTimeout(graceCtx, max(r.gracePeriod-stopTail, r.gracePeriod/2))
	defer cancelWait()

	stopCount := r.startedCount
	r.logger.Info("stopping runtime", "components", stopCount, "gracePeriod", r.gracePeriod)
	var firstErr error
	for i := stopCount - 1; i >= 0; i-- {
		nc := r.components[i]
		r.logger.Info("stopping component", "name", nc.name)
		stopCtx := waitCtx
		if waitCtx.Err() != nil {
			stopCtx = graceCtx
		}
		if err := nc.comp.Stop(stopCtx); err != nil {
			r.logger.Error("stop component failed", "name", nc.name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("stop %s: %w", nc.name, err)
			}
		}
	}

	r.phase = PhaseStopped
	return firstErr
}

func (r *Runtime) IsDraining() bool {
	return r.draining.Load()
}

func (r *Runtime) Health() error {
	r.mu.Lock()
	snapshot := make([]namedComponent, len(r.components))
	copy(snapshot, r.components)
	r.mu.Unlock()

	for _, nc := range snapshot {
		err := nc.comp.Health()
		if err == nil || errors.Is(err, ErrDegraded) {
			continue
		}
		return fmt.Errorf("component %s unhealthy: %w", nc.name, err)
	}
	return nil
}

func (r *Runtime) Ready() bool {
	r.mu.Lock()
	phase := r.phase
	brk := r.services.broker
	r.mu.Unlock()

	if phase != PhaseRunning {
		return false
	}
	if brk != nil && !brk.IsConnected() {
		return false
	}
	return r.gatesOpen()
}

func (r *Runtime) gatesOpen() bool {
	r.mu.Lock()
	snapshot := make([]namedComponent, len(r.components))
	copy(snapshot, r.components)
	r.mu.Unlock()
	for _, nc := range snapshot {
		if gate, ok := nc.comp.(ReadinessGate); ok && !gate.ReadinessOpen() {
			return false
		}
	}
	return true
}

func (r *Runtime) Phase() Phase {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phase
}

func (r *Runtime) Services() Services {
	return r.services
}

type ComponentInfo struct {
	Name      string
	Component Component
}

func (r *Runtime) Components() []ComponentInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]ComponentInfo, len(r.components))
	for i, nc := range r.components {
		result[i] = ComponentInfo{Name: nc.name, Component: nc.comp}
	}
	return result
}

const defaultStartupAbortTimeout = 10 * time.Second

func (r *Runtime) Run(ctx context.Context) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	startupCtx, cancelStartup := context.WithCancel(ctx)
	defer cancelStartup()
	startupErr := make(chan error, 1)
	go func() { startupErr <- r.startup(startupCtx) }()
	select {
	case err := <-startupErr:
		if err != nil {
			_ = r.Stop(context.Background())
			return err
		}
	case sig := <-sigCh:
		r.logger.Warn("received signal during startup, cancelling it", "signal", sig)
		cancelStartup()
		return r.abortStartup(startupErr)
	case <-ctx.Done():
		r.logger.Info("context cancelled during startup")
		cancelStartup()
		return r.abortStartup(startupErr)
	}

	r.logger.Debug("waiting for shutdown signal")

	usr1Ch := make(chan os.Signal, 1)
	notifyDiagSignal(usr1Ch)
	defer signal.Stop(usr1Ch)

	for {
		select {
		case sig := <-sigCh:
			r.logger.Info("received signal, shutting down", "signal", sig)
			return r.Stop(context.Background())
		case <-usr1Ch:
			DumpDiagnostics(r.logger)
		case <-ctx.Done():
			r.logger.Info("context cancelled, shutting down")
			return r.Stop(context.Background())
		}
	}
}

func (r *Runtime) startup(ctx context.Context) error {
	if err := r.Init(ctx); err != nil {
		return err
	}
	return r.Start(ctx)
}

func (r *Runtime) abortStartup(startupErr <-chan error) error {
	select {
	case err := <-startupErr:
		stopErr := r.Stop(context.Background())
		if err == nil {
			return stopErr
		}
		return fmt.Errorf("startup cancelled: %w", err)
	case <-time.After(r.startupAbortTimeout):
		r.logger.Error("startup did not unwind after cancellation; exiting", "timeout", r.startupAbortTimeout)
		return errors.New("startup cancelled and did not unwind")
	}
}

type runtimeServices struct {
	broker             broker.Broker
	config             config.Config
	kvStore            KVStore
	cache              Cache
	metrics            Metrics
	tracer             Tracer
	defaultLLM         llm.Client
	llmClients         map[string]llm.Client
	artifacts          *artifact.ScopedService
	connectorSpecStore ConnectorSpecStore
	sessions           session.Store
	featureRegistry    *features.Registry
	usageReporter      Component

	volumes volume.Service

	modelResolver ModelAliasResolver

	budgetGate *budget.Gate

	bindings ModelBindings

	httpClient *http.Client

	logger *slog.Logger

	authenticator     auth.Authenticator
	toolAuthorizer    auth.ToolAuthorizer
	agentAuthorizer   auth.AgentAuthorizer
	auditLogger       auth.AuditLogger
	credentialService auth.CredentialService
	controlAuthorizer auth.ControlAuthorizer
}

func (s *runtimeServices) Broker() broker.Broker { return s.broker }

func (s *runtimeServices) Config() config.Config { return s.config }

func (s *runtimeServices) KVStore() KVStore { return s.kvStore }

func (s *runtimeServices) Cache() Cache { return s.cache }

func (s *runtimeServices) Metrics() Metrics { return s.metrics }

func (s *runtimeServices) Tracer() Tracer { return s.tracer }

func (s *runtimeServices) Logger() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

func (s *runtimeServices) LLM(model string) llm.Client {
	if model == "" {
		return s.defaultLLM
	}
	if c, ok := s.llmClients[model]; ok {
		return c
	}
	return s.defaultLLM
}

func (s *runtimeServices) Artifacts() *artifact.ScopedService { return s.artifacts }

func (s *runtimeServices) ConnectorSpecStore() ConnectorSpecStore { return s.connectorSpecStore }

func (s *runtimeServices) Sessions() session.Store { return s.sessions }

func (s *runtimeServices) FeatureRegistry() *features.Registry { return s.featureRegistry }

func (s *runtimeServices) UsageReporter() Component {
	return s.usageReporter
}

func (s *runtimeServices) ModelResolver() ModelAliasResolver { return s.modelResolver }

func (s *runtimeServices) Bindings() ModelBindings { return s.bindings }

func (s *runtimeServices) Volumes() volume.Service { return s.volumes }

func (s *runtimeServices) BudgetGate() *budget.Gate { return s.budgetGate }

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

func (s *runtimeServices) HTTPClient() *http.Client {
	if s.httpClient != nil {
		return s.httpClient
	}
	return defaultHTTPClient
}

func (s *runtimeServices) Authenticator() auth.Authenticator {
	if s.authenticator != nil {
		return s.authenticator
	}
	return auth.NoopAuthenticator{}
}

func (s *runtimeServices) ToolAuthorizer() auth.ToolAuthorizer {
	if s.toolAuthorizer != nil {
		return s.toolAuthorizer
	}
	return auth.NoopToolAuthorizer{}
}

func (s *runtimeServices) AgentAuthorizer() auth.AgentAuthorizer {
	if s.agentAuthorizer != nil {
		return s.agentAuthorizer
	}
	return auth.NoopAgentAuthorizer{}
}

func (s *runtimeServices) AuditLogger() auth.AuditLogger {
	if s.auditLogger != nil {
		return s.auditLogger
	}
	return auth.NoopAuditLogger{}
}

func (s *runtimeServices) CredentialService() auth.CredentialService {
	if s.credentialService != nil {
		return s.credentialService
	}
	return auth.NoopCredentialService{}
}

func (s *runtimeServices) ControlAuthorizer() auth.ControlAuthorizer {
	if s.controlAuthorizer != nil {
		return s.controlAuthorizer
	}
	return auth.NoopControlAuthorizer{}
}

var _ Services = (*runtimeServices)(nil)
