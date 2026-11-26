// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const samCancelledSpelling = "cancelled"

func TestSAMsPromptStatusIsMappedToTheLinkWiresSpellingWhereThePromptIsAnswered(t *testing.T) {
	for _, tc := range []struct{ name, sam, wire string }{
		{"SAM's two-l cancel", samCancelledSpelling, "canceled"},
		{"a status SAM does not define", "bogus", "canceled"},
		{"timed out passes through", "timed_out", "timed_out"},
		{"answered passes through", "answered", "answered"},
	} {
		t.Run("question: "+tc.name, func(t *testing.T) {
			r, c := connectedRig(t, nil)
			r.taskOn(c, "T1", nil)
			c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
			waitFor(t, func() bool {
				s := r.adapter.agent(agentName)
				s.mu.Lock()
				defer s.mu.Unlock()
				return len(s.asks) == 1
			})

			publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: tc.sam,
				Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})

			requireStatus(t, c.next(typePromptAnswer), tc.wire)
		})
	}

	t.Run("approval: SAM's two-l cancel is a refusal", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		c.send(approvalPrompt("hil-approval-1", 600))
		waitFor(t, func() bool {
			s := r.adapter.agent(agentName)
			s.mu.Lock()
			defer s.mu.Unlock()
			return len(s.asks) == 1
		})

		publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: samCancelledSpelling,
			Data: map[string]any{"approved": true}})

		f := c.next(typePromptAnswer)
		requireStatus(t, f, "canceled")
		if f.Approved == nil || *f.Approved {
			t.Fatalf("a cancelled approval was reported as approved: %s", f.Raw)
		}
	})
}

func requireStatus(t *testing.T, f received, want string) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(f.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["status"] != want {
		t.Fatalf("prompt_answer status %v, want the literal %q: %s", raw["status"], want, f.Raw)
	}
}

func TestAnAnswerToACancelledTasksPromptDoesNotRestoreTheTasksTimeLimit(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
	s := r.adapter.agent(agentName)
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.asks) == 1
	})
	cancelledUntil := time.Now().Add(cancelGrace)
	r.locked(func(s *agentState) {
		s.tasks["T1"].cancelled = true
		s.tasks["T1"].deadline = cancelledUntil
	})

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: "answered",
		Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})
	c.next(typePromptAnswer)

	r.locked(func(s *agentState) {
		if got := s.tasks["T1"].deadline; !got.Equal(cancelledUntil) {
			t.Fatalf("the cancelled task's deadline moved by %s", got.Sub(cancelledUntil))
		}
	})
}

func TestAnAnswerToALiveTasksPromptRestartsTheTasksTimeLimit(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
	s := r.adapter.agent(agentName)
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.asks) == 1
	})
	almostOver := time.Now().Add(time.Minute)
	r.locked(func(s *agentState) { s.tasks["T1"].deadline = almostOver })

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: "answered",
		Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})
	requireStatus(t, c.next(typePromptAnswer), "answered")

	r.locked(func(s *agentState) {
		left := time.Until(s.tasks["T1"].deadline)
		if ttl := r.adapter.cfg.TaskTTL; left < ttl-time.Minute || left > ttl {
			t.Fatalf("the task has %s left after its prompt was answered, want about %s", left, ttl)
		}
	})
}
