// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

const (
	StatusCreated  = "created"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusStopped  = "stopped"
	StatusError    = "error"
)

type InstanceFactory func(cfg config.Config) (Instance, error)

type managedInstance struct {
	mu               sync.Mutex
	inst             Instance
	kind             string
	enabled          bool
	status           string
	lastErr          error
	neverInitialized bool
	cfg              config.Config
	deploymentID     string
}

func (mi *managedInstance) getStatus() string {
	mi.mu.Lock()
	defer mi.mu.Unlock()
	return mi.status
}

func (mi *managedInstance) setStatus(s string) {
	mi.mu.Lock()
	mi.status = s
	mi.mu.Unlock()
}

func (mi *managedInstance) setFailed(err error) {
	mi.mu.Lock()
	mi.status = StatusError
	mi.lastErr = err
	mi.mu.Unlock()
}

func (mi *managedInstance) setInitFailed(err error) {
	mi.mu.Lock()
	mi.status = StatusError
	mi.lastErr = err
	mi.neverInitialized = true
	mi.mu.Unlock()
}

func (mi *managedInstance) failure() error {
	mi.mu.Lock()
	defer mi.mu.Unlock()
	return mi.lastErr
}

func (mi *managedInstance) wasNeverInitialized() bool {
	mi.mu.Lock()
	defer mi.mu.Unlock()
	return mi.neverInitialized
}

func (mi *managedInstance) getEnabled() bool {
	mi.mu.Lock()
	defer mi.mu.Unlock()
	return mi.enabled
}

func (mi *managedInstance) setEnabled(v bool) {
	mi.mu.Lock()
	mi.enabled = v
	mi.mu.Unlock()
}

type InstanceInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
}

type Executor struct {
	svc       runtime.Services
	mu        sync.RWMutex
	instances []*managedInstance
	stopHooks []func(context.Context) error
	stopping  bool
	factories map[string]InstanceFactory
	bindings  *bindings.BindingsRegistry
	logger    *slog.Logger
	nameLocks sync.Map
	platform  platformAgents
}

var ErrExecutorStopping = errors.New("executor is stopping")

var _ runtime.Component = (*Executor)(nil)

func NewExecutor(svc runtime.Services) *Executor {
	logger := slog.Default().With("logger_name", "awe")
	e := &Executor{
		svc:       svc,
		factories: make(map[string]InstanceFactory),
		logger:    logger,
	}
	e.adoptBindings(bindings.New(bindings.WithLogger(logger)))
	return e
}

func (e *Executor) adoptBindings(b *bindings.BindingsRegistry) {
	b.Want(wantEveryModel)
	e.bindings = b
}

func wantEveryModel(string) bool { return true }

func (e *Executor) Bindings() *bindings.BindingsRegistry {
	return e.bindings
}

func (e *Executor) SetBindings(b *bindings.BindingsRegistry) {
	if b == nil {
		return
	}
	e.adoptBindings(b)
}

func (e *Executor) RegisterKind(kind string, factory InstanceFactory) {
	e.factories[kind] = factory
}

func (e *Executor) AddInstance(inst Instance) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.instances = append(e.instances, &managedInstance{
		inst:    inst,
		kind:    inst.Kind(),
		enabled: true,
		status:  StatusCreated,
	})
}

func (e *Executor) LoadConfig(cfg config.Config) error {
	kind := cfg.GetString("kind")
	if kind == "" {
		name := cfg.GetString("name")
		return fmt.Errorf("config %q missing required 'kind' field", name)
	}

	factory, ok := e.factories[kind]
	if !ok {
		return fmt.Errorf("unknown instance kind %q", kind)
	}

	inst, err := factory(cfg)
	if err != nil {
		return fmt.Errorf("create %q instance: %w", kind, err)
	}

	e.mu.Lock()
	e.instances = append(e.instances, &managedInstance{
		inst:    inst,
		kind:    kind,
		enabled: true,
		status:  StatusCreated,
		cfg:     cfg,
	})
	e.mu.Unlock()
	return nil
}

func (e *Executor) Instances() []Instance {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Instance, len(e.instances))
	for i, mi := range e.instances {
		result[i] = mi.inst
	}
	return result
}

func (e *Executor) Init(ctx context.Context, _ config.Config) error {
	e.mu.RLock()
	if len(e.instances) == 0 {
		e.mu.RUnlock()
		e.logger.Warn("awe executor has no instances to initialize; awaiting control-plane deploys")
		return nil
	}
	snapshot := make([]*managedInstance, len(e.instances))
	copy(snapshot, e.instances)
	e.mu.RUnlock()

	var failed int
	for _, mi := range snapshot {
		e.logger.Info("initializing instance", "name", mi.inst.Name(), "kind", mi.kind)
		if err := mi.inst.Init(ctx, nil, e.svc); err != nil {
			mi.setInitFailed(fmt.Errorf("init: %w", err))
			failed++
			e.logger.Error("instance failed to initialize; it will not serve",
				"name", mi.inst.Name(), "kind", mi.kind, "error", err)
		}
	}
	if failed == len(snapshot) {
		return fmt.Errorf("all %d configured instance(s) failed to initialize", failed)
	}
	return nil
}

func (e *Executor) OnStop(hook func(context.Context) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopHooks = append(e.stopHooks, hook)
}

func (e *Executor) Start(ctx context.Context) error {
	e.mu.Lock()
	e.stopping = false
	snapshot := make([]*managedInstance, len(e.instances))
	copy(snapshot, e.instances)
	e.mu.Unlock()

	var running, attempted int
	for _, mi := range snapshot {
		if !mi.getEnabled() || mi.getStatus() == StatusError {
			continue
		}
		attempted++
		mi.setStatus(StatusStarting)
		e.logger.Info("starting instance", "name", mi.inst.Name(), "kind", mi.kind)
		if err := mi.inst.Start(ctx); err != nil {
			mi.setFailed(fmt.Errorf("start: %w", err))
			e.logger.Error("instance failed to start; it will not serve",
				"name", mi.inst.Name(), "kind", mi.kind, "error", err)
			continue
		}
		mi.setStatus(StatusRunning)
		running++
	}
	e.logger.Info("instances started", "started", running, "attempted", attempted, "configured", len(snapshot))
	if attempted > 0 && running == 0 {
		return fmt.Errorf("all %d enabled instance(s) failed to start", attempted)
	}

	go e.checkpointPurgeLoop(ctx)

	if volSvc := e.svc.Volumes(); volSvc != nil {
		go e.volumeCleanupLoop(ctx, volSvc)
	}

	e.publishLifecycleEvent(ctx, "started")

	return nil
}

func (e *Executor) deprovisionAgentQueueByName(instanceName string) error {
	if e.svc == nil {
		return nil
	}
	brk := e.svc.Broker()
	if brk == nil {
		return nil
	}
	queueName := a2a.QueueName(e.resolveNamespace(), "a2a", instanceName)
	if err := brk.DeprovisionQueue(queueName); err != nil {
		return fmt.Errorf("deprovision queue %q: %w", queueName, err)
	}
	return nil
}

func (e *Executor) resolveNamespace() string {
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
		for _, mi := range e.instances {
			if mi.cfg == nil {
				continue
			}
			appCfg := mi.cfg.GetSection(config.KeyAppConfig)
			if appCfg == nil {
				continue
			}
			if ns := appCfg.GetString(config.KeyNamespace); ns != "" {
				namespace = ns
				break
			}
		}
		e.mu.RUnlock()
	}
	return namespace
}

func (e *Executor) publishLifecycleEvent(ctx context.Context, event string) {
	if e.svc == nil {
		return
	}
	brk := e.svc.Broker()
	if brk == nil {
		return
	}
	namespace := e.resolveNamespace()
	topic := a2a.LifecycleTopic(namespace, "awe", event)
	msg := &broker.Message{
		Topic:   topic,
		Payload: fmt.Appendf(nil, `{"component":"awe","event":%q}`, event),
	}
	if err := brk.Publish(ctx, topic, msg); err != nil {
		e.logger.Warn("failed to publish lifecycle event", "event", event, "error", err)
	}
}

func (e *Executor) Stop(ctx context.Context) error {
	e.stopFollowingPlatform(ctx)
	e.mu.Lock()
	e.stopping = true
	var names []string
	seen := make(map[string]bool, len(e.instances))
	for _, mi := range e.instances {
		if name := mi.inst.Name(); !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	hooks := slices.Clone(e.stopHooks)
	e.mu.Unlock()

	var wg sync.WaitGroup
	errs := make([][]error, len(names))

	for i, name := range names {
		wg.Go(func() {
			l := e.lockForName(name)
			if err := l.lockContext(ctx); err != nil {
				errs[i] = append(errs[i], fmt.Errorf("stop %q: waiting for an in-flight lifecycle operation: %w", name, err))
				return
			}
			defer l.unlock()
			for _, mi := range e.listedUnder(name) {
				if err := e.stopManaged(ctx, mi); err != nil {
					errs[i] = append(errs[i], err)
				}
			}
		})
	}
	wg.Wait()

	for _, hook := range hooks {
		errs = append(errs, []error{hook(ctx)})
	}
	return errors.Join(slices.Concat(errs...)...)
}

func (e *Executor) listedUnder(name string) []*managedInstance {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var listed []*managedInstance
	for _, mi := range e.instances {
		if mi.inst.Name() == name {
			listed = append(listed, mi)
		}
	}
	return listed
}

func (e *Executor) stopManaged(ctx context.Context, mi *managedInstance) error {
	if !needsStop(mi) {
		return nil
	}
	mi.setStatus(StatusStopping)
	e.logger.Info("stopping instance", "name", mi.inst.Name(), "kind", mi.kind)
	stop := mi.inst.Stop
	if ps, ok := mi.inst.(ProcessStopper); ok {
		stop = ps.StopForProcessExit
	}
	if err := stop(ctx); err != nil {
		mi.setFailed(fmt.Errorf("stop: %w", err))
		return fmt.Errorf("stop %s %q: %w", mi.kind, mi.inst.Name(), err)
	}
	mi.setStatus(StatusStopped)
	return nil
}

func needsStop(mi *managedInstance) bool {
	s := mi.getStatus()
	return s != StatusStopped && s != StatusStopping && (s != StatusError || !mi.wasNeverInitialized())
}

func (e *Executor) Health() error {
	e.mu.RLock()
	snapshot := make([]*managedInstance, len(e.instances))
	copy(snapshot, e.instances)
	e.mu.RUnlock()

	var alive int
	var deadErrs, restartErrs []error
	for _, mi := range snapshot {
		switch mi.getStatus() {
		case StatusStopped, StatusStopping:
			continue
		case StatusError:
			cause := mi.failure()
			if cause == nil {
				cause = errors.New("failed with no recorded cause")
			}
			deadErrs = append(deadErrs, fmt.Errorf("%s %q: %w", mi.kind, mi.inst.Name(), cause))
			continue
		}
		err := mi.inst.Health()
		if err == nil || errors.Is(err, runtime.ErrDegraded) {
			alive++
			continue
		}
		instErr := fmt.Errorf("%s %q unhealthy: %w", mi.kind, mi.inst.Name(), err)
		if errors.Is(err, runtime.ErrRestartRequired) {
			restartErrs = append(restartErrs, instErr)
		}
		deadErrs = append(deadErrs, instErr)
	}
	if len(deadErrs) == 0 {
		return nil
	}
	if len(restartErrs) > 0 {
		return fmt.Errorf("%d managed instance(s) need a process restart: %w", len(restartErrs), errors.Join(restartErrs...))
	}
	if alive == 0 {
		return fmt.Errorf("all %d managed instance(s) dead: %w", len(deadErrs), errors.Join(deadErrs...))
	}
	return fmt.Errorf("%d of %d managed instance(s) not serving: %w: %w",
		len(deadErrs), len(deadErrs)+alive, errors.Join(deadErrs...), runtime.ErrDegraded)
}

func (e *Executor) ListInstances() []InstanceInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]InstanceInfo, len(e.instances))
	for i, mi := range e.instances {
		result[i] = InstanceInfo{
			Name:        mi.inst.Name(),
			DisplayName: mi.inst.DisplayName(),
			Kind:        mi.kind,
			Enabled:     mi.getEnabled(),
			Status:      mi.getStatus(),
		}
	}
	return result
}

func (e *Executor) ListAgentNames() []string {
	var names []string
	for _, inst := range e.ListInstances() {
		if inst.Kind != "agent" {
			continue
		}
		switch inst.Status {
		case StatusError, StatusStopped, StatusStopping:
			continue
		}
		names = append(names, cmp.Or(inst.DisplayName, inst.Name))
	}
	return names
}

func (e *Executor) GetInstance(name string) (*InstanceInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, mi := range e.instances {
		if mi.inst.Name() == name {
			return &InstanceInfo{
				Name:        mi.inst.Name(),
				DisplayName: mi.inst.DisplayName(),
				Kind:        mi.kind,
				Enabled:     mi.getEnabled(),
				Status:      mi.getStatus(),
			}, nil
		}
	}
	return nil, fmt.Errorf("instance %q: %w", name, control.ErrResourceNotFound)
}

func (e *Executor) lockForName(name string) nameLock {
	if v, ok := e.nameLocks.Load(name); ok {
		return v.(nameLock)
	}
	v, _ := e.nameLocks.LoadOrStore(name, make(nameLock, 1))
	return v.(nameLock)
}

type nameLock chan struct{}

func (l nameLock) lock() { l <- struct{}{} }

func (l nameLock) lockContext(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l nameLock) unlock() { <-l }

func (e *Executor) runningDeployment(name string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if mi := e.findInstance(name); mi != nil {
		return mi.deploymentID
	}
	return ""
}

func (e *Executor) AddAndStart(ctx context.Context, cfg config.Config) (*InstanceInfo, error) {
	return e.addAndStart(ctx, cfg, "", false)
}

func (e *Executor) addAndStart(ctx context.Context, cfg config.Config, deploymentID string, detach bool) (*InstanceInfo, error) {
	kind := cfg.GetString("kind")
	if kind == "" {
		return nil, fmt.Errorf("config missing required 'kind' field")
	}

	factory, ok := e.factories[kind]
	if !ok {
		return nil, fmt.Errorf("unknown instance kind %q", kind)
	}

	inst, err := factory(cfg)
	if err != nil {
		return nil, fmt.Errorf("create %q instance: %w", kind, err)
	}

	name := inst.Name()
	l := e.lockForName(name)
	l.lock()
	defer l.unlock()

	if deploymentID != "" && deploymentID != e.runningDeployment(name) {
		e.mu.RLock()
		running := e.findInstance(name)
		e.mu.RUnlock()
		if running != nil {
			if err := e.removeInstanceLocked(ctx, name, removeForReplace); err != nil {
				return nil, fmt.Errorf("replace %s %q: %w", kind, name, err)
			}
		}
	}

	mi := &managedInstance{
		inst:         inst,
		kind:         kind,
		enabled:      true,
		status:       StatusCreated,
		cfg:          cfg,
		deploymentID: deploymentID,
	}

	var replaced Instance
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return nil, fmt.Errorf("add %s %q: %w", kind, name, ErrExecutorStopping)
	}
	for i, existing := range e.instances {
		if existing.inst.Name() != name {
			continue
		}
		switch existing.getStatus() {
		case StatusStopped, StatusError:
			if existing.getStatus() == StatusError && !existing.wasNeverInitialized() {
				replaced = existing.inst
			}
			e.instances = slices.Delete(e.instances, i, i+1)
		default:
			e.mu.Unlock()
			return nil, fmt.Errorf("instance %q already exists", name)
		}
		break
	}
	e.instances = append(e.instances, mi)
	e.mu.Unlock()

	if replaced != nil {
		if stopErr := replaced.Stop(ctx); stopErr != nil {
			e.logger.Warn("stop of a replaced failed instance returned error", "name", name, "error", stopErr)
		}
	}

	runCtx, stopCancelWatch := ctx, func() bool { return true }
	if detach {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(context.WithoutCancel(ctx))
		stopCancelWatch = context.AfterFunc(ctx, cancel)
		defer stopCancelWatch()
	}

	e.logger.Debug("initializing new instance", "name", name, "kind", kind)
	if err := inst.Init(runCtx, nil, e.svc); err != nil {
		e.removePlaceholder(name)
		return nil, fmt.Errorf("init %s %q: %w", kind, name, err)
	}

	e.logger.Debug("starting new instance", "name", name, "kind", kind)
	startErr := inst.Start(runCtx)
	if startErr == nil && !stopCancelWatch() {
		startErr = context.Cause(ctx)
	}
	if startErr != nil {
		_ = inst.Stop(ctx)
		e.removePlaceholder(name)
		return nil, fmt.Errorf("start %s %q: %w", kind, name, startErr)
	}

	mi.setStatus(StatusRunning)
	info := &InstanceInfo{Name: name, DisplayName: inst.DisplayName(), Kind: kind, Enabled: true, Status: StatusRunning}
	e.logger.Info("instance added and started", "name", name, "kind", kind)
	return info, nil
}

func (e *Executor) removePlaceholder(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, mi := range e.instances {
		if mi.inst.Name() == name {
			e.instances = append(e.instances[:i], e.instances[i+1:]...)
			return
		}
	}
}

func (e *Executor) RemoveInstance(ctx context.Context, name string) error {
	return e.removeInstance(ctx, name, removeWithQueues)
}

type removal int

const (
	removeForReplace removal = iota
	removeForUndeploy
	removeWithQueues
)

func removalFor(deprovision bool) removal {
	if deprovision {
		return removeWithQueues
	}
	return removeForReplace
}

func (e *Executor) removeInstance(ctx context.Context, name string, how removal) error {
	l := e.lockForName(name)
	l.lock()
	defer l.unlock()
	return e.removeInstanceLocked(ctx, name, how)
}

func (e *Executor) removeInstanceLocked(ctx context.Context, name string, how removal) error {
	deprovisionQueues := how == removeWithQueues
	e.mu.RLock()
	target := e.findInstance(name)
	e.mu.RUnlock()

	if target == nil {
		if !deprovisionQueues {
			if how == removeForUndeploy {
				e.purgeAgentCheckpoints(ctx, name)
			}
			e.logger.Info("cold-cache remove with queue preservation; nothing to do", "name", name)
			return nil
		}
		if err := e.deprovisionAgentQueueByName(name); err != nil {
			return fmt.Errorf("remove cold-cache instance %q: %w", name, err)
		}
		e.purgeAgentCheckpoints(ctx, name)
		e.logger.Info("removed cold-cache instance broker artifacts", "name", name)
		return nil
	}

	if s := target.getStatus(); s == StatusRunning || s == StatusStarting {
		target.setStatus(StatusStopping)
		e.logger.Info("removing instance", "name", name, "deprovisionQueues", deprovisionQueues, "undeploy", how == removeForUndeploy)
		var removeErr error
		if r, ok := target.inst.(Remover); ok && deprovisionQueues {
			removeErr = r.Remove(ctx)
		} else if r, ok := target.inst.(Replacer); ok && how == removeForReplace {
			removeErr = r.StopForReplace(ctx)
		} else {
			removeErr = target.inst.Stop(ctx)
		}
		if removeErr != nil {
			target.setFailed(fmt.Errorf("remove: %w", removeErr))
			e.logger.Error("instance removal failed", "name", name, "error", removeErr)
			return fmt.Errorf("remove %s %q: %w", target.kind, name, removeErr)
		}
	} else if r, ok := target.inst.(Remover); ok && deprovisionQueues {
		if err := r.Remove(ctx); err != nil {
			e.logger.Warn("post-stop deprovision returned error", "name", name, "error", err)
		}
	} else if s == StatusError && !target.wasNeverInitialized() {
		if err := target.inst.Stop(ctx); err != nil {
			e.logger.Warn("stop of a failed instance returned error", "name", name, "error", err)
		}
	}
	target.setStatus(StatusStopped)

	e.mu.Lock()
	for i, mi := range e.instances {
		if mi.inst.Name() == name {
			e.instances = append(e.instances[:i], e.instances[i+1:]...)
			break
		}
	}
	e.mu.Unlock()

	if how != removeForReplace {
		e.purgeAgentCheckpoints(ctx, name)
	}
	e.logger.Info("instance removed", "name", name)
	return nil
}

func (e *Executor) purgeAgentCheckpoints(ctx context.Context, agentName string) {
	if e.svc == nil || e.svc.Sessions() == nil {
		return
	}
	removed, now := 0, time.Now()
	for {
		n, err := e.svc.Sessions().PurgeAbandonedCheckpoints(ctx, agentName, now, checkpointPurgeBatch)
		removed += n
		if err != nil {
			e.logger.Warn("could not remove the paused tasks of a removed agent; the hourly cleanup removes them later", "name", agentName, "error", err)
			return
		}
		if n < checkpointPurgeBatch {
			break
		}
	}
	if removed > 0 {
		e.logger.Info("removed the paused tasks of a removed agent", "name", agentName, "checkpoints", removed)
	}
}

func (e *Executor) StopInstance(ctx context.Context, name string) error {
	l := e.lockForName(name)
	l.lock()
	defer l.unlock()

	e.mu.RLock()
	target := e.findInstance(name)
	e.mu.RUnlock()

	if target == nil {
		return fmt.Errorf("instance %q: %w", name, control.ErrResourceNotFound)
	}
	if s := target.getStatus(); s != StatusRunning {
		return fmt.Errorf("instance %q is not running (status: %s)", name, s)
	}

	target.setStatus(StatusStopping)
	target.setEnabled(false)
	e.logger.Info("stopping instance", "name", name)
	if err := target.inst.Stop(ctx); err != nil {
		target.setFailed(fmt.Errorf("stop: %w", err))
		return fmt.Errorf("stop %s %q: %w", target.kind, name, err)
	}
	target.setStatus(StatusStopped)
	e.logger.Info("instance stopped", "name", name)
	return nil
}

func (e *Executor) StartInstance(ctx context.Context, name string) error {
	l := e.lockForName(name)
	l.lock()
	defer l.unlock()

	e.mu.RLock()
	target := e.findInstance(name)
	stopping := e.stopping
	e.mu.RUnlock()

	if stopping {
		return fmt.Errorf("start %q: %w", name, ErrExecutorStopping)
	}
	if target == nil {
		return fmt.Errorf("instance %q: %w", name, control.ErrResourceNotFound)
	}
	if s := target.getStatus(); s == StatusRunning {
		return fmt.Errorf("instance %q is already running", name)
	}
	if target.wasNeverInitialized() {
		return fmt.Errorf("instance %q failed to initialize and cannot be started; redeploy it: %w", name, target.failure())
	}

	target.setStatus(StatusStarting)
	target.setEnabled(true)
	e.logger.Info("starting instance", "name", name)
	if err := target.inst.Start(ctx); err != nil {
		target.setFailed(fmt.Errorf("start: %w", err))
		return fmt.Errorf("start %s %q: %w", target.kind, name, err)
	}
	target.setStatus(StatusRunning)
	e.logger.Info("instance started", "name", name)
	return nil
}

func (e *Executor) findInstance(name string) *managedInstance {
	for _, mi := range e.instances {
		if mi.inst.Name() == name {
			return mi
		}
	}
	return nil
}

func (e *Executor) volumeCleanupLoop(ctx context.Context, volSvc volume.Service) {
	tickOnce := func() {
		if n, err := volSvc.CleanupExpired(ctx); err != nil {
			e.logger.Warn("volume cleanup failed", "error", err)
		} else if n > 0 {
			e.logger.Info("cleaned up expired volumes", "count", n)
		}
		if n, err := volSvc.RefreshAllSizes(ctx); err != nil {
			e.logger.Warn("volume size refresh failed", "error", err)
		} else if n > 0 {
			e.logger.Debug("refreshed managed-volume sizes", "count", n)
		}
	}
	tickOnce()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickOnce()
		}
	}
}
