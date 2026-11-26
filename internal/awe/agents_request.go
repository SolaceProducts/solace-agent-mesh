// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	agentsAskInterval     = 5 * time.Second
	agentsCheckInterval   = 30 * time.Second
	agentsReadyWaitMax    = 2 * time.Minute
	agentRetryFirst       = 5 * time.Second
	agentRetryJitter      = 0.2
	agentRetryMax         = 15 * time.Minute
	agentsApplyConcurrent = 32
)

var errAnswerWithoutAgentList = errors.New("the answer lists no agents; a GWE of an older version answered")

var errAskCutByChange = errors.New("an agent change arrived before the answer; asking again")

func agentRetryDelay(failures int) time.Duration {
	d := agentRetryMax
	if failures < 20 {
		d = min(agentRetryFirst<<max(failures-1, 0), agentRetryMax)
	}
	return d + time.Duration((rand.Float64()*2*agentRetryJitter-agentRetryJitter)*float64(d))
}

type appliedAgent struct {
	deploymentID string
	err          string
	failures     int
	retryAt      time.Time
}

type platformAgents struct {
	mu      sync.Mutex
	applied map[string]appliedAgent
	skipped map[string]bool

	applying  sync.Map
	slots     chan struct{}
	slotsOnce sync.Once
	checking  sync.Mutex

	wake      chan struct{}
	statusNow chan struct{}
	answered  atomic.Bool
	following atomic.Bool
	noWait    atomic.Bool
	waitAsked atomic.Bool
	since     atomic.Int64
	loops     sync.WaitGroup

	stopMu  sync.Mutex
	cancel  context.CancelFunc
	stopped bool
}

func (p *platformAgents) lockFor(name string) *sync.Mutex {
	v, _ := p.applying.LoadOrStore(name, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (p *platformAgents) applySlots() chan struct{} {
	p.slotsOnce.Do(func() { p.slots = make(chan struct{}, agentsApplyConcurrent) })
	return p.slots
}

func (p *platformAgents) markSkipped(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.skipped == nil {
		p.skipped = map[string]bool{}
	}
	p.skipped[name] = true
}

func (p *platformAgents) takeSkipped(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.skipped[name]
	delete(p.skipped, name)
	return was
}

func (p *platformAgents) nudgeStatus() {
	select {
	case p.statusNow <- struct{}{}:
	default:
	}
}

func (p *platformAgents) current(name string) (appliedAgent, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.applied[name]
	return a, ok
}

func (p *platformAgents) record(name string, a appliedAgent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.applied == nil {
		p.applied = map[string]appliedAgent{}
	}
	p.applied[name] = a
}

func (p *platformAgents) forget(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.applied, name)
}

func (p *platformAgents) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.applied))
	for name := range p.applied {
		out = append(out, name)
	}
	return out
}

func (p *platformAgents) anyRetryDue(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.applied {
		if a.err != "" && !now.Before(a.retryAt) {
			return true
		}
	}
	return false
}

func (p *platformAgents) failed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, a := range p.applied {
		if a.err != "" {
			n++
		}
	}
	return n
}

func (p *platformAgents) report() []control.AgentStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]control.AgentStatus, 0, len(p.applied))
	for name, a := range p.applied {
		out = append(out, control.AgentStatus{Name: name, DeploymentID: a.deploymentID, Error: a.err})
	}
	return out
}

func (p *platformAgents) trigger() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

var _ runtime.ReadinessGate = (*Executor)(nil)

func (e *Executor) WaitForPlatformBeforeReady(wait bool) {
	p := &e.platform
	p.noWait.Store(!wait)
	if wait {
		p.since.CompareAndSwap(0, time.Now().UnixNano())
		p.waitAsked.Store(true)
	}
}

func (e *Executor) ReadinessOpen() bool {
	p := &e.platform
	if p.noWait.Load() || p.answered.Load() {
		return true
	}
	if !p.following.Load() && !p.waitAsked.Load() {
		return true
	}
	return time.Since(time.Unix(0, p.since.Load())) > agentsReadyWaitMax
}

func (e *Executor) FollowPlatformAgents(ctx context.Context, namespace string) {
	brk := e.svc.Broker()
	if brk == nil {
		return
	}
	p := &e.platform
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	if p.stopped {
		return
	}
	p.wake = make(chan struct{}, 1)
	p.statusNow = make(chan struct{}, 1)
	p.since.CompareAndSwap(0, time.Now().UnixNano())
	p.following.Store(true)
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	if queue, err := brk.CreateDirectSubscription(); err != nil {
		e.logger.Warn("agent change subscription not created; changes reach this AWE only through its own check", "error", err)
	} else if err := brk.Subscribe(queue, a2a.AWEAgentsChangedTopic(namespace)); err != nil {
		_ = brk.DeleteQueue(queue.Name())
		e.logger.Warn("agent change topic not subscribed; changes reach this AWE only through its own check", "error", err)
	} else {
		p.loops.Go(func() { e.followAgentChanges(ctx, brk, queue) })
	}
	e.publishAgentStatus(ctx, brk, namespace, false)
	p.loops.Go(func() { e.checkPlatformAgents(ctx, brk, namespace) })
	p.loops.Go(func() {
		ticker := time.NewTicker(control.AWEStatusInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-p.statusNow:
			}
			e.publishAgentStatus(ctx, brk, namespace, false)
		}
	})
}

func (e *Executor) stopFollowingPlatform(ctx context.Context) {
	p := &e.platform
	p.stopMu.Lock()
	p.stopped = true
	cancel := p.cancel
	p.stopMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	applied := make(chan struct{})
	go func() {
		p.loops.Wait()
		close(applied)
	}()
	select {
	case <-applied:
	case <-ctx.Done():
		e.logger.Warn("platform agent changes still applying at stop; stopping the agents without waiting for them", "error", ctx.Err())
	}
	if brk := e.svc.Broker(); brk != nil {
		e.publishAgentStatus(ctx, brk, e.resolveNamespace(), true)
	}
}

func (e *Executor) followAgentChanges(ctx context.Context, brk broker.Broker, queue broker.Queue) {
	defer func() { _ = brk.DeleteQueue(queue.Name()) }()
	for {
		msg, err := brk.Receive(ctx, queue)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, broker.ErrQueueNotFound) {
			e.logger.Warn("agent change subscription is gone; changes reach this AWE only through its own check", "error", err)
			return
		}
		if err != nil || msg == nil {
			continue
		}
		msg.Ack()
		e.platform.trigger()
	}
}

func (e *Executor) checkPlatformAgents(ctx context.Context, brk broker.Broker, namespace string) {
	p := &e.platform
	var firstApplies *sync.WaitGroup
	for asks := 1; ; asks++ {
		var err error
		if firstApplies, err = e.checkOnce(ctx, brk, namespace, true); err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errAskCutByChange) {
			e.logger.Debug("asking again for this AWE's agents", "error", err)
			continue
		}
		level := slog.LevelDebug
		if asks == 1 {
			level = slog.LevelWarn
		}
		e.logger.Log(ctx, level, "no answer yet to this AWE's request for its agents; asking again", "retryIn", agentsAskInterval, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(agentsAskInterval):
		}
	}
	p.loops.Go(func() {
		firstApplies.Wait()
		if ctx.Err() != nil {
			return
		}
		p.answered.Store(true)
		if failed := p.failed(); failed > 0 {
			e.logger.Warn("applied this AWE's platform agents; some failed and are retried", "failed", failed)
			return
		}
		e.logger.Info("started this AWE's platform agents")
	})
	check := time.NewTicker(agentsCheckInterval)
	defer check.Stop()
	retry := time.NewTicker(agentRetryFirst)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-check.C:
		case <-retry.C:
			if !p.anyRetryDue(time.Now()) {
				continue
			}
		}
		if _, err := e.checkOnce(ctx, brk, namespace, false); err != nil && ctx.Err() == nil {
			e.logger.Warn("platform agents not checked; the next check asks again", "error", err)
		}
	}
}

func (e *Executor) checkOnce(ctx context.Context, brk broker.Broker, namespace string, first bool) (*sync.WaitGroup, error) {
	p := &e.platform
	p.checking.Lock()
	defer p.checking.Unlock()
	var reply control.AgentsReply
	var err error
	if first {
		reply, err = e.askUntilAChange(ctx, brk, namespace)
	} else {
		reply, err = e.askForAgents(ctx, brk, namespace, false)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return e.applyPlatformAgents(ctx, reply.Agents), nil
}

func (e *Executor) askUntilAChange(ctx context.Context, brk broker.Broker, namespace string) (control.AgentsReply, error) {
	p := &e.platform
	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	answered := make(chan struct{})
	woken := make(chan bool, 1)
	go func() {
		select {
		case <-p.wake:
			cancel()
			woken <- true
		case <-answered:
			woken <- false
		}
	}()
	reply, err := e.askForAgents(askCtx, brk, namespace, true)
	close(answered)
	if !<-woken {
		return reply, err
	}
	if err == nil {
		p.trigger()
		return reply, nil
	}
	if ctx.Err() != nil {
		return reply, err
	}
	return reply, errAskCutByChange
}

func (e *Executor) askForAgents(ctx context.Context, brk broker.Broker, namespace string, first bool) (control.AgentsReply, error) {
	var reply control.AgentsReply
	props := map[string]string{discovery.ProcessIDProperty: runtime.ProcessID()}
	if first {
		props[control.FirstAskProperty] = "true"
	}
	msg := &broker.Message{
		ReplyTo:        a2a.AWEAgentsReplyTopic(namespace, idgen.New()),
		UserProperties: props,
	}
	resp, err := brk.Request(ctx, a2a.AWEAgentsRequestTopic(namespace), msg, control.AWEAgentsAnswerWait)
	if err != nil {
		return reply, err
	}
	var listed struct {
		Agents *json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(resp.Payload, &listed); err != nil {
		return reply, fmt.Errorf("decode the platform's answer: %w", err)
	}
	if listed.Agents == nil {
		return reply, errAnswerWithoutAgentList
	}
	if err := json.Unmarshal(resp.Payload, &reply); err != nil {
		return reply, fmt.Errorf("decode the platform's answer: %w", err)
	}
	return reply, nil
}

func (e *Executor) applyPlatformAgents(ctx context.Context, desired []control.AgentApply) *sync.WaitGroup {
	p := &e.platform
	listed := make(map[string]*control.AgentApply, len(desired))
	for i := range desired {
		if desired[i].Name != "" {
			listed[desired[i].Name] = &desired[i]
		}
	}
	work := maps.Clone(listed)
	for _, name := range p.names() {
		if _, ok := work[name]; !ok {
			work[name] = nil
		}
	}
	for _, name := range e.platformInstanceNames() {
		if _, ok := work[name]; !ok {
			work[name] = nil
		}
	}
	slots := p.applySlots()
	var applying sync.WaitGroup
	for name, d := range work {
		l := p.lockFor(name)
		if !l.TryLock() {
			p.markSkipped(name)
			if !l.TryLock() {
				continue
			}
			p.takeSkipped(name)
		}
		applying.Add(1)
		p.loops.Go(func() {
			defer applying.Done()
			defer func() {
				l.Unlock()
				if p.takeSkipped(name) {
					p.trigger()
				}
				p.nudgeStatus()
			}()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			e.applyPlatformAgent(ctx, name, d)
		})
	}
	return &applying
}

func (e *Executor) platformInstanceNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []string
	for _, mi := range e.instances {
		if mi.deploymentID != "" {
			out = append(out, mi.inst.Name())
		}
	}
	return out
}

func (e *Executor) holdsInstance(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.findInstance(name) != nil
}

func (e *Executor) runningPlatformDeployment(name string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	mi := e.findInstance(name)
	if mi == nil || mi.deploymentID == "" {
		return ""
	}
	switch mi.getStatus() {
	case StatusRunning, StatusStarting:
		return mi.deploymentID
	}
	return ""
}

func (e *Executor) applyPlatformAgent(ctx context.Context, name string, d *control.AgentApply) {
	p := &e.platform
	cur, known := p.current(name)
	running := e.runningPlatformDeployment(name)
	switch {
	case d == nil:
		if running != "" {
			if err := e.removeInstance(ctx, name, removeForUndeploy); err != nil {
				e.logger.Warn("agent no longer deployed not stopped on this AWE; the next check retries", "name", name, "error", err)
				return
			}
			e.logger.Info("stopped an agent that is no longer deployed", "name", name)
		}
		p.forget(name)
		return
	case d.Undeploy:
		if known && cur.deploymentID == d.DeploymentID && cur.err == "" {
			return
		}
		if known && cur.deploymentID == d.DeploymentID && time.Now().Before(cur.retryAt) {
			return
		}
		var err error
		if e.holdsInstance(name) {
			err = e.removeInstance(ctx, name, removeForUndeploy)
		}
		e.recordApply(name, d.DeploymentID, cur, err)
		return
	case len(d.Payloads) == 0:
		if known && cur.err != "" {
			cur.failures++
			cur.retryAt = time.Now().Add(agentRetryDelay(cur.failures))
			p.record(name, cur)
		}
		return
	}
	if running == d.DeploymentID {
		p.record(name, appliedAgent{deploymentID: d.DeploymentID})
		return
	}
	if known && cur.deploymentID == d.DeploymentID && cur.err != "" && time.Now().Before(cur.retryAt) {
		return
	}
	var err error
	for _, payload := range d.Payloads {
		body := maps.Clone(payload)
		delete(body, control.DeploymentIDField)
		if _, err = e.addAndStart(ctx, config.FromMap(body), d.DeploymentID, true); err != nil {
			break
		}
	}
	e.recordApply(name, d.DeploymentID, cur, err)
}

func (e *Executor) recordApply(name, deploymentID string, cur appliedAgent, err error) {
	applied := appliedAgent{deploymentID: deploymentID}
	if err != nil {
		applied.err = err.Error()
		applied.failures = 1
		if cur.err != "" && cur.deploymentID == deploymentID {
			applied.failures = cur.failures + 1
		}
		retryIn := agentRetryDelay(applied.failures)
		applied.retryAt = time.Now().Add(retryIn)
		e.logger.Warn("agent deployment or undeployment failed on this AWE; it is retried", "name", name, "deploymentID", deploymentID, "retryIn", retryIn, "error", err)
	}
	e.platform.record(name, applied)
}

func (e *Executor) publishAgentStatus(ctx context.Context, brk broker.Broker, namespace string, leaving bool) {
	status := control.AWEStatus{ProcessID: runtime.ProcessID(), Leaving: leaving}
	if !leaving {
		status.Agents = e.platform.report()
	}
	payload, err := json.Marshal(status)
	if err != nil {
		return
	}
	if err := brk.Publish(ctx, a2a.AWEStatusTopic(namespace), &broker.Message{Payload: payload}); err != nil {
		e.logger.Debug("AWE status not published", "error", err)
	}
}
