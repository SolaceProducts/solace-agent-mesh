// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeDrive struct {
	mu    sync.Mutex
	calls []string

	upload func(ctx context.Context, name, mimeType string, props map[string]string) (string, error)
	share  func(ctx context.Context, fileID, email string, expires time.Time) error
	remove func(ctx context.Context, fileID string) (bool, error)
}

func (f *fakeDrive) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeDrive) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeDrive) Upload(ctx context.Context, folderID, name, mimeType string, _ []byte, props map[string]string) (string, error) {
	f.record("upload:" + folderID)
	if f.upload != nil {
		return f.upload(ctx, name, mimeType, props)
	}
	return fakeFileID(name), nil
}

func fakeFileID(name string) string {
	return "fakefileid" + strings.NewReplacer(".", "", "\u202e", "").Replace(name)
}

func (f *fakeDrive) ShareWithUser(ctx context.Context, fileID, email string, expires time.Time) error {
	f.record("share")
	if f.share != nil {
		return f.share(ctx, fileID, email, expires)
	}
	return nil
}

func (f *fakeDrive) Delete(ctx context.Context, fileID string) (bool, error) {
	f.record("delete")
	if f.remove != nil {
		return f.remove(ctx, fileID)
	}
	return false, nil
}

type handOffRig struct {
	a     *Adapter
	chat  *fakeChat
	drive *fakeDrive
	logs  *logCapture
}

func newHandOffRig(t *testing.T) *handOffRig {
	t.Helper()
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.cfg.ArtifactDriveFolderID = "folder-0123456789"
	a.cfg.ArtifactDriveAccessDays = 14
	a.cfg.DriveMaxArtifactBytes = defaultMaxDriveArtifactBytes
	a.driveRemoveBackoff = time.Millisecond
	drive := &fakeDrive{}
	a.drive = drive
	a.driveSlots = make(chan struct{}, maxConcurrentDriveHandOffs)
	return &handOffRig{a: a, chat: chat, drive: drive, logs: recordLogs(a)}
}

func (r *handOffRig) work(names ...string) *driveHandOff {
	files := make([]entrypoint.SavedArtifact, len(names))
	for i, name := range names {
		files[i] = entrypoint.SavedArtifact{Filename: name, MIMEType: "text/csv"}
	}
	meta := dmMeta()
	return &driveHandOff{
		taskID: tid("task-hand-off"), space: meta.SpaceName, thread: meta.ThreadName,
		email: requesterEmail, files: files,
		readFile: func(context.Context, entrypoint.SavedArtifact) ([]byte, error) { return []byte("a,b\n1,2"), nil },
	}
}

func (r *handOffRig) posts() []fakeWrite {
	writes, _ := r.chat.snapshot()
	var posts []fakeWrite
	for _, w := range writes {
		if w.op == "create" {
			posts = append(posts, w)
		}
	}
	return posts
}

func TestHandOffPostsOneMessageInTheTasksThread(t *testing.T) {
	r := newHandOffRig(t)
	r.drive.share = func(_ context.Context, fileID, _ string, _ time.Time) error {
		if fileID == fakeFileID("b.csv") {
			return errors.New("refused")
		}
		return nil
	}
	r.a.handOffToDrive(r.work("a.csv", "b.csv"))

	posts := r.posts()
	if len(posts) != 1 {
		t.Fatalf("posts = %+v, want one message for both files", posts)
	}
	_, texts := r.chat.snapshot()
	link := driveFileLinkPrefix + fakeFileID("a.csv") + driveFileLinkSuffix
	if !strings.Contains(texts[0], "<"+link+"|a.csv>") || !strings.Contains(texts[0], "b.csv") || !strings.Contains(texts[0], "Open it in Solace Agent Mesh") {
		t.Errorf("message = %q, want a link for a.csv and the name-only notice for b.csv", texts[0])
	}
	if posts[0].thread != dmMeta().ThreadName || posts[0].space != dmMeta().SpaceName {
		t.Errorf("posted in %s %s, want the task's thread %s %s", posts[0].space, posts[0].thread, dmMeta().SpaceName, dmMeta().ThreadName)
	}
}

func TestHandOffSendsTheCleanedNameTypeAndExpiry(t *testing.T) {
	r := newHandOffRig(t)
	var gotName, gotType string
	var gotProps map[string]string
	var gotExpiry time.Time
	r.drive.upload = func(_ context.Context, name, mimeType string, props map[string]string) (string, error) {
		gotName, gotType, gotProps = name, mimeType, props
		return "fakefileid123456", nil
	}
	r.drive.share = func(_ context.Context, _, _ string, expires time.Time) error {
		gotExpiry = expires
		return nil
	}
	work := r.work("re" + string(rune(0x202e)) + "port\x00.csv")
	work.files[0].MIMEType = "application/vnd.google-apps.document"
	start := time.Now()
	r.a.handOffToDrive(work)

	if gotName != "report.csv" || gotType != defaultDriveMIMEType {
		t.Errorf("uploaded %q as %q, want the cleaned name as plain bytes", gotName, gotType)
	}
	if gotProps[AppPropertyTaskID] != work.taskID {
		t.Errorf("properties = %v, want the task id kept", gotProps)
	}
	if want := start.Add(14 * 24 * time.Hour); gotExpiry.Before(want) || gotExpiry.After(want.Add(time.Minute)) {
		t.Errorf("expiry = %v, want 14 days from %v", gotExpiry, start)
	}
	if r.drive.count("upload:folder-0123456789") != 1 {
		t.Errorf("calls = %v, want one upload into the configured folder", r.drive.calls)
	}
}

func TestHandOffSlotsBoundConcurrencyAndAreReleased(t *testing.T) {
	r := newHandOffRig(t)
	r.a.driveSlots = make(chan struct{}, 1)
	r.a.driveBudget = 5 * time.Second
	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	t.Cleanup(func() { closeOnce(release) })
	r.drive.upload = func(ctx context.Context, name, _ string, _ map[string]string) (string, error) {
		now := inFlight.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		defer inFlight.Add(-1)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return fakeFileID(name), nil
	}
	var done sync.WaitGroup
	for _, name := range []string{"a.csv", "b.csv", "c.csv"} {
		done.Go(func() { r.a.handOffToDrive(r.work(name)) })
	}
	waitUntil(t, "an upload to be in flight", func() bool { return inFlight.Load() == 1 })
	for range 10 {
		if got := inFlight.Load(); got != 1 {
			t.Fatalf("uploads in flight = %d, want one while the other tasks wait for the slot", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeOnce(release)
	done.Wait()
	if peak.Load() != 1 {
		t.Errorf("peak concurrency = %d, want 1", peak.Load())
	}
	if len(r.a.driveSlots) != 0 {
		t.Errorf("slots held = %d after every task finished, want none", len(r.a.driveSlots))
	}
	if got := len(r.posts()); got != 3 {
		t.Errorf("posts = %d, want a message for each of the three tasks", got)
	}
}

func TestHandOffGivesUpWaitingForASlot(t *testing.T) {
	r := newHandOffRig(t)
	r.a.driveBudget = 50 * time.Millisecond
	r.a.driveSlots = make(chan struct{}, 1)
	r.a.driveSlots <- struct{}{}
	r.a.handOffToDrive(r.work("a.csv"))

	if r.drive.count("upload:folder-0123456789") != 0 {
		t.Error("a file was uploaded without a slot")
	}
	_, texts := r.chat.snapshot()
	if len(texts) != 1 || !strings.Contains(texts[0], "a.csv") || !strings.Contains(texts[0], "Open it in Solace Agent Mesh") {
		t.Errorf("messages = %q, want the name-only notice", texts)
	}
	if r.logs.count(slog.LevelWarn, "google chat drive hand-off gave up waiting for its turn") != 1 {
		t.Error("the wait for a slot ending was not logged")
	}
	if len(r.a.driveSlots) != 1 {
		t.Error("the slot another task holds was taken")
	}
}

func TestHandOffBudgetRunsOutPartWay(t *testing.T) {
	r := newHandOffRig(t)
	r.a.driveBudget = 100 * time.Millisecond
	r.drive.upload = func(ctx context.Context, _, _ string, _ map[string]string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	r.a.handOffToDrive(r.work("a.csv", "b.csv", "c.csv"))

	posts := r.posts()
	_, texts := r.chat.snapshot()
	if len(posts) != 1 || !strings.Contains(texts[0], "a.csv") || !strings.Contains(texts[0], "b.csv") || !strings.Contains(texts[0], "c.csv") {
		t.Errorf("messages = %q, want one notice naming all three files", texts)
	}
	if r.drive.count("upload:folder-0123456789") != 1 {
		t.Errorf("uploads = %d, want only the one that ran out of time", r.drive.count("upload:folder-0123456789"))
	}
	if r.logs.count(slog.LevelWarn, "google chat drive hand-off ran out of time") != 1 {
		t.Error("the budget running out was not logged")
	}
}

func TestHandOffContainsAPanic(t *testing.T) {
	r := newHandOffRig(t)
	r.drive.upload = func(context.Context, string, string, map[string]string) (string, error) {
		panic("boom")
	}
	r.a.handOffToDrive(r.work("a.csv"))

	_, texts := r.chat.snapshot()
	if len(texts) != 1 || !strings.Contains(texts[0], "a.csv") || !strings.Contains(texts[0], "Open it in Solace Agent Mesh") {
		t.Errorf("messages = %q, want the name-only notice", texts)
	}
	if r.logs.count(slog.LevelError, "google chat drive hand-off panicked") != 1 {
		t.Error("the panic was not logged")
	}
}

func apiFailure(code int, reasons ...string) error {
	e := &googleapi.Error{Code: code}
	for _, reason := range reasons {
		e.Errors = append(e.Errors, googleapi.ErrorItem{Reason: reason})
	}
	return e
}

func TestRemovalOfAFileThatCouldNotBeShared(t *testing.T) {
	for name, tc := range map[string]struct {
		results     []error
		trashed     bool
		wantDeletes int
		wantWarn    int
		wantError   int
		wantRemoved bool
	}{
		"removed first time":      {results: []error{nil}, wantDeletes: 1, wantRemoved: true},
		"retried once":            {results: []error{apiFailure(http.StatusInternalServerError), nil}, wantDeletes: 2, wantRemoved: true},
		"already gone":            {results: []error{apiFailure(http.StatusNotFound)}, wantDeletes: 1, wantRemoved: true},
		"gone before the trash":   {results: []error{&trashFailedError{deleteErr: apiFailure(http.StatusForbidden), trashErr: apiFailure(http.StatusNotFound)}}, wantDeletes: 1, wantRemoved: true},
		"trashed instead":         {results: []error{nil}, trashed: true, wantDeletes: 1, wantWarn: 1, wantRemoved: true},
		"fails both times":        {results: []error{apiFailure(http.StatusInternalServerError), apiFailure(http.StatusInternalServerError)}, wantDeletes: 2, wantError: 1},
		"rate limited both times": {results: []error{apiFailure(http.StatusForbidden, "rateLimitExceeded"), apiFailure(http.StatusForbidden, "rateLimitExceeded")}, wantDeletes: 2, wantError: 1},
	} {
		t.Run(name, func(t *testing.T) {
			r := newHandOffRig(t)
			var n int
			r.drive.remove = func(context.Context, string) (bool, error) {
				err := tc.results[min(n, len(tc.results)-1)]
				n++
				return tc.trashed && err == nil, err
			}
			logs := jsonLogsOf(r.a)
			if removed := r.a.removeFromDrive(tid("task-remove"), "fakefileid123456"); removed != tc.wantRemoved {
				t.Errorf("removeFromDrive = %v, want %v", removed, tc.wantRemoved)
			}
			if tc.wantError == 1 && !strings.Contains(logs.String(), `"attempts":2`) {
				t.Errorf("log = %s, want the number of attempts on the left-in-drive line", logs.String())
			}
			if tc.wantRemoved && tc.results[0] != nil && strings.Contains(logs.String(), "left in drive") {
				t.Errorf("log = %s, want a file already gone not logged as left", logs.String())
			}

			if got := r.drive.count("delete"); got != tc.wantDeletes {
				t.Errorf("delete attempts = %d, want %d", got, tc.wantDeletes)
			}
			if got := strings.Count(logs.String(), "google chat drive file moved to the trash"); got != tc.wantWarn {
				t.Errorf("trash warnings = %d, want %d", got, tc.wantWarn)
			}
			if got := strings.Count(logs.String(), `"msg":"google chat artifact left in drive: it could not be shared or removed"`); got != tc.wantError {
				t.Errorf("left-in-drive errors = %d, want %d", got, tc.wantError)
			}
		})
	}
}

func TestRefusedForPermission(t *testing.T) {
	for name, tc := range map[string]struct {
		err  *googleapi.Error
		want bool
	}{
		"forbidden without a reason":   {&googleapi.Error{Code: http.StatusForbidden}, true},
		"insufficient permissions":     {&googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: "insufficientFilePermissions"}}}, true},
		"rate limit":                   {&googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}}}, false},
		"user rate limit":              {&googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: "userRateLimitExceeded"}}}, false},
		"daily limit":                  {&googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: "dailyLimitExceeded"}}}, false},
		"not found":                    {&googleapi.Error{Code: http.StatusNotFound}, false},
		"server error":                 {&googleapi.Error{Code: http.StatusInternalServerError}, false},
		"rate limit among other items": {&googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: "other"}, {Reason: "sharingRateLimitExceeded"}}}, false},
	} {
		if got := refusedForPermission(tc.err); got != tc.want {
			t.Errorf("%s: refusedForPermission = %v, want %v", name, got, tc.want)
		}
	}
}

func TestUploadOutcomeUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"timed out":       {&driveStageError{stage: "upload", err: context.DeadlineExceeded}, true},
		"server error":    {&driveStageError{stage: "upload", err: apiFailure(http.StatusServiceUnavailable)}, true},
		"request timeout": {&driveStageError{stage: "upload", err: apiFailure(http.StatusRequestTimeout)}, true},
		"refused":         {&driveStageError{stage: "upload", err: apiFailure(http.StatusForbidden)}, false},
		"read failure":    {&driveStageError{stage: "read", err: context.DeadlineExceeded}, false},
		"not staged":      {errors.New("x"), false},
	} {
		if got := uploadOutcomeUnknown(tc.err); got != tc.want {
			t.Errorf("%s: uploadOutcomeUnknown = %v, want %v", name, got, tc.want)
		}
	}
}

func TestHandOffCountsAnUnusableFileIDAsLeftInDrive(t *testing.T) {
	r := newHandOffRig(t)
	r.drive.upload = func(context.Context, string, string, map[string]string) (string, error) {
		return "../not/a/file?id", nil
	}
	outcome := &driveOutcome{}
	r.a.putFilesInDrive(r.work("a.csv"), outcome)

	if !outcome.filesMayRemain {
		t.Error("outcome = no file may remain, want the file stored under the unusable id counted")
	}
	if len(outcome.unshared) != 1 || len(outcome.shared) != 0 {
		t.Errorf("outcome = %+v, want the file named as not shared", outcome)
	}
}

func TestFinalAnswerPutsMarkdownInDriveAndPostsItWhenItDoesNotGoThere(t *testing.T) {
	const (
		answer    = "Here are your notes."
		notesPost = "📄 *notes.md*\n\nShip on Friday."
		notesLink = "📎 <https://drive.google.com/file/d/fakefileidnotesmd/view|notes.md> is shared with you in Google Drive."
	)
	type savedText struct{ name, mimeType, content string }
	notes := savedText{"notes.md", "text/markdown", "Ship on Friday."}
	fiveCSVs := []savedText{
		{"f1.csv", "text/csv", "a,b"}, {"f2.csv", "text/csv", "a,b"}, {"f3.csv", "text/csv", "a,b"},
		{"f4.csv", "text/csv", "a,b"}, {"f5.csv", "text/csv", "a,b"},
	}
	for name, tc := range map[string]struct {
		meta        taskMeta
		drive       bool
		saves       []savedText
		wantTexts   []string
		wantUploads []string
	}{
		"drive set up": {
			meta: dmMeta(), drive: true, saves: []savedText{notes},
			wantTexts:   []string{answer, notesLink},
			wantUploads: []string{"notes.md text/markdown"},
		},
		"no drive": {
			meta: dmMeta(), saves: []savedText{notes},
			wantTexts: []string{answer, notesPost},
		},
		"the task ran as a system user": {
			meta: sharedMeta(), drive: true, saves: []savedText{notes},
			wantTexts: []string{answer, notesPost},
		},
		"past the files one task may put in drive": {
			meta: dmMeta(), drive: true, saves: append(slices.Clone(fiveCSVs), notes),
			wantTexts: []string{answer, notesPost,
				"📎 <https://drive.google.com/file/d/fakefileidf1csv/view|f1.csv> is shared with you in Google Drive.\n" +
					"📎 <https://drive.google.com/file/d/fakefileidf2csv/view|f2.csv> is shared with you in Google Drive.\n" +
					"📎 <https://drive.google.com/file/d/fakefileidf3csv/view|f3.csv> is shared with you in Google Drive.\n" +
					"📎 <https://drive.google.com/file/d/fakefileidf4csv/view|f4.csv> is shared with you in Google Drive.\n" +
					"📎 <https://drive.google.com/file/d/fakefileidf5csv/view|f5.csv> is shared with you in Google Drive."},
			wantUploads: []string{"f1.csv text/csv", "f2.csv text/csv", "f3.csv text/csv", "f4.csv text/csv", "f5.csv text/csv"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newHandOffRig(t)
			taskID := tid("task-markdown")
			submitForTest(t, r.a, taskID, tc.meta)
			r.chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
			var mu sync.Mutex
			var uploads []string
			r.drive.upload = func(_ context.Context, name, mimeType string, _ map[string]string) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				uploads = append(uploads, name+" "+mimeType)
				return fakeFileID(name), nil
			}
			if !tc.drive {
				r.a.drive = nil
			}
			mem := memory.New()
			r.a.core = entrypoint.NewGatewayCore(r.a, nil)
			r.a.core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithArtifacts(
				&artifact.ScopedService{Inner: mem, Scope: "namespace", Namespace: "test-ns"})).Services())
			artifacts := r.a.core.Services().Artifacts()
			parts := make([]a2a.Part, 0, len(tc.saves))
			for _, f := range tc.saves {
				version, err := mem.Save(t.Context(), artifacts.Resolve(tc.meta.AgentName, requesterEmail, "", f.name), []byte(f.content), f.mimeType)
				if err != nil {
					t.Fatal(err)
				}
				parts = append(parts, a2a.NewDataPart(map[string]any{"type": string(a2a.SignalArtifactSaved), "filename": f.name, "mime_type": f.mimeType, "version": version}))
			}

			r.a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: parts}}}, nil)
			r.a.OnFinalResponse(t.Context(), taskID, completedTask(answer), nil)
			r.a.awaitLingeringReplies(t.Context())

			if _, texts := r.chat.snapshot(); !slices.Equal(texts, tc.wantTexts) {
				t.Errorf("messages = %q, want %q", texts, tc.wantTexts)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(uploads, tc.wantUploads) {
				t.Errorf("uploads = %q, want %q", uploads, tc.wantUploads)
			}
		})
	}
}
