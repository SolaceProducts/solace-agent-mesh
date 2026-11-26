// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const wantTaskTraceReport = "tasks/{id}/trace"

func newTaskTraceExec(t *testing.T) (*GatewayExecutor, *bytes.Buffer) {
	t.Helper()
	exe := newStoreBackedActivityExecWith(t, runtime.WithControlAuthorizer(auth.DenyAllControlAuthorizer{}))
	var logs bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	return exe, &logs
}

func traceReaderX() *auth.Identity {
	return identityWith("reader", rbac.AgentReadTraceScope(uuidA))
}

func logLinesWithMsg(t *testing.T, logs *bytes.Buffer, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(logs.Bytes()))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line is not JSON: %s", sc.Bytes())
		}
		if line["msg"] == msg {
			out = append(out, line)
		}
	}
	return out
}

func assertTaskTraceRefused(t *testing.T, logs *bytes.Buffer, got *analytics.TaskTreeResponse, err error, taskID, reason string) {
	t.Helper()
	want := fmt.Sprintf("activity: task %q not found", taskID)
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got != nil {
		t.Fatalf("refusal returned a tree: %+v", got)
	}
	denied := logLinesWithMsg(t, logs, "activity access denied")
	if len(denied) != 1 {
		t.Fatalf("denial lines = %d, want 1: %s", len(denied), logs.String())
	}
	line := denied[0]
	if line["reason"] != reason || line["report"] != wantTaskTraceReport || line["taskID"] != taskID {
		t.Fatalf("denial line = %v, want reason %q report %q taskID %q", line, reason, wantTaskTraceReport, taskID)
	}
	if reads := logLinesWithMsg(t, logs, "activity read"); len(reads) != 0 {
		t.Fatalf("refusal also logged a read: %v", reads)
	}
	assertNoOwnerProbeDenial(t, logs)
}

func assertNoOwnerProbeDenial(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	if lines := logLinesWithMsg(t, logs, "activity trace access denied"); len(lines) != 0 {
		t.Fatalf("non-owner ran the owner probe: %v", lines)
	}
}

func assertOneTraceRead(t *testing.T, logs *bytes.Buffer, arm, targetUser string, crossUser bool) {
	t.Helper()
	reads := logLinesWithMsg(t, logs, "activity read")
	if len(reads) != 1 {
		t.Fatalf("read lines = %d, want 1: %s", len(reads), logs.String())
	}
	line := reads[0]
	if line["arm"] != arm || line["targetUser"] != targetUser || line["crossUser"] != crossUser || line["report"] != wantTaskTraceReport {
		t.Fatalf("read line = %v, want arm %q targetUser %q crossUser %v", line, arm, targetUser, crossUser)
	}
	if denied := logLinesWithMsg(t, logs, "activity access denied"); len(denied) != 0 {
		t.Fatalf("an opened trace also logged a denial: %v", denied)
	}
}

func treeIDs(tree *analytics.TaskTreeResponse) []string {
	ids := []string{tree.Root.ID}
	for _, n := range tree.Tree {
		ids = append(ids, n.ID)
	}
	return ids
}

func assertTreeIDs(t *testing.T, tree *analytics.TaskTreeResponse, want ...string) {
	t.Helper()
	got := treeIDs(tree)
	for _, id := range want {
		found := false
		for _, g := range got {
			found = found || g == id
		}
		if !found {
			t.Fatalf("tree ids = %v, missing %s", got, id)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tree ids = %v, want exactly %v", got, want)
	}
}

func rootAChildA(user string) []sessionTask {
	return []sessionTask{
		{subject: uuidA, parent: -1, start: 1000, user: user},
		{subject: uuidA, parent: 0, start: 1001, user: user},
	}
}

func TestActivityTaskTrace_TraceHolderOpensOtherUsersTask(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, rootAChildA("u2"))

	tree, err := callTaskTrace(t, exe, ids[0], traceReaderX())
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	assertTreeIDs(t, tree, ids[0], ids[1])
	assertOneTraceRead(t, logs, "agent_trace", "u2", true)
	assertNoOwnerProbeDenial(t, logs)
}

func TestActivityTaskTrace_QueriedSubTaskOpensWithinScope(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, rootAChildA("u2"))

	tree, err := callTaskTrace(t, exe, ids[1], traceReaderX())
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if tree.Root.ID != ids[0] || tree.QueriedTaskID != ids[1] {
		t.Fatalf("root=%s queried=%s, want root %s queried %s", tree.Root.ID, tree.QueriedTaskID, ids[0], ids[1])
	}
	assertTreeIDs(t, tree, ids[0], ids[1])
	assertOneTraceRead(t, logs, "agent_trace", "u2", true)
	assertNoOwnerProbeDenial(t, logs)
}

func TestActivityTaskTrace_UncoveredSubAgentRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: 1000, user: "u2"},
		{subject: uuidB, parent: 0, start: 1001, user: "u2"},
	})

	got, err := callTaskTrace(t, exe, ids[0], traceReaderX())
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenyParticipantUncovered)
	if strings.Contains(err.Error(), uuidB) || strings.Contains(err.Error(), ids[1]) {
		t.Fatalf("refusal names the uncovered agent or its task: %v", err)
	}
}

func TestActivityTaskTrace_UncoveredForwardedPeerRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: 1000, user: "u2"}})
	seedForwardedPeerEvent(t, exe.Gateways()[0].Store(), ids[0],
		a2a.InstanceName(a2a.KindAgent, uuidA), a2a.InstanceName(a2a.KindAgent, uuidB))

	got, err := callTaskTrace(t, exe, ids[0], traceReaderX())
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenyParticipantUncovered)
}

func TestActivityTaskTrace_UncoveredRootRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{{subject: uuidB, parent: -1, start: 1000, user: "u2"}})

	got, err := callTaskTrace(t, exe, ids[0], traceReaderX())
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenySubjectNotInReach)
}

func TestActivityTaskTrace_WorkflowRootRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidW, kind: store.TaskHandlerTypeWorkflow, parent: -1, start: 1000, user: "u2"},
		{subject: uuidA, parent: 0, start: 1001, user: "u2"},
	})

	got, err := callTaskTrace(t, exe, ids[0], identityWith("reader", rbac.AgentReadTraceScope("*")))
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenySubjectNotInReach)
}

func TestActivityTaskTrace_TreeTooDeepRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	chain := make([]sessionTask, store.TaskTreeMaxDepth+1)
	for i := range chain {
		chain[i] = sessionTask{subject: uuidA, parent: i - 1, start: 1000 + int64(i), user: "u2"}
	}
	_, ids := seedSessionTasks(t, exe, chain)

	got, err := callTaskTrace(t, exe, ids[0], traceReaderX())
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenyTreeTooDeep)
}

func TestActivityTaskTrace_OrphanedSubTaskRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	db := exe.Gateways()[0].Store()
	orphan, neverInserted := idgen.New(), idgen.New()
	handler := a2a.InstanceName(a2a.KindAgent, uuidA)
	if err := store.NewTaskRepo(db).Create(t.Context(), &store.Task{
		ID: orphan, UserID: "u2", StartTime: 1000, ParentTaskID: &neverInserted,
		Status: ptr("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: &handler, HandlerType: ptr(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	got, err := callTaskTrace(t, exe, orphan, traceReaderX())
	assertTaskTraceRefused(t, logs, got, err, orphan, TraceDenyRootRowMissing)
}

func TestActivityTaskTrace_NoGrantRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: 1000, user: "u2"}})

	got, err := callTaskTrace(t, exe, ids[0], identityWith("reader"))
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenyNoGrant)
}

func TestActivityTaskTrace_PartialGlobTraceRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: 1000, user: "u2"}})

	got, err := callTaskTrace(t, exe, ids[0], identityWith("reader", "a*:*:read_trace"))
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenyNoGrant)
}

func TestActivityTaskTrace_OwnerWithForeignChildFallsThroughToTrace(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: 1000, user: "u1"},
		{subject: uuidA, parent: 0, start: 1001, user: "u2"},
	})

	tree, err := callTaskTrace(t, exe, ids[0], identityWith("u1", rbac.AgentReadTraceScope(uuidA)))
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	assertTreeIDs(t, tree, ids[0], ids[1])
	assertOneTraceRead(t, logs, "agent_trace", "u1", false)
	assertNoOwnerProbeDenial(t, logs)
}

func TestActivityTaskTrace_OwnerWithForeignChildNoGrantRefused(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: 1000, user: "u1"},
		{subject: uuidA, parent: 0, start: 1001, user: "u2"},
	})

	got, err := callTaskTrace(t, exe, ids[0], identityWith("u1"))
	assertTaskTraceRefused(t, logs, got, err, ids[0], TraceDenyNoGrant)
}

func TestActivityTaskTrace_StoreFailureIsAnErrorNotARefusal(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, rootAChildA("u2"))
	if err := exe.Gateways()[0].Store().Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	got, err := callTaskTrace(t, exe, ids[0], traceReaderX())
	wantPrefix := fmt.Sprintf("activity tasks/%s/trace failed (errorId=", ids[0])
	if err == nil || !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Fatalf("err = %v, want prefix %q", err, wantPrefix)
	}
	if got != nil {
		t.Fatalf("failure returned a tree: %+v", got)
	}
	if failed := logLinesWithMsg(t, logs, "activity task tree failed"); len(failed) != 1 {
		t.Fatalf("failure lines = %d, want 1: %s", len(failed), logs.String())
	}
	for _, msg := range []string{"activity read", "activity access denied"} {
		if lines := logLinesWithMsg(t, logs, msg); len(lines) != 0 {
			t.Fatalf("store failure also logged %q: %v", msg, lines)
		}
	}
}

func TestActivityTaskTrace_OwnerAndAdminArmsLogged(t *testing.T) {
	exe, logs := newTaskTraceExec(t)
	_, ids := seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: 1000, user: "u2"}})

	tree, err := callTaskTrace(t, exe, ids[0], identityWith("u2"))
	if err != nil {
		t.Fatalf("owner trace: %v", err)
	}
	assertTreeIDs(t, tree, ids[0])
	assertOneTraceRead(t, logs, "owner", "u2", false)

	logs.Reset()
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	tree, err = callTaskTrace(t, exe, ids[0], admin)
	if err != nil {
		t.Fatalf("admin trace: %v", err)
	}
	assertTreeIDs(t, tree, ids[0])
	assertOneTraceRead(t, logs, "activity_read", "u2", true)
}

func TestActivityTaskTrace_FeedbackIDStillHandsOff(t *testing.T) {
	exe, _ := newTaskTraceExec(t)
	fb := store.Feedback{TaskID: idgen.New(), UserID: "u2", SessionID: idgen.New(), Rating: "down",
		SubjectID: uuidA, SubjectType: store.SubjectTypeAgent}
	store.SeedFeedback(t, exe.Gateways()[0].Store(), &fb)

	caller := identityWith("reader", rbac.AgentReadFeedbackScope(uuidA), rbac.AgentReadTraceScope(uuidA))
	got, err := callTaskTrace(t, exe, fb.ID, caller)
	if err != nil {
		t.Fatalf("trace on a feedback id: %v", err)
	}
	if !reflect.DeepEqual(*got, analytics.TaskTreeResponse{FeedbackID: fb.ID}) {
		t.Fatalf("got %+v, want only FeedbackID %s", got, fb.ID)
	}
}
