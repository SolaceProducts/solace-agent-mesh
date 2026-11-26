// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type logLine map[string]any

func logLines(t *testing.T, buf *bytes.Buffer) []logLine {
	t.Helper()
	var lines []logLine
	for raw := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func messages(lines []logLine, level string) []string {
	var out []string
	for _, l := range lines {
		if l["level"] == level {
			out = append(out, l["msg"].(string))
		}
	}
	return out
}

func ids(line logLine, field string) []string {
	values, _ := line[field].([]any)
	out := make([]string, len(values))
	for i, v := range values {
		out[i], _ = v.(string)
	}
	return out
}

func lineWith(t *testing.T, lines []logLine, msg string) logLine {
	t.Helper()
	for _, l := range lines {
		if l["msg"] == msg {
			return l
		}
	}
	t.Fatalf("no log line %q in %v", msg, lines)
	return nil
}

func TestHandOffStartedAfterStopGaveUpDoesNothing(t *testing.T) {
	r := newHandOffRig(t)
	r.a.abandonCancel()
	for range 300 {
		r.a.handOffToDrive(r.work("a.csv"))
	}
	if n := r.uploads(); n != 0 {
		t.Errorf("uploads = %d after Stop gave up, want none", n)
	}
	if n := len(r.posts()); n != 0 {
		t.Errorf("posts = %d after Stop gave up, want none", n)
	}
	for range 2000 {
		ctx, release := r.a.abandonableContext(time.Minute)
		if ctx.Err() == nil {
			release()
			t.Fatal("a context made after Stop gave up was not already done")
		}
		release()
	}
}

func TestHandOffContainsAPanicWhilePosting(t *testing.T) {
	r := newHandOffRig(t)
	logs := jsonLogsOf(r.a)
	r.chat.onWrite = func(context.Context) error { panic("boom in post") }
	r.a.handOffToDrive(r.work("a.csv"))

	lines := logLines(t, logs)
	line := lineWith(t, lines, "google chat drive hand-off panicked")
	if line["callback"] != "drive_hand_off_post" || line["panicType"] != "string" || line["stack"] == "" {
		t.Errorf("line = %v, want the post stage, the panic's type and a stack", line)
	}
	if got := ids(line, "sharedFileIDs"); !slices.Equal(got, []string{fakeFileID("a.csv")}) {
		t.Errorf("sharedFileIDs = %v, want the file already shared", got)
	}
	if strings.Contains(logs.String(), "boom in post") {
		t.Error("the panic's value reached the logs")
	}
}

func TestStopAbandonsAHandOffAtEachStageAtItsDeadline(t *testing.T) {
	const (
		logUnsent  = "google chat reply lost: the result of a drive hand-off could not be posted"
		twoFiles   = 2
		threeFiles = 3
	)
	type stage struct {
		setup          func(t *testing.T, r *handOffRig) (start func(), ready func() bool)
		wantErrors     []string
		wantDriveWarns []string
		check          func(t *testing.T, r *handOffRig, lines []logLine)
	}
	hand := func(r *handOffRig, names ...string) (func(), func() bool) {
		return func() { handOffInBackground(r.a, r.work(names...)) }, func() bool { return true }
	}
	for name, tc := range map[string]stage{
		"waiting for a slot": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				r.a.driveSlots = make(chan struct{}, 1)
				r.a.driveSlots <- struct{}{}
				return hand(r, "a.csv")
			},
			wantErrors: []string{logFilesNotHandedOff},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				if r.uploads() != 0 {
					t.Error("a file was uploaded without a slot")
				}
				if line := lineWith(t, lines, logFilesNotHandedOff); line["filesMayRemain"] != false {
					t.Errorf("line = %v, want filesMayRemain false: nothing was uploaded", line)
				}
			},
		},
		"uploading": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				r.drive.upload = func(ctx context.Context, _, _ string, _ map[string]string) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				}
				start, _ := hand(r, "a.csv")
				return start, func() bool { return r.uploads() == 1 }
			},
			wantErrors: []string{logFilesNotHandedOff},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				line := lineWith(t, lines, logFilesNotHandedOff)
				if line["filesMayRemain"] != true {
					t.Errorf("line = %v, want filesMayRemain: an upload cut off may have been stored", line)
				}
			},
		},
		"sharing": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				r.drive.share = func(ctx context.Context, _, _ string, _ time.Time) error {
					<-ctx.Done()
					return ctx.Err()
				}
				start, _ := hand(r, "a.csv")
				return start, func() bool { return r.drive.count("share") == 1 }
			},
			wantErrors: []string{logFilesNotHandedOff},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				line := lineWith(t, lines, logFilesNotHandedOff)
				if got := ids(line, "leftFileIDs"); !slices.Equal(got, []string{fakeFileID("a.csv")}) {
					t.Errorf("leftFileIDs = %v, want the file whose share was cut off", got)
				}
				if r.drive.count("delete") != 0 {
					t.Error("a delete was sent after Stop gave up")
				}
			},
		},
		"reading a file": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				work := r.work("a.csv")
				work.readFile = func(context.Context, entrypoint.SavedArtifact) ([]byte, error) {
					<-r.a.abandonCtx.Done()
					return []byte("a,b\n1,2"), nil
				}
				return func() { handOffInBackground(r.a, work) }, func() bool { return true }
			},
			wantErrors: []string{logFilesNotHandedOff},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				if r.uploads() != 0 {
					t.Error("a file was uploaded after Stop gave up")
				}
				if line := lineWith(t, lines, logFilesNotHandedOff); line["filesMayRemain"] != false {
					t.Errorf("line = %v, want filesMayRemain false: nothing reached Drive", line)
				}
			},
		},
		"pausing before a second removal attempt": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				r.a.driveRemoveBackoff = time.Minute
				r.drive.share = func(context.Context, string, string, time.Time) error { return errors.New("refused") }
				r.drive.remove = func(context.Context, string) (bool, error) { return false, apiFailure(http.StatusInternalServerError) }
				start, _ := hand(r, "a.csv")
				return start, func() bool { return r.drive.count("delete") == 1 }
			},
			wantErrors:     []string{logFilesNotHandedOff},
			wantDriveWarns: []string{"google chat artifact could not be shared in drive"},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				line := lineWith(t, lines, logFilesNotHandedOff)
				if got := ids(line, "leftFileIDs"); !slices.Equal(got, []string{fakeFileID("a.csv")}) {
					t.Errorf("leftFileIDs = %v, want the file that could not be removed", got)
				}
				if r.drive.count("delete") != 1 {
					t.Errorf("deletes = %d, want only the attempt made before Stop gave up", r.drive.count("delete"))
				}
			},
		},
		"posting the result": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				holdWrites(r.chat)
				start, _ := hand(r, "a.csv")
				return start, func() bool { return r.drive.count("share") == 1 }
			},
			wantErrors: []string{logUnsent},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				line := lineWith(t, lines, logUnsent)
				if got := ids(line, "sharedFileIDs"); !slices.Equal(got, []string{fakeFileID("a.csv")}) {
					t.Errorf("sharedFileIDs = %v, want the file the person was not told about", got)
				}
			},
		},
		"the second of three files' upload": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				r.drive.upload = func(ctx context.Context, name, _ string, _ map[string]string) (string, error) {
					if name == "b.csv" {
						<-ctx.Done()
						return "", ctx.Err()
					}
					return fakeFileID(name), nil
				}
				start, _ := hand(r, "a.csv", "b.csv", "c.csv")
				return start, func() bool { return r.uploads() == twoFiles }
			},
			wantErrors: []string{logFilesNotHandedOff},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				line := lineWith(t, lines, logFilesNotHandedOff)
				if got := ids(line, "sharedFileIDs"); !slices.Equal(got, []string{fakeFileID("a.csv")}) {
					t.Errorf("sharedFileIDs = %v, want the first file, shared before Stop gave up", got)
				}
				if line["files"] != float64(threeFiles) {
					t.Errorf("files = %v, want %d", line["files"], threeFiles)
				}
				if r.uploads() != twoFiles {
					t.Errorf("uploads = %d, want none after Stop gave up on the file in flight", r.uploads())
				}
			},
		},
		"a lingering answer": {
			setup: func(t *testing.T, r *handOffRig) (func(), func() bool) {
				taskID := tid("task-lingering")
				st := submitForTest(t, r.a, taskID, dmMeta())
				r.chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
				r.a.lingerTimeout = time.Minute
				holdWrites(r.chat)
				q := r.a.queueFor(taskID, st)
				q.Replace("The final answer")
				return func() {
					r.a.drainTerminal(taskID, st, q, r.work("a.csv"), nil)
					r.a.finishTask(taskID)
				}, func() bool { return true }
			},
			wantErrors: []string{logReplyLostStopped},
			check: func(t *testing.T, r *handOffRig, lines []logLine) {
				if r.uploads() != 0 {
					t.Error("a file was uploaded after Stop gave up on the answer it was to follow")
				}
				if line := lineWith(t, lines, logReplyLostStopped); line["files"] != float64(1) {
					t.Errorf("line = %v, want the one line to carry the number of files", line)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newHandOffRig(t)
			logs := jsonLogsOf(r.a)
			start, ready := tc.setup(t, r)
			postsBefore := len(r.posts())
			start()
			waitUntil(t, "the hand-off to reach its stage", ready)
			time.Sleep(50 * time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			stopped := make(chan struct{})
			go func() {
				_ = r.a.Stop(ctx)
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("Stop is still waiting on the hand-off long after its 200ms deadline")
			}

			lines := logLines(t, logs)
			if got := messages(lines, "ERROR"); !slices.Equal(got, tc.wantErrors) {
				t.Errorf("ERROR lines = %q, want %q", got, tc.wantErrors)
			}
			var driveWarns []string
			for _, warn := range messages(lines, "WARN") {
				if strings.Contains(warn, "drive") {
					driveWarns = append(driveWarns, warn)
				}
			}
			if !slices.Equal(driveWarns, tc.wantDriveWarns) {
				t.Errorf("Drive WARN lines = %q, want %q", driveWarns, tc.wantDriveWarns)
			}
			if n := len(r.posts()) - postsBefore; n != 0 {
				t.Errorf("%d message(s) posted after Stop gave up, want none", n)
			}
			tc.check(t, r, lines)
		})
	}
}

func restDrive(t *testing.T, handler http.HandlerFunc) *restDriveAPI {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc, err := drive.NewService(t.Context(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return &restDriveAPI{svc: svc}
}

func driveErrorBody(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": code, "message": "nope", "errors": []map[string]string{{"reason": reason, "message": "nope"}},
	}})
	_, _ = w.Write(body)
}

func TestRestDriveDeleteFallsBackToTheTrashOnlyForPermission(t *testing.T) {
	for name, tc := range map[string]struct {
		deleteCode   int
		deleteReason string
		trashCode    int
		wantTrashed  bool
		wantErr      bool
		wantCalls    []string
	}{
		"deleted":                           {deleteCode: http.StatusNoContent, wantCalls: []string{"DELETE"}},
		"refused for permission, trashed":   {deleteCode: http.StatusForbidden, deleteReason: "insufficientFilePermissions", trashCode: http.StatusOK, wantTrashed: true, wantCalls: []string{"DELETE", "PATCH"}},
		"refused for permission, trash too": {deleteCode: http.StatusForbidden, deleteReason: "insufficientFilePermissions", trashCode: http.StatusInternalServerError, wantErr: true, wantCalls: []string{"DELETE", "PATCH"}},
		"rate limited":                      {deleteCode: http.StatusForbidden, deleteReason: "rateLimitExceeded", wantErr: true, wantCalls: []string{"DELETE"}},
		"server error":                      {deleteCode: http.StatusInternalServerError, wantErr: true, wantCalls: []string{"DELETE"}},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Value
			calls.Store([]string{})
			record := func(m string) { calls.Store(append(calls.Load().([]string), m)) }
			api := restDrive(t, func(w http.ResponseWriter, r *http.Request) {
				record(r.Method)
				switch r.Method {
				case http.MethodDelete:
					if tc.deleteCode == http.StatusNoContent {
						w.WriteHeader(tc.deleteCode)
						return
					}
					driveErrorBody(w, tc.deleteCode, tc.deleteReason)
				case http.MethodPatch:
					var body struct {
						Trashed bool `json:"trashed"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if !body.Trashed {
						t.Error("the update did not ask for the trash")
					}
					if tc.trashCode != http.StatusOK {
						driveErrorBody(w, tc.trashCode, "backendError")
						return
					}
					_, _ = w.Write([]byte(`{"id":"x"}`))
				}
			})
			trashed, err := api.Delete(t.Context(), "someid1234567")
			if trashed != tc.wantTrashed || (err != nil) != tc.wantErr {
				t.Errorf("Delete = (%v, %v), want trashed %v and an error: %v", trashed, err, tc.wantTrashed, tc.wantErr)
			}
			if got := calls.Load().([]string); !slices.Equal(got, tc.wantCalls) {
				t.Errorf("requests = %v, want %v", got, tc.wantCalls)
			}
			if tc.wantErr && tc.trashCode != 0 {
				fields := driveErrorFields(&driveStageError{stage: "remove", err: err})
				if !slices.Contains(fields, "insufficientFilePermissions") || !slices.Contains(fields, "trashHTTPStatus") || !slices.Contains(fields, any(http.StatusInternalServerError)) {
					t.Errorf("fields = %v, want the delete's reason and the trash's status", fields)
				}
			}
		})
	}
}

func TestDriveFileGone(t *testing.T) {
	notFound := apiFailure(http.StatusNotFound)
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"no error":                 {nil, false},
		"delete not found":         {notFound, true},
		"trash not found":          {&trashFailedError{deleteErr: apiFailure(http.StatusForbidden), trashErr: notFound}, true},
		"trash failed another way": {&trashFailedError{deleteErr: apiFailure(http.StatusForbidden), trashErr: apiFailure(http.StatusInternalServerError)}, false},
		"forbidden":                {apiFailure(http.StatusForbidden), false},
	} {
		if got := driveFileGone(tc.err); got != tc.want {
			t.Errorf("%s: driveFileGone = %v, want %v", name, got, tc.want)
		}
	}
}

func TestParseConfig_DriveRemoveBackoff(t *testing.T) {
	loopback := map[string]any{
		"test_skip_google_auth": true, "api_base_url": "http://127.0.0.1:1", "pubsub_base_url": "http://127.0.0.1:1",
	}
	with := func(extra map[string]any) map[string]any {
		raw := driveConfig(extra)
		for k, v := range loopback {
			raw[k] = v
		}
		return raw
	}
	cfg, err := ParseConfig(driveConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DriveRemoveBackoff != driveRemoveBackoffDefault {
		t.Fatalf("default: backoff = %v", cfg.DriveRemoveBackoff)
	}
	cfg, err = ParseConfig(with(map[string]any{"test_drive_remove_backoff_ms": 50}))
	if err != nil {
		t.Fatalf("a shortened pause: %v", err)
	}
	if cfg.DriveRemoveBackoff != 50*time.Millisecond {
		t.Errorf("a shortened pause: backoff = %v", cfg.DriveRemoveBackoff)
	}
	for _, ms := range []any{50, int(driveRemoveBackoffDefault / time.Millisecond)} {
		if _, err := ParseConfig(driveConfig(map[string]any{"test_drive_remove_backoff_ms": ms})); err == nil || !strings.HasPrefix(err.Error(), configErrorPrefix) {
			t.Errorf("pause %v without skipped authentication: err = %v, want a google_chat config error: a test-only key is refused whatever its value", ms, err)
		}
	}
	for _, bad := range []any{0, -1, 1001, "soon"} {
		if _, err := ParseConfig(with(map[string]any{"test_drive_remove_backoff_ms": bad})); err == nil || !strings.HasPrefix(err.Error(), configErrorPrefix) {
			t.Errorf("pause %v: err = %v, want a google_chat config error", bad, err)
		}
	}
}

func TestHandOffParkedOnASlotDoesNothingOnceStopGaveUp(t *testing.T) {
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			r := newHandOffRig(t)
			r.a.driveSlots = make(chan struct{}, 1)
			r.a.driveSlots <- struct{}{}
			done := make(chan struct{})
			go func() {
				r.a.handOffToDrive(r.work("a.csv"))
				close(done)
			}()
			time.Sleep(5 * time.Millisecond)
			r.a.abandonCancel()
			<-r.a.driveSlots
			<-done
			if n := r.uploads(); n != 0 {
				t.Fatalf("uploads = %d after Stop gave up while the hand-off waited for a slot, want none", n)
			}
		})
	}
}

func TestAbandonableContextIsDoneWhenTheAbandonIsRaised(t *testing.T) {
	for range 5000 {
		a := NewAdapter(nil)
		ctx, release := a.abandonableContext(time.Minute)
		a.abandonCancel()
		if ctx.Err() == nil {
			release()
			t.Fatal("a context made before the abandon was still live after it was raised")
		}
		release()
	}
}

func TestHandOffDoesNotShareAnUploadThatFinishedAfterStopGaveUp(t *testing.T) {
	r := newHandOffRig(t)
	logs := jsonLogsOf(r.a)
	r.drive.upload = func(ctx context.Context, name, _ string, _ map[string]string) (string, error) {
		<-r.a.abandonCtx.Done()
		return fakeFileID(name), nil
	}
	done := make(chan struct{})
	go func() {
		r.a.handOffToDrive(r.work("a.csv"))
		close(done)
	}()
	waitUntil(t, "the upload to start", func() bool { return r.uploads() == 1 })
	r.a.abandonCancel()
	<-done

	if n := r.drive.count("share"); n != 0 {
		t.Errorf("shares = %d after Stop gave up, want none", n)
	}
	line := lineWith(t, logLines(t, logs), logFilesNotHandedOff)
	if got := ids(line, "leftFileIDs"); !slices.Equal(got, []string{fakeFileID("a.csv")}) {
		t.Errorf("leftFileIDs = %v, want the file that was uploaded and not shared", got)
	}
}

func TestPutInDriveStopsBeforeUploadOnceStopGaveUp(t *testing.T) {
	r := newHandOffRig(t)
	r.a.abandonCancel()

	_, err := r.a.putInDrive(context.Background(), r.work("a.csv"), entrypoint.SavedArtifact{Filename: "a.csv", MIMEType: "text/csv"})

	staged, ok := errors.AsType[*driveStageError](err)
	if !ok || staged.stage != "before_upload" || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a before_upload stage error caused by the abandon", err)
	}
	if n := r.uploads(); n != 0 {
		t.Errorf("uploads = %d after Stop gave up, want none", n)
	}
	if fileMayRemain(err) {
		t.Error("fileMayRemain = true for a file that was never sent")
	}
}

func TestPutInDriveStopsBeforeUploadWhenItsOwnTimeIsUp(t *testing.T) {
	r := newHandOffRig(t)
	expired, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.a.putInDrive(expired, r.work("a.csv"), entrypoint.SavedArtifact{Filename: "a.csv", MIMEType: "text/csv"})

	if staged, ok := errors.AsType[*driveStageError](err); !ok || staged.stage != "before_upload" {
		t.Fatalf("err = %v, want a before_upload stage error", err)
	}
	if n := r.uploads(); n != 0 {
		t.Errorf("uploads = %d with no time left, want none", n)
	}
}

func TestPutInDriveDoesNotShareOnceStopGaveUp(t *testing.T) {
	r := newHandOffRig(t)
	r.drive.upload = func(_ context.Context, name, _ string, _ map[string]string) (string, error) {
		r.a.abandonCancel()
		return fakeFileID(name), nil
	}

	_, err := r.a.putInDrive(context.Background(), r.work("a.csv"), entrypoint.SavedArtifact{Filename: "a.csv", MIMEType: "text/csv"})

	staged, ok := errors.AsType[*driveStageError](err)
	if !ok || staged.stage != "before_share" || staged.fileID != fakeFileID("a.csv") || !staged.fileRemains {
		t.Fatalf("err = %v, want a before_share error that keeps the id of the file left in the folder", err)
	}
	if n := r.drive.count("share"); n != 0 {
		t.Errorf("shares = %d after Stop gave up, want none", n)
	}
}

func TestPutInDriveDoesNotShareWhenItsOwnTimeIsUp(t *testing.T) {
	r := newHandOffRig(t)
	parent, cancel := context.WithCancel(context.Background())
	r.drive.upload = func(_ context.Context, name, _ string, _ map[string]string) (string, error) {
		cancel()
		return fakeFileID(name), nil
	}

	_, err := r.a.putInDrive(parent, r.work("a.csv"), entrypoint.SavedArtifact{Filename: "a.csv", MIMEType: "text/csv"})

	staged, ok := errors.AsType[*driveStageError](err)
	if !ok || staged.stage != "before_share" || staged.fileRemains {
		t.Fatalf("err = %v, want a before_share error for a file that was removed again", err)
	}
	if n := r.drive.count("share"); n != 0 {
		t.Errorf("shares = %d with no time left, want none", n)
	}
	if n := r.drive.count("delete"); n != 1 {
		t.Errorf("removals = %d, want the uploaded file removed once", n)
	}
}

type abandonedWithoutDone struct{ context.Context }

func (abandonedWithoutDone) Err() error { return context.Canceled }

func TestPutFilesInDriveReadsNoFileOnceStopGaveUp(t *testing.T) {
	r := newHandOffRig(t)
	r.a.abandonCtx = abandonedWithoutDone{context.Background()}
	var reads atomic.Int32
	work := r.work("a.csv", "b.csv")
	work.readFile = func(context.Context, entrypoint.SavedArtifact) ([]byte, error) {
		reads.Add(1)
		return []byte("x"), nil
	}
	outcome := &driveOutcome{}

	r.a.putFilesInDrive(work, outcome)

	if n := reads.Load(); n != 0 {
		t.Errorf("files read = %d after Stop gave up, want none", n)
	}
	if !slices.Equal(outcome.unshared, []string{"a.csv", "b.csv"}) || len(outcome.shared) != 0 {
		t.Errorf("outcome = %+v, want both files named as not in Drive", outcome)
	}
}

func TestPutInDriveNamesTheShareStageWhenDriveRefusesTheShare(t *testing.T) {
	r := newHandOffRig(t)
	r.drive.share = func(context.Context, string, string, time.Time) error { return apiFailure(http.StatusForbidden) }

	_, err := r.a.putInDrive(context.Background(), r.work("a.csv"), entrypoint.SavedArtifact{Filename: "a.csv", MIMEType: "text/csv"})

	if staged, ok := errors.AsType[*driveStageError](err); !ok || staged.stage != "share" {
		t.Fatalf("err = %v, want a share stage error", err)
	}
}
