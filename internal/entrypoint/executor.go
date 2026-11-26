// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

const (
	StatusCreated  = "created"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusStopped  = "stopped"
	StatusError    = "error"
)

const errStopGatewayFmt = "stop gateway %q: %w"

type GatewayFactory func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error)

type managedGateway struct {
	mu           sync.Mutex
	core         *GatewayCore
	name         string
	enabled      bool
	status       string
	appConfig    map[string]any
	initializing bool
}

func (mg *managedGateway) getStatus() string  { mg.mu.Lock(); defer mg.mu.Unlock(); return mg.status }
func (mg *managedGateway) setStatus(s string) { mg.mu.Lock(); mg.status = s; mg.mu.Unlock() }
func (mg *managedGateway) getEnabled() bool   { mg.mu.Lock(); defer mg.mu.Unlock(); return mg.enabled }

func (mg *managedGateway) isInitializing() bool {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	return mg.initializing
}

func (mg *managedGateway) initialized() {
	mg.mu.Lock()
	mg.initializing = false
	mg.mu.Unlock()
}

type GatewayInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"`
}

type GatewayExecutor struct {
	svc     runtime.Services
	factory GatewayFactory
	mu      sync.RWMutex
	gws     []*managedGateway
	logger  *slog.Logger

	nameLocks sync.Map

	withheld sync.Map

	leaderIngress atomic.Bool

	AllowRuntimeManagement bool

	deployed    func(ctx context.Context) ([]control.EntrypointApply, error)
	deployedOne func(ctx context.Context, name string) (*control.EntrypointApply, error)

	targetLookup atomic.Pointer[TargetLookup]

	entrypoints entrypointState

	readinessOpened atomic.Bool

	gatewayHooks *GatewayHooks

	OnGatewayReady func(name string, handler http.Handler)

	OnGatewayRemoved func(name string)
}

type GatewayHooks struct {
	TaskSigner       TaskSigner
	ScopeResolver    ScopeResolver
	IdentityResolver IdentityResolver

	BuildSenderAuthorizer func(gatewayID string) auth.SenderAuthorizer

	BuildResolvers func(gatewayID string, appConfig map[string]any) (ScopeResolver, IdentityResolver, error)

	AuthExpected bool

	ConfigureProjectSharing func(core *GatewayCore) error

	ConfigurePromptSharing func(core *GatewayCore) error

	IdentityPlatformService auth.IdentityService

	WebuiSettingsRegistry *webuisettings.Registry

	Activation ActivationGate

	ProfileCache *auth.ProfileCache

	BuildLazyEnrichmentLookup func(gatewayID string, appConfig map[string]any) auth.LazyEnrichmentLookup

	OnGatewayBeforeInit func(ctx context.Context, core *GatewayCore, appConfig map[string]any) error

	OnGatewayBeforeRemove func(ctx context.Context, core *GatewayCore, appConfig map[string]any) error
}

var _ runtime.Component = (*GatewayExecutor)(nil)

func (e *GatewayExecutor) lockForName(name string) *sync.Mutex {
	v, _ := e.nameLocks.LoadOrStore(name, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func NewGatewayExecutor(svc runtime.Services, factory GatewayFactory) *GatewayExecutor {
	base := slog.Default()
	if svc != nil {
		base = svc.Logger()
	}
	return &GatewayExecutor{
		svc:                    svc,
		factory:                factory,
		logger:                 base.With("logger_name", "gateway-executor"),
		AllowRuntimeManagement: true,
	}
}

func (e *GatewayExecutor) SetGatewayHooks(hooks *GatewayHooks) {
	e.gatewayHooks = hooks
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, gw := range e.gws {
		e.applyGatewayHooks(gw.core, gw.appConfig)
	}
}

func (e *GatewayExecutor) applyGatewayHooks(core *GatewayCore, appConfig map[string]any) {
	if lookup := e.targetLookup.Load(); lookup != nil {
		core.SetTargetLookup(*lookup)
	}
	if e.gatewayHooks == nil {
		return
	}
	if e.gatewayHooks.TaskSigner != nil {
		core.SetTaskSigner(e.gatewayHooks.TaskSigner)
	}

	if e.gatewayHooks.ConfigureProjectSharing != nil {
		if err := e.gatewayHooks.ConfigureProjectSharing(core); err != nil {
			e.logger.Warn("ConfigureProjectSharing failed; project sharing disabled for gateway",
				"gatewayID", core.GatewayID(), "error", err)
		}
	}

	if e.gatewayHooks.ConfigurePromptSharing != nil {
		if err := e.gatewayHooks.ConfigurePromptSharing(core); err != nil {
			e.logger.Warn("ConfigurePromptSharing failed; prompt sharing disabled for gateway",
				"gatewayID", core.GatewayID(), "error", err)
		}
	}

	if e.gatewayHooks.IdentityPlatformService != nil {
		core.SetIdentityService(e.gatewayHooks.IdentityPlatformService)
	}

	if e.gatewayHooks.WebuiSettingsRegistry != nil {
		core.SetWebuiSettingsRegistry(e.gatewayHooks.WebuiSettingsRegistry)
	}

	if e.gatewayHooks.Activation != nil {
		core.SetActivation(e.gatewayHooks.Activation)
	}

	if e.gatewayHooks.ProfileCache != nil {
		core.SetProfileCache(e.gatewayHooks.ProfileCache)
	}

	if e.gatewayHooks.BuildLazyEnrichmentLookup != nil {
		if fn := e.gatewayHooks.BuildLazyEnrichmentLookup(core.GatewayID(), appConfig); fn != nil {
			core.SetLazyEnrichmentLookup(fn)
		}
	}

	enforcing := AppConfigAuthzEnforcing(appConfig)
	core.SetAuthzEnabled(enforcing)

	if e.svc != nil {
		core.SetAgentAuthorizer(rbac.NewAgentAuth(e.svc.AuditLogger(), e.logger))
	}

	if e.gatewayHooks.AuthExpected && !enforcing {
		e.logger.Error("entrypoint deployed without RBAC enforcement while the platform runs authorization; all callers will be allowed — set frontend_use_authorization/authorization_service on this entrypoint",
			"gatewayID", core.GatewayID())
	}

	if e.gatewayHooks.BuildSenderAuthorizer != nil {
		core.SetIdentityAuthorizer(e.gatewayHooks.BuildSenderAuthorizer(core.GatewayID()))
	}

	if e.gatewayHooks.BuildResolvers != nil {
		sr, ir, err := e.gatewayHooks.BuildResolvers(core.GatewayID(), appConfig)
		if err != nil {
			if enforcing {
				e.logger.Error("BuildResolvers failed while authz is enforcing; gateway will deny all agent invocations until resolved",
					"gatewayID", core.GatewayID(), "error", err)
			} else {
				e.logger.Warn("BuildResolvers failed; scope/identity resolution disabled for gateway",
					"gatewayID", core.GatewayID(), "error", err)
			}
			return
		}
		if sr != nil {
			core.SetScopeResolver(sr)
		}
		if ir != nil {
			core.SetIdentityResolver(ir)
		}
		return
	}

	if e.gatewayHooks.ScopeResolver != nil {
		core.SetScopeResolver(e.gatewayHooks.ScopeResolver)
	}
	if e.gatewayHooks.IdentityResolver != nil {
		core.SetIdentityResolver(e.gatewayHooks.IdentityResolver)
	}
}

func (e *GatewayExecutor) LoadConfig(name string, appConfig map[string]any) error {
	if _, ok := appConfig["gateway_id"].(string); !ok || appConfig["gateway_id"] == "" {
		appConfig["gateway_id"] = name
		e.logger.Warn("no gateway_id provided; defaulting to app name — the id namespaces this entrypoint's per-sender state, including its identity bindings, so an app rename or a reused name inherits or strands that state",
			"gatewayID", name)
	}
	defaultGatewayUUID(appConfig, name)
	core, err := e.factory(appConfig, e.svc)
	if err != nil {
		return fmt.Errorf("create gateway %q: %w", name, err)
	}
	e.applyGatewayHooks(core, appConfig)
	core.setActivityFallback(e.webUIActivityEmitter)

	e.mu.Lock()
	e.gws = append(e.gws, &managedGateway{
		core:      core,
		name:      name,
		enabled:   true,
		status:    StatusCreated,
		appConfig: appConfig,
	})
	e.mu.Unlock()
	return nil
}

const gatewayUUIDKey = "gateway_uuid"

func defaultGatewayUUID(appConfig map[string]any, name string) {
	if _, ok := appConfig[gatewayUUIDKey]; !ok && idgen.IsCanonical(name) {
		appConfig[gatewayUUIDKey] = name
	}
}

func (e *GatewayExecutor) AddGateway(name string, core *GatewayCore, appConfig map[string]any) {
	e.applyGatewayHooks(core, appConfig)
	core.setActivityFallback(e.webUIActivityEmitter)
	if e.leaderIngress.Load() {
		core.UseLeaderIngress()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.gws = append(e.gws, &managedGateway{
		core:      core,
		name:      name,
		enabled:   true,
		status:    StatusCreated,
		appConfig: appConfig,
	})
}

func (e *GatewayExecutor) Gateways() []*GatewayCore {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*GatewayCore, len(e.gws))
	for i, mg := range e.gws {
		result[i] = mg.core
	}
	return result
}

func (e *GatewayExecutor) initializedCores() []*GatewayCore {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cores := make([]*GatewayCore, 0, len(e.gws))
	for _, mg := range e.gws {
		if !mg.isInitializing() {
			cores = append(cores, mg.core)
		}
	}
	return cores
}

func (e *GatewayExecutor) webUIActivityEmitter() ActivityEventEmitter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, mg := range e.gws {
		if mg.core == nil || mg.isInitializing() || !mg.core.ServesWebUI() {
			continue
		}
		if view, ok := mg.core.Adapter().(ForwardedActivityView); ok {
			return view.ForwardedActivity()
		}
	}
	return nil
}

func (e *GatewayExecutor) GetGateway(name string) *GatewayCore {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if mg := e.findGatewayLocked(name); mg != nil {
		return mg.core
	}
	return nil
}

func (e *GatewayExecutor) Init(ctx context.Context, _ config.Config) error {
	e.mu.RLock()
	if len(e.gws) == 0 {
		e.mu.RUnlock()
		e.logger.Warn("gateway executor has no gateways to initialize")
		return nil
	}
	snapshot := make([]*managedGateway, len(e.gws))
	copy(snapshot, e.gws)
	e.mu.RUnlock()

	for i, mg := range snapshot {
		e.logger.Info("initializing gateway", "name", mg.name)
		if err := mg.core.Init(ctx, nil); err != nil {
			mg.setStatus(StatusError)
			for j := i - 1; j >= 0; j-- {
				_ = snapshot[j].core.Stop(ctx)
			}
			return fmt.Errorf("init gateway %q: %w", mg.name, err)
		}
	}
	return nil
}

func (e *GatewayExecutor) SetDeployedEntrypoints(list func(ctx context.Context) ([]control.EntrypointApply, error), one func(ctx context.Context, name string) (*control.EntrypointApply, error)) {
	e.deployed, e.deployedOne = list, one
}

func (e *GatewayExecutor) SetTargetLookup(lookup TargetLookup) {
	e.targetLookup.Store(&lookup)
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, gw := range e.gws {
		gw.core.SetTargetLookup(lookup)
	}
}

func (e *GatewayExecutor) Start(ctx context.Context) error {
	e.mu.RLock()
	snapshot := make([]*managedGateway, len(e.gws))
	copy(snapshot, e.gws)
	e.mu.RUnlock()

	var started []*managedGateway
	for _, mg := range snapshot {
		if !mg.getEnabled() {
			continue
		}
		mg.setStatus(StatusStarting)
		e.logger.Info("starting gateway", "name", mg.name)
		if err := mg.core.Start(ctx); err != nil {
			mg.setStatus(StatusError)
			for _, s := range started {
				_ = s.core.Stop(ctx)
				s.setStatus(StatusStopped)
			}
			return fmt.Errorf("start gateway %q: %w", mg.name, err)
		}
		mg.setStatus(StatusRunning)
		started = append(started, mg)
	}
	e.logger.Info("all gateways started", "count", len(snapshot))
	e.startEntrypointLoops()
	if err := e.checkEntrypoints(ctx); err != nil {
		return fmt.Errorf("read deployed entrypoints: %w", err)
	}
	e.publishLifecycleEvent(ctx, "started")
	return nil
}

func (e *GatewayExecutor) ReadinessOpen() bool {
	if e.readinessOpened.Load() {
		return true
	}
	e.mu.RLock()
	snapshot := make([]*managedGateway, len(e.gws))
	copy(snapshot, e.gws)
	e.mu.RUnlock()
	for _, mg := range snapshot {
		if mg.getStatus() == StatusRunning && !mg.core.QueuesBound() {
			return false
		}
	}
	e.readinessOpened.Store(true)
	return true
}

var _ runtime.ReadinessGate = (*GatewayExecutor)(nil)

func (e *GatewayExecutor) Stop(ctx context.Context) error {
	e.stopEntrypointLoops(ctx)
	e.publishLifecycleEvent(ctx, "stopped")

	e.mu.RLock()
	snapshot := make([]*managedGateway, len(e.gws))
	copy(snapshot, e.gws)
	e.mu.RUnlock()

	var wg sync.WaitGroup
	errs := make([]error, len(snapshot))

	for i, mg := range snapshot {
		status := mg.getStatus()
		if status == StatusStopped || status == StatusStopping {
			continue
		}
		wg.Go(func() {
			mg.setStatus(StatusStopping)
			e.logger.Info("stopping gateway", "name", mg.name)
			if err := mg.core.Stop(ctx); err != nil {
				mg.setStatus(StatusError)
				errs[i] = fmt.Errorf(errStopGatewayFmt, mg.name, err)
				return
			}
			mg.setStatus(StatusStopped)
		})
	}
	wg.Wait()

	return errors.Join(append(errs, e.stopWithheld()...)...)
}

func (e *GatewayExecutor) resolveNamespace() string {
	namespace := config.DefaultNamespace
	if e.svc != nil {
		if cfg := e.svc.Config(); cfg != nil {
			if ns := cfg.GetString(config.KeyNamespace); ns != "" {
				namespace = ns
			}
		}
	}
	if namespace == config.DefaultNamespace {
		e.mu.RLock()
		for _, mg := range e.gws {
			if ns, ok := mg.appConfig["namespace"].(string); ok && ns != "" {
				namespace = ns
				break
			}
		}
		e.mu.RUnlock()
	}
	return namespace
}

func (e *GatewayExecutor) deprovisionGatewayQueuesByName(gatewayID string) error {
	if e.svc == nil {
		return nil
	}
	brk := e.svc.Broker()
	if brk == nil {
		return nil
	}
	namespace := e.resolveNamespace()
	var errs []error
	for _, queueName := range a2a.EntrypointQueueNames(namespace, gatewayID) {
		if err := brk.DeprovisionQueue(queueName); err != nil {
			errs = append(errs, fmt.Errorf("deprovision queue %q: %w", queueName, err))
		}
	}
	return errors.Join(errs...)
}

func (e *GatewayExecutor) publishLifecycleEvent(ctx context.Context, event string) {
	if e.svc == nil {
		return
	}
	brk := e.svc.Broker()
	if brk == nil {
		return
	}

	namespace := e.resolveNamespace()
	topic := a2a.LifecycleTopic(namespace, "gateway", event)
	msg := &broker.Message{
		Topic:   topic,
		Payload: []byte(fmt.Sprintf(`{"component":"gateway","event":%q}`, event)),
	}
	if err := brk.Publish(ctx, topic, msg); err != nil {
		e.logger.Warn("failed to publish lifecycle event", "event", event, "error", err)
	} else {
		e.logger.Info("published lifecycle event", "topic", topic)
	}
}

func (e *GatewayExecutor) Health() error {
	e.mu.RLock()
	snapshot := make([]*managedGateway, len(e.gws))
	copy(snapshot, e.gws)
	e.mu.RUnlock()

	for _, mg := range snapshot {
		status := mg.getStatus()
		if status == StatusStopped || status == StatusStopping {
			continue
		}
		if err := mg.core.Health(); err != nil {
			return fmt.Errorf("gateway %q unhealthy: %w", mg.name, err)
		}
	}
	return nil
}

func (e *GatewayExecutor) ListInstances() []GatewayInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]GatewayInfo, len(e.gws))
	for i, mg := range e.gws {
		result[i] = GatewayInfo{
			Name:    mg.name,
			Enabled: mg.getEnabled(),
			Status:  mg.getStatus(),
		}
	}
	return result
}

func (e *GatewayExecutor) ListGatewayNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.gws))
	for _, mg := range e.gws {
		gwID, _ := mg.appConfig["gateway_id"].(string)
		names = append(names, cmp.Or(gwID, mg.name))
	}
	return names
}

func (e *GatewayExecutor) GetInstance(name string) (*GatewayInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	mg := e.findGatewayLocked(name)
	if mg == nil {
		return nil, fmt.Errorf("gateway %q not found", name)
	}
	return &GatewayInfo{
		Name:    mg.name,
		Enabled: mg.getEnabled(),
		Status:  mg.getStatus(),
	}, nil
}

func (e *GatewayExecutor) AddAndStart(ctx context.Context, name string, appConfig map[string]any) (*GatewayInfo, error) {
	if !e.AllowRuntimeManagement {
		return nil, fmt.Errorf("runtime gateway management is disabled")
	}

	l := e.lockForName(name)
	l.Lock()
	defer l.Unlock()

	if appConfig == nil {
		appConfig = make(map[string]any)
	}
	if _, ok := appConfig["gateway_id"].(string); !ok || appConfig["gateway_id"] == "" {
		appConfig["gateway_id"] = name
		e.logger.Warn("no gateway_id provided; defaulting to app name — the id namespaces this entrypoint's per-sender state, including its identity bindings, so an app rename or a reused name inherits or strands that state",
			"gatewayID", name)
	}
	defaultGatewayUUID(appConfig, name)

	core, err := e.factory(appConfig, e.svc)
	if err != nil {
		return nil, fmt.Errorf("create gateway %q: %w", name, err)
	}
	e.applyGatewayHooks(core, appConfig)
	core.setActivityFallback(e.webUIActivityEmitter)
	if e.leaderIngress.Load() {
		core.UseLeaderIngress()
	}

	mg := &managedGateway{
		core:         core,
		name:         name,
		enabled:      true,
		status:       StatusCreated,
		appConfig:    appConfig,
		initializing: true,
	}

	e.mu.Lock()
	for _, existing := range e.gws {
		if existing.name == name {
			e.mu.Unlock()
			return nil, fmt.Errorf("gateway %q already exists", name)
		}
	}
	e.gws = append(e.gws, mg)
	e.mu.Unlock()

	if e.OnGatewayReady != nil {
		if adapter := core.Adapter(); adapter != nil {
			if d, ok := adapter.(DeferredHTTPAdapter); ok {
				d.SetOnHandlerReady(func(h http.Handler) {
					e.OnGatewayReady(core.GatewayID(), h)
				})
			}
		}
	}

	preInitDone := false
	if e.gatewayHooks != nil && e.gatewayHooks.OnGatewayBeforeInit != nil {
		if err := e.gatewayHooks.OnGatewayBeforeInit(ctx, core, appConfig); err != nil {
			e.removePlaceholder(name)
			return nil, fmt.Errorf("enterprise pre-init for gateway %q: %w", name, err)
		}
		preInitDone = true
	}

	rollbackPreInit := func() {
		if !preInitDone || e.gatewayHooks == nil || e.gatewayHooks.OnGatewayBeforeRemove == nil {
			return
		}
		if err := e.gatewayHooks.OnGatewayBeforeRemove(ctx, core, appConfig); err != nil {
			e.logger.Warn("rollback of pre-init registrations reported error", "name", name, "error", err)
		}
	}

	e.logger.Info("initializing new gateway", "name", name)
	if err := core.Init(ctx, nil); err != nil {
		rollbackPreInit()
		e.removePlaceholder(name)
		return nil, fmt.Errorf("init gateway %q: %w", name, err)
	}
	mg.initialized()

	e.logger.Info("starting new gateway", "name", name)
	if err := core.Start(ctx); err != nil {
		if cleanupErr := core.Stop(ctx); cleanupErr != nil {
			e.logger.Warn("cleanup after failed start reported error", "name", name, "error", cleanupErr)
		}
		rollbackPreInit()
		e.removePlaceholder(name)
		return nil, fmt.Errorf("start gateway %q: %w", name, err)
	}

	mg.setStatus(StatusRunning)
	info := &GatewayInfo{Name: name, Enabled: true, Status: StatusRunning}
	e.logger.Info("gateway added and started", "name", name)

	return info, nil
}

func (e *GatewayExecutor) RemoveInstance(ctx context.Context, name string) error {
	return e.removeInstance(ctx, name, true)
}

func (e *GatewayExecutor) removeInstance(ctx context.Context, name string, deprovisionQueues bool) error {
	if !e.AllowRuntimeManagement {
		return fmt.Errorf("runtime gateway management is disabled")
	}

	l := e.lockForName(name)
	l.Lock()
	defer l.Unlock()

	e.mu.RLock()
	target := e.findGatewayLocked(name)
	e.mu.RUnlock()

	if target == nil {
		if !deprovisionQueues {
			e.logger.Info("cold-cache gateway remove with queue preservation; nothing to do", "name", name)
			return nil
		}
		if err := e.deprovisionGatewayQueuesByName(name); err != nil {
			return fmt.Errorf("remove cold-cache gateway %q: %w", name, err)
		}
		e.logger.Info("removed cold-cache gateway broker artifacts", "name", name)
		return nil
	}

	if e.gatewayHooks != nil && e.gatewayHooks.OnGatewayBeforeRemove != nil {
		if err := e.gatewayHooks.OnGatewayBeforeRemove(ctx, target.core, target.appConfig); err != nil {
			e.logger.Warn("OnGatewayBeforeRemove reported error (continuing removal)", "name", name, "error", err)
		}
	}

	if e.OnGatewayRemoved != nil {
		e.OnGatewayRemoved(target.core.GatewayID())
	}

	if target.getStatus() == StatusRunning {
		target.setStatus(StatusStopping)
		e.logger.Info("stopping gateway for removal", "name", name, "deprovisionQueues", deprovisionQueues)
		var stopErr error
		if deprovisionQueues {
			stopErr = target.core.Remove(ctx)
		} else {
			stopErr = target.core.Stop(ctx)
		}
		if stopErr != nil {
			target.setStatus(StatusError)
			e.logger.Error("error stopping gateway during removal", "name", name, "error", stopErr)
			return fmt.Errorf(errStopGatewayFmt, name, stopErr)
		}
	} else if deprovisionQueues {
		target.core.deprovisionQueues()
	}
	target.setStatus(StatusStopped)

	e.mu.Lock()
	for i, mg := range e.gws {
		if mg.name == name {
			e.gws = append(e.gws[:i], e.gws[i+1:]...)
			break
		}
	}
	e.mu.Unlock()

	e.logger.Info("gateway removed", "name", name)
	return nil
}

func (e *GatewayExecutor) StopInstance(ctx context.Context, name string) error {
	if !e.AllowRuntimeManagement {
		return fmt.Errorf("runtime gateway management is disabled")
	}

	l := e.lockForName(name)
	l.Lock()
	defer l.Unlock()

	e.mu.RLock()
	target := e.findGatewayLocked(name)
	e.mu.RUnlock()

	if target == nil {
		return fmt.Errorf("gateway %q not found", name)
	}

	target.mu.Lock()
	if target.status != StatusRunning {
		status := target.status
		target.mu.Unlock()
		return fmt.Errorf("gateway %q is not running (status: %s)", name, status)
	}
	target.status = StatusStopping
	target.enabled = false
	target.mu.Unlock()

	e.logger.Info("stopping gateway", "name", name)
	if err := target.core.Stop(ctx); err != nil {
		target.setStatus(StatusError)
		return fmt.Errorf(errStopGatewayFmt, name, err)
	}
	target.setStatus(StatusStopped)
	e.logger.Info("gateway stopped", "name", name)
	return nil
}

func (e *GatewayExecutor) StartInstance(ctx context.Context, name string) error {
	if !e.AllowRuntimeManagement {
		return fmt.Errorf("runtime gateway management is disabled")
	}

	l := e.lockForName(name)
	l.Lock()
	defer l.Unlock()

	e.mu.RLock()
	target := e.findGatewayLocked(name)
	e.mu.RUnlock()

	if target == nil {
		return fmt.Errorf("gateway %q not found", name)
	}

	target.mu.Lock()
	if target.status == StatusRunning {
		target.mu.Unlock()
		return fmt.Errorf("gateway %q is already running", name)
	}
	target.status = StatusStarting
	target.enabled = true
	target.mu.Unlock()

	e.logger.Info("starting gateway", "name", name)
	if err := target.core.Start(ctx); err != nil {
		target.setStatus(StatusError)
		return fmt.Errorf("start gateway %q: %w", name, err)
	}
	target.setStatus(StatusRunning)
	e.logger.Info("gateway started", "name", name)
	return nil
}

func (e *GatewayExecutor) removePlaceholder(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, mg := range e.gws {
		if mg.name == name {
			e.gws = append(e.gws[:i], e.gws[i+1:]...)
			return
		}
	}
}

func (e *GatewayExecutor) findGatewayLocked(name string) *managedGateway {
	for _, mg := range e.gws {
		if mg.name == name {
			return mg
		}
	}
	return nil
}
