// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"
)

func nlog() *slog.Logger { return slog.Default().With("logger_name", "session") }

type MemoryStore struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	checkpoints  map[string]*TaskCheckpoint
	pendingCalls map[string]PendingCall
	runningTasks map[string]memRunningTask
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:     make(map[string]*Session),
		checkpoints:  make(map[string]*TaskCheckpoint),
		pendingCalls: make(map[string]PendingCall),
		runningTasks: make(map[string]memRunningTask),
	}
}

func (s *MemoryStore) OutlivesProcess() bool { return false }

func OutlivesProcess(store Store) bool {
	if s, ok := store.(interface{ OutlivesProcess() bool }); ok {
		return s.OutlivesProcess()
	}
	return true
}

func sessionKey(appName, userID, sessionID string) string {
	return appName + "/" + userID + "/" + sessionID
}

func (s *MemoryStore) Get(_ context.Context, appName, userID, sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionKey(appName, userID, sessionID)]
	if !ok {
		return nil, nil
	}
	return copySession(sess), nil
}

func (s *MemoryStore) GetByContextAndTask(_ context.Context, appName, userID, contextID, taskID string) (*Session, error) {
	if taskID == "" {
		return nil, fmt.Errorf("GetByContextAndTask: taskID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sess := range s.sessions {
		if sess.AppName != appName || sess.UserID != userID || sess.ContextID != contextID {
			continue
		}
		if sess.TaskID == nil || *sess.TaskID != taskID {
			continue
		}
		return copySession(sess), nil
	}
	return nil, nil
}

func (s *MemoryStore) Create(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sessionKey(sess.AppName, sess.UserID, sess.SessionID)
	if _, exists := s.sessions[key]; exists {
		return fmt.Errorf("session %q: %w", key, ErrSessionExists)
	}
	if sess.TaskID != nil && *sess.TaskID != "" {
		for _, other := range s.sessions {
			if other.AppName == sess.AppName && other.UserID == sess.UserID &&
				other.ContextID == sess.ContextID && other.TaskID != nil &&
				*other.TaskID == *sess.TaskID {
				return fmt.Errorf("session for (context_id=%s, task_id=%s): %w",
					sess.ContextID, *sess.TaskID, ErrSessionExists)
			}
		}
	}

	stored := copySession(sess)
	stored.Version = 0
	if stored.ContextID == "" {
		stored.ContextID = stored.SessionID
	}
	s.sessions[key] = stored
	return nil
}

func (s *MemoryStore) Save(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sessionKey(sess.AppName, sess.UserID, sess.SessionID)
	existing, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("session %q not found", key)
	}

	if existing.Version != sess.Version {
		return fmt.Errorf("%w: expected version %d, got %d", ErrOptimisticLock, existing.Version, sess.Version)
	}

	stored := copySession(sess)
	stored.Messages = existing.Messages
	stored.Version = sess.Version + 1
	stored.UpdatedAt = time.Now()
	s.sessions[key] = stored

	sess.Version = stored.Version
	sess.UpdatedAt = stored.UpdatedAt

	return nil
}

func (s *MemoryStore) AppendMessages(_ context.Context, appName, userID, sessionID string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sessionKey(appName, userID, sessionID)
	sess, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("append messages: session %q not found", key)
	}
	sess.Messages = append(sess.Messages, msgs...)
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, appName, userID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionKey(appName, userID, sessionID))
	s.deleteSessionRunningTasksLocked(appName, userID, sessionID)
	return nil
}

func (s *MemoryStore) ListSessions(_ context.Context, appName, userID string) ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := appName + "/" + userID + "/"
	var result []*Session
	for key, sess := range s.sessions {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, copySession(sess))
		}
	}
	if result == nil {
		result = []*Session{}
	}
	return result, nil
}

func heldBy(cp *TaskCheckpoint, ownerID string) bool {
	return (cp.OwnerID == "" || cp.OwnerID == ownerID) && cp.Final == nil
}

func ownerGone(cp *TaskCheckpoint, now time.Time, silentFor time.Duration, releasedOnly bool) bool {
	if cp.OwnerID == "" {
		return true
	}
	if releasedOnly {
		return false
	}
	if cp.RenewedAt != nil {
		return !cp.RenewedAt.After(time.Now().Add(-silentFor))
	}
	return cp.LeaseExpiresAt == nil || !cp.LeaseExpiresAt.After(now)
}

func (s *MemoryStore) SaveCheckpoint(_ context.Context, cp *TaskCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.checkpoints[cp.TaskID]
	if exists && !heldBy(existing, cp.OwnerID) {
		return ErrNotOwner
	}
	if err := s.refuseTakenCallsLocked(cp); err != nil {
		return err
	}
	stored := copyCheckpoint(cp)
	now := time.Now()
	stored.UpdatedAt = now
	stored.RenewedAt = &now
	stored.Final = nil
	if exists {
		stored.CreatedAt = existing.CreatedAt
	} else {
		stored.CreatedAt = stored.UpdatedAt
	}
	s.checkpoints[cp.TaskID] = stored
	s.replaceCallsLocked(cp)
	return nil
}

func (s *MemoryStore) UpdateCheckpoint(_ context.Context, cp *TaskCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.checkpoints[cp.TaskID]
	if !exists || !heldBy(existing, cp.OwnerID) {
		return ErrNotOwner
	}
	if err := s.refuseTakenCallsLocked(cp); err != nil {
		return err
	}
	stored := copyCheckpoint(cp)
	now := time.Now()
	stored.UpdatedAt = now
	stored.RenewedAt = &now
	stored.Final = nil
	stored.CreatedAt = existing.CreatedAt
	stored.AppName, stored.UserID, stored.SessionID, stored.AgentName = existing.AppName, existing.UserID, existing.SessionID, existing.AgentName
	s.checkpoints[cp.TaskID] = stored
	s.replaceCallsLocked(cp)
	return nil
}

func (s *MemoryStore) MarkAnswering(_ context.Context, taskID, ownerID string, final *FinalMessage, until time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[taskID]
	if !ok || !heldBy(cp, ownerID) {
		return false, nil
	}
	now := time.Now()
	stored := *final
	stored.UserProperties = maps.Clone(final.UserProperties)
	stored.Payload = slices.Clone(final.Payload)
	cp.Final = &stored
	cp.PendingCount = 0
	cp.OwnerID = ownerID
	cp.LeaseExpiresAt = &until
	cp.RenewedAt = &now
	cp.UpdatedAt = now
	for corrID, pc := range s.pendingCalls {
		if pc.TaskID == taskID {
			delete(s.pendingCalls, corrID)
		}
	}
	return true, nil
}

func (s *MemoryStore) RecordToolResult(_ context.Context, taskID, corrID string, result *ToolCallResult) (int, error) {
	if result == nil || result.ToolCallID == "" {
		return 0, errors.New("session: tool result has no tool call id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[taskID]
	if !ok {
		return 0, fmt.Errorf("checkpoint %q not found", taskID)
	}

	seen := false
	for i := range cp.ToolResults {
		if cp.ToolResults[i].ToolCallID == result.ToolCallID {
			seen = true
			break
		}
	}

	if corrID != "" {
		if row, outstanding := s.pendingCalls[corrID]; !outstanding || row.TaskID != taskID {
			return cp.PendingCount, ErrCallNotOutstanding
		}
		delete(s.pendingCalls, corrID)
	}

	if !seen {
		recorded := *result
		recorded.Uncounted = false
		cp.ToolResults = append(cp.ToolResults, recorded)
		if !result.Uncounted {
			cp.PendingCount--
		}
	}
	if cp.PendingCount < 0 {
		nlog().Warn("session: pending count went negative, clamping to 0",
			"taskID", taskID, "sessionID", cp.SessionID, "pendingCountBefore", cp.PendingCount+1)
		cp.PendingCount = 0
	}
	cp.UpdatedAt = time.Now()

	return cp.PendingCount, nil
}

func (s *MemoryStore) PopCheckpoint(_ context.Context, taskID string) (*TaskCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[taskID]
	if !ok || cp.Final != nil {
		return nil, nil
	}
	delete(s.checkpoints, taskID)
	for corrID, pending := range s.pendingCalls {
		if pending.TaskID == taskID {
			delete(s.pendingCalls, corrID)
		}
	}
	return cp, nil
}

func (s *MemoryStore) AdoptCheckpoint(_ context.Context, taskID, newOwnerID string, now time.Time, leaseFor, silentFor time.Duration, releasedOnly bool) (*TaskCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[taskID]
	if !ok || cp.PendingCount > 0 || !ownerGone(cp, now, silentFor, releasedOnly) {
		return nil, nil
	}

	lease := now.Add(leaseFor)
	renewed := time.Now()
	cp.OwnerID = newOwnerID
	cp.LeaseExpiresAt = &lease
	cp.RenewedAt = &renewed
	cp.UpdatedAt = now
	return s.readCheckpoint(cp), nil
}

func (s *MemoryStore) readCheckpoint(cp *TaskCheckpoint) *TaskCheckpoint {
	read := copyCheckpoint(cp)
	read.ReadAt = time.Now()
	return read
}

func (s *MemoryStore) GetCheckpoint(_ context.Context, taskID string) (*TaskCheckpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cp, ok := s.checkpoints[taskID]
	if !ok {
		return nil, nil
	}
	return s.readCheckpoint(cp), nil
}

func (s *MemoryStore) ReclaimUnowned(_ context.Context, taskID, ownerID string, until time.Time) (bool, error) {
	if ownerID == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[taskID]
	if !ok || cp.OwnerID != "" {
		return false, nil
	}
	lease, renewed := until, time.Now()
	cp.OwnerID = ownerID
	cp.LeaseExpiresAt = &lease
	cp.RenewedAt = &renewed
	return true, nil
}

func (s *MemoryStore) CheckpointOwner(_ context.Context, taskID string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cp, ok := s.checkpoints[taskID]
	if !ok {
		return "", false, nil
	}
	return cp.OwnerID, true, nil
}

func (s *MemoryStore) RenewLeases(_ context.Context, ownerID string, taskIDs []string, until time.Time) (int, error) {
	if ownerID == "" || len(taskIDs) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	renewed := 0
	for _, taskID := range taskIDs {
		cp, ok := s.checkpoints[taskID]
		if !ok || cp.OwnerID != ownerID {
			continue
		}
		lease, renewedAt := until, time.Now()
		cp.LeaseExpiresAt = &lease
		cp.RenewedAt = &renewedAt
		renewed++
	}
	return renewed, nil
}

func (s *MemoryStore) ReleaseOwnership(_ context.Context, taskID, ownerID string) error {
	if ownerID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[taskID]
	if !ok || cp.OwnerID != ownerID {
		return nil
	}
	cp.OwnerID = ""
	lapsed := time.Now()
	cp.LeaseExpiresAt = &lapsed
	return nil
}

func (s *MemoryStore) DeleteCheckpointIfOwned(_ context.Context, taskID, ownerID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.checkpoints[taskID]
	if !ok {
		return false, nil
	}
	if cp.OwnerID != "" && cp.OwnerID != ownerID {
		return false, nil
	}
	delete(s.checkpoints, taskID)
	for corrID, pc := range s.pendingCalls {
		if pc.TaskID == taskID {
			delete(s.pendingCalls, corrID)
		}
	}
	return true, nil
}

func (s *MemoryStore) DeleteAbandonedCheckpoint(_ context.Context, taskID string, lapsedBefore time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cp, ok := s.checkpoints[taskID]; ok {
		live := cp.OwnerID != "" && cp.LeaseExpiresAt != nil && cp.LeaseExpiresAt.After(lapsedBefore)
		if live {
			return false, nil
		}
		delete(s.checkpoints, taskID)
	}
	for corrID, pc := range s.pendingCalls {
		if pc.TaskID == taskID {
			delete(s.pendingCalls, corrID)
		}
	}
	return true, nil
}

func (s *MemoryStore) PurgeAbandonedCheckpoints(_ context.Context, agentName string, lapsedBefore time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	purged := map[string]bool{}
	for taskID, cp := range s.checkpoints {
		if len(purged) >= limit {
			break
		}
		if agentName != "" && cp.AgentName != agentName {
			continue
		}
		if cp.LeaseExpiresAt != nil && cp.LeaseExpiresAt.After(lapsedBefore) {
			continue
		}
		delete(s.checkpoints, taskID)
		purged[taskID] = true
	}
	orphans := 0
	for corrID, pc := range s.pendingCalls {
		if purged[pc.TaskID] {
			delete(s.pendingCalls, corrID)
			continue
		}
		if orphans >= limit {
			continue
		}
		if agentName != "" && pc.AgentName != agentName {
			continue
		}
		if _, ok := s.checkpoints[pc.TaskID]; ok {
			continue
		}
		if pc.ExpiresAt == nil || pc.ExpiresAt.After(lapsedBefore) {
			continue
		}
		delete(s.pendingCalls, corrID)
		orphans++
	}
	return len(purged), nil
}

func (s *MemoryStore) DeleteCheckpoint(_ context.Context, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.checkpoints, taskID)
	for corrID, pc := range s.pendingCalls {
		if pc.TaskID == taskID {
			delete(s.pendingCalls, corrID)
		}
	}
	return nil
}

func (s *MemoryStore) RegisterPendingCalls(_ context.Context, taskID, ownerID string, calls []PendingCall) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cp, ok := s.checkpoints[taskID]; !ok || !heldBy(cp, ownerID) {
		return ErrNotOwner
	}
	replacement := &TaskCheckpoint{TaskID: taskID, ReplacePendingCalls: true, PendingCalls: calls}
	if err := s.refuseTakenCallsLocked(replacement); err != nil {
		return err
	}
	s.replaceCallsLocked(replacement)
	return nil
}

func (s *MemoryStore) refuseTakenCallsLocked(cp *TaskCheckpoint) error {
	if !cp.ReplacePendingCalls {
		return nil
	}
	for i := range cp.PendingCalls {
		if row, held := s.pendingCalls[cp.PendingCalls[i].CorrID]; held && row.TaskID != cp.PendingCalls[i].TaskID {
			return &CorrelationTakenError{CorrID: cp.PendingCalls[i].CorrID}
		}
	}
	return nil
}

func (s *MemoryStore) replaceCallsLocked(cp *TaskCheckpoint) {
	if !cp.ReplacePendingCalls {
		return
	}
	for corrID, pc := range s.pendingCalls {
		if pc.TaskID == cp.TaskID {
			delete(s.pendingCalls, corrID)
		}
	}
	for i := range cp.PendingCalls {
		stored := cp.PendingCalls[i]
		stored.CreatedAt = time.Now()
		s.pendingCalls[stored.CorrID] = stored
	}
}

func (s *MemoryStore) LookupPendingCall(_ context.Context, agentName, corrID string) (*PendingCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pc, ok := s.pendingCalls[corrID]
	if !ok || pc.AgentName != agentName {
		return nil, nil
	}
	return &pc, nil
}

func (s *MemoryStore) PendingCallTask(_ context.Context, corrID string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pc, held := s.pendingCalls[corrID]
	return pc.TaskID, held, nil
}

func (s *MemoryStore) ExtendPendingCall(_ context.Context, agentName, corrID string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pc, ok := s.pendingCalls[corrID]
	if !ok || pc.AgentName != agentName || pc.ExpiresAt == nil || !until.After(*pc.ExpiresAt) {
		return nil
	}
	pc.ExpiresAt = &until
	s.pendingCalls[corrID] = pc
	return nil
}

func (s *MemoryStore) DeletePendingCall(_ context.Context, corrID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingCalls, corrID)
	return nil
}

func (s *MemoryStore) ListExpiredPendingCalls(_ context.Context, agentName string, now time.Time, limit int) ([]PendingCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []PendingCall
	for _, pc := range s.pendingCalls {
		if pc.AgentName != agentName {
			continue
		}
		if pc.ExpiresAt != nil && !pc.ExpiresAt.After(now) {
			out = append(out, pc)
		}
	}
	slices.SortFunc(out, func(a, b PendingCall) int { return a.ExpiresAt.Compare(*b.ExpiresAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ListResumableCheckpoints(_ context.Context, agentName string, now time.Time, silentFor time.Duration, limit int, releasedOnly bool) ([]TaskCheckpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []*TaskCheckpoint
	for _, cp := range s.checkpoints {
		if cp.AgentName != agentName || cp.PendingCount > 0 || !ownerGone(cp, now, silentFor, releasedOnly) {
			continue
		}
		matched = append(matched, cp)
	}
	slices.SortFunc(matched, func(a, b *TaskCheckpoint) int {
		return leaseOrZero(a).Compare(leaseOrZero(b))
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	out := make([]TaskCheckpoint, 0, len(matched))
	for _, cp := range matched {
		out = append(out, *s.readCheckpoint(cp))
	}
	return out, nil
}

func leaseOrZero(cp *TaskCheckpoint) time.Time {
	if cp.LeaseExpiresAt == nil {
		return time.Time{}
	}
	return *cp.LeaseExpiresAt
}

func copySession(s *Session) *Session {
	cp := *s
	if s.Messages != nil {
		cp.Messages = make([]Message, len(s.Messages))
		copy(cp.Messages, s.Messages)
	}
	if s.Metadata != nil {
		cp.Metadata = nil
		b, _ := json.Marshal(s.Metadata)
		_ = json.Unmarshal(b, &cp.Metadata)
	}
	return &cp
}

func copyCheckpoint(c *TaskCheckpoint) *TaskCheckpoint {
	cp := *c
	cp.ReplacePendingCalls, cp.PendingCalls = false, nil
	if c.ToolResults != nil {
		cp.ToolResults = make([]ToolCallResult, len(c.ToolResults))
		copy(cp.ToolResults, c.ToolResults)
	}
	if c.State != nil {
		cp.State = make([]byte, len(c.State))
		copy(cp.State, c.State)
	}
	return &cp
}
