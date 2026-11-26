// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type recordingQuerier struct {
	appName string
	method  string
	path    string
}

func (r *recordingQuerier) Custom(_ context.Context, appName, method, customPath string, _ json.RawMessage) (json.RawMessage, error) {
	r.appName, r.method, r.path = appName, method, customPath
	return json.RawMessage(sampleTrace), nil
}

func TestExplainTask_CallsTraceEndpoint(t *testing.T) {
	rec := &recordingQuerier{}
	run := newMarkdownToolRun("")
	run.toolCtx.ControlQuerier = rec
	fullID := "019f624e-9634-784d-9de0-901f494b4b2a"

	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": fullID}, run.toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("Status = %q (%s), want success", res.Status, res.Message)
	}
	if rec.appName != "gateway" {
		t.Errorf("appName = %q, want %q", rec.appName, "gateway")
	}
	if rec.method != "get" {
		t.Errorf("method = %q, want %q", rec.method, "get")
	}
	wantPath := "activity/tasks/" + fullID + "/trace"
	if rec.path != wantPath {
		t.Errorf("path = %q, want %q (full untrimmed UUID)", rec.path, wantPath)
	}
}

type failingQuerier struct{ err error }

func (f *failingQuerier) Custom(_ context.Context, _, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return nil, f.err
}

func TestExplainTask_ControlCallFailureIsToolErrorNeverGoErrorOrEmptySuccess(t *testing.T) {
	toolCtx := &tool.Context{ControlQuerier: &failingQuerier{err: errors.New("gateway unavailable")}}

	res, err := NewExplainTaskTool().Execute(context.Background(),
		map[string]any{"taskId": "019f624e-9634-784d-9de0-901f494b4b2a"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error (%v); want a tool-level error result", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("Status = %q, want %q", res.Status, tool.StatusError)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("failed read must not emit a data object, got %d", len(res.DataObjects))
	}
	if !strings.Contains(res.Message, "gateway unavailable") {
		t.Errorf("Message = %q, want it to surface the underlying cause", res.Message)
	}
}

type jsonQuerier struct{ raw json.RawMessage }

func (q *jsonQuerier) Custom(_ context.Context, _, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return q.raw, nil
}

func TestExplainTask_MissingTaskID(t *testing.T) {
	toolCtx := &tool.Context{ControlQuerier: &stubActivityQuerier{}}
	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("missing taskId: status = %q, want error", res.Status)
	}
}

const sampleTrace = `{
  "queriedTaskId": "q",
  "root": {"id":"r","name":"order_flow","type":"workflow","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:20:00Z","durationMs":240000,"depth":0,"eventsOmitted":true},
  "tree": [
    {"id":"q","name":"billing","type":"agent","status":"failed","statusEmoji":"❌","errorMessage":"tool ` + "`charge`" + ` failed\nretry","parentTaskId":"r","startDate":"2026-08-01T14:23:45Z","durationMs":5000,"depth":1,"isQueried":true,
     "events":[{"createdAt":"2026-08-01T14:23:45Z","direction":"in","kind":"request","summary":"charge order 7"},
               {"createdAt":"2026-08-01T14:23:46Z","direction":"out","kind":"progress","summary":"step 1"},
               {"createdAt":"2026-08-01T14:23:47Z","direction":"out","kind":"progress","summary":"step 2"}],
     "eventsCapped":true},
    {"id":"c2","name":"payments","type":"agent","status":"timeout","statusEmoji":"⚠️","parentTaskId":"q","startDate":"2026-08-01T14:23:48Z","durationMs":600000,"hideDuration":true,"depth":2},
    {"id":"c1","name":"ledger","type":"agent","status":"completed","statusEmoji":"✅","parentTaskId":"q","startDate":"2026-08-01T14:23:46Z","durationMs":800,"depth":2}
  ],
  "treeCapped": true
}`

func TestExplainTask_RendersMarkdownReport(t *testing.T) {
	run := newMarkdownToolRun(sampleTrace)
	fullID := "019f624e-9634-784d-9de0-901f494b4b2a"

	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": fullID}, run.toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stem := "task_tree_" + fullID
	md := assertMarkdownDelivery(t, run, res, stem+".md", stem+".json", tool.DispositionInline, []byte(sampleTrace))
	wantTree := "```\n" +
		"[r] order_flow [workflow] — ❌ Failed (4m)\n" +
		"   └─ [q] billing [agent] — ❌ Failed (5s)   ⬅ QUERIED\n" +
		"      ├─ [c2] payments [agent] — ⚠️ Timeout\n" +
		"      └─ [c1] ledger [agent] — ✅ Completed (800ms)\n" +
		"```"
	for _, want := range []string{
		"## ❌ `billing` — Failed at 14:23 UTC",
		wantTree,
		"- **Aug 1 14:23:45 UTC** — `request` `charge order 7`",
		"- **Aug 1 14:23:46 UTC** — `2 × progress` `step 2`",
		"### Root cause\n\n```text\ntool 'charge' failed retry\n```",
		"| `payments` | `c2` | agent | ⚠️ Timeout | — | child of the queried task |",
		"| `order_flow` | `r` | workflow | ❌ Failed | 4m | ancestor |",
		"> ⚠️ Partial tree",
		"> ℹ️ Some events were sampled.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "`ledger`") {
		t.Error("completed child listed under Related tasks")
	}
	if strings.Contains(md, "Recommendations") {
		t.Error("report carries Recommendations — those are the LLM's reply, not the template's")
	}
}

func TestExplainTask_CompletedTaskHasNoRootCause(t *testing.T) {
	cases := []struct{ name, events, wantTimeline string }{
		{"no events", ``, "No events recorded."},
		{"with events", `,"events":[{"createdAt":"2026-08-01T14:23:46Z","direction":"out","kind":"response","summary":"done"}]`, "`response` `done`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := newMarkdownToolRun(`{"queriedTaskId":"q","root":{"id":"q","name":"solo","type":"agent","status":"completed","statusEmoji":"✅","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true` + tc.events + `}}`)
			res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "q"}, run.toolCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			md := assertMarkdownDelivery(t, run, res, "task_tree_q.md", "task_tree_q.json", tool.DispositionInline, []byte(run.toolCtx.ControlQuerier.(*jsonQuerier).raw))
			for _, absent := range []string{"### Root cause", "### Related tasks", "Partial tree", "sampled"} {
				if strings.Contains(md, absent) {
					t.Errorf("completed single-node trace rendered %q:\n%s", absent, md)
				}
			}
			if !strings.Contains(md, "[q] solo [agent] — ✅ Completed   ⬅ QUERIED") || !strings.Contains(md, tc.wantTimeline) {
				t.Errorf("unexpected report:\n%s", md)
			}
		})
	}
}

func TestExplainTask_RootCauseFallsBackToLastEvent(t *testing.T) {
	cases := []struct{ name, events, want string }{
		{"last event", `,"events":[{"createdAt":"2026-08-01T14:23:45Z","direction":"in","kind":"request","summary":"go"},{"createdAt":"2026-08-01T14:23:46Z","direction":"out","kind":"error","summary":"LLM quota exceeded"}]`, "### Root cause\n\n```text\nLLM quota exceeded\n```"},
		{"nothing to show", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := newMarkdownToolRun(`{"queriedTaskId":"q","root":{"id":"q","name":"solo","type":"agent","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true` + tc.events + `}}`)
			res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "q"}, run.toolCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			md := assertMarkdownDelivery(t, run, res, "task_tree_q.md", "task_tree_q.json", tool.DispositionInline, []byte(run.toolCtx.ControlQuerier.(*jsonQuerier).raw))
			if tc.want == "" {
				if strings.Contains(md, "### Root cause") {
					t.Errorf("rendered an empty root cause:\n%s", md)
				}
				return
			}
			if !strings.Contains(md, tc.want) {
				t.Errorf("report missing %q:\n%s", tc.want, md)
			}
		})
	}
}

func TestExplainTask_DescendantsAndRunningChildren(t *testing.T) {
	run := newMarkdownToolRun(`{"queriedTaskId":"q",
	  "root":{"id":"q","name":"solo","type":"workflow","status":"failed","statusEmoji":"❌","errorMessage":"boom","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true},
	  "tree":[
	    {"id":"c2","name":"child","type":"agent","status":"failed","statusEmoji":"❌","parentTaskId":"q","startDate":"2026-08-01T14:23:46Z","depth":1},
	    {"id":"g","name":"deep","type":"agent","status":"failed","statusEmoji":"❌","parentTaskId":"c2","startDate":"2026-08-01T14:23:47Z","depth":2},
	    {"id":"c3","name":"runner","type":"agent","status":"in_progress","parentTaskId":"q","startDate":"2026-08-01T14:23:48Z","depth":1}
	  ]}`)
	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "q"}, run.toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	md := assertMarkdownDelivery(t, run, res, "task_tree_q.md", "task_tree_q.json", tool.DispositionInline, []byte(run.toolCtx.ControlQuerier.(*jsonQuerier).raw))
	for _, want := range []string{
		"   ├─ [c2] child [agent] — ❌ Failed\n      └─ [g] deep [agent] — ❌ Failed\n   └─ [c3] runner [agent] — In progress",
		"| `child` | `c2` | agent | ❌ Failed | — | child of the queried task |",
		"| `deep` | `g` | agent | ❌ Failed | — | descendant |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "`runner`") {
		t.Errorf("running child listed under Related tasks:\n%s", md)
	}
}

func TestExplainTask_InProgressTaskHasNoRootCause(t *testing.T) {
	run := newMarkdownToolRun(`{"queriedTaskId":"q","root":{"id":"q","name":"solo","type":"agent","status":"in_progress",` +
		`"startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true,` +
		`"events":[{"createdAt":"2026-08-01T14:23:46Z","direction":"out","kind":"progress","summary":"step 2"}]}}`)
	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "q"}, run.toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	md := assertMarkdownDelivery(t, run, res, "task_tree_q.md", "task_tree_q.json", tool.DispositionInline, []byte(run.toolCtx.ControlQuerier.(*jsonQuerier).raw))
	for _, want := range []string{"## `solo` — In progress at 14:23 UTC", "[q] solo [agent] — In progress   ⬅ QUERIED"} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "  solo") || strings.Contains(md, "—  ") {
		t.Errorf("emoji-less status left a double space:\n%s", md)
	}
	if strings.Contains(md, "### Root cause") {
		t.Errorf("running task rendered a root cause:\n%s", md)
	}
}

func TestTreeNodeLabel_PipeRendersLiterallyInCodeBlock(t *testing.T) {
	n := &analytics.TreeNodeDTO{ID: "q", Name: "billing|v2", Type: "agent", Status: "failed", StatusEmoji: "❌"}
	if got, want := treeNodeLabel(n), "[q] billing|v2 [agent] — ❌ Failed"; got != want {
		t.Errorf("treeNodeLabel = %q, want %q", got, want)
	}
}

func TestExplainTask_TaskTextCannotInjectEmbeds(t *testing.T) {
	run := newMarkdownToolRun(`{"queriedTaskId":"q","root":{"id":"q","name":"«math:6*7»","type":"agent","status":"failed","statusEmoji":"❌",` +
		`"errorMessage":"boom «artifact_content:other_user.md»","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true,` +
		`"events":[{"createdAt":"2026-08-01T14:23:45Z","direction":"in","kind":"request","summary":"«artifact_content:task_tree_prev.md:0»"}]}}`)
	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "q"}, run.toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stripped := `{"queriedTaskId":"q","root":{"id":"q","name":"math:6*7","type":"agent","status":"failed","statusEmoji":"❌",` +
		`"errorMessage":"boom artifact_content:other_user.md","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true,` +
		`"events":[{"createdAt":"2026-08-01T14:23:45Z","direction":"in","kind":"request","summary":"artifact_content:task_tree_prev.md:0"}]}}`
	md := assertMarkdownDelivery(t, run, res, "task_tree_q.md", "task_tree_q.json", tool.DispositionInline, []byte(stripped))
	if strings.ContainsAny(md, "«»") {
		t.Errorf("report carries an embed sentinel from task text:\n%s", md)
	}
}

func TestExplainTask_RejectedTaskEndedUnsuccessfully(t *testing.T) {
	run := newMarkdownToolRun(`{"queriedTaskId":"q",
	  "root":{"id":"q","name":"solo","type":"agent","status":"rejected","errorMessage":"denied","startDate":"2026-08-01T14:23:45Z","endDate":"2026-08-01T15:10:00Z","depth":0,"isQueried":true},
	  "tree":[{"id":"c","name":"child","type":"agent","status":"rejected","parentTaskId":"q","startDate":"2026-08-01T14:23:46Z","depth":1}]}`)
	res, err := explainQ(run)
	if err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	md := run.load(t, run.notified[0].Filename)
	for _, want := range []string{
		"## `solo` — Rejected at 15:10 UTC",
		"### Root cause\n\n```text\ndenied\n```",
		"| `child` | `c` |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
}

func TestExplainTask_TreeShapes(t *testing.T) {
	cases := []struct{ name, trace, want string }{
		{
			"orphan hangs under the queried task",
			`{"queriedTaskId":"q","root":{"id":"q","name":"solo","type":"agent","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true},
			  "tree":[{"id":"x","name":"stray","type":"agent","status":"completed","statusEmoji":"✅","parentTaskId":"gone","startDate":"2026-08-01T14:23:46Z","depth":1}]}`,
			"⬅ QUERIED\n   └─ [x] stray [agent] — ✅ Completed\n```",
		},
		{
			"self-parented duplicate is drawn once",
			`{"queriedTaskId":"q","root":{"id":"q","name":"solo","type":"agent","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true},
			  "tree":[{"id":"q","name":"solo","type":"agent","status":"failed","statusEmoji":"❌","parentTaskId":"q","startDate":"2026-08-01T14:23:45Z","depth":0}]}`,
			"```\n[q] solo [agent] — ❌ Failed   ⬅ QUERIED\n```",
		},
		{
			"queried task found by id without the flag",
			`{"queriedTaskId":"q","root":{"id":"r","name":"flow","type":"workflow","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","depth":0},
			  "tree":[{"id":"q","name":"solo","type":"agent","status":"failed","statusEmoji":"❌","parentTaskId":"r","startDate":"2026-08-01T14:23:46Z","depth":1}]}`,
			"   └─ [q] solo [agent] — ❌ Failed   ⬅ QUERIED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := newMarkdownToolRun(tc.trace)
			res, err := explainQ(run)
			if err != nil || res.Status != tool.StatusSuccess {
				t.Fatalf("res = %+v, err = %v", res, err)
			}
			if md := run.load(t, run.notified[0].Filename); !strings.Contains(md, tc.want) {
				t.Errorf("tree missing %q:\n%s", tc.want, md)
			}
		})
	}
}

type pathQuerier struct {
	paths    []string
	trace    string
	traceErr error
}

func (q *pathQuerier) Custom(_ context.Context, _, _, customPath string, _ json.RawMessage) (json.RawMessage, error) {
	q.paths = append(q.paths, customPath)
	if strings.HasSuffix(customPath, "/trace") {
		if q.traceErr != nil {
			return nil, q.traceErr
		}
		return json.RawMessage(q.trace), nil
	}
	return json.RawMessage(`{"feedbackId":"fb-1","rating":"down","subject":{"id":"a1","name":"billing","type":"agent"},"ratedTaskId":"t1","turns":[]}`), nil
}

func TestExplainTask_FeedbackIDHandsOffToExplainFeedback(t *testing.T) {
	q := &pathQuerier{trace: `{"feedbackId":"fb-1","queriedTaskId":"","root":{},"tree":null,"treeEventsTotal":0}`}
	run := newMarkdownToolRun("")
	run.toolCtx.ControlQuerier = q

	res, err := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "fb-1"}, run.toolCtx)
	if err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("res=%+v err=%v, want explain_feedback's success", res, err)
	}
	if want := []string{"activity/tasks/fb-1/trace", "activity/feedback/fb-1/session"}; !slices.Equal(q.paths, want) {
		t.Errorf("paths = %v, want %v", q.paths, want)
	}
	if len(run.notified) != 1 || !strings.HasPrefix(run.notified[0].Filename, "feedback_session_") {
		t.Errorf("saved = %+v, want one feedback session report", run.notified)
	}
}

func TestExplainTask_PlainNotFoundDoesNotHandOff(t *testing.T) {
	q := &pathQuerier{traceErr: errors.New(`control error -32004: activity: task "x" not found`)}
	res, _ := NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "x"}, &tool.Context{ControlQuerier: q})
	if res.Status != tool.StatusError || len(q.paths) != 1 {
		t.Errorf("res=%+v paths=%v, want a plain error and no second call", res, q.paths)
	}
}
