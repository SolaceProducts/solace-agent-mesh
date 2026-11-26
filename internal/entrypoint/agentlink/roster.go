// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"cmp"
	"context"
	"crypto/subtle"
	"slices"
	"sync"
	"time"
)

type agentRoster struct {
	mu       sync.RWMutex
	byName   map[string]*agentState
	starting map[string]*failedStart

	changing chan struct{}
}

func newAgentRoster() agentRoster {
	return agentRoster{byName: map[string]*agentState{}, changing: make(chan struct{}, 1)}
}

type failedStart struct {
	cfg      *AgentConfig
	failures int
	retryAt  time.Time
}

func (r *agentRoster) beginChange() { r.changing <- struct{}{} }

func (r *agentRoster) tryBeginChange() bool {
	select {
	case r.changing <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *agentRoster) endChange() { <-r.changing }

func (r *agentRoster) get(name string) *agentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byName[name]
}

func (r *agentRoster) all() []*agentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]*agentState, 0, len(r.byName))
	for _, s := range r.byName {
		agents = append(agents, s)
	}
	slices.SortFunc(agents, func(x, y *agentState) int { return cmp.Compare(x.cfg.Name, y.cfg.Name) })
	return agents
}

func (r *agentRoster) settled(ctx context.Context) (agents []*agentState, waited bool) {
	select {
	case r.changing <- struct{}{}:
		defer r.endChange()
		return r.all(), true
	case <-ctx.Done():
		return r.all(), false
	}
}

func (r *agentRoster) size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}

func (r *agentRoster) put(s *agentState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[s.cfg.Name] = s
	delete(r.starting, s.cfg.Name)
}

func (r *agentRoster) putUnlessDone(ctx context.Context, s *agentState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	r.byName[s.cfg.Name] = s
	delete(r.starting, s.cfg.Name)
	return true
}

func (r *agentRoster) failedToStart(cfg *AgentConfig, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.starting == nil {
		r.starting = map[string]*failedStart{}
	}
	failures := 1
	if prev := r.starting[cfg.Name]; prev != nil {
		failures = prev.failures + 1
	}
	r.starting[cfg.Name] = &failedStart{cfg: cfg, failures: failures, retryAt: now.Add(startRetryDelay(failures))}
}

func (r *agentRoster) nextStartAttempt(name string) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, failed := r.starting[name]
	if !failed {
		return time.Time{}, false
	}
	return f.retryAt, true
}

func (r *agentRoster) startsDue(now time.Time) []failedStart {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var due []failedStart
	for _, f := range r.starting {
		if !now.Before(f.retryAt) {
			due = append(due, *f)
		}
	}
	slices.SortFunc(due, func(x, y failedStart) int { return cmp.Compare(x.cfg.Name, y.cfg.Name) })
	return due
}

func (r *agentRoster) forgetFailedStartsNotIn(next map[string]*AgentConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.starting {
		if _, kept := next[name]; !kept {
			delete(r.starting, name)
		}
	}
}

func (r *agentRoster) isStarting(name string, tokenDigest []byte) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, failed := r.starting[name]
	return failed && subtle.ConstantTimeCompare(tokenDigest, f.cfg.TokenSHA256) == 1
}

func (r *agentRoster) drop(s *agentState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName[s.cfg.Name] == s {
		delete(r.byName, s.cfg.Name)
	}
}

func (a *Adapter) agent(name string) *agentState { return a.roster.get(name) }
