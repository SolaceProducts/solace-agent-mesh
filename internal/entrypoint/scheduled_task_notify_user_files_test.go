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
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	notifyOwner        = "alice@example.com"
	notifySession      = "sess-exec"
	notifySessionFile  = "chart.png"
	notifyProducedName = "user:report.md"
	notifyOtherName    = "user:private.md"
	notifyAnotherChat  = "an-earlier-chat-of-the-owner"
)

type failingVersionLister struct {
	artifact.Service
	fail              atomic.Bool
	userScopedLookups atomic.Int32
}

func (f *failingVersionLister) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	if strings.HasPrefix(loc.Filename, "user:") {
		f.userScopedLookups.Add(1)
	}
	if f.fail.Load() {
		return nil, errors.New("artifact store unavailable")
	}
	return f.Service.ListVersions(ctx, loc)
}

type notifyUserFilesFixture struct {
	t        *testing.T
	svc      *ScheduledTaskService
	exec     *store.ScheduledTaskExecution
	task     *store.ScheduledTask
	logs     *bytes.Buffer
	versions *failingVersionLister
}

func newNotifyUserFilesFixture(t *testing.T, bubbles string) *notifyUserFilesFixture {
	t.Helper()
	versions := &failingVersionLister{Service: filesystem.New(t.TempDir())}
	svc := newSchedulerSvcWithArtifactStore(t, 0, versions)
	logs := &bytes.Buffer{}
	svc.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := t.Context()

	owner := notifyOwner
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "report-task", Namespace: "test-ns",
		UserID: &owner, CreatedBy: owner,
		ScheduleType: "interval", ScheduleExpression: "300s", Timezone: "UTC",
		TargetAgentName: "ReportAgent", TargetType: "agent",
		TaskMessage: `[{"kind":"text","text":"go"}]`, Source: "ui",
	}
	if err := svc.repo.Create(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	exec := &store.ScheduledTaskExecution{ID: idgen.New(), ScheduledTaskID: task.ID, Status: "completed", ScheduledFor: 1}
	if err := svc.repo.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	if err := svc.sessKeyRepo.CreateMapping(ctx, store.GatewayTypeScheduled, exec.ID, notifySession); err != nil {
		t.Fatalf("map session: %v", err)
	}
	if err := svc.sessRepo.Create(ctx, &store.Session{ID: notifySession, UserID: owner}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	f := &notifyUserFilesFixture{t: t, svc: svc, exec: exec, task: task, logs: logs, versions: versions}
	f.addChatTask(owner, bubbles)
	f.save(notifySession, notifySessionFile)
	f.save(notifySession, notifyProducedName)
	f.save(notifyAnotherChat, notifyOtherName)
	return f
}

func (f *notifyUserFilesFixture) save(session, filename string) {
	f.t.Helper()
	art := f.svc.core.Services().Artifacts()
	if _, err := art.Inner.Save(f.t.Context(), art.Resolve("", notifyOwner, session, filename), []byte("x"), "text/plain"); err != nil {
		f.t.Fatalf("save %s: %v", filename, err)
	}
}

func (f *notifyUserFilesFixture) addChatTask(user, bubbles string) {
	f.t.Helper()
	if err := f.svc.chatRepo.Create(f.t.Context(), &store.ChatTask{
		ID: idgen.New(), SessionID: notifySession, UserID: user, MessageBubbles: bubbles,
	}); err != nil {
		f.t.Fatalf("seed chat task: %v", err)
	}
}

func (f *notifyUserFilesFixture) notified() []a2a.NotifyArtifact {
	f.t.Helper()
	arts := f.svc.buildNotifyArtifacts(f.t.Context(), f.exec, f.task)
	slices.SortFunc(arts, func(a, b a2a.NotifyArtifact) int { return strings.Compare(a.Filename, b.Filename) })
	return arts
}

func (f *notifyUserFilesFixture) delivered() []string {
	f.t.Helper()
	var names []string
	for _, art := range f.notified() {
		names = append(names, art.Filename)
	}
	return names
}

func (f *notifyUserFilesFixture) logged(level string) []map[string]any {
	var out []map[string]any
	for line := range bytes.SplitSeq(f.logs.Bytes(), []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(line, &rec) == nil && rec["level"] == level {
			out = append(out, rec)
		}
	}
	return out
}

func (f *notifyUserFilesFixture) warnings() []map[string]any { return f.logged("WARN") }

func producedBubbles(names ...string) string {
	parts := make([]map[string]any, 0, len(names))
	for _, name := range names {
		uri := "artifact://" + notifySession + "/" + name + "?version=0"
		parts = append(parts, map[string]any{"kind": "artifact", "status": "completed", "name": name, "file": map[string]any{"name": name, "uri": uri}})
	}
	raw, err := json.Marshal([]map[string]any{{"id": "m", "type": "agent", "text": "done", "parts": parts}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestBuildNotifyArtifactsLeavesOutTheOwnersOtherUserScopedFiles(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))

	want := []string{notifySessionFile, notifyProducedName}
	if got := f.delivered(); !slices.Equal(got, want) {
		t.Errorf("delivered %v, want the session's file and the user-scoped file it produced %v", got, want)
	}
	if got := f.warnings(); len(got) != 0 {
		t.Errorf("warnings = %v, want none when the execution's messages are readable", got)
	}
}

func TestBuildNotifyArtifactsPinsTheVersionOfAUserScopedFile(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))

	for _, art := range f.notified() {
		switch {
		case art.Filename == notifyProducedName && (art.Version == nil || *art.Version != 0):
			t.Errorf("user-scoped file carries version %v, want 0", art.Version)
		case art.Filename == notifySessionFile && art.Version != nil:
			t.Errorf("session file carries version %v, want none (latest)", *art.Version)
		}
	}
}

func TestBuildNotifyArtifactsLeavesOutAUserScopedFileWrittenAgainFromAnotherSession(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))
	f.save(notifyAnotherChat, notifyProducedName)

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file once another session wrote a later version", got)
	}
	if got := f.warnings(); len(got) != 1 || got[0]["count"] != float64(1) {
		t.Errorf("warnings = %v, want one counting the file a later version left out", got)
	}
	if text := f.logs.String(); strings.Contains(text, notifyProducedName) {
		t.Errorf("the log names a user-scoped file: %s", text)
	}
}

func TestBuildNotifyArtifactsDeliversAFileTheRunSavedTwice(t *testing.T) {
	for name, manifest := range map[string][]any{
		"the lower version first": {
			map[string]any{"filename": notifyProducedName, "version": 4},
			map[string]any{"filename": notifyProducedName, "version": 5},
		},
		"the higher version first": {
			map[string]any{"filename": notifyProducedName, "version": 5},
			map[string]any{"filename": notifyProducedName, "version": 4},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNotifyUserFilesFixture(t, `[]`)
			for range 5 {
				f.save(notifySession, notifyProducedName)
			}
			result := &a2a.Task{
				ID: idgen.New(), Kind: "task",
				Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: "saved twice"}}}},
				Metadata: map[string]any{"produced_artifacts": manifest},
			}
			f.svc.saveChatTask(t.Context(), f.task, notifySession, result.ID, result, "", nil)

			var version *int
			for _, art := range f.notified() {
				if art.Filename == notifyProducedName {
					version = art.Version
				}
			}
			if version == nil || *version != 5 {
				t.Errorf("delivered version %v, want 5", version)
			}
		})
	}
}

func TestBuildNotifyArtifactsDeliversAFileWhoseNameHoldsReservedCharacters(t *testing.T) {
	const awkward = "user:q 1#2%3.md"
	f := newNotifyUserFilesFixture(t, `[]`)
	f.save(notifySession, awkward)
	result := &a2a.Task{
		ID: idgen.New(), Kind: "task",
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: "saved"}}}},
		Metadata: map[string]any{"produced_artifacts": []any{map[string]any{"filename": awkward, "version": 0}}},
	}
	f.svc.saveChatTask(t.Context(), f.task, notifySession, result.ID, result, "", nil)

	if got := f.delivered(); !slices.Contains(got, awkward) {
		t.Errorf("delivered %v, want %q", got, awkward)
	}
}

func TestBuildNotifyArtifactsLooksUpVersionsOnlyForTheFilesTheExecutionNames(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile))
	f.delivered()
	if got := f.versions.userScopedLookups.Load(); got != 0 {
		t.Errorf("%d version lookups for user-scoped files the execution names none of, want 0", got)
	}

	named := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))
	named.delivered()
	if got := named.versions.userScopedLookups.Load(); got != 1 {
		t.Errorf("%d version lookups, want 1 for the one user-scoped file the execution names", got)
	}
}

func TestBuildNotifyArtifactsLeavesOutAUserScopedFileThatNamesNoVersion(t *testing.T) {
	f := newNotifyUserFilesFixture(t, `[{"id":"m","type":"agent","text":"done«artifact_return:`+notifyProducedName+`»"}]`)

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file", got)
	}
}

func TestBuildNotifyArtifactsDeliversAUserScopedFileTheSchedulersOwnChatTaskReferences(t *testing.T) {
	f := newNotifyUserFilesFixture(t, `[]`)
	result := &a2a.Task{
		ID: idgen.New(), Kind: "task",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: "report saved"}},
		}},
		Metadata: map[string]any{"produced_artifacts": []any{map[string]any{"filename": notifyProducedName, "version": 0}}},
	}
	f.svc.saveChatTask(t.Context(), f.task, notifySession, result.ID, result, "", nil)

	want := []string{notifySessionFile, notifyProducedName}
	if got := f.delivered(); !slices.Equal(got, want) {
		t.Errorf("delivered %v, want %v", got, want)
	}
}

func TestBuildNotifyArtifactsDeliversNoUserScopedFileWhenTheExecutionReferencesNone(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile))

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file", got)
	}
}

func TestBuildNotifyArtifactsDoesNotLetAnotherUsersMessageVouchForAUserScopedFile(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile))
	f.addChatTask("bob@example.com", producedBubbles(notifyOtherName))

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file", got)
	}
}

func TestBuildNotifyArtifactsKeepsWhatReadableMessagesReferenceWhenOneTaskIsUnreadable(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))
	f.addChatTask(notifyOwner, `[{"id": "not json`)

	want := []string{notifySessionFile, notifyProducedName}
	if got := f.delivered(); !slices.Equal(got, want) {
		t.Errorf("delivered %v, want %v", got, want)
	}
	warnings := f.warnings()
	if len(warnings) != 1 || warnings[0]["execID"] != f.exec.ID {
		t.Fatalf("warnings = %v, want one naming the execution", warnings)
	}
	if text := f.logs.String(); strings.Contains(text, notifyProducedName) || strings.Contains(text, notifyOtherName) {
		t.Errorf("the log names a user-scoped file: %s", text)
	}
}

func TestBuildNotifyArtifactsDeliversNoUserScopedFileWhenTheChatTasksCannotBeRead(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))
	if _, err := f.svc.core.Store().ExecContext(t.Context(), `DROP TABLE chat_tasks`); err != nil {
		t.Fatalf("break the chat store: %v", err)
	}

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file", got)
	}
	if got := f.warnings(); len(got) != 1 || got[0]["execID"] != f.exec.ID {
		t.Errorf("warnings = %v, want one naming the execution", got)
	}
}

func TestBuildNotifyArtifactsDeliversNoUserScopedFileWhenItsVersionsCannotBeRead(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))
	f.versions.fail.Store(true)

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file while versions cannot be read", got)
	}
	if got := f.warnings(); len(got) != 1 || got[0]["execID"] != f.exec.ID {
		t.Errorf("warnings = %v, want one naming the execution", got)
	}
}

func TestBuildNotifyArtifactsDeliversNoUserScopedFileWithoutAChatStore(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile, notifyProducedName))
	f.svc.chatRepo = nil

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want only the session's own file", got)
	}
	if got := f.warnings(); len(got) != 1 {
		t.Errorf("warnings = %v, want one", got)
	}
}

func TestBuildNotifyArtifactsDoesNotNeedTheChatStoreForASessionWithoutUserScopedFiles(t *testing.T) {
	f := newNotifyUserFilesFixture(t, producedBubbles(notifySessionFile))
	art := f.svc.core.Services().Artifacts()
	for _, name := range []string{notifyProducedName, notifyOtherName} {
		session := notifySession
		if name == notifyOtherName {
			session = notifyAnotherChat
		}
		if err := art.Inner.Delete(t.Context(), art.Resolve("", notifyOwner, session, name)); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
	}
	f.svc.chatRepo = nil

	if got := f.delivered(); !slices.Equal(got, []string{notifySessionFile}) {
		t.Errorf("delivered %v, want the session's own file", got)
	}
	if got := f.warnings(); len(got) != 0 {
		t.Errorf("warnings = %v, want none", got)
	}
}
