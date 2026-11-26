// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const (
	msgAgentRestarting   = "the agent is restarting with new settings"
	closedForRedeploy    = "the agent was deployed again with new settings"
	closedAgentRemoved   = "the agent was removed from this entrypoint"
	closedTokenReplaced  = "the agent's token was replaced"
	settingChangeRefused = "agent link: %s changed: an entrypoint-level setting changes only when the entrypoint is deployed again, which restarts it"
)

var errNotRunning = fmt.Errorf("%w: agent link: only a running agent link takes a new set of agents", entrypoint.ErrRestartRequired)

type agentRestart struct {
	running *agentState
	next    *AgentConfig
}

type rosterChange struct {
	removed   []*agentState
	restarted []agentRestart
	added     []*AgentConfig
	unchanged int
}

func (r *agentRoster) changeTo(next map[string]*AgentConfig) rosterChange {
	var change rosterChange
	running := map[string]bool{}
	for _, s := range r.all() {
		running[s.cfg.Name] = true
		cfg, kept := next[s.cfg.Name]
		switch {
		case !kept:
			change.removed = append(change.removed, s)
		case s.cfg.sameAs(cfg):
			change.unchanged++
		default:
			change.restarted = append(change.restarted, agentRestart{running: s, next: cfg})
		}
	}
	for name, cfg := range next {
		if !running[name] {
			change.added = append(change.added, cfg)
		}
	}
	slices.SortFunc(change.added, func(x, y *AgentConfig) int { return cmp.Compare(x.Name, y.Name) })
	return change
}

func (a *AgentConfig) sameAs(other *AgentConfig) bool {
	return reflect.DeepEqual(a.peersInOrder(), other.peersInOrder())
}

func (a *AgentConfig) peersInOrder() AgentConfig {
	ordered := *a
	ordered.Peers = slices.Sorted(slices.Values(a.Peers))
	return ordered
}

func (c *Config) settingsChangedIn(next *Config) []string {
	var changed []string
	for _, setting := range []struct {
		key     string
		differs bool
	}{
		{"max_frame_bytes", c.MaxFrameBytes != next.MaxFrameBytes},
		{"max_inflight", c.MaxInflight != next.MaxInflight},
		{"max_connections_per_agent", c.MaxConnectionsPerAgent != next.MaxConnectionsPerAgent},
		{"max_connects_per_minute", c.MaxConnectsPerMinute != next.MaxConnectsPerMinute},
		{"task_ttl_seconds", c.TaskTTL != next.TaskTTL},
		{"platform_managed", c.PlatformManaged != next.PlatformManaged},
	} {
		if setting.differs {
			changed = append(changed, setting.key)
		}
	}
	return changed
}

func (a *Adapter) Reconfigure(ctx context.Context, adapterConfig map[string]any) error {
	if a.configErr != nil {
		return a.configErr
	}
	next, err := ParseConfig(adapterConfig)
	if err != nil {
		return fmt.Errorf("agent_link config: %w", err)
	}
	a.roster.beginChange()
	defer a.roster.endChange()
	if !a.started.Load() || a.ctx.Err() != nil {
		return errNotRunning
	}
	if changed := a.cfg.settingsChangedIn(next); len(changed) > 0 {
		return fmt.Errorf("%w: "+settingChangeRefused, entrypoint.ErrRestartRequired, strings.Join(changed, ", "))
	}
	a.roster.forgetFailedStartsNotIn(next.Agents)
	change := a.roster.changeTo(next.Agents)
	a.shutDownDeparting(change)
	for _, s := range change.removed {
		s.delist()
		s.letGoOfQueues()
	}
	if a.ctx.Err() != nil {
		return errNotRunning
	}
	var failures []error
	for _, restart := range change.restarted {
		if a.ctx.Err() != nil {
			return errNotRunning
		}
		if err := a.restartAgent(ctx, restart.running, restart.next); err != nil {
			failures = append(failures, err)
		}
	}
	for _, cfg := range change.added {
		if a.ctx.Err() != nil {
			return errNotRunning
		}
		if err := a.serveAgent(ctx, cfg); err != nil {
			failures = append(failures, err)
		}
	}
	a.logger.Info("agent link reconfigured", "added", len(change.added), "removed", len(change.removed),
		"changed", len(change.restarted), "unchanged", change.unchanged, "failed", len(failures))
	return errors.Join(failures...)
}

func (a *Adapter) serveAgent(ctx context.Context, cfg *AgentConfig) error {
	return a.bringUp(ctx, newAgentState(a, cfg))
}

func (a *Adapter) bringUp(ctx context.Context, s *agentState) error {
	if err := s.start(ctx); err != nil {
		a.roster.failedToStart(s.cfg, a.now())
		a.takeDown(s, redeployed(websocket.CloseServiceRestart, closedForRedeploy))
		return fmt.Errorf("agent link: start %s: %w", s.cfg.Name, err)
	}
	if !a.roster.putUnlessDone(a.ctx, s) {
		a.takeDown(s, redeployed(websocket.CloseServiceRestart, closedForRedeploy))
		return errNotRunning
	}
	if term := a.leading.Load(); term != nil {
		s.lead(term)
	}
	return nil
}

func (a *Adapter) takeDown(s *agentState, leaving departure) {
	s.end(leaving)
	s.letGoOfQueues()
	a.roster.drop(s)
}

func (a *Adapter) shutDownDeparting(change rosterChange) {
	for _, s := range change.removed {
		a.roster.drop(s)
	}
	var shutdowns sync.WaitGroup
	for _, s := range change.removed {
		shutdowns.Go(func() { s.shutDown(removedFromItsEntrypoint) })
	}
	for _, restart := range change.restarted {
		shutdowns.Go(func() { restart.running.shutDown(restart.departure()) })
	}
	shutdowns.Wait()
}

func (r agentRestart) departure() departure {
	if !bytes.Equal(r.running.cfg.TokenSHA256, r.next.TokenSHA256) {
		return redeployed(closeCredentialsRevoked, closedTokenReplaced)
	}
	return redeployed(websocket.CloseServiceRestart, closedForRedeploy)
}

func (a *Adapter) restartAgent(ctx context.Context, running *agentState, next *AgentConfig) error {
	if !next.Enabled {
		running.delist()
	}
	if running.cfg.Wake != nil && next.Wake == nil {
		running.removeWakeQueue()
	}
	if err := a.serveAgent(ctx, next); err != nil {
		a.roster.drop(running)
		return err
	}
	return nil
}

const settledADepartingAgent = "agent link settled the tasks of an agent it removed or restarted"

var removedFromItsEntrypoint = departure{
	closeCode:       closeCredentialsRevoked,
	closeReason:     closedAgentRemoved,
	unanswered:      failedByRemoval,
	requestLost:     failedByRemoval,
	requestsEndHere: true,
}

func redeployed(closeCode int, closeReason string) departure {
	return departure{closeCode: closeCode, closeReason: closeReason, unanswered: failedByRedeploy, requestLost: failedByRedeployWithItsRequestLost}
}

func (s *agentState) shutDown(leaving departure) {
	settled, unanswered := s.end(leaving)
	s.a.logger.Info(settledADepartingAgent, "agentName", s.cfg.Name, "failed", settled.failed, "forgotten", settled.forgotten,
		"unanswered", unanswered, "resultsDropped", settled.resultsDropped, "usageEventsDropped", settled.usageEventsDropped)
}

func (s *agentState) end(leaving departure) (settled standDownTally, unanswered int) {
	s.departing.Store(true)
	settle, cancel := context.WithTimeout(s.a.contextOutlivingStop(), s.a.settleWait)
	settled = s.standDown(settle, leaving)
	cancel()
	s.retire()
	s.router.CancelAllWithPropagation(s.ns())
	s.work.closeAndWait()
	s.stopPromptTimers()
	return settled, s.giveUpItsUnpublishedAnswers()
}

func (s *agentState) giveUpItsUnpublishedAnswers() (givenUp int) {
	type unpublished struct{ taskID, traceID, answerKind string }
	var left []unpublished
	s.mu.Lock()
	for taskID, b := range s.tasks {
		if b.final {
			continue
		}
		if b.unanswered != nil {
			left = append(left, unpublished{taskID, a2a.DecodeUserProperties(b.props).TraceID, b.unanswered.kind})
		}
		delete(s.tasks, taskID)
	}
	s.mu.Unlock()
	for _, task := range left {
		s.a.logger.Error(gaveUpAnsweringATask, "agentName", s.cfg.Name, "taskID", task.taskID, "traceID", task.traceID,
			"answerKind", task.answerKind)
	}
	return len(left)
}

func (s *agentState) stopPromptTimers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, asked := range s.asks {
		asked.timer.Stop()
		delete(s.asks, id)
	}
}

func (s *agentState) openTasks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	open := 0
	for _, b := range s.tasks {
		if !b.final {
			open++
		}
	}
	return open
}

func (s *agentState) delist() {
	s.cardMu.Lock()
	defer s.cardMu.Unlock()
	_ = s.publishRemovalCard(s.a.ctx)
}

func (s *agentState) letGoOfQueues() {
	_ = s.brk().DeleteQueue(s.queueName())
	_ = s.brk().DeleteQueue(s.wakeQueueName())
}

func (s *agentState) removeWakeQueue() {
	if !s.a.serves() {
		return
	}
	if err := s.brk().DeprovisionQueue(s.wakeQueueName()); err != nil {
		s.a.logger.Warn("agent link could not remove the wake queue of an agent that is no longer request-driven",
			"agentName", s.cfg.Name, "error", err)
	}
}
