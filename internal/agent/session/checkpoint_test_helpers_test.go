// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"testing"
	"time"
)

const (
	testTakeoverWindow = 2 * time.Minute
	testPurgeBatch     = 1000
)

func heldOwner(t *testing.T, store interface {
	CheckpointOwner(context.Context, string) (string, bool, error)
}, taskID string) string {
	t.Helper()
	owner, _, err := store.CheckpointOwner(t.Context(), taskID)
	if err != nil {
		t.Fatalf("read checkpoint owner: %v", err)
	}
	return owner
}

func silence(t *testing.T, store any, taskID string, d time.Duration) {
	t.Helper()
	switch s := store.(type) {
	case *SQLStore:
		if _, err := s.db.ExecContext(t.Context(),
			`UPDATE task_checkpoints SET renewed_at = renewed_at - ? WHERE task_id = ?`, d.Milliseconds(), taskID); err != nil {
			t.Fatalf("silence %s: %v", taskID, err)
		}
	case *MemoryStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		if cp, ok := s.checkpoints[taskID]; ok && cp.RenewedAt != nil {
			earlier := cp.RenewedAt.Add(-d)
			cp.RenewedAt = &earlier
		}
	default:
		t.Fatalf("silence: unsupported store %T", store)
	}
}

func clearRenewal(t *testing.T, store any, taskID string) {
	t.Helper()
	switch s := store.(type) {
	case *SQLStore:
		if _, err := s.db.ExecContext(t.Context(),
			`UPDATE task_checkpoints SET renewed_at = NULL WHERE task_id = ?`, taskID); err != nil {
			t.Fatalf("clear renewal %s: %v", taskID, err)
		}
	case *MemoryStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		if cp, ok := s.checkpoints[taskID]; ok {
			cp.RenewedAt = nil
		}
	default:
		t.Fatalf("clearRenewal: unsupported store %T", store)
	}
}

func seedOrphanCalls(t *testing.T, store any, calls []PendingCall) {
	t.Helper()
	switch s := store.(type) {
	case *SQLStore:
		for _, c := range calls {
			var expires any
			if c.ExpiresAt != nil {
				expires = formatDBTime(*c.ExpiresAt)
			}
			if _, err := s.db.ExecContext(t.Context(),
				`INSERT INTO task_pending_calls (corr_id, task_id, agent_name, tool_call_id, tool_name, trace_id, kind, created_at, expires_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				c.CorrID, c.TaskID, c.AgentName, c.ToolCallID, c.ToolName, c.TraceID, c.Kind, formatDBTime(time.Now()), expires); err != nil {
				t.Fatalf("seed orphan call: %v", err)
			}
		}
	case *MemoryStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range calls {
			s.pendingCalls[c.CorrID] = c
		}
	default:
		t.Fatalf("seedOrphanCalls: unsupported store %T", store)
	}
}
