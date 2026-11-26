// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type taskMeta struct {
	taskDestination
	StatusMessage   string
	ControlsMessage string
	SignInMessages  []string
	Prompted        bool
	Saved           []recordedSave
}

func (m taskMeta) anchors() queueAnchors {
	return queueAnchors{Status: m.StatusMessage, Controls: m.ControlsMessage, SignIn: slices.Clone(m.SignInMessages), Prompted: m.Prompted}
}

type taskState struct {
	mu   sync.Mutex
	meta taskMeta

	lastSeen atomic.Int64

	queue     *replyQueue
	citations citationBook
	forgotten bool
	recovered bool
	streamed  atomic.Bool
	terminal  atomic.Bool
	end       atomic.Pointer[taskEnd]

	savedMu  sync.Mutex
	saved    []entrypoint.SavedArtifact
	savedSeq map[string]int
	lastSeq  int
}

func (s *taskState) recordSaved(saves []entrypoint.SavedArtifact) []recordedSave {
	if len(saves) == 0 {
		return nil
	}
	s.savedMu.Lock()
	defer s.savedMu.Unlock()
	s.saved = append(s.saved, saves...)
	if s.savedSeq == nil {
		s.savedSeq = make(map[string]int)
	}
	var record []recordedSave
	for _, saved := range saves {
		seq, known := s.savedSeq[saved.Filename]
		if !known {
			if len(s.savedSeq) >= maxRecordedSaves {
				continue
			}
			s.lastSeq++
			seq = s.lastSeq
			s.savedSeq[saved.Filename] = seq
		}
		record = append(record, recordedSave{SavedArtifact: saved, Seq: seq})
	}
	return record
}

func (s *taskState) mergeSaved(files []recordedSave) {
	s.savedMu.Lock()
	defer s.savedMu.Unlock()
	if s.savedSeq == nil {
		s.savedSeq = make(map[string]int, len(files))
	}
	for _, f := range files {
		if _, known := s.savedSeq[f.Filename]; known {
			continue
		}
		s.saved = append(s.saved, f.SavedArtifact)
		s.savedSeq[f.Filename] = f.Seq
		s.lastSeq = max(s.lastSeq, f.Seq)
	}
}

func (s *taskState) loadSaved(files []recordedSave) {
	s.savedMu.Lock()
	defer s.savedMu.Unlock()
	s.savedSeq = make(map[string]int, len(files))
	for _, f := range files {
		s.saved = append(s.saved, f.SavedArtifact)
		s.savedSeq[f.Filename] = f.Seq
		s.lastSeq = max(s.lastSeq, f.Seq)
	}
}

func (s *taskState) savedArtifacts() []entrypoint.SavedArtifact {
	s.savedMu.Lock()
	defer s.savedMu.Unlock()
	return append([]entrypoint.SavedArtifact(nil), s.saved...)
}

func (s *taskState) snapshot() taskMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta
}

func (s *taskState) setAnchors(a queueAnchors) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta.StatusMessage, s.meta.ControlsMessage, s.meta.SignInMessages, s.meta.Prompted = a.Status, a.Controls, a.SignIn, a.Prompted
}

func (s *taskState) touch(now time.Time) { s.lastSeen.Store(now.UnixNano()) }

func (s *taskState) idleSince(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, s.lastSeen.Load()))
}

type routingLookup int

const (
	lookupStatus routingLookup = iota
	lookupTerminal
)

func (a *Adapter) trackTask(taskID string, st *taskState) {
	st.touch(time.Now())
	a.tasksMu.Lock()
	a.tasks[taskID] = st
	a.tasksMu.Unlock()
}

func (a *Adapter) runIdleTaskSweep(ctx context.Context) {
	ticker := time.NewTicker(a.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.releaseIdleTasks(now)
		}
	}
}

func (a *Adapter) releaseIdleTasks(now time.Time) {
	a.tasksMu.Lock()
	var idle []*taskState
	for id, st := range a.tasks {
		if st.idleSince(now) >= taskIdleRelease {
			idle = append(idle, st)
			delete(a.tasks, id)
		}
	}
	a.tasksMu.Unlock()
	for _, st := range idle {
		st.release()
	}
	if len(idle) > 0 {
		a.logger.Debug("google chat idle task state released", "count", len(idle))
	}
}

func (a *Adapter) adoptStatus(taskID string) func(context.Context) (queueAnchors, bool) {
	return func(ctx context.Context) (queueAnchors, bool) {
		deadline := time.Now().Add(statusAdoptWait)
		readFailed := false
		for {
			meta, state, err := a.readTaskItems(ctx, taskID)
			if err == nil && state == destinationFound && meta.StatusMessage != "" {
				return meta.anchors(), true
			}
			if err != nil && ctx.Err() == nil && !readFailed {
				readFailed = true
				a.logger.Warn("google chat status message lookup failed; a second status message may be posted", "taskID", taskID, "error", err)
			}
			if time.Now().After(deadline) {
				return queueAnchors{}, false
			}
			select {
			case <-ctx.Done():
				return queueAnchors{}, false
			case <-time.After(statusAdoptPoll):
			}
		}
	}
}

func (a *Adapter) forgetTask(taskID string) {
	if st := a.untrack(taskID); st != nil {
		st.release()
	}
}

func (a *Adapter) abandonTask(taskID string) {
	if st := a.untrack(taskID); st != nil {
		st.closeQueue((*replyQueue).Abandon)
	}
}

func (a *Adapter) untrack(taskID string) *taskState {
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	st := a.tasks[taskID]
	delete(a.tasks, taskID)
	return st
}

func (s *taskState) release() { s.closeQueue((*replyQueue).Close) }

func (s *taskState) closeQueue(closeQueue func(*replyQueue)) {
	s.mu.Lock()
	s.forgotten = true
	q := s.queue
	s.mu.Unlock()
	if q != nil {
		closeQueue(q)
	}
}

func (a *Adapter) trackedTask(taskID string) *taskState {
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	st := a.tasks[taskID]
	if st != nil {
		st.touch(time.Now())
	}
	return st
}

func (a *Adapter) LoadTask(ctx context.Context, taskID string) error {
	if a.trackedTask(taskID) != nil {
		return nil
	}
	_, _, err := a.loadTask(ctx, taskID)
	return err
}

func (a *Adapter) loadTask(ctx context.Context, taskID string) (*taskState, destinationState, error) {
	meta, state, err := a.readTaskItems(ctx, taskID)
	if err != nil || state != destinationFound {
		return nil, state, err
	}
	loaded := &taskState{meta: meta, recovered: true}
	loaded.loadSaved(meta.Saved)
	loaded.touch(time.Now())
	a.tasksMu.Lock()
	if existing := a.tasks[taskID]; existing != nil {
		a.tasksMu.Unlock()
		return existing, state, nil
	}
	a.tasks[taskID] = loaded
	a.tasksMu.Unlock()
	a.taskRegistry.SetMetadata(taskID, meta.taskDestination)
	a.meter().Counter(metricTaskRoutingRecovered, 1, "gatewayId", a.gatewayID)
	a.logger.Debug("google chat task loaded from its task items", "taskID", taskID)
	return loaded, state, nil
}

func (a *Adapter) resolveTask(ctx context.Context, taskID string, lookup routingLookup) *taskState {
	st := a.trackedTask(taskID)
	if st == nil {
		if st = a.loadForEvent(ctx, taskID, lookup); st == nil {
			return nil
		}
	}
	if lookup == lookupTerminal && !st.terminal.CompareAndSwap(false, true) {
		if end := st.end.Load(); end != nil {
			end.join(holdEvent(ctx))
		}
		return nil
	}
	if lookup == lookupStatus && st.terminal.Load() {
		return nil
	}
	return st
}

func (a *Adapter) loadForEvent(ctx context.Context, taskID string, lookup routingLookup) *taskState {
	attempts := 1
	if lookup == lookupTerminal {
		attempts = terminalLoadAttempts
	}
	for attempt := 1; ; attempt++ {
		readCtx, cancel := readShare(ctx, attempts-attempt+1, terminalLoadRetryDelay)
		st, state, err := a.loadTask(readCtx, taskID)
		cancel()
		if err == nil {
			if st == nil {
				a.logUnroutable(taskID, lookup, state)
			}
			return st
		}
		if attempt < attempts && sleepCtx(ctx, terminalLoadRetryDelay) {
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			a.logger.Debug("google chat task items read abandoned: the gateway is stopping", "taskID", taskID)
			return nil
		}
		a.logLookupFailed(taskID, lookup, err)
		return nil
	}
}

func readShare(ctx context.Context, reads int, pause time.Duration) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok || reads <= 1 {
		return context.WithCancel(ctx)
	}
	left := time.Until(deadline)
	if forReads := left - time.Duration(reads-1)*pause; forReads > 0 {
		left = forReads
	}
	return context.WithTimeout(ctx, left/time.Duration(reads))
}

func (a *Adapter) logLookupFailed(taskID string, lookup routingLookup, err error) {
	a.meter().Counter(metricTaskRoutingLost, 1, "gatewayId", a.gatewayID, "reason", "lookup_error")
	if lookup == lookupTerminal {
		a.logger.Error("google chat reply dropped: the task items could not be read", "taskID", taskID, "error", err)
		return
	}
	a.logger.Warn("google chat status update dropped: the task items could not be read", "taskID", taskID, "error", err)
}

func (a *Adapter) logUnroutable(taskID string, lookup routingLookup, state destinationState) {
	if lookup != lookupTerminal {
		a.logger.Debug("google chat status update for unknown task", "taskID", taskID)
		return
	}
	if state == destinationUndecodable {
		a.logger.Error("google chat reply dropped: the task destination cannot be decoded", "taskID", taskID)
		a.meter().Counter(metricTaskRoutingLost, 1, "gatewayId", a.gatewayID, "reason", "undecodable")
		return
	}
	a.logger.Warn("google chat reply dropped: task is unknown to this gateway", "taskID", taskID)
	a.meter().Counter(metricTaskRoutingLost, 1, "gatewayId", a.gatewayID, "reason", "not_found")
}

func (a *Adapter) OnTaskEndedElsewhere(taskID string) {
	a.stopMu.Lock()
	defer a.stopMu.Unlock()
	if a.bgStopped {
		return
	}
	a.bgWG.Go(func() { a.forgetTask(taskID) })
}
