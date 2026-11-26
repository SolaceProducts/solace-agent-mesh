// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const entrypointCheckInterval = 30 * time.Second

const (
	entrypointRetryFirst  = 5 * time.Second
	entrypointRetryJitter = 0.2
	entrypointRetryMax    = 15 * time.Minute
)

func entrypointRetryDelay(failures int) time.Duration {
	d := entrypointRetryMax
	if failures < 20 {
		d = min(entrypointRetryFirst<<max(failures-1, 0), entrypointRetryMax)
	}
	return d + time.Duration((rand.Float64()*2*entrypointRetryJitter-entrypointRetryJitter)*float64(d))
}

type appliedEntrypoint struct {
	deploymentID string
	revision     string
	attempt      string
	err          string
	failedSince  time.Time
	failures     int
	retryAt      time.Time
	wireID       string
	namespace    string
}

type entrypointState struct {
	mu       sync.Mutex
	applied  map[string]appliedEntrypoint
	applying sync.Map
	cancel   context.CancelFunc
	loops    sync.WaitGroup
}

func (s *entrypointState) lockFor(name string) *sync.Mutex {
	v, _ := s.applying.LoadOrStore(name, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *entrypointState) current(name string) (appliedEntrypoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.applied[name]
	return a, ok
}

func (s *entrypointState) record(name string, a appliedEntrypoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applied == nil {
		s.applied = map[string]appliedEntrypoint{}
	}
	s.applied[name] = a
}

func (s *entrypointState) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.applied, name)
}

func (s *entrypointState) failedOn(name, deploymentID, revision string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.applied[name]
	return ok && a.err != "" && a.deploymentID == deploymentID && a.revision == revision
}

func (s *entrypointState) dueRetries(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []string
	for name, a := range s.applied {
		if a.err == "" || now.Before(a.retryAt) {
			continue
		}
		a.retryAt = now.Add(entrypointRetryDelay(a.failures))
		s.applied[name] = a
		due = append(due, name)
	}
	return due
}

func (s *entrypointState) report() []control.EntrypointStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]control.EntrypointStatus, 0, len(s.applied))
	for name, a := range s.applied {
		out = append(out, control.EntrypointStatus{Name: name, DeploymentID: a.deploymentID, Revision: a.revision, Attempt: a.attempt, Error: a.err})
	}
	return out
}

func (e *GatewayExecutor) applyEntrypoint(ctx context.Context, name string, received *control.EntrypointApply) {
	l := e.entrypoints.lockFor(name)
	l.Lock()
	defer l.Unlock()
	d := received
	if e.deployedOne != nil {
		var err error
		if d, err = e.deployedOne(ctx, name); err != nil {
			e.logger.Warn("entrypoint deployment not read; the next check retries", "gatewayID", name, "error", err)
			return
		}
	}
	cur, known := e.entrypoints.current(name)
	if d == nil {
		if known {
			e.retireDeleted(ctx, name, cur)
		}
		return
	}
	if !d.Undeploy && d.AppConfig == nil {
		return
	}
	sameDeployment := known && d.DeploymentID == cur.deploymentID
	attempt := ""
	if sameDeployment && d.Revision == cur.revision {
		attempt = cur.attempt
	}
	if received != nil && received.Attempt != "" {
		attempt = received.Attempt
	}
	if sameDeployment && d.Revision == cur.revision && cur.err == "" {
		if attempt != cur.attempt {
			cur.attempt = attempt
			e.entrypoints.record(d.Name, cur)
		}
		return
	}
	var err error
	switch reconfigurer := e.runningReconfigurer(d.Name); {
	case d.Undeploy:
		err = e.retireInstance(ctx, d.Name)
	case sameDeployment && reconfigurer != nil:
		err = reconfigurer.Reconfigure(ctx, AdapterConfig(d.AppConfig))
		if errors.Is(err, ErrRestartRequired) {
			e.logger.Info("entrypoint cannot take its configuration in place; it restarts", "gatewayID", d.Name, "deploymentID", d.DeploymentID, "revision", d.Revision)
			err = e.restartInstance(ctx, d.Name, d.AppConfig)
		} else if err == nil {
			e.logger.Info("entrypoint reconfigured in place", "gatewayID", d.Name, "deploymentID", d.DeploymentID, "revision", d.Revision)
		}
	default:
		err = e.restartInstance(ctx, d.Name, d.AppConfig)
	}
	applied := appliedEntrypoint{deploymentID: d.DeploymentID, revision: d.Revision, attempt: attempt, wireID: d.Name, namespace: e.resolveNamespace()}
	if known {
		applied.wireID, applied.namespace = cur.wireID, cur.namespace
	}
	if wireID, ok := d.AppConfig["gateway_id"].(string); ok && wireID != "" {
		applied.wireID = wireID
	}
	if namespace, ok := d.AppConfig["namespace"].(string); ok && namespace != "" {
		applied.namespace = namespace
	}
	if err != nil {
		applied.err = err.Error()
		applied.failedSince = cur.failedSince
		if cur.err == "" || applied.failedSince.IsZero() {
			applied.failedSince = time.Now()
		}
		applied.failures = 1
		if sameDeployment && cur.err != "" && cur.revision == d.Revision {
			applied.failures = cur.failures + 1
		}
		retryIn := entrypointRetryDelay(applied.failures)
		applied.retryAt = time.Now().Add(retryIn)
		e.logger.Warn("entrypoint deployment failed on this GWE; it is retried", "gatewayID", d.Name, "deploymentID", d.DeploymentID, "revision", d.Revision, "retryIn", retryIn, "error", err)
	}
	e.entrypoints.record(d.Name, applied)
	switch {
	case err != nil:
		e.publishStartFailure(ctx, d.Name, applied)
	case known && cur.err != "":
		e.withdrawStartFailure(ctx, d.Name, cur)
	}
}

func (e *GatewayExecutor) restartInstance(ctx context.Context, name string, appConfig map[string]any) error {
	if e.GetGateway(name) != nil {
		if err := e.removeInstance(ctx, name, false); err != nil {
			return err
		}
	}
	_, err := e.AddAndStart(ctx, name, appConfig)
	return err
}

func (e *GatewayExecutor) runningReconfigurer(name string) Reconfigurer {
	e.mu.RLock()
	defer e.mu.RUnlock()
	mg := e.findGatewayLocked(name)
	if mg == nil || mg.core == nil || mg.getStatus() != StatusRunning {
		return nil
	}
	reconfigurer, _ := mg.core.Adapter().(Reconfigurer)
	return reconfigurer
}

func (e *GatewayExecutor) publishStartFailure(ctx context.Context, name string, applied appliedEntrypoint) {
	brk := e.broker()
	if brk == nil {
		return
	}
	problem := health.Problem{ID: health.StartFailedProblemID, Severity: health.Degraded, Detail: string(health.DetailStartFailed), Since: applied.failedSince}
	if err := publishGatewayCard(ctx, brk, startFailureCardID(name, applied),
		cardState{healthProblems: func() []health.Problem { return []health.Problem{problem} }}); err != nil {
		e.logger.Warn("entrypoint start failure not published", "gatewayID", name, "error", err)
	}
}

func (e *GatewayExecutor) withdrawStartFailure(ctx context.Context, name string, failed appliedEntrypoint) {
	if brk := e.broker(); brk != nil {
		publishGatewayRemovalCard(ctx, brk, startFailureCardID(name, failed), e.logger)
	}
}

func startFailureCardID(name string, applied appliedEntrypoint) gatewayCardID {
	return gatewayCardID{namespace: applied.namespace, gatewayID: applied.wireID, instanceID: runtime.ProcessID() + "/" + name}
}

func (e *GatewayExecutor) republishStartFailures(ctx context.Context) {
	e.entrypoints.mu.Lock()
	failed := map[string]appliedEntrypoint{}
	for name, a := range e.entrypoints.applied {
		if a.err != "" {
			failed[name] = a
		}
	}
	e.entrypoints.mu.Unlock()
	for name, a := range failed {
		e.publishStartFailure(ctx, name, a)
	}
}

func (e *GatewayExecutor) retireInstance(ctx context.Context, name string) error {
	if e.GetGateway(name) == nil {
		return nil
	}
	return e.removeInstance(ctx, name, false)
}

func (e *GatewayExecutor) checkEntrypoints(ctx context.Context) error {
	if e.deployed == nil {
		return nil
	}
	desired, err := e.deployed(ctx)
	if err != nil {
		return err
	}
	listed := make(map[string]*control.EntrypointApply, len(desired))
	for i := range desired {
		if desired[i].Name != "" {
			listed[desired[i].Name] = &desired[i]
		}
	}
	for _, status := range e.entrypoints.report() {
		if _, ok := listed[status.Name]; !ok {
			listed[status.Name] = nil
		}
	}
	for name, d := range listed {
		if d != nil && e.entrypoints.failedOn(name, d.DeploymentID, d.Revision) {
			continue
		}
		e.applyEntrypoint(ctx, name, d)
	}
	return nil
}

func (e *GatewayExecutor) retryFailed(ctx context.Context) {
	if e.deployedOne == nil {
		return
	}
	for _, name := range e.entrypoints.dueRetries(time.Now()) {
		e.applyEntrypoint(ctx, name, nil)
	}
}

func (e *GatewayExecutor) retireDeleted(ctx context.Context, name string, cur appliedEntrypoint) {
	if err := e.retireInstance(ctx, name); err != nil {
		e.logger.Warn("deleted entrypoint not stopped on this GWE", "gatewayID", name, "error", err)
	}
	if cur.err != "" {
		e.withdrawStartFailure(ctx, name, cur)
	}
	e.entrypoints.forget(name)
}

func (e *GatewayExecutor) startEntrypointLoops() {
	brk := e.broker()
	if brk == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.entrypoints.cancel = cancel
	namespace := e.resolveNamespace()
	if queue, err := brk.CreateDirectSubscription(); err != nil {
		e.logger.Warn("entrypoint change subscription not created; changes reach this GWE only through its own check", "error", err)
	} else if err := brk.Subscribe(queue, a2a.EntrypointApplyTopic(namespace)); err != nil {
		_ = brk.DeleteQueue(queue.Name())
		e.logger.Warn("entrypoint change topic not subscribed; changes reach this GWE only through its own check", "error", err)
	} else {
		e.entrypoints.loops.Go(func() { e.followEntrypointChanges(ctx, brk, queue) })
	}
	if e.deployed != nil {
		e.entrypoints.loops.Go(func() { e.everyInterval(ctx, entrypointCheckInterval, e.checkEntrypointsLogged) })
		e.entrypoints.loops.Go(func() { e.everyInterval(ctx, entrypointRetryFirst, e.retryFailed) })
	}
	e.entrypoints.loops.Go(func() {
		e.everyInterval(ctx, control.GatewayStatusInterval, func(ctx context.Context) { e.publishStatus(ctx, brk, namespace, false) })
	})
	e.entrypoints.loops.Go(func() { e.everyInterval(ctx, entrypointCheckInterval, e.republishStartFailures) })
}

func (e *GatewayExecutor) stopEntrypointLoops(ctx context.Context) {
	if e.entrypoints.cancel == nil {
		return
	}
	e.entrypoints.cancel()
	e.entrypoints.loops.Wait()
	if brk := e.broker(); brk != nil {
		e.publishStatus(ctx, brk, e.resolveNamespace(), true)
	}
}

func (e *GatewayExecutor) broker() broker.Broker {
	if e.svc == nil {
		return nil
	}
	return e.svc.Broker()
}

func (e *GatewayExecutor) followEntrypointChanges(ctx context.Context, brk broker.Broker, queue broker.Queue) {
	defer func() { _ = brk.DeleteQueue(queue.Name()) }()
	var applying sync.WaitGroup
	defer applying.Wait()
	for {
		msg, err := brk.Receive(ctx, queue)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, broker.ErrQueueNotFound) {
			e.logger.Warn("entrypoint change subscription is gone; changes reach this GWE only through its own check", "error", err)
			return
		}
		if err != nil || msg == nil {
			continue
		}
		msg.Ack()
		var d control.EntrypointApply
		if err := json.Unmarshal(msg.Payload, &d); err != nil || d.Name == "" || d.DeploymentID == "" {
			e.logger.Warn("unreadable entrypoint change dropped", "error", err)
			continue
		}
		applying.Go(func() { e.applyEntrypoint(ctx, d.Name, &d) })
	}
}

func (e *GatewayExecutor) checkEntrypointsLogged(ctx context.Context) {
	if err := e.checkEntrypoints(ctx); err != nil {
		e.logger.Warn("platform entrypoints not checked; the next check retries", "error", err)
	}
}

func (e *GatewayExecutor) everyInterval(ctx context.Context, every time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

func (e *GatewayExecutor) publishStatus(ctx context.Context, brk broker.Broker, namespace string, leaving bool) {
	status := control.GatewayStatus{ProcessID: runtime.ProcessID(), Leaving: leaving}
	if !leaving {
		status.Entrypoints = e.entrypoints.report()
	}
	payload, err := json.Marshal(status)
	if err != nil {
		return
	}
	if err := brk.Publish(ctx, a2a.GWEStatusTopic(namespace), &broker.Message{Payload: payload}); err != nil {
		e.logger.Debug("gateway status not published", "error", err)
	}
}
