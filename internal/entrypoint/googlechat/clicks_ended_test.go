// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func (r *clickRig) replicaWithTaskLog(t *testing.T, chat *fakeChat) *Adapter {
	t.Helper()
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  clickNamespace,
		"gateway_id": "gw-test",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
	})
	core.SetServices(runtime.NewRuntime(r.brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	core.SetIdentityAuthorizer(matchedAuthorizer())
	a := newTestAdapter(t, core.Store(), chat)
	a.core = core
	a.taskRegistry = core.Tasks()
	return a
}

func TestRequesterClickAfterARealCompletionSaysItEnded(t *testing.T) {
	const taskID = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a6b"
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replicaWithTaskLog(t, chat)
	logs := recordLogs(a)
	submitForTest(t, a, taskID, dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	card := postCard(t, chat, dmMeta())

	ended := time.Now().UnixMilli()
	completed := "completed"
	if err := store.NewTaskRepo(a.core.Store()).Create(t.Context(), &store.Task{
		ID: taskID, UserID: requesterEmail, StartTime: ended - 1000, EndTime: &ended, Status: &completed, ExecutionMode: "foreground",
	}); err != nil {
		t.Fatalf("record the finished task: %v", err)
	}
	a.OnFinalResponse(t.Context(), taskID, completedTask("Done."), nil)
	waitTerminalWritten(t, a)
	if _, state, err := a.readTaskItems(t.Context(), taskID); err != nil || state != destinationMissing {
		t.Fatalf("task items after the final response: state=%v err=%v, want gone", state, err)
	}
	rig.mustReceiveNothing(t)

	for _, click := range []*inboundEvent{
		clickBy(dmMeta(), requesterName, card, hilClick(actionHILSubmit, taskID)),
		clickBy(dmMeta(), requesterName, status, cancelClick(taskID)),
	} {
		if res := a.process(t.Context(), click); res.outcome != outcomeDeclined {
			t.Fatalf("%s outcome = %v, want declined", click.Action[actionMethodParam], res.outcome)
		}
	}
	writes, texts := chat.waitFor(t, func(w []fakeWrite, texts []string) bool {
		return strings.Count(strings.Join(texts, "\n"), msgRequestEnded) == 2 && controlStripped(w, status)
	})
	a.bgWG.Wait()
	rig.mustReceiveNothing(t)

	if !slices.ContainsFunc(writes, func(w fakeWrite) bool { return w.name == card && w.text == msgRequestEnded }) {
		t.Errorf("the question card was not retired: %+v", writes)
	}
	if anyTextContains(texts, msgRequestNotFound) || anyTextContains(texts, msgNotYourTask) {
		t.Errorf("messages = %q, want only the request-ended notice", texts)
	}
	if n := logs.count(slog.LevelWarn, "google chat card click"); n != 0 {
		t.Errorf("card-click warnings = %d, want none for a click on a finished task", n)
	}
}
