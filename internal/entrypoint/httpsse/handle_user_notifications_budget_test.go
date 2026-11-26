// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
)

func openNotificationStream(t *testing.T, adapter *HTTPSSEAdapter) *streamReader {
	t.Helper()
	srv := httptest.NewServer(adapter.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sse/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	reader := newStreamReader(resp)
	if got := reader.wait(t, "notification stream established", 2*time.Second); !strings.Contains(got, "stream established") {
		t.Fatalf("did not receive the opening frame: %q", got)
	}
	return reader
}

func budgetStatusFrames(stream string) []string {
	var frames []string
	for _, frame := range strings.Split(stream, "\n\n") {
		if strings.HasPrefix(frame, "event: "+budgetStatusEvent+"\n") {
			frames = append(frames, frame)
		}
	}
	return frames
}

func waitForBudgetStatusFrames(t *testing.T, reader *streamReader, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		frames := budgetStatusFrames(reader.snapshot())
		if len(frames) >= n || time.Now().After(deadline) {
			return frames
		}
		reader.wait(t, "\x00", 50*time.Millisecond)
	}
}

func TestUserNotificationsEndpoint_PushesTheCallersBudgetStatusWhenItChanges(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	gate := adapter.core.BudgetGate()
	periodEnd := budget.PeriodEnd(time.Now())
	quiet := budgettest.MeshState(false, periodEnd, time.Now())

	reader := openNotificationStream(t, adapter)
	frames := waitForBudgetStatusFrames(t, reader, 1)
	if len(frames) != 1 || !strings.Contains(frames[0], `"ownLevel":"none"`) {
		t.Fatalf("frames on connect = %q, want the caller's current status", frames)
	}

	user := budget.UserKey("", adapter.core.Config().DefaultUserIdentity)
	warned := quiet
	warned.WarnedUsers = []string{user}
	warned.WarnCapKeys = map[string]string{user: "cap-1"}
	gate.Apply(t.Context(), warned)
	frames = waitForBudgetStatusFrames(t, reader, 2)
	if len(frames) != 2 || !strings.Contains(frames[1], `"ownLevel":"warn"`) || !strings.Contains(frames[1], `"capKey":"cap-1"`) {
		t.Fatalf("frames after the warning = %q, want the warning with its cap key", frames)
	}

	meshSpent := budgettest.MeshState(true, periodEnd, warned.ComputedAt.Add(time.Second))
	gate.Apply(t.Context(), meshSpent)
	frames = waitForBudgetStatusFrames(t, reader, 3)
	if len(frames) != 3 || !strings.Contains(frames[2], `"meshLimitReached":true`) {
		t.Fatalf("frames after the mesh limit = %q, want the mesh limit sent", frames)
	}
}

func TestBudgetStatusPusher_SendsAStatusOnlyWhenItDiffersFromTheLastSent(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	gate := adapter.core.BudgetGate()
	user := budget.UserKey("", adapter.core.Config().DefaultUserIdentity)
	warnedWith := func(capKey string, at time.Time) budget.EnforcementState {
		state := budgettest.MeshState(false, budget.PeriodEnd(at), at)
		state.WarnedUsers = []string{user}
		state.WarnCapKeys = map[string]string{user: capKey}
		return state
	}
	rec := httptest.NewRecorder()
	pusher := budgetStatusPusher{r: httptest.NewRequest(http.MethodGet, "/api/v1/sse/notifications", nil), core: adapter.core}
	push := func() []string {
		t.Helper()
		if _, err := pusher.push(rec, rec); err != nil {
			t.Fatalf("push: %v", err)
		}
		return budgetStatusFrames(rec.Body.String())
	}

	now := time.Now()
	gate.Apply(t.Context(), warnedWith("cap-1", now))
	if frames := push(); len(frames) != 1 || !strings.Contains(frames[0], `"capKey":"cap-1"`) {
		t.Fatalf("frames after the first push = %q, want the warning", frames)
	}

	gate.Apply(t.Context(), warnedWith("cap-1", now.Add(time.Second)))
	if frames := push(); len(frames) != 1 {
		t.Fatalf("frames after an unchanged status = %q, want the repeat skipped", frames)
	}

	gate.Apply(t.Context(), warnedWith("cap-2", now.Add(2*time.Second)))
	if frames := push(); len(frames) != 2 || !strings.Contains(frames[1], `"capKey":"cap-2"`) {
		t.Fatalf("frames after the cap changed = %q, want the new cap sent", frames)
	}
}
