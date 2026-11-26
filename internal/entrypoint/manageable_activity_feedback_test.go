// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
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
	"github.com/google/uuid"
)

func feedbackCallerReadingX() *auth.Identity {
	return &auth.Identity{UserID: "reader", Scopes: []string{rbac.AgentReadFeedbackScope(uuidA)}}
}

func seedActivityFeedback(t *testing.T, exe *GatewayExecutor, f store.Feedback) {
	t.Helper()
	db := exe.Gateways()[0].Store()
	start := cmp.Or(f.CreatedAt, time.Now().UnixMilli())
	handler, kind := f.SubjectID, a2a.KindAgent
	if _, err := uuid.Parse(handler); err == nil {
		handler = a2a.InstanceName(kind, handler)
	}
	zero := 0
	task := &store.Task{
		ID: f.TaskID, UserID: f.UserID, StartTime: start,
		Status: ptr("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: &handler, HandlerType: ptr(store.TaskHandlerTypeAgent),
		TotalInputTokens: &zero, TotalOutputTokens: &zero, TotalCachedInputTokens: &zero,
	}
	if err := store.NewTaskRepo(db).Create(t.Context(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	f.SessionID = idgen.New()
	store.SeedFeedback(t, db, &f)
}

func taskUUID(n int) string { return fmt.Sprintf("90000000-0000-0000-0000-%012d", n) }

func addAgentCards(exe *GatewayExecutor, cards ...string) {
	reg := exe.Gateways()[0].AgentRegistry()
	for i := 0; i+1 < len(cards); i += 2 {
		reg.AddOrUpdate(card(cards[i], cards[i+1]))
	}
}

func callActivityFeedback[T any](t *testing.T, exe *GatewayExecutor, report string, body string, id *auth.Identity) (T, error) {
	t.Helper()
	var out T
	res, err := exe.HandleManagement(t.Context(), "get", []string{"activity", "feedback", report}, []byte(body), id)
	if err != nil {
		return out, err
	}
	raw, mErr := json.Marshal(res)
	if mErr != nil {
		t.Fatalf("marshal: %v", mErr)
	}
	if uErr := json.Unmarshal(raw, &out); uErr != nil {
		t.Fatalf("unmarshal: %v", uErr)
	}
	return out, nil
}

func TestActivityFeedbackSummary_TenantSampleCapped(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	for i := 1; i <= 25; i++ {
		seedActivityFeedback(t, exe, store.Feedback{
			TaskID: taskUUID(i), UserID: "u1", Rating: "down", SubjectID: uuidA,
			CreatedAt: int64(1_000_000_000_000 + i), UpdatedAt: int64(1_000_000_000_000 + i),
		})
	}
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	got, err := callActivityFeedback[analytics.FeedbackSummaryResponse](t, exe, "summary",
		`{"startDate":999999999999,"endDate":1000000001000}`, admin)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(got.NegativeSample) != feedbackNegativeSampleSize {
		t.Fatalf("sample = %d, want %d", len(got.NegativeSample), feedbackNegativeSampleSize)
	}
	if got.NegativeSample[0].TaskID != taskUUID(25) {
		t.Fatalf("sample[0] = %s, want newest %s", got.NegativeSample[0].TaskID, taskUUID(25))
	}
}

func TestActivityFeedbackRows_RequiresFilter(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	_, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows", `{"window":"7d"}`, feedbackCallerReadingX())
	if err == nil || err.Error() != "activity: feedback rows need an agent, workflow, user or feedback id filter" {
		t.Fatalf("err = %v", err)
	}
}

func TestActivityFeedbackRows_UserFilter(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "user1", Rating: "down", SubjectID: uuidA})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(2), UserID: "user2", Rating: "down", SubjectID: uuidA})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(3), UserID: "user1", Rating: "down", SubjectID: uuidB})

	got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","feedbackUserId":"user1"}`, feedbackCallerReadingX())
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got.Data) != 1 || got.Data[0].TaskID != taskUUID(1) || got.Data[0].ID == "" {
		t.Fatalf("rows = %+v, want only user1 on X, with an id", got.Data)
	}
}

func TestActivityFeedbackRows_TruncatedFlag(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	for i := 1; i <= 60; i++ {
		seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(i), UserID: "u1", Rating: "down", SubjectID: uuidA})
	}
	got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","feedbackUserId":"u1"}`, feedbackCallerReadingX())
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got.Data) != feedbackRowsLimit || !got.Meta.Truncated {
		t.Fatalf("len=%d truncated=%v, want %d and true", len(got.Data), got.Meta.Truncated, feedbackRowsLimit)
	}
}

func TestActivityFeedbackRows_OwnDataNamesRevealNothing(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	addAgentCards(exe,
		"agent_11111111_1111_1111_1111_111111111111", "HR Assistant",
		"agent_22222222_2222_2222_2222_222222222222", "HR Assistant",
		"agent_44444444_4444_4444_4444_444444444444", "Payroll Agent")
	payroll := "44444444-4444-4444-4444-444444444444"
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "caller", Rating: "down", SubjectID: "caller-only-agent"})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(2), UserID: "someone", Rating: "down", SubjectID: payroll})

	caller := &auth.Identity{UserID: "caller", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	answer := func(name string) analytics.FeedbackRowsResponse {
		got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
			fmt.Sprintf(`{"window":"7d","subjects":[%q]}`, name), caller)
		if err != nil {
			t.Fatalf("rows for %q: %v", name, err)
		}
		return got
	}
	unknown := answer("No Such Agent")
	if len(unknown.Data) != 0 || len(unknown.Candidates) != 0 || len(unknown.UnmatchedSubjects) != 0 {
		t.Fatalf("unknown name answered %+v, want an empty page naming nothing", unknown)
	}
	for _, name := range []string{"Payroll Agent", "HR Assistant", payroll} {
		if got := answer(name); !reflect.DeepEqual(got, unknown) {
			t.Errorf("%q answered %+v, an unknown name %+v; they must match", name, got, unknown)
		}
	}
}

func TestActivityFeedbackRows_ReportsUnmatchedNames(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	addAgentCards(exe, a2a.InstanceName(a2a.KindAgent, uuidA), "Billing Agent")
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "u1", Rating: "down", SubjectID: uuidA})
	reader := identityWith("reader", rbac.AgentReadFeedbackScope(uuidA))

	got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","subjects":["Billing Agent","Bilin"]}`, reader)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got.Data) != 1 || !slices.Equal(got.UnmatchedSubjects, []string{"Bilin"}) {
		t.Fatalf("rows=%d unmatched=%v, want the resolved agent's row and Bilin named", len(got.Data), got.UnmatchedSubjects)
	}

	none, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","subjects":["Bilin"]}`, reader)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(none.Data) != 0 || none.Meta.Pagination.PageNumber != 1 || none.Meta.Pagination.PageSize == 0 {
		t.Fatalf("unresolved answer = %+v, want an empty first page with its metadata", none)
	}
}

func TestActivityFeedback_OwnDataOtherUserRefused(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "caller", Rating: "down", SubjectID: uuidA})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(2), UserID: "user2", Rating: "down", SubjectID: uuidA})
	caller := &auth.Identity{UserID: "caller", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	var logs bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logs, nil))

	_, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","feedbackUserId":"user2"}`, caller)
	if err == nil || err.Error() != msgFeedbackRowsOtherUser {
		t.Fatalf("rows for another user: err = %v, want %q", err, msgFeedbackRowsOtherUser)
	}
	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "activity feedback read refused" {
			line = m
		}
	}
	if line == nil || line["level"] != "WARN" || line["crossUser"] != true || line["targetUser"] != "user2" || line["callerUser"] != "caller" {
		t.Errorf("refusal log = %v, want a WARN naming caller, target and crossUser=true", line)
	}

	self, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","feedbackUserId":"caller"}`, caller)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(self.Data) != 1 || self.Data[0].UserID != "caller" {
		t.Fatalf("rows naming the caller = %+v, want the caller's own", self.Data)
	}
}

func TestActivityFeedbackRows_GuessedIDWithNoFeedbackIsUnmatched(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	addAgentCards(exe, a2a.InstanceName(a2a.KindAgent, uuidA), "Orchestrator")
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "u1", Rating: "down", SubjectID: uuidA})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(2), UserID: "u1", Rating: "down", SubjectID: "retired-helper"})
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}

	for _, report := range []string{"rows", "summary"} {
		typo, err := callActivityFeedback[map[string]any](t, exe, report, `{"window":"7d","subjects":["Orchestrater"]}`, admin)
		if err != nil {
			t.Fatalf("%s: %v", report, err)
		}
		if unmatched, _ := typo["unmatchedSubjects"].([]any); len(unmatched) != 1 || unmatched[0] != "Orchestrater" {
			t.Errorf("%s for a typo: unmatchedSubjects = %v, want [Orchestrater]", report, typo["unmatchedSubjects"])
		}
	}

	both, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		`{"window":"7d","subjects":["retired-helper","Orchestrater"]}`, admin)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(both.Data) != 1 || both.Data[0].TaskID != taskUUID(2) || !slices.Equal(both.UnmatchedSubjects, []string{"Orchestrater"}) {
		t.Fatalf("rows = %+v unmatched = %v, want the retired agent's row and the typo named", both.Data, both.UnmatchedSubjects)
	}
}

type sessionTask struct {
	subject, kind string
	parent        int
	start         int64
	user          string
	unclassified  bool
}

type seededSession struct {
	feedbackID string
	taskIDs    []string
}

func seedFeedbackSession(t *testing.T, exe *GatewayExecutor, specs []sessionTask, rated int, subject string) seededSession {
	t.Helper()
	sessionID, ids := seedSessionTasks(t, exe, specs)
	fb := store.Feedback{TaskID: ids[rated], UserID: cmp.Or(specs[rated].user, "u1"), SessionID: sessionID, Rating: "down", SubjectID: subject}
	store.SeedFeedback(t, exe.Gateways()[0].Store(), &fb)
	return seededSession{feedbackID: fb.ID, taskIDs: ids}
}

func seedSessionTasks(t *testing.T, exe *GatewayExecutor, specs []sessionTask) (string, []string) {
	t.Helper()
	db := exe.Gateways()[0].Store()
	sessionID := idgen.New()
	ids := make([]string, len(specs))
	for i := range specs {
		ids[i] = idgen.New()
	}
	tasks := store.NewTaskRepo(db)
	for i, s := range specs {
		kind := cmp.Or(s.kind, store.TaskHandlerTypeAgent)
		componentKind := a2a.KindAgent
		if kind == store.TaskHandlerTypeWorkflow {
			componentKind = a2a.KindWorkflow
		}
		handler := a2a.InstanceName(componentKind, s.subject)
		task := &store.Task{
			ID: ids[i], UserID: cmp.Or(s.user, "u1"), StartTime: s.start,
			Status: ptr("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: &handler, HandlerType: &kind, SessionID: &sessionID,
		}
		if s.parent >= 0 {
			task.ParentTaskID = &ids[s.parent]
		}
		if s.unclassified {
			task.HandlerName, task.HandlerType = nil, nil
		}
		if err := tasks.Create(t.Context(), task); err != nil {
			t.Fatalf("seed task %d: %v", i, err)
		}
	}
	return sessionID, ids
}

func callFeedbackSession(t *testing.T, exe *GatewayExecutor, feedbackID string, id *auth.Identity) (*analytics.SessionTraceResponse, error) {
	t.Helper()
	res, err := exe.HandleManagement(t.Context(), "get", []string{"activity", "feedback", feedbackID, "session"}, nil, id)
	if err != nil {
		return nil, err
	}
	out, ok := res.(*analytics.SessionTraceResponse)
	if !ok {
		t.Fatalf("result type %T", res)
	}
	return out, nil
}

func assertFeedbackSessionRefused(t *testing.T, got *analytics.SessionTraceResponse, err error, feedbackID string) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v, want a refused entry", err)
	}
	if got.Refusal != analytics.SessionRefusalNoAccess || got.FeedbackID != feedbackID {
		t.Fatalf("refusal=%q feedback=%q, want %q for %s", got.Refusal, got.FeedbackID, analytics.SessionRefusalNoAccess, feedbackID)
	}
	if len(got.Turns) != 0 || got.RatedTaskID != "" {
		t.Fatalf("refused entry carries turns=%d ratedTaskId=%q, want none", len(got.Turns), got.RatedTaskID)
	}
}

func identityWith(user string, scopes ...string) *auth.Identity {
	return &auth.Identity{UserID: user, Scopes: append([]string{rbac.ScopeAgentInvokeAny}, scopes...)}
}

func singleTurnOnX() []sessionTask {
	return []sessionTask{{subject: uuidA, parent: -1, start: 1000}}
}

func TestFeedbackSession_RatedRootAgedOutRefusedForTraceArm(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	db := exe.Gateways()[0].Store()
	sessionID, gone, child := idgen.New(), idgen.New(), idgen.New()
	handler := a2a.InstanceName(a2a.KindAgent, uuidA)
	if err := store.NewTaskRepo(db).Create(t.Context(), &store.Task{
		ID: child, UserID: "u1", StartTime: 2000, ParentTaskID: &gone, SessionID: &sessionID,
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: &handler, HandlerType: ptr(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fb := store.Feedback{TaskID: child, UserID: "u1", SessionID: sessionID, Rating: "down", SubjectID: uuidA}
	store.SeedFeedback(t, db, &fb)

	refused, err := callFeedbackSession(t, exe, fb.ID,
		identityWith("reader", rbac.AgentReadFeedbackScope(uuidA), rbac.AgentReadTraceScope(uuidA)))
	assertFeedbackSessionRefused(t, refused, err, fb.ID)

	got, err := callFeedbackSession(t, exe, fb.ID, identityWith("u1"))
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	if !got.RetentionGap {
		t.Fatal("owner must see the retention gap for the aged-out rated root")
	}
}

func TestFeedbackSession_MalformedID(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	_, err := callFeedbackSession(t, exe, "not-a-uuid", identityWith("u1"))
	if err == nil || !strings.Contains(err.Error(), "feedbackId must be a valid UUID") {
		t.Fatalf("err = %v", err)
	}
}

func TestFeedbackSession_OwnerOpensWithSystemOwnedChild(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: 1000},
		{subject: uuidB, parent: 0, start: 1001, user: store.TaskUserUnknown},
	}, 0, uuidA)
	got, err := callFeedbackSession(t, exe, s.feedbackID, identityWith("u1"))
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	if len(got.Turns) != 1 || len(got.Turns[0].Tree) != 1 || got.Turns[0].Tree[0].ID != s.taskIDs[1] {
		t.Fatalf("got %+v, want the turn with its system-owned child", got.Turns)
	}
}

func TestFeedbackSession_NoSessionIDOpensRatedTurnAlone(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	db := exe.Gateways()[0].Store()
	if db.Dialect() == "postgres" {
		t.Skip("feedback.session_id is UUID NOT NULL on Postgres, so a row without one cannot exist there")
	}
	taskID := idgen.New()
	handler := a2a.InstanceName(a2a.KindAgent, uuidA)
	if err := store.NewTaskRepo(db).Create(t.Context(), &store.Task{
		ID: taskID, UserID: "u1", StartTime: 1000, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: &handler, HandlerType: ptr(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fb := store.Feedback{TaskID: taskID, UserID: "u1", Rating: "down", SubjectID: uuidA}
	store.SeedFeedback(t, db, &fb)

	got, err := callFeedbackSession(t, exe, fb.ID, &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}})
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	if got.Refusal != "" || got.RetentionGap || len(got.Turns) != 1 || got.Turns[0].Root.ID != taskID {
		t.Fatalf("refusal=%q gap=%v turns=%d, want the rated turn alone", got.Refusal, got.RetentionGap, len(got.Turns))
	}

	refused, err := callFeedbackSession(t, exe, fb.ID, identityWith("reader", rbac.AgentReadFeedbackScope(uuidA)))
	assertFeedbackSessionRefused(t, refused, err, fb.ID)
}

func TestActivityFeedback_AuditLineNamesTargetUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	addAgentCards(exe, a2a.InstanceName(a2a.KindAgent, uuidA), "Audit Agent X")
	comment := "audit-secret-comment"
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "u1", Rating: "down", SubjectID: uuidA, Comment: &comment})
	var logs bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logs, nil))

	end := time.Now().UnixMilli() - 1000
	start := end - 24*time.Hour.Milliseconds()
	if _, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
		fmt.Sprintf(`{"startDate":%d,"endDate":%d,"feedbackUserId":"u1","subjects":["Audit Agent X"]}`, start, end), feedbackCallerReadingX()); err != nil {
		t.Fatalf("rows: %v", err)
	}
	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "activity read" {
			line = m
		}
	}
	if line == nil {
		t.Fatalf("no audit line in %s", logs.String())
	}
	if line["targetUser"] != "u1" || line["crossUser"] != true || line["startDate"] != float64(start) ||
		line["endDate"] != float64(end) || line["subjectCount"] != float64(1) || line["callerUser"] != "reader" {
		t.Errorf("audit line = %v", line)
	}
	for _, leak := range []string{uuidA, "Audit Agent X", comment} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("audit log leaks %q: %s", leak, logs.String())
		}
	}
}

func TestActivityFeedbackRows_ExactIDWithoutCard(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "u1", Rating: "down", SubjectID: uuidA})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(2), UserID: "u1", Rating: "down", SubjectID: uuidB})
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(3), UserID: "u1", Rating: "down", SubjectID: "retired-helper"})
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}

	for _, tc := range []struct {
		name     string
		identity *auth.Identity
		subject  string
		wantTask string
	}{
		{"listed id in reach", feedbackCallerReadingX(), uuidA, taskUUID(1)},
		{"id outside reach", feedbackCallerReadingX(), uuidB, ""},
		{"uuid under all-reach", admin, uuidB, taskUUID(2)},
		{"plain name under all-reach", admin, "retired-helper", taskUUID(3)},
		{"display-name-shaped text is not an id", admin, "Retired Helper", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows",
				fmt.Sprintf(`{"window":"7d","subjects":[%q]}`, tc.subject), tc.identity)
			if err != nil {
				t.Fatalf("rows: %v", err)
			}
			var tasks []string
			for _, r := range got.Data {
				tasks = append(tasks, r.TaskID)
			}
			if want := []string{tc.wantTask}; tc.wantTask == "" && len(tasks) != 0 || tc.wantTask != "" && !reflect.DeepEqual(tasks, want) {
				t.Errorf("rows = %v, want %q", tasks, tc.wantTask)
			}
		})
	}
}

func TestActivityFeedbackSummary_SortBy(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(1), UserID: "u1", Rating: "down", SubjectID: uuidA})
	for i := 2; i <= 7; i++ {
		rating := "up"
		if i <= 4 {
			rating = "down"
		}
		seedActivityFeedback(t, exe, store.Feedback{TaskID: taskUUID(i), UserID: "u1", Rating: rating, SubjectID: uuidB})
	}
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	for _, tc := range []struct{ body, first string }{
		{`{"window":"7d"}`, uuidA},
		{`{"window":"7d","sortBy":"negativeRatio"}`, uuidA},
		{`{"window":"7d","sortBy":"negativeFeedback"}`, uuidB},
	} {
		got, err := callActivityFeedback[analytics.FeedbackSummaryResponse](t, exe, "summary", tc.body, admin)
		if err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if len(got.Rows) != 2 || got.Rows[0].Subject.ID != tc.first {
			t.Errorf("%s: rows = %+v, want %s first", tc.body, got.Rows, tc.first)
		}
	}
	_, err := callActivityFeedback[analytics.FeedbackSummaryResponse](t, exe, "summary", `{"window":"7d","sortBy":"name"}`, admin)
	if err == nil || err.Error() != "activity: sortBy must be 'negativeRatio' or 'negativeFeedback'" {
		t.Errorf("bad sortBy: err = %v", err)
	}
}

func callFeedbackSessionByTask(t *testing.T, exe *GatewayExecutor, taskID string, id *auth.Identity) (*analytics.SessionTraceResponse, error) {
	t.Helper()
	res, err := exe.HandleManagement(t.Context(), "get", []string{"activity", "feedback", "task", taskID, "session"}, nil, id)
	if err != nil {
		return nil, err
	}
	out, ok := res.(*analytics.SessionTraceResponse)
	if !ok {
		t.Fatalf("result type %T", res)
	}
	return out, nil
}

func TestFeedbackSessionByTask_UnreadableRowIdenticalToMissing(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, singleTurnOnX(), 0, uuidA)
	caller := identityWith("reader", rbac.AgentReadFeedbackScope(uuidB), rbac.AgentReadTraceScope(uuidB))

	missingTask := idgen.New()
	_, missingErr := callFeedbackSessionByTask(t, exe, missingTask, caller)
	_, refusedErr := callFeedbackSessionByTask(t, exe, s.taskIDs[0], caller)
	if missingErr == nil || refusedErr == nil {
		t.Fatalf("want both refused, got missing=%v refused=%v", missingErr, refusedErr)
	}
	if want := fmt.Sprintf(msgTaskFeedbackNotFound, missingTask); missingErr.Error() != want {
		t.Fatalf("missing = %q, want %q", missingErr, want)
	}
	if strings.ReplaceAll(missingErr.Error(), missingTask, "ID") != strings.ReplaceAll(refusedErr.Error(), s.taskIDs[0], "ID") {
		t.Fatalf("refusal %q differs from missing %q", refusedErr, missingErr)
	}
	if strings.Contains(refusedErr.Error(), s.feedbackID) {
		t.Fatalf("refusal %q names the unreadable feedback id", refusedErr)
	}
}

func TestFeedbackSessionByTask_ReadFeedbackWithoutTraceRefused(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, singleTurnOnX(), 0, uuidA)
	refused, err := callFeedbackSessionByTask(t, exe, s.taskIDs[0], identityWith("reader", rbac.AgentReadFeedbackScope(uuidA)))
	assertFeedbackSessionRefused(t, refused, err, s.feedbackID)
}

func TestFeedbackSessionByTask_UsesOnlyTheCallersReadableRow(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, singleTurnOnX(), 0, uuidA)
	theirs := store.Feedback{TaskID: s.taskIDs[0], UserID: "u2", SessionID: idgen.New(), Rating: "up", SubjectID: uuidA}
	store.SeedFeedback(t, exe.Gateways()[0].Store(), &theirs)

	refused, err := callFeedbackSessionByTask(t, exe, s.taskIDs[0], identityWith("u2"))
	assertFeedbackSessionRefused(t, refused, err, theirs.ID)
	if b, _ := json.Marshal(refused); strings.Contains(string(b), s.feedbackID) {
		t.Fatalf("refusal %s names u1's feedback id", b)
	}

	got, err := callFeedbackSessionByTask(t, exe, s.taskIDs[0], identityWith("u1"))
	if err != nil {
		t.Fatalf("u1: %v", err)
	}
	if got.FeedbackID != s.feedbackID || got.Rating != "down" {
		t.Fatalf("u1 got feedback %q rated %q, want their own %q", got.FeedbackID, got.Rating, s.feedbackID)
	}
}

func TestFeedbackSessionByTask_MalformedID(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	_, err := callFeedbackSessionByTask(t, exe, "not-a-uuid", identityWith("u1"))
	if err == nil || !strings.Contains(err.Error(), "taskId must be a valid UUID") {
		t.Fatalf("err = %v, want a taskId validation error", err)
	}
}

func callTaskTrace(t *testing.T, exe *GatewayExecutor, taskID string, id *auth.Identity) (*analytics.TaskTreeResponse, error) {
	t.Helper()
	res, err := exe.HandleManagement(t.Context(), "get", []string{"activity", "tasks", taskID, "trace"}, nil, id)
	if err != nil {
		return nil, err
	}
	out, ok := res.(*analytics.TaskTreeResponse)
	if !ok {
		t.Fatalf("result type %T", res)
	}
	return out, nil
}

func TestTaskTrace_ReadableFeedbackIDPointsToExplainFeedback(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, singleTurnOnX(), 0, uuidA)
	got, err := callTaskTrace(t, exe, s.feedbackID, identityWith("reader", rbac.AgentReadFeedbackScope(uuidA)))
	if err != nil || got.FeedbackID != s.feedbackID || got.QueriedTaskID != "" {
		t.Fatalf("got %+v, %v; want only feedbackId %s", got, err, s.feedbackID)
	}
}

func TestTaskTrace_UnreadableFeedbackIDIsPlainNotFound(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, singleTurnOnX(), 0, uuidA)
	caller := identityWith("reader", rbac.AgentReadFeedbackScope(uuidB))
	missing := idgen.New()
	_, refusedErr := callTaskTrace(t, exe, s.feedbackID, caller)
	_, missingErr := callTaskTrace(t, exe, missing, caller)
	if refusedErr == nil || missingErr == nil {
		t.Fatalf("want both not found, got %v / %v", refusedErr, missingErr)
	}
	if strings.ReplaceAll(refusedErr.Error(), s.feedbackID, "ID") != strings.ReplaceAll(missingErr.Error(), missing, "ID") {
		t.Fatalf("unreadable feedback id answered %q, missing id %q", refusedErr, missingErr)
	}
}
