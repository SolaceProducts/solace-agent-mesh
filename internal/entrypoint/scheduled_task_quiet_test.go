// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func quietSvc(t *testing.T, on bool) (*ScheduledTaskService, *notifTestBroker) {
	t.Helper()
	return quietSvcOver(t, on, artmem.New())
}

func quietSvcOver(t *testing.T, on bool, backend artifact.Service) (*ScheduledTaskService, *notifTestBroker) {
	t.Helper()
	return quietSvcWith(t, on, &artifact.ScopedService{Inner: backend, Scope: artifact.ScopeNamespace, Namespace: "test-ns"})
}

func quietSvcWith(t *testing.T, on bool, artifacts *artifact.ScopedService) (*ScheduledTaskService, *notifTestBroker) {
	t.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyQuietScheduledRuns, Name: "Quiet Scheduled Runs", Default: on})
	opts := []runtime.Option{runtime.WithFeatureRegistry(reg)}
	if artifacts != nil {
		opts = append(opts, runtime.WithArtifacts(artifacts))
	}
	svc := setupIdentityTestCore(t, opts...).ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() returned nil")
	}
	brk := &notifTestBroker{}
	svc.SetNotifier(NewNotificationService(brk, testLogger()))
	return svc, brk
}

func quietAnswer(summary string) *a2a.Task {
	task := finalTask(a2a.TaskStateCompleted, summary)
	task.Metadata = map[string]any{"runOutcome": "quiet"}
	return task
}

func answerAndFinish(t *testing.T, svc *ScheduledTaskService, taskID string, now time.Time, answer *a2a.Task) *store.ScheduledTaskExecution {
	t.Helper()
	run := sentRun(t, svc, taskID, now)
	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, answer, nil)
	storeFinalEvent(t, svc, run, svc.runSessionID(t.Context(), run.ID), answer)
	mustRunPasses(t, svc, now.Add(2*completionMarkWait))
	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil {
		t.Fatalf("reload run: %v, %v", got, err)
	}
	return got
}

func TestQuietRun_RecordedQuietWithoutANotificationAndResetsTheStreak(t *testing.T) {
	svc, brk := quietSvc(t, true)
	svc.cfg.AutoPauseAfterConsecutiveFailures = 3
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
		st.ConsecutiveFailureCount = 2
		st.RunCount = 5
	})

	run := answerAndFinish(t, svc, task.ID, now, quietAnswer("No new tickets since the last check."))

	if run.Status != "quiet" {
		t.Fatalf("status = %q, want %q", run.Status, "quiet")
	}
	if run.StartedAt == nil || run.CompletedAt == nil {
		t.Fatalf("timing = started %v completed %v, want both recorded", run.StartedAt, run.CompletedAt)
	}
	if run.ResultSummary == nil || !contains(*run.ResultSummary, "No new tickets since the last check.") {
		t.Fatalf("result_summary = %v, want the agent's summary kept on the run", run.ResultSummary)
	}
	if run.FinalizedAt == nil {
		t.Fatal("a quiet run was never finished")
	}
	if run.NotificationsSent != nil {
		t.Fatalf("notifications_sent = %s, want none", *run.NotificationsSent)
	}
	if notices := inboxNotices(t, brk); len(notices) != 0 {
		t.Fatalf("a quiet run notified its owner: %+v", notices)
	}
	got := reloadTask(t, svc, task.ID)
	if got.ConsecutiveFailureCount != 0 || got.RunCount != 6 || !got.Enabled {
		t.Fatalf("schedule = {failures %d, runs %d, enabled %v}, want a success: 0, 6, enabled",
			got.ConsecutiveFailureCount, got.RunCount, got.Enabled)
	}
}

func ownerSidebarSessions(t *testing.T, svc *ScheduledTaskService, owner string) []*store.Session {
	t.Helper()
	filter := store.SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true}
	sessions, _, err := svc.sessRepo.ListByUserPage(t.Context(), owner, filter, 50, 0)
	if err != nil {
		t.Fatalf("ListByUserPage(%s): %v", owner, err)
	}
	return sessions
}

func sessionCreatedSessionIDs(notifier *fakeUserNotifier) []string {
	var ids []string
	for _, c := range notifier.Calls() {
		if c.EventType != "session_created" {
			continue
		}
		payload, _ := c.Data.(map[string]any)
		id, _ := payload["session_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func TestQuietRun_SurfacesNoChatSession(t *testing.T) {
	tests := map[string]struct {
		answer         *a2a.Task
		wantSidebar    int
		wantRunSession bool
	}{
		"a quiet run is hidden from the owner and announces a refresh without a session": {quietAnswer("Nothing new."), 0, false},
		"a completed run stays listed and announces its session":                         {finalTask(a2a.TaskStateCompleted, "Nothing to report."), 1, true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := quietSvc(t, true)
			notifier := &fakeUserNotifier{}
			svc.SetUserNotifier(notifier)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)

			run := answerAndFinish(t, svc, task.ID, now, tc.answer)

			if got := len(ownerSidebarSessions(t, svc, "alice")); got != tc.wantSidebar {
				t.Fatalf("sessions in the owner's sidebar = %d, want %d", got, tc.wantSidebar)
			}
			wantAnnounced := []string{""}
			if tc.wantRunSession {
				wantAnnounced = []string{svc.runSessionID(t.Context(), run.ID)}
			}
			if got := sessionCreatedSessionIDs(notifier); !slices.Equal(got, wantAnnounced) {
				t.Fatalf("session_created session ids = %q, want %q", got, wantAnnounced)
			}
			if got := svc.runSessionID(t.Context(), run.ID) != ""; got != tc.wantRunSession {
				t.Fatalf("run has a session mapping = %v, want %v", got, tc.wantRunSession)
			}
			if run.FinalizedAt == nil {
				t.Fatal("run was never finished")
			}
		})
	}
}

func quietAnswerProducing(summary string, produced ...string) *a2a.Task {
	task := quietAnswer(summary)
	entries := make([]any, 0, len(produced))
	for _, name := range produced {
		entries = append(entries, map[string]any{"filename": name, "version": 0})
	}
	if len(entries) > 0 {
		task.Metadata["produced_artifacts"] = entries
	}
	return task
}

func quietAnswerProducingUnversioned(summary string, produced ...string) *a2a.Task {
	task := quietAnswer(summary)
	entries := make([]any, 0, len(produced))
	for _, name := range produced {
		entries = append(entries, map[string]any{"filename": name})
	}
	task.Metadata["produced_artifacts"] = entries
	return task
}

type userScopeListing struct{ artifact.Service }

func (l userScopeListing) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	keys, err := l.Service.ListKeys(ctx, scope)
	if err != nil || scope.SessionID == "" {
		return keys, err
	}
	userKeys, err := l.Service.ListKeys(ctx, artifact.KeyScope{AppName: scope.AppName, UserID: scope.UserID})
	if err != nil {
		return nil, err
	}
	for _, name := range userKeys {
		keys = append(keys, "user:"+name)
	}
	return keys, nil
}

type quietFile struct {
	name        string
	userScoped  bool
	taggedWorks bool
}

func saveQuietFile(t *testing.T, svc *ScheduledTaskService, owner, sessionID string, f quietFile) {
	t.Helper()
	artifacts := svc.core.Services().Artifacts()
	session := sessionID
	if f.userScoped {
		session = ""
	}
	loc := artifacts.Resolve("", owner, session, f.name)
	if _, err := artifacts.Inner.Save(t.Context(), loc, []byte("report body"), "text/plain"); err != nil {
		t.Fatalf("save %s: %v", f.name, err)
	}
	if !f.taggedWorks {
		return
	}
	companion, err := json.Marshal(map[string]any{"tags": []string{a2a.ArtifactTagWorking}})
	if err != nil {
		t.Fatalf("encode the tags of %s: %v", f.name, err)
	}
	if _, err := artifacts.Inner.Save(t.Context(), artifacts.Resolve("", owner, session, f.name+artifact.MetadataSuffix), companion, "application/json"); err != nil {
		t.Fatalf("tag %s: %v", f.name, err)
	}
}

func storeFinalEvent(t *testing.T, svc *ScheduledTaskService, run *store.ScheduledTaskExecution, sessionID string, answer *a2a.Task) {
	t.Helper()
	a2aTaskID := *run.A2ATaskID
	storeEvent(t, svc, a2aTaskID, sessionID, 1, "final_response", a2a.Response{JSONRPC: "2.0", ID: a2aTaskID, Result: answer})
}

func markEventsComplete(t *testing.T, svc *ScheduledTaskService, run *store.ScheduledTaskExecution) {
	t.Helper()
	if err := svc.sseRepo.MarkCompleted(t.Context(), *run.A2ATaskID); err != nil {
		t.Fatalf("MarkCompleted(%s): %v", *run.A2ATaskID, err)
	}
}

func finishQuietRunWithStoredFinal(t *testing.T, svc *ScheduledTaskService, run *store.ScheduledTaskExecution, sessionID string, answer *a2a.Task, now time.Time) {
	t.Helper()
	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, answer, nil)
	storeFinalEvent(t, svc, run, sessionID, answer)
	markEventsComplete(t, svc, run)
	mustRunPasses(t, svc, now.Add(2*completionMarkWait))
}

const workflowHandoffFile = "wf_invoice-flow_output_9f2c41ab.json"

func TestQuietRun_KeepsItsSessionForAServableFile(t *testing.T) {
	const (
		owner   = "alice"
		report  = "report.txt"
		summary = "Nothing new."
	)
	tests := map[string]struct {
		saved       []quietFile
		answer      *a2a.Task
		wantKept    bool
		wantChatHas string
	}{
		"a produced file keeps the session, saves the chat and stays downloadable": {
			[]quietFile{{name: report}}, quietAnswerProducing(summary, report), true, report},
		"a file a delegated agent saved, listed without a version, keeps the session": {
			[]quietFile{{name: report}}, quietAnswerProducingUnversioned(summary, report), true, report},
		"a produced file keeps the session even when the owner also has a user file": {
			[]quietFile{{name: report}, {name: "owner-notes.txt", userScoped: true}}, quietAnswerProducing(summary, report), true, report},
		"a servable file the final answer does not list keeps the session": {
			[]quietFile{{name: report}}, quietAnswer(summary), true, ""},
		"a servable file beside working, auto-named and internal saves keeps the session": {
			[]quietFile{{name: "scratch.csv", taggedWorks: true}, {name: "web_content_0190a1b2"}, {name: "tool_result_9f2c41ab.json"}, {name: report}},
			quietAnswer(summary), true, ""},
		"a user-scoped owner file alone hides the session": {
			[]quietFile{{name: "owner-notes.txt", userScoped: true}}, quietAnswer(summary), false, ""},
		"a working-tagged save alone hides the session": {
			[]quietFile{{name: "scratch.csv", taggedWorks: true}}, quietAnswer(summary), false, ""},
		"an auto-named web page save alone hides the session": {
			[]quietFile{{name: "web_content_0190a1b2"}}, quietAnswer(summary), false, ""},
		"an auto-named tool result save alone hides the session": {
			[]quietFile{{name: "tool_result_9f2c41ab.json"}}, quietAnswer(summary), false, ""},
		"only internal files hide the session": {
			[]quietFile{{name: report + ".metadata.json"}}, quietAnswer(summary), false, ""},
		"a workflow's hand-off file alone, listed as produced, hides the session": {
			[]quietFile{{name: workflowHandoffFile, taggedWorks: true}}, quietAnswerProducing(summary, workflowHandoffFile), false, ""},
		"a workflow's hand-off file alone, listed as produced and not tagged working, hides the session": {
			[]quietFile{{name: workflowHandoffFile}}, quietAnswerProducing(summary, workflowHandoffFile), false, ""},
		"a workflow's hand-off file alone, not listed and not tagged working, hides the session": {
			[]quietFile{{name: workflowHandoffFile}}, quietAnswer(summary), false, ""},
		"a produced list naming only a file tagged working hides the session": {
			[]quietFile{{name: "scratch.csv", taggedWorks: true}}, quietAnswerProducing(summary, "scratch.csv"), false, ""},
		"a produced list naming only an auto-named tool result hides the session": {
			[]quietFile{{name: "tool_result_9f2c41ab.json"}}, quietAnswerProducing(summary, "tool_result_9f2c41ab.json"), false, ""},
		"a produced list naming only an internal file hides the session": {
			[]quietFile{{name: report + ".metadata.json"}}, quietAnswerProducing(summary, report+".metadata.json"), false, ""},
		"a produced list naming a user-scoped file only hides the session": {
			[]quietFile{{name: "owner-notes.txt", userScoped: true}}, quietAnswerProducing(summary, "user:owner-notes.txt"), false, ""},
		"a produced list naming only an auto-named tool result keeps the session for an unlisted servable file": {
			[]quietFile{{name: "tool_result_9f2c41ab.json"}, {name: report}}, quietAnswerProducing(summary, "tool_result_9f2c41ab.json"), true, ""},
		"a workflow's hand-off file beside a produced servable file keeps the session": {
			[]quietFile{{name: workflowHandoffFile, taggedWorks: true}, {name: report}}, quietAnswerProducing(summary, workflowHandoffFile, report), true, report},
		"a produced list naming only an auto-named web page hides the session": {
			[]quietFile{{name: "web_content_0190a1b2"}}, quietAnswerProducing(summary, "web_content_0190a1b2"), false, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, brk := quietSvcOver(t, true, userScopeListing{artmem.New()})
			notifier := &fakeUserNotifier{}
			svc.SetUserNotifier(notifier)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)
			run := sentRun(t, svc, task.ID, now)
			sessionID := svc.runSessionID(t.Context(), run.ID)
			for _, f := range tc.saved {
				saveQuietFile(t, svc, owner, sessionID, f)
			}

			finishQuietRunWithStoredFinal(t, svc, run, sessionID, tc.answer, now)

			got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
			if err != nil || got == nil {
				t.Fatalf("reload run: %v, %v", got, err)
			}
			if got.Status != store.ExecutionStatusQuiet || got.FinalizedAt == nil {
				t.Errorf("run = {status %q, finalized %v}, want a finished quiet run", got.Status, got.FinalizedAt != nil)
			}
			if got.NotificationsSent != nil || len(inboxNotices(t, brk)) != 0 {
				t.Errorf("a quiet run notified its owner: sent %v, notices %+v", got.NotificationsSent, inboxNotices(t, brk))
			}
			wantSessions, wantAnnounced := 0, []string{""}
			if tc.wantKept {
				wantSessions, wantAnnounced = 1, []string{sessionID}
			}
			if n := len(ownerSidebarSessions(t, svc, owner)); n != wantSessions {
				t.Errorf("sessions in the owner's sidebar = %d, want %d", n, wantSessions)
			}
			if got := sessionCreatedSessionIDs(notifier); !slices.Equal(got, wantAnnounced) {
				t.Errorf("session_created session ids = %q, want %q", got, wantAnnounced)
			}
			if mapped := svc.runSessionID(t.Context(), run.ID) == sessionID; mapped != tc.wantKept {
				t.Errorf("run keeps its session mapping = %v, want %v", mapped, tc.wantKept)
			}
			chat, err := svc.chatRepo.GetByID(t.Context(), *run.A2ATaskID)
			if err != nil {
				t.Fatalf("chat task: %v", err)
			}
			if tc.wantKept != (chat != nil) {
				t.Fatalf("saved chat = %v, want one = %v", chat, tc.wantKept)
			}
			if chat != nil && (!contains(chat.MessageBubbles, summary) || !contains(chat.MessageBubbles, tc.wantChatHas)) {
				t.Errorf("chat bubbles = %s, want the summary %q and the file %q", chat.MessageBubbles, summary, tc.wantChatHas)
			}
			wantServed := tc.wantKept && slices.ContainsFunc(tc.saved, func(f quietFile) bool { return f.name == report })
			if served := quietFileServedForSession(t, svc, sessionID, owner, report); served != wantServed {
				t.Errorf("scheduled-artifact route would serve %s = %v, want %v", report, served, wantServed)
			}
		})
	}
}

func quietFileServedForSession(t *testing.T, svc *ScheduledTaskService, sessionID, owner, filename string) bool {
	t.Helper()
	keys, err := svc.sessKeyRepo.GetByGatewayAndSessionIDs(t.Context(), store.GatewayTypeScheduled, []string{sessionID})
	if err != nil {
		t.Fatalf("GetByGatewayAndSessionIDs(%s): %v", sessionID, err)
	}
	if _, mapped := keys[sessionID]; !mapped {
		return false
	}
	artifacts := svc.core.Services().Artifacts()
	content, _, err := artifacts.Inner.Load(t.Context(), artifacts.Resolve("", owner, sessionID, filename), nil)
	if err != nil {
		t.Fatalf("Load(%s): %v", filename, err)
	}
	return string(content) == "report body"
}

func TestQuietRun_FinishesWhenItsSessionWasAlreadyHidden(t *testing.T) {
	svc, _ := quietSvc(t, true)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, quietAnswer("Nothing new."), nil)
	if hidden, err := svc.sessRepo.HideEmptySession(t.Context(), svc.runSessionID(t.Context(), run.ID), "alice"); err != nil || !hidden {
		t.Fatalf("hide = %v, %v, want hidden", hidden, err)
	}

	mustRunPasses(t, svc, now.Add(2*completionMarkWait))

	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil || got.FinalizedAt == nil {
		t.Fatalf("run = %+v, %v, want it finished", got, err)
	}
	if got := sessionCreatedSessionIDs(notifier); !slices.Equal(got, []string{""}) {
		t.Fatalf("session_created session ids = %q, want one refresh without a session", got)
	}
}

type unlistableArtifacts struct{ artifact.Service }

func (unlistableArtifacts) ListKeys(context.Context, artifact.KeyScope) ([]string, error) {
	return nil, errors.New("artifact store is down")
}

func requireQuietRunUnfinished(t *testing.T, svc *ScheduledTaskService, run *store.ScheduledTaskExecution, sessionID string) {
	t.Helper()
	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil {
		t.Fatalf("reload run: %v, %v", got, err)
	}
	if got.FinalizedAt != nil {
		t.Errorf("run was finished while its session could not be settled")
	}
	if kept := svc.runSessionID(t.Context(), run.ID); kept != sessionID {
		t.Errorf("run's session mapping = %q, want %q kept for the retry", kept, sessionID)
	}
}

func TestQuietRun_IsRetriedWhenItsSessionCannotBeHidden(t *testing.T) {
	svc, _ := quietSvc(t, true)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	sessionID := svc.runSessionID(t.Context(), run.ID)
	answer := quietAnswer("Nothing new.")
	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, answer, nil)
	storeFinalEvent(t, svc, run, sessionID, answer)
	db := svc.core.Store()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE mcp_artifact_links RENAME TO mcp_artifact_links_away`); err != nil {
		t.Fatalf("break the hide: %v", err)
	}

	mustRunPasses(t, svc, now.Add(2*completionMarkWait))

	requireQuietRunUnfinished(t, svc, run, sessionID)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE mcp_artifact_links_away RENAME TO mcp_artifact_links`); err != nil {
		t.Fatalf("repair the hide: %v", err)
	}
	mustRunPasses(t, svc, now.Add(3*completionMarkWait))
	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil || got.FinalizedAt == nil {
		t.Fatalf("run = %+v, %v, want it finished once the hide works", got, err)
	}
	if kept := svc.runSessionID(t.Context(), run.ID); kept != "" {
		t.Errorf("run's session mapping = %q, want it dropped with the hidden session", kept)
	}
	if n := len(ownerSidebarSessions(t, svc, "alice")); n != 0 {
		t.Errorf("sessions in the owner's sidebar = %d, want 0", n)
	}
	if got := sessionCreatedSessionIDs(notifier); !slices.Equal(got, []string{""}) {
		t.Errorf("session_created session ids = %q, want one refresh without a session", got)
	}
}

type stalledListing struct{ artifact.Service }

const stalledListingGiveUp = 2 * time.Second

func (stalledListing) ListKeys(ctx context.Context, _ artifact.KeyScope) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(stalledListingGiveUp):
		return nil, errors.New("the call deadline never reached the store")
	}
}

type strictlyUnlistableArtifacts struct{ artifact.Service }

func (strictlyUnlistableArtifacts) ListKeys(context.Context, artifact.KeyScope) ([]string, error) {
	return nil, nil
}

func (strictlyUnlistableArtifacts) ListKeysStrict(context.Context, artifact.KeyScope) ([]string, error) {
	return nil, errors.New("artifact directory is unreadable")
}

func TestQuietRun_KeepsItsSessionWhenTheArtifactStoreCannotList(t *testing.T) {
	tests := map[string]struct {
		store     string
		answer    *a2a.Task
		wantClass string
	}{
		"a store that errors, final lists a file":                           {"errors", quietAnswerProducing("Nothing new.", "report.txt"), ""},
		"a store that errors, final lists no file":                          {"errors", quietAnswer("Nothing new."), "list_failed"},
		"a store that errors on a strict listing only, final lists no file": {"strict", quietAnswer("Nothing new."), "list_failed"},
		"a store that stalls past the call timeout, no file":                {"stalls", quietAnswer("Nothing new."), "deadline"},
		"no artifact service, final lists no file":                          {"absent", quietAnswer("Nothing new."), "no_artifact_store"},
		"a component-scoped store, final lists no file":                     {"component", quietAnswer("Nothing new."), "scope_not_namespace"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var svc *ScheduledTaskService
			switch tc.store {
			case "errors":
				svc, _ = quietSvcOver(t, true, unlistableArtifacts{artmem.New()})
			case "strict":
				svc, _ = quietSvcOver(t, true, strictlyUnlistableArtifacts{artmem.New()})
			case "stalls":
				svc, _ = quietSvcOver(t, true, stalledListing{artmem.New()})
			case "absent":
				svc, _ = quietSvcWith(t, true, nil)
			case "component":
				svc, _ = quietSvcWith(t, true, &artifact.ScopedService{Inner: artmem.New(), Scope: "component"})
			}
			svc.quietStoreTimeout = 20 * time.Millisecond
			logs := &bytes.Buffer{}
			svc.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)
			run := sentRun(t, svc, task.ID, now)
			sessionID := svc.runSessionID(t.Context(), run.ID)

			finishQuietRunWithStoredFinal(t, svc, run, sessionID, tc.answer, now)

			got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
			if err != nil || got == nil || got.FinalizedAt == nil {
				t.Fatalf("run = %+v, %v, want it finished at once, never retried, though the artifact store cannot list", got, err)
			}
			if kept := svc.runSessionID(t.Context(), run.ID); kept != sessionID {
				t.Errorf("run's session mapping = %q, want %q kept: a store that cannot list cannot show the run produced nothing", kept, sessionID)
			}
			if chat, err := svc.chatRepo.GetByID(t.Context(), *run.A2ATaskID); err != nil || chat == nil {
				t.Errorf("saved chat = %v, %v, want the run's chat saved", chat, err)
			}
			warned := strings.Contains(logs.String(), `"level":"WARN"`) && strings.Contains(logs.String(), "could not be listed")
			if warned != (tc.wantClass != "") {
				t.Errorf("warned about the listing = %v, want %v; logs: %s", warned, tc.wantClass != "", logs.String())
			}
			if tc.wantClass != "" && !strings.Contains(logs.String(), `"errorClass":"`+tc.wantClass+`"`) {
				t.Errorf("the warning's error class is not %q; logs: %s", tc.wantClass, logs.String())
			}
			for _, leaked := range []string{"artifact store is down", "artifact directory is unreadable"} {
				if strings.Contains(logs.String(), leaked) {
					t.Errorf("the log carries the store's own error text %q: %s", leaked, logs.String())
				}
			}
		})
	}
}

type slowArtifacts struct {
	artifact.Service
	delay time.Duration
}

func (s *slowArtifacts) pause(ctx context.Context) error {
	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *slowArtifacts) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	if err := s.pause(ctx); err != nil {
		return nil, err
	}
	return s.Service.ListKeys(ctx, scope)
}

func (s *slowArtifacts) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	if err := s.pause(ctx); err != nil {
		return nil, "", err
	}
	return s.Service.Load(ctx, loc, version)
}

func TestQuietRun_ChecksItsSessionWithinOneDeadline(t *testing.T) {
	slow := &slowArtifacts{Service: artmem.New()}
	svc, _ := quietSvcOver(t, true, slow)
	svc.quietStoreTimeout = 400 * time.Millisecond
	logs := &bytes.Buffer{}
	svc.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	sessionID := svc.runSessionID(t.Context(), run.ID)
	saveQuietFile(t, svc, "alice", sessionID, quietFile{name: "scratch.csv", taggedWorks: true})
	slow.delay = 250 * time.Millisecond

	finishQuietRunWithStoredFinal(t, svc, run, sessionID, quietAnswer("Nothing new."), now)

	if kept := svc.runSessionID(t.Context(), run.ID); kept != sessionID {
		t.Errorf("run's session mapping = %q, want %q kept: a listing and a read that together outlast the deadline cannot show the run produced nothing", kept, sessionID)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), `"errorClass":"deadline"`) {
		t.Errorf("a deadline that ran out while reading a file's tags was not warned about as a deadline; logs: %s", logs.String())
	}
	if strings.Contains(logs.String(), "saved a servable file") {
		t.Errorf("a deadline that ran out was logged as a servable file; logs: %s", logs.String())
	}
}

func TestQuietRun_LogsTheHideItMakes(t *testing.T) {
	svc, _ := quietSvc(t, true)
	logs := &bytes.Buffer{}
	svc.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	sessionID := svc.runSessionID(t.Context(), run.ID)

	finishQuietRunWithStoredFinal(t, svc, run, sessionID, quietAnswer("Nothing new."), now)

	if !strings.Contains(logs.String(), `"level":"INFO"`) || !strings.Contains(logs.String(), "its session was hidden") || !strings.Contains(logs.String(), sessionID) {
		t.Fatalf("hiding a session was not logged at Info with its id; logs: %s", logs.String())
	}
	if strings.Contains(logs.String(), "Nothing new.") {
		t.Errorf("the hide log carries the run's summary: %s", logs.String())
	}
}

func TestQuietRun_IsRetriedWhenItsSessionMappingCannotBeRead(t *testing.T) {
	svc, _ := quietSvc(t, true)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	logs := &bytes.Buffer{}
	svc.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	sessionID := svc.runSessionID(t.Context(), run.ID)
	answer := quietAnswer("Nothing new.")
	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, answer, nil)
	storeFinalEvent(t, svc, run, sessionID, answer)
	markEventsComplete(t, svc, run)
	db := svc.core.Store()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE gateway_session_keys RENAME TO gateway_session_keys_away`); err != nil {
		t.Fatalf("break the mapping lookup: %v", err)
	}

	mustRunPasses(t, svc, now.Add(2*completionMarkWait))

	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil || got.FinalizedAt != nil {
		t.Fatalf("run = %+v, %v, want it left unfinished while its session cannot be looked up", got, err)
	}
	if ids := sessionCreatedSessionIDs(notifier); len(ids) != 0 {
		t.Errorf("session_created session ids = %q, want none before the run has a decision", ids)
	}
	if kept, lookupErr := svc.sessRepo.GetByID(t.Context(), sessionID); lookupErr != nil || kept == nil {
		t.Errorf("session = %v, %v, want it kept while it cannot be settled", kept, lookupErr)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "look up the session") {
		t.Errorf("the failed lookup was not warned about; logs: %s", logs.String())
	}
	if _, repairErr := db.ExecContext(t.Context(), `ALTER TABLE gateway_session_keys_away RENAME TO gateway_session_keys`); repairErr != nil {
		t.Fatalf("repair the mapping lookup: %v", repairErr)
	}
	mustRunPasses(t, svc, now.Add(3*completionMarkWait))
	got, err = svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil || got.FinalizedAt == nil {
		t.Fatalf("run = %+v, %v, want it finished once the lookup works", got, err)
	}
	if hidden, err := svc.sessRepo.GetByID(t.Context(), sessionID); err != nil || hidden != nil {
		t.Errorf("session = %v, %v, want the empty session hidden once the lookup works", hidden, err)
	}
}

func TestFinish_AnswersARunWhoseSessionMappingCannotBeReadAsBefore(t *testing.T) {
	failedAnswer := finalTask(a2a.TaskStateFailed, "boom")
	tests := map[string]struct {
		answer     *a2a.Task
		wantStatus string
	}{
		"a completed run": {finalTask(a2a.TaskStateCompleted, "Done."), "completed"},
		"a failed run":    {failedAnswer, "failed"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, brk := quietSvc(t, true)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)
			run := sentRun(t, svc, task.ID, now)
			otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, tc.answer, nil)
			if _, err := svc.core.Store().ExecContext(t.Context(), `ALTER TABLE gateway_session_keys RENAME TO gateway_session_keys_away`); err != nil {
				t.Fatalf("break the mapping lookup: %v", err)
			}

			mustRunPasses(t, svc, now.Add(2*completionMarkWait))

			got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
			if err != nil || got == nil {
				t.Fatalf("reload run: %v, %v", got, err)
			}
			if got.Status != tc.wantStatus || got.FinalizedAt == nil {
				t.Errorf("run = {status %q, finalized %v}, want a finished %s run", got.Status, got.FinalizedAt != nil, tc.wantStatus)
			}
			if notices := inboxNotices(t, brk); len(notices) != 1 {
				t.Errorf("notices = %d, want the one notification a %s run sends", len(notices), tc.wantStatus)
			}
		})
	}
}

func TestArtifactCheckFailure_NamesACanceledListing(t *testing.T) {
	tests := map[string]struct {
		err  error
		want string
	}{
		"a canceled context":             {fmt.Errorf("list: %w", context.Canceled), "canceled"},
		"an exceeded deadline":           {fmt.Errorf("list: %w", context.DeadlineExceeded), "deadline"},
		"any other store failure":        {errors.New("store is down"), "list_failed"},
		"a missing artifact store":       {errNoArtifactStore, "no_artifact_store"},
		"a scope the gateway can't list": {errArtifactScopeNotNamespace, "scope_not_namespace"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := artifactCheckFailure(tc.err); got != tc.want {
				t.Errorf("artifactCheckFailure(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestQuietRun_ListsTheFilesUnderTheUserTheRunRanAs(t *testing.T) {
	const (
		owner          = "alice"
		serviceAccount = "svc-reports"
	)
	tests := map[string]struct {
		savedUnder string
		wantKept   bool
	}{
		"a file the service account saved keeps the owner's session": {serviceAccount, true},
		"a file saved under the owner is not one the run wrote":      {owner, false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := quietSvcOver(t, true, userScopeListing{artmem.New()})
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)
			run := sentRun(t, svc, task.ID, now)
			if err := svc.repo.UpdateExecution(t.Context(), run.ID, map[string]any{"run_as": serviceAccount}); err != nil {
				t.Fatalf("record the run as run by a service account: %v", err)
			}
			sessionID := svc.runSessionID(t.Context(), run.ID)
			saveQuietFile(t, svc, tc.savedUnder, sessionID, quietFile{name: "report.txt"})

			finishQuietRunWithStoredFinal(t, svc, run, sessionID, quietAnswer("Nothing new."), now)

			if kept := svc.runSessionID(t.Context(), run.ID) == sessionID; kept != tc.wantKept {
				t.Errorf("run keeps its session mapping = %v, want %v", kept, tc.wantKept)
			}
			if n := len(ownerSidebarSessions(t, svc, owner)); (n == 1) != tc.wantKept {
				t.Errorf("sessions in the owner's sidebar = %d, want kept = %v", n, tc.wantKept)
			}
		})
	}
}

func TestQuietRun_OtherEndsNotifyAsBefore(t *testing.T) {
	failedQuiet := quietAnswer("boom")
	failedQuiet.Status.State = a2a.TaskStateFailed
	tests := map[string]struct {
		flagOn     bool
		answer     *a2a.Task
		wantStatus string
	}{
		"flag off records a quiet answer as completed": {false, quietAnswer("Nothing to report."), "completed"},
		"flag on, a normal reply stays completed":      {true, finalTask(a2a.TaskStateCompleted, "Nothing to report."), "completed"},
		"flag on, a failed quiet answer stays failed":  {true, failedQuiet, "failed"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, brk := quietSvc(t, tc.flagOn)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)

			run := answerAndFinish(t, svc, task.ID, now, tc.answer)

			if run.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", run.Status, tc.wantStatus)
			}
			if notices := inboxNotices(t, brk); len(notices) != 1 {
				t.Fatalf("notices = %d, want the one notification a %s run sends", len(notices), tc.wantStatus)
			}
		})
	}
}

func TestQuietRun_RequestAllowsAQuietEndOnlyForAnAgentWithTheFlagOn(t *testing.T) {
	tests := map[string]struct {
		flagOn     bool
		targetType string
		want       bool
	}{
		"flag on, agent target":    {true, "agent", true},
		"flag on, workflow target": {true, "workflow", false},
		"flag off, agent target":   {false, "agent", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := quietSvc(t, tc.flagOn)
			task := makeIdentityTestTask("task-1", "alice")
			task.TargetType = tc.targetType

			msg := svc.buildMessage(task, "exec-1", "session-1")

			allowed, _ := msg.Metadata["quietEndAllowed"].(bool)
			if allowed != tc.want {
				t.Fatalf("quietEndAllowed = %v, want %v", allowed, tc.want)
			}
		})
	}
}

func TestQuietRun_ListedAsTheLastFinishedRun(t *testing.T) {
	svc, _ := quietSvc(t, true)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := answerAndFinish(t, svc, task.ID, now, quietAnswer("Nothing new."))

	last, err := svc.repo.FindLastCompletedExecutionsByTasks(t.Context(), []string{task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := last[task.ID]; got == nil || got.ID != run.ID {
		t.Fatalf("last finished run = %+v, want the quiet run %s", got, run.ID)
	}
	one, err := svc.repo.FindLastCompletedExecutionByTask(t.Context(), task.ID)
	if err != nil || one == nil || one.ID != run.ID {
		t.Fatalf("last finished run = %+v, %v, want the quiet run %s", one, err, run.ID)
	}
}

func storeRawFinalEvent(t *testing.T, svc *ScheduledTaskService, run *store.ScheduledTaskExecution, sessionID, eventData string) {
	t.Helper()
	if err := svc.sseRepo.Append(t.Context(), &store.SSEEventBufferRow{
		TaskID: *run.A2ATaskID, SessionID: sessionID, UserID: "alice", EventSequence: 1,
		EventType: "final_response", EventData: eventData, CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("append the final event: %v", err)
	}
}

func TestQuietRun_KeepsItsSessionWhenItsFinalAnswerCannotBeRead(t *testing.T) {
	tests := map[string]struct {
		finalEvent string
	}{
		"no final answer was stored":                {""},
		"the stored final answer is not valid JSON": {`{"jsonrpc": "2.0", "result": {`},
		"the stored final answer is not a Task":     {`{"jsonrpc": "2.0", "id": "x", "result": "done"}`},
		"the stored final answer has a null result": {`{"jsonrpc": "2.0", "id": "x", "result": null}`},
		"the stored final answer has no result":     {`{"jsonrpc": "2.0", "id": "x"}`},
		"the stored final answer has an empty Task": {`{"jsonrpc": "2.0", "id": "x", "result": {}}`},
		"the stored final answer is an agent error": {`{"jsonrpc": "2.0", "id": "x", "error": {"code": -32000, "message": "agent failed"}}`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := quietSvc(t, true)
			notifier := &fakeUserNotifier{}
			svc.SetUserNotifier(notifier)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)
			run := sentRun(t, svc, task.ID, now)
			sessionID := svc.runSessionID(t.Context(), run.ID)
			otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, quietAnswer("Nothing new."), nil)
			if tc.finalEvent != "" {
				storeRawFinalEvent(t, svc, run, sessionID, tc.finalEvent)
			}
			markEventsComplete(t, svc, run)

			mustRunPasses(t, svc, now.Add(2*completionMarkWait))

			got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
			if err != nil || got == nil {
				t.Fatalf("reload run: %v, %v", got, err)
			}
			if got.Status != store.ExecutionStatusQuiet || got.FinalizedAt == nil {
				t.Errorf("run = {status %q, finalized %v}, want a finished quiet run", got.Status, got.FinalizedAt != nil)
			}
			if kept := svc.runSessionID(t.Context(), run.ID); kept != sessionID {
				t.Errorf("run's session mapping = %q, want %q kept: an unreadable final cannot show the run produced nothing", kept, sessionID)
			}
			if n := len(ownerSidebarSessions(t, svc, "alice")); n != 1 {
				t.Errorf("sessions in the owner's sidebar = %d, want 1", n)
			}
			if got := sessionCreatedSessionIDs(notifier); !slices.Equal(got, []string{sessionID}) {
				t.Errorf("session_created session ids = %q, want the kept session %q", got, sessionID)
			}
			if chat, err := svc.chatRepo.GetByID(t.Context(), *run.A2ATaskID); err != nil || chat == nil {
				t.Errorf("saved chat = %v, %v, want the run's chat saved with what is available", chat, err)
			}
		})
	}
}
func TestQuietRun_WaitsForItsStoredFinalBeforeDecidingTheSession(t *testing.T) {
	tests := map[string]struct {
		answer   *a2a.Task
		wantKept bool
	}{
		"a final listing a file":  {quietAnswerProducing("Nothing new.", "report.txt"), true},
		"a final listing no file": {quietAnswer("Nothing new."), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := quietSvc(t, true)
			notifier := &fakeUserNotifier{}
			svc.SetUserNotifier(notifier)
			now := time.Now()
			task := dueTask(t, svc, now.Add(-time.Second), nil)
			run := sentRun(t, svc, task.ID, now)
			sessionID := svc.runSessionID(t.Context(), run.ID)
			otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, tc.answer, nil)
			storeFinalEvent(t, svc, run, sessionID, tc.answer)

			mustRunPasses(t, svc, now.Add(completionMarkWait/2))

			requireQuietRunUnfinished(t, svc, run, sessionID)
			if got := sessionCreatedSessionIDs(notifier); len(got) != 0 {
				t.Fatalf("session_created session ids = %q before the events were complete, want none", got)
			}

			markEventsComplete(t, svc, run)
			mustRunPasses(t, svc, now.Add(completionMarkWait/2))

			got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
			if err != nil || got == nil || got.FinalizedAt == nil {
				t.Fatalf("run = %+v, %v, want it finished once the events are complete", got, err)
			}
			if kept := svc.runSessionID(t.Context(), run.ID) == sessionID; kept != tc.wantKept {
				t.Errorf("run keeps its session mapping = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}

func TestQuietRun_KeepsASessionThatAlreadyHoldsAChat(t *testing.T) {
	svc, _ := quietSvc(t, true)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)
	run := sentRun(t, svc, task.ID, now)
	sessionID := svc.runSessionID(t.Context(), run.ID)
	prompt := "earlier question"
	if err := svc.chatRepo.Create(t.Context(), &store.ChatTask{
		ID: "earlier-chat-task", SessionID: sessionID, UserID: "alice", UserMessage: &prompt,
		MessageBubbles: "[]", CreatedTime: now.UnixMilli(), Kind: store.ChatTaskKindNormal,
	}); err != nil {
		t.Fatalf("seed a chat in the session: %v", err)
	}
	answer := quietAnswer("Nothing new.")
	otherGWE(svc).handleAnswer(t.Context(), *run.A2ATaskID, answer, nil)
	storeFinalEvent(t, svc, run, sessionID, answer)
	markEventsComplete(t, svc, run)

	mustRunPasses(t, svc, now.Add(2*completionMarkWait))

	got, err := svc.repo.GetExecutionByID(t.Context(), run.ID)
	if err != nil || got == nil || got.FinalizedAt == nil {
		t.Fatalf("run = %+v, %v, want it finished", got, err)
	}
	if kept := svc.runSessionID(t.Context(), run.ID); kept != sessionID {
		t.Errorf("run's session mapping = %q, want %q: the hide skipped a session that holds a chat", kept, sessionID)
	}
	if got := sessionCreatedSessionIDs(notifier); !slices.Equal(got, []string{sessionID}) {
		t.Errorf("session_created session ids = %q, want the session that stayed %q", got, sessionID)
	}
}
