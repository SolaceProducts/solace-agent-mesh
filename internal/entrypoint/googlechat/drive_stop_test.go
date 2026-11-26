// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func (r *handOffRig) uploads() int { return r.drive.count("upload:folder-0123456789") }

func newDrainRig(t *testing.T) (*handOffRig, *taskState, string) {
	t.Helper()
	r := newHandOffRig(t)
	taskID := tid("task-drain")
	st := submitForTest(t, r.a, taskID, dmMeta())
	r.chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	return r, st, taskID
}

func waitForResultMessage(t *testing.T, chat *fakeChat) []string {
	t.Helper()
	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
		return anyTextContains(texts, "is shared with you in Google Drive.")
	})
	return texts
}

func TestDrainTerminalHandsFilesToDriveOnlyAfterTheAnswerIsWritten(t *testing.T) {
	r, st, taskID := newDrainRig(t)
	release := holdWrites(r.chat)
	t.Cleanup(func() { closeOnce(release) })
	q := r.a.queueFor(taskID, st)
	q.Replace("The final answer")
	done := make(chan struct{})
	go func() {
		r.a.drainTerminal(taskID, st, q, r.work("a.csv"), nil)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	if n := r.uploads(); n != 0 {
		t.Fatalf("uploads = %d while the answer is still being written, want none", n)
	}
	closeOnce(release)
	<-done
	texts := waitForResultMessage(t, r.chat)
	if len(texts) != 2 || texts[0] != "The final answer" {
		t.Errorf("messages = %q, want the answer first and the result after it", texts)
	}
}

func TestDrainTerminalHandsFilesToDriveAfterALingeringAnswer(t *testing.T) {
	r, st, taskID := newDrainRig(t)
	r.a.lingerTimeout = time.Minute
	release := holdWrites(r.chat)
	t.Cleanup(func() { closeOnce(release) })
	q := r.a.queueFor(taskID, st)
	q.Replace("The final answer")
	r.a.drainTerminal(taskID, st, q, r.work("a.csv"), nil)
	r.a.finishTask(taskID)

	time.Sleep(200 * time.Millisecond)
	if n := r.uploads(); n != 0 {
		t.Fatalf("uploads = %d before the lingering answer was written, want none", n)
	}
	closeOnce(release)
	texts := waitForResultMessage(t, r.chat)
	if texts[0] != "The final answer" {
		t.Errorf("messages = %q, want the answer first", texts)
	}
}

func TestDrainTerminalHandsFilesToDriveWhenChatRefusesPartOfTheAnswer(t *testing.T) {
	r, st, taskID := newDrainRig(t)
	r.chat.mu.Lock()
	r.chat.rejectText = answerChunks(longAnswer())[1]
	r.chat.mu.Unlock()
	q := r.a.queueFor(taskID, st)
	q.Replace(longAnswer())
	r.a.drainTerminal(taskID, st, q, r.work("a.csv"), nil)

	waitForResultMessage(t, r.chat)
	if n := r.uploads(); n != 1 {
		t.Errorf("uploads = %d, want the file handed over", n)
	}
}

func TestDrainTerminalWhileStoppingLogsTheReplyAndItsFilesAsLeft(t *testing.T) {
	r, st, taskID := newDrainRig(t)
	logs := jsonLogsOf(r.a)
	r.a.awaitLingeringReplies(t.Context())
	q := r.a.queueFor(taskID, st)
	q.Replace("The final answer")
	r.a.drainTerminal(taskID, st, q, r.work("a.csv", "b.csv"), nil)

	lines := logLines(t, logs)
	if got := messages(lines, "ERROR"); !slices.Equal(got, []string{logReplyLostStopped}) {
		t.Errorf("ERROR lines = %q, want one line for the one event", got)
	}
	if line := lineWith(t, lines, logReplyLostStopped); line["files"] != float64(2) {
		t.Errorf("line = %v, want the number of files that were to follow", line)
	}
	if n := r.uploads(); n != 0 {
		t.Errorf("uploads = %d, want none", n)
	}
}

func closeOnce(c chan struct{}) {
	select {
	case <-c:
	default:
		close(c)
	}
}

func TestHandOffOutlivesTheAdaptersContext(t *testing.T) {
	r := newHandOffRig(t)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { closeOnce(release) })
	r.drive.upload = func(ctx context.Context, name, _ string, _ map[string]string) (string, error) {
		close(started)
		select {
		case <-release:
			return fakeFileID(name), nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	handOffInBackground(r.a, r.work("a.csv"))
	<-started

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		_ = r.a.Stop(stopCtx)
		close(stopped)
	}()
	<-r.a.ctx.Done()
	time.Sleep(100 * time.Millisecond)
	closeOnce(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the upload finished")
	}
	_, texts := r.chat.snapshot()
	if !anyTextContains(texts, "is shared with you in Google Drive.") {
		t.Errorf("messages = %q, want the link: Stop gave the upload time to finish", texts)
	}
}

func TestHandOffLogsWhetherTheFileMayRemain(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(r *handOffRig)
		want  string
	}{
		"upload timed out": {
			setup: func(r *handOffRig) {
				r.drive.upload = func(context.Context, string, string, map[string]string) (string, error) {
					return "", context.DeadlineExceeded
				}
			},
			want: `"fileMayRemain":true`,
		},
		"upload answered with a server error": {
			setup: func(r *handOffRig) {
				r.drive.upload = func(context.Context, string, string, map[string]string) (string, error) {
					return "", apiFailure(http.StatusBadGateway)
				}
			},
			want: `"fileMayRemain":true`,
		},
		"upload refused": {
			setup: func(r *handOffRig) {
				r.drive.upload = func(context.Context, string, string, map[string]string) (string, error) {
					return "", apiFailure(http.StatusForbidden)
				}
			},
			want: `"fileMayRemain":false`,
		},
		"share refused and the file removed": {
			setup: func(r *handOffRig) {
				r.drive.share = func(context.Context, string, string, time.Time) error { return errors.New("refused") }
			},
			want: `"fileMayRemain":false`,
		},
		"share refused and the file not removed": {
			setup: func(r *handOffRig) {
				r.drive.share = func(context.Context, string, string, time.Time) error { return errors.New("refused") }
				r.drive.remove = func(context.Context, string) (bool, error) { return false, apiFailure(http.StatusInternalServerError) }
			},
			want: `"fileMayRemain":true,"fileID":"` + fakeFileID("a.csv") + `"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newHandOffRig(t)
			logs := jsonLogsOf(r.a)
			tc.setup(r)
			r.a.handOffToDrive(r.work("a.csv"))
			if !strings.Contains(logs.String(), tc.want) {
				t.Errorf("log = %s, want it to hold %s", logs.String(), tc.want)
			}
		})
	}
}

func TestHandOffLogsTheIDsOfFilesItCouldNotTellThePersonAbout(t *testing.T) {
	r := newHandOffRig(t)
	logs := jsonLogsOf(r.a)
	r.chat.onWrite = func(context.Context) error { return &permanentError{err: errors.New("refused")} }
	r.a.handOffToDrive(r.work("a.csv"))

	out := logs.String()
	if !strings.Contains(out, `"sharedFileIDs":["`+fakeFileID("a.csv")+`"]`) {
		t.Errorf("log = %s, want the id of the shared file", out)
	}
	if strings.Count(out, "could not be posted") != 1 {
		t.Errorf("log = %s, want the lost result logged once", out)
	}
}
