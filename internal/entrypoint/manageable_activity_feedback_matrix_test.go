// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type matrixSubject struct {
	id, display string
	kind        a2a.ComponentKind
}

func (s matrixSubject) leakStrings() []string {
	return []string{s.id, a2a.InstanceName(s.kind, s.id), s.display}
}

const (
	uuidTwin1 = "44444444-4444-4444-4444-444444444444"
	uuidTwin2 = "55555555-5555-5555-5555-555555555555"

	matrixTwinName = "Matrix Twin"
	matrixYName    = "Matrix Agent Y"
)

var matrixSubjects = map[string]matrixSubject{
	"X":  {uuidA, "Matrix Agent X", a2a.KindAgent},
	"Y":  {uuidB, matrixYName, a2a.KindAgent},
	"W":  {uuidW, "Matrix Workflow W", a2a.KindWorkflow},
	"T1": {uuidTwin1, matrixTwinName, a2a.KindAgent},
	"T2": {uuidTwin2, matrixTwinName, a2a.KindAgent},
}

type matrixFeedback struct {
	id, user, subject, rating, comment string
	inRollup                           bool
	roots                              []string
}

type matrixFixture struct {
	feedback map[string]matrixFeedback
}

func seedFeedbackMatrix(t *testing.T, exe *GatewayExecutor) matrixFixture {
	t.Helper()
	db := exe.Gateways()[0].Store()
	for _, s := range matrixSubjects {
		addAgentCards(exe, a2a.InstanceName(s.kind, s.id), s.display)
	}
	fx := matrixFixture{feedback: map[string]matrixFeedback{}}
	rate := func(key, taskID, sessionID, user, subject, rating string, roots ...string) {
		subj := matrixSubjects[subject]
		comment := "matrix-comment[" + key + "]"
		f := store.Feedback{
			TaskID: taskID, UserID: user, SessionID: sessionID, Rating: rating, Comment: &comment,
			SubjectID: subj.id, SubjectType: string(subj.kind),
		}
		store.SeedFeedback(t, db, &f)
		fx.feedback[key] = matrixFeedback{id: f.ID, user: user, subject: subject, rating: rating,
			comment: comment, inRollup: len(roots) > 0, roots: roots}
	}
	base := time.Now().Add(-time.Hour).UnixMilli()
	wf := store.TaskHandlerTypeWorkflow

	s1, t1 := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: base},
		{subject: uuidA, parent: -1, start: base + 100},
		{subject: uuidA, parent: -1, start: base + 200},
	})
	rate("F1", t1[2], s1, "u1", "X", "down", t1...)
	rate("F1b", t1[0], s1, "u1", "X", "up", t1[0])

	s2, t2 := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: base + 1000},
		{subject: uuidB, parent: -1, start: base + 1100},
		{subject: uuidA, parent: -1, start: base + 1200},
	})
	rate("F2", t2[1], s2, "u1", "Y", "down", t2[:2]...)
	rate("F2b", t2[2], s2, "u1", "X", "down", t2...)

	s3, t3 := seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: base + 2000, user: "u2"}})
	seedForwardedPeerEvent(t, db, t3[0], a2a.InstanceName(a2a.KindAgent, uuidA), a2a.InstanceName(a2a.KindAgent, uuidB))
	rate("F3", t3[0], s3, "u2", "X", "down", t3[0])

	s4, t4 := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidW, kind: wf, parent: -1, start: base + 3000, user: "u2"},
		{subject: uuidA, parent: 0, start: base + 3001, user: "u2"},
	})
	rate("F4", t4[0], s4, "u2", "W", "down", t4[0])

	s5, t5 := seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: base + 4000}})
	rate("F5", idgen.New(), s5, "u1", "X", "down", t5...)
	f5 := fx.feedback["F5"]
	f5.inRollup = false
	fx.feedback["F5"] = f5

	s6, t6 := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: base + 5000},
		{subject: uuidA, parent: 0, start: base + 5001, user: "u2"},
	})
	rate("F6", t6[0], s6, "u1", "X", "up", t6[0])

	long := []sessionTask{{subject: uuidB, parent: -1, start: base + 6000}}
	for i := 1; i <= analytics.MaxSessionTurns; i++ {
		long = append(long, sessionTask{subject: uuidA, parent: -1, start: base + 6000 + int64(i)*10})
	}
	s7, t7 := seedSessionTasks(t, exe, long)
	rate("F7", t7[len(t7)-1], s7, "u1", "X", "down", t7[1:]...)

	seedSessionTasks(t, exe, []sessionTask{{subject: uuidA, parent: -1, start: base + 8000, user: "u3"}})

	s9, t9 := seedSessionTasks(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: base + 9000, user: "u2"},
		{parent: 0, start: base + 9001, user: "u2", unclassified: true},
	})
	rate("F9", t9[0], s9, "u2", "X", "down", t9[0])
	return fx
}

func seedForwardedPeerEvent(t *testing.T, db *store.DB, taskID, forwarder, peer string) {
	t.Helper()
	payload := `{"result":{"status":{"message":{"metadata":{"agent_name":"` + forwarder +
		`","forwarded_from_peer":"` + peer + `"}}}}}`
	if err := store.NewTaskEventRepo(db).Create(t.Context(), &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, CreatedTime: time.Now().UnixMilli(), Topic: "t", Direction: "in", Payload: payload,
	}); err != nil {
		t.Fatalf("seed forwarded event: %v", err)
	}
}

type matrixCaller struct {
	name        string
	identity    *auth.Identity
	ownData     bool
	visible     []string
	sees        []string
	sessionSees []string
	opens       []string
}

var allMatrixFeedback = []string{"F1", "F1b", "F2", "F2b", "F3", "F4", "F5", "F6", "F7", "F9"}

var matrixU1Wrote = []string{"F1", "F1b", "F2", "F2b", "F5", "F6", "F7"}

func (c matrixCaller) readsRow(key string) bool {
	return slices.Contains(c.visible, key) || (c.identity.UserID == "u1" && slices.Contains(matrixU1Wrote, key))
}

func matrixCallers() []matrixCaller {
	reader := func(scopes ...string) *auth.Identity { return identityWith("reader", scopes...) }
	fbX, trX := rbac.AgentReadFeedbackScope(uuidA), rbac.AgentReadTraceScope(uuidA)
	fbY, trY := rbac.AgentReadFeedbackScope(uuidB), rbac.AgentReadTraceScope(uuidB)
	fbW := rbac.WorkflowReadFeedbackScope(uuidW)
	onX := []string{"F1", "F1b", "F2b", "F3", "F5", "F6", "F7", "F9"}
	u1Wrote := matrixU1Wrote
	u1Opens := []string{"F1", "F1b", "F2", "F2b", "F5", "F7"}
	return []matrixCaller{
		{name: "none", identity: reader(), ownData: true},
		{name: "read_feedback X", identity: reader(fbX), visible: onX, sees: []string{"X"}},
		{name: "read_trace X", identity: reader(trX), ownData: true},
		{name: "both verbs X", identity: reader(fbX, trX), visible: onX, sees: []string{"X"},
			opens: []string{"F1", "F1b", "F6", "F7"}},
		{name: "both verbs X and Y", identity: reader(fbX, trX, fbY, trY),
			visible: []string{"F1", "F1b", "F2", "F2b", "F3", "F5", "F6", "F7", "F9"}, sees: []string{"X", "Y"},
			opens: []string{"F1", "F1b", "F2", "F2b", "F3", "F6", "F7"}},
		{name: "read_feedback W", identity: reader(fbW), visible: []string{"F4"}, sees: []string{"W"}},
		{name: "read_feedback W with read_trace everywhere", identity: reader(fbW, rbac.AgentReadTraceScope("*")),
			visible: []string{"F4"}, sees: []string{"W"}},
		{name: "read_feedback every agent", identity: reader(rbac.AgentReadFeedbackScope("*")),
			visible: []string{"F1", "F1b", "F2", "F2b", "F3", "F5", "F6", "F7", "F9"}, sees: []string{"X", "Y", "T1", "T2"}},
		{name: "read_feedback every workflow", identity: reader(rbac.WorkflowReadFeedbackScope("*")),
			visible: []string{"F4"}, sees: []string{"W"}},
		{name: "activity admin", identity: &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
			visible: allMatrixFeedback, sees: []string{"X", "Y", "W", "T1", "T2"}, opens: allMatrixFeedback},
		{name: "u1 as owner", identity: identityWith("u1"), ownData: true,
			visible: u1Wrote, sees: []string{"X", "Y"}, opens: u1Opens},
		{name: "u1 with read_feedback W", identity: identityWith("u1", fbW),
			visible: []string{"F4"}, sees: []string{"W"}, sessionSees: []string{"X", "Y"}, opens: u1Opens},
		{name: "u1 with partial glob read_feedback", identity: identityWith("u1", "a*:*:read_feedback"), ownData: true,
			visible: u1Wrote, sees: []string{"X", "Y"}, opens: u1Opens},
		{name: "u1 with read_trace X", identity: identityWith("u1", trX), ownData: true,
			visible: u1Wrote, sees: []string{"X", "Y"}, opens: u1Opens},
		{name: "partial glob read_feedback", identity: reader("a*:*:read_feedback"), ownData: true},
		{name: "empty scope set", identity: &auth.Identity{UserID: "reader", Scopes: []string{}}, ownData: true},
	}
}

func TestFeedbackAccessMatrix(t *testing.T) {
	exe := newStoreBackedActivityExecWith(t, runtime.WithControlAuthorizer(auth.DenyAllControlAuthorizer{}))
	fx := seedFeedbackMatrix(t, exe)
	var logs bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logs, nil))

	for _, c := range matrixCallers() {
		t.Run(c.name, func(t *testing.T) {
			forbidden := fx.forbiddenStrings(c.sees, c.visible)
			t.Run("summary", func(t *testing.T) { assertMatrixSummary(t, exe, fx, c, forbidden) })
			t.Run("summary by rater", func(t *testing.T) { assertMatrixSummaryByRater(t, exe, c, forbidden) })
			t.Run("rows", func(t *testing.T) { assertMatrixRows(t, exe, fx, c, forbidden) })
			t.Run("rows by name", func(t *testing.T) { assertMatrixRowsByName(t, exe, fx, c, forbidden) })
			t.Run("rows by feedback id", func(t *testing.T) { assertMatrixRowsByID(t, exe, fx, c, forbidden) })
			t.Run("ambiguous name", func(t *testing.T) { assertMatrixAmbiguousName(t, exe, c, forbidden) })
			t.Run("out-of-reach id", func(t *testing.T) { assertMatrixOutOfReachIDs(t, exe, c) })
			t.Run("name of the other kind", func(t *testing.T) { assertMatrixOtherKindName(t, exe, c, forbidden) })
			sessionForbidden := fx.forbiddenStrings(slices.Concat(c.sees, c.sessionSees), slices.Concat(c.visible, c.opens))
			t.Run("session", func(t *testing.T) { assertMatrixSessions(t, exe, fx, c, sessionForbidden, &logs) })
		})
	}
}

func (fx matrixFixture) forbiddenStrings(sees, readable []string) []string {
	var out []string
	for key, s := range matrixSubjects {
		if !slices.Contains(sees, key) {
			out = append(out, s.leakStrings()...)
		}
	}
	for key, f := range fx.feedback {
		if !slices.Contains(readable, key) {
			out = append(out, f.id, f.comment)
		}
	}
	return out
}

func assertNoLeak(t *testing.T, payload any, forbidden []string) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, s := range forbidden {
		if s != "" && strings.Contains(string(raw), s) {
			t.Errorf("payload leaks %q: %s", s, raw)
		}
	}
}

func (fx matrixFixture) idsOf(keys []string, keep func(matrixFeedback) bool) []string {
	var ids []string
	for _, k := range keys {
		if f := fx.feedback[k]; keep(f) {
			ids = append(ids, f.id)
		}
	}
	slices.Sort(ids)
	return ids
}

func rowIDs(t *testing.T, rows []analytics.ActivityFeedbackRow) []string {
	t.Helper()
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	if n := len(slices.Compact(slices.Clone(ids))); n != len(ids) {
		t.Errorf("response repeats a row: %v", ids)
	}
	return ids
}

func assertMatrixSummary(t *testing.T, exe *GatewayExecutor, fx matrixFixture, c matrixCaller, forbidden []string) {
	got, err := callActivityFeedback[analytics.FeedbackSummaryResponse](t, exe, "summary", `{"window":"7d"}`, c.identity)
	if c.ownData {
		assertOwnDataSummaryRefused(t, err)
		return
	}
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	assertNoLeak(t, got, forbidden)

	var wantUp, wantDown, gotUp, gotDown int
	for _, k := range c.visible {
		if f := fx.feedback[k]; f.inRollup && f.rating == "up" {
			wantUp++
		} else if f.inRollup {
			wantDown++
		}
	}
	for _, r := range got.Rows {
		if r.Subject == nil || !slices.ContainsFunc(c.sees, func(k string) bool { return matrixSubjects[k].id == r.Subject.ID }) {
			t.Errorf("summary row for subject outside reach: %+v", r.Subject)
		}
		gotUp += r.PositiveFeedback
		gotDown += r.NegativeFeedback
	}
	if gotUp != wantUp || gotDown != wantDown {
		t.Errorf("summary totals +%d/-%d, want +%d/-%d from in-reach rows", gotUp, gotDown, wantUp, wantDown)
	}

	wantSample := fx.idsOf(c.visible, func(f matrixFeedback) bool { return f.rating == "down" && !c.ownData })
	if gotSample := rowIDs(t, got.NegativeSample); !slices.Equal(gotSample, wantSample) {
		t.Errorf("negative sample = %v, want %v", gotSample, wantSample)
	}
}

func assertOwnDataSummaryRefused(t *testing.T, err error) {
	t.Helper()
	if err == nil || err.Error() != msgFeedbackSummaryOwnData {
		t.Fatalf("own-data summary err = %v, want %q", err, msgFeedbackSummaryOwnData)
	}
}

func assertMatrixSummaryByRater(t *testing.T, exe *GatewayExecutor, c matrixCaller, forbidden []string) {
	admin := slices.Contains(c.identity.Scopes, rbac.ScopeActivityRead)
	for _, user := range []string{"u1", "u3"} {
		got, err := callActivityFeedback[analytics.FeedbackSummaryResponse](t, exe, "summary",
			fmt.Sprintf(`{"window":"7d","feedbackUserId":%q}`, user), c.identity)
		if c.ownData {
			assertOwnDataSummaryRefused(t, err)
			continue
		}
		if err != nil {
			t.Fatalf("summary for %s: %v", user, err)
		}
		assertNoLeak(t, got, forbidden)
		raw, mErr := json.Marshal(got)
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		hasActivity := strings.Contains(string(raw), "totalActivities")
		switch {
		case admin && user == "u3":
			if len(got.Rows) != 1 || got.Rows[0].TotalActivities == nil || *got.Rows[0].TotalActivities != 1 {
				t.Errorf("admin summary for u3 = %s, want X with one activity", raw)
			}
		case admin:
			if !hasActivity {
				t.Errorf("admin summary for %s dropped activity counts: %s", user, raw)
			}
		case user == "u3":
			if len(got.Rows) != 0 || hasActivity {
				t.Errorf("summary for u3 = %s, want empty: u3 rated nothing", raw)
			}
		case hasActivity:
			t.Errorf("summary for %s carries activity counts: %s", user, raw)
		}
	}
}

func assertMatrixRows(t *testing.T, exe *GatewayExecutor, fx matrixFixture, c matrixCaller, forbidden []string) {
	var all []string
	for _, user := range []string{"u1", "u2"} {
		got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
			fmt.Sprintf(`{"window":"7d","feedbackUserId":%q}`, user), c.identity)
		if c.ownData && user != c.identity.UserID {
			if err == nil || err.Error() != msgFeedbackRowsOtherUser {
				t.Fatalf("own-data rows for %s: got %+v, %v; want the other-user refusal", user, got, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("rows for %s: %v", user, err)
		}
		assertNoLeak(t, got, forbidden)
		if got.Meta.Truncated {
			t.Errorf("rows for %s truncated on a %d-row fixture", user, len(fx.feedback))
		}
		all = append(all, rowIDs(t, got.Data)...)
	}
	slices.Sort(all)
	all = slices.Compact(all)
	want := fx.idsOf(c.visible, func(matrixFeedback) bool { return true })
	if got := all; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func assertMatrixRowsByID(t *testing.T, exe *GatewayExecutor, fx matrixFixture, c matrixCaller, forbidden []string) {
	for _, key := range allMatrixFeedback {
		f := fx.feedback[key]
		got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
			fmt.Sprintf(`{"window":"1h","feedbackId":%q}`, f.id), c.identity)
		if err != nil {
			t.Fatalf("rows for %s: %v", key, err)
		}
		assertNoLeak(t, got, forbidden)
		want := []string{}
		if slices.Contains(c.visible, key) {
			want = []string{f.id}
		}
		if ids := rowIDs(t, got.Data); !slices.Equal(ids, want) {
			t.Errorf("rows for %s = %v, want %v", key, ids, want)
		}
	}
}

func assertEchoedOnly(t *testing.T, got *analytics.FeedbackRowsResponse, asked string) {
	t.Helper()
	if len(got.UnmatchedSubjects) > 0 && !slices.Equal(got.UnmatchedSubjects, []string{asked}) {
		t.Errorf("unmatched = %v, want at most the name asked for (%q)", got.UnmatchedSubjects, asked)
	}
	got.UnmatchedSubjects = nil
}

func assertMatrixRowsByName(t *testing.T, exe *GatewayExecutor, fx matrixFixture, c matrixCaller, forbidden []string) {
	got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","subjects":["`+matrixYName+`"]}`, c.identity)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	assertEchoedOnly(t, &got, matrixYName)
	assertNoLeak(t, got, forbidden)
	want := fx.idsOf(c.visible, func(f matrixFeedback) bool { return f.subject == "Y" })
	if ids := rowIDs(t, got.Data); !slices.Equal(ids, want) {
		t.Errorf("rows on Y by name = %v, want %v", ids, want)
	}
	if len(got.Candidates) != 0 {
		t.Errorf("unambiguous name returned candidates %+v", got.Candidates)
	}
}

func assertMatrixAmbiguousName(t *testing.T, exe *GatewayExecutor, c matrixCaller, forbidden []string) {
	got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","subjects":["`+matrixTwinName+`"]}`, c.identity)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	assertEchoedOnly(t, &got, matrixTwinName)
	assertNoLeak(t, got, forbidden)
	if len(got.Data) != 0 {
		t.Errorf("ambiguous name returned rows %+v", got.Data)
	}
	wantCandidates := 0
	if !c.ownData && slices.Contains(c.sees, "T1") {
		wantCandidates = 2
	}
	if len(got.Candidates) != wantCandidates {
		t.Errorf("candidates=%+v, want %d", got.Candidates, wantCandidates)
	}
}

func assertMatrixOutOfReachIDs(t *testing.T, exe *GatewayExecutor, c matrixCaller) {
	ask := func(id string) string {
		got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
			fmt.Sprintf(`{"window":"7d","subjects":[%q]}`, id), c.identity)
		raw, mErr := json.Marshal(got)
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		answer := strings.ReplaceAll(string(raw), id, "<asked>")
		if err != nil {
			answer = strings.ReplaceAll(err.Error(), id, "<asked>")
		}
		return answer
	}
	unused := ask(idgen.New())
	for key, s := range matrixSubjects {
		if slices.Contains(c.sees, key) {
			continue
		}
		if got := ask(s.id); got != unused {
			t.Errorf("asking for out-of-reach %s by id answered %s, want the unused-id answer %s", key, got, unused)
		}
	}
}

func assertMatrixOtherKindName(t *testing.T, exe *GatewayExecutor, c matrixCaller, forbidden []string) {
	got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","subjectType":"workflow","subjects":["`+matrixYName+`"]}`, c.identity)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("agent name asked for as a workflow returned rows %+v", got.Data)
	}
	wantUnmatched := []string{matrixYName}
	if c.ownData {
		wantUnmatched = nil
	}
	if !slices.Equal(got.UnmatchedSubjects, wantUnmatched) {
		t.Errorf("unmatched = %v, want %v", got.UnmatchedSubjects, wantUnmatched)
	}
	got.UnmatchedSubjects = nil
	assertNoLeak(t, got, forbidden)
}

func assertMatrixRefusal(t *testing.T, key, feedbackID string, readable bool, got *analytics.SessionTraceResponse, err error, forbidden []string) {
	t.Helper()
	if !readable {
		if want := fmt.Sprintf(msgFeedbackNotFound, feedbackID); err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", key, err, want)
		}
		return
	}
	if err != nil || got.Refusal != analytics.SessionRefusalNoAccess || got.FeedbackID != feedbackID || len(got.Turns) != 0 {
		t.Errorf("%s: err=%v got=%+v, want the entry marked refused", key, err, got)
		return
	}
	assertNoLeak(t, got, forbidden)
}

func assertMatrixSessions(t *testing.T, exe *GatewayExecutor, fx matrixFixture, c matrixCaller, forbidden []string, logs *bytes.Buffer) {
	denials := func() int { return strings.Count(logs.String(), `"msg":"activity access denied"`) }
	for _, key := range allMatrixFeedback {
		f := fx.feedback[key]
		before := denials()
		got, err := callFeedbackSession(t, exe, f.id, c.identity)
		if !slices.Contains(c.opens, key) {
			entryForbidden := fx.forbiddenStrings(slices.Concat(c.sees, c.sessionSees), slices.Concat(c.visible, c.opens, []string{key}))
			assertMatrixRefusal(t, key, f.id, c.readsRow(key), got, err, entryForbidden)
			if c.readsRow(key) {
				if n := denials() - before; n != 1 {
					t.Errorf("%s: refusal wrote %d denial audit lines, want 1", key, n)
				}
			}
			continue
		}
		if n := denials() - before; n != 0 {
			t.Errorf("%s: opened session wrote %d denial audit lines, want 0", key, n)
		}
		if err != nil {
			t.Errorf("%s: %v, want the session", key, err)
			continue
		}
		if got.Refusal != "" {
			t.Errorf("%s: refused (%s), want the session", key, got.Refusal)
			continue
		}
		assertNoLeak(t, got, forbidden)
		roots := make([]string, 0, len(got.Turns))
		for _, turn := range got.Turns {
			roots = append(roots, turn.Root.ID)
		}
		if got.FeedbackID != f.id || !slices.Equal(roots, f.roots) {
			t.Errorf("%s: feedback %s turns %v, want %s turns %v", key, got.FeedbackID, roots, f.id, f.roots)
		}
		if got.RetentionGap != !f.inRollup {
			t.Errorf("%s: RetentionGap = %v, want %v", key, got.RetentionGap, !f.inRollup)
		}
		if clipped := len(f.roots) == analytics.MaxSessionTurns; got.EarlierTurnsOmitted != clipped {
			t.Errorf("%s: EarlierTurnsOmitted = %v, want %v", key, got.EarlierTurnsOmitted, clipped)
		}
	}
	missing := idgen.New()
	_, err := callFeedbackSession(t, exe, missing, c.identity)
	if err == nil || err.Error() != fmt.Sprintf(msgFeedbackNotFound, missing) {
		t.Errorf("nonexistent feedback: err = %v", err)
	}
}
