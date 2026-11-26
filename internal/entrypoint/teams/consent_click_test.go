// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	allowClickID   = "1792767300000"
	allowedFile    = "report.pdf"
	allowedContent = "%PDF-1.7 report"
	fileUploaded   = "✅ File uploaded: " + allowedFile
)

type uploadSession struct {
	srv  *httptest.Server
	puts atomic.Int32

	mu       sync.Mutex
	held     chan struct{}
	arrived  chan struct{}
	release  func()
	finished bool
}

func newUploadSession(t *testing.T) *uploadSession {
	t.Helper()
	u := &uploadSession{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.puts.Add(1)
		u.mu.Lock()
		held, arrived := u.held, u.arrived
		u.arrived = nil
		u.mu.Unlock()
		if arrived != nil {
			close(arrived)
		}
		if held != nil {
			select {
			case <-held:
			case <-r.Context().Done():
				return
			}
		}
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.finished {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		u.finished = true
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *uploadSession) hold(t *testing.T) (arrived <-chan struct{}, release func()) {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	held := make(chan struct{})
	u.held = held
	u.arrived = make(chan struct{})
	u.release = sync.OnceFunc(func() { close(held) })
	t.Cleanup(u.release)
	return u.arrived, u.release
}

func (inst *teamsInstances) allowClick(t *testing.T, session *uploadSession) map[string]any {
	t.Helper()
	activity := dmActivity(allowClickID)
	delete(activity, "text")
	activity["type"] = ActivityTypeInvoke
	activity["name"] = InvokeFileConsent
	activity["value"] = map[string]any{
		"type":   "fileUpload",
		"action": "accept",
		"context": map[string]any{
			"filename": allowedFile, "task_id": "task-done",
			"app_name": twoInstanceAgent, "user_id": inst.clicker(t, activity), "session_id": "session-1",
			"artifact_filename": allowedFile, "version": 0,
		},
		"uploadInfo": map[string]any{
			"uploadUrl": session.srv.URL, "uniqueId": "drive-item-1", "fileType": "pdf",
			"contentUrl": "https://contoso.sharepoint.com/" + allowedFile, "name": allowedFile,
		},
	}
	return activity
}

func (inst *teamsInstances) clicker(t *testing.T, activity map[string]any) string {
	t.Helper()
	parsed, err := parseInbound(marshalActivity(t, activity))
	if err != nil {
		t.Fatal(err)
	}
	user := inst.adapters[0].requestingUserID(t.Context(), parsed)
	loc := inst.arts.Resolve(twoInstanceAgent, user, "session-1", allowedFile)
	if _, err := inst.arts.Inner.Save(t.Context(), loc, []byte(allowedContent), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	return user
}

func click(t *testing.T, a *TeamsAdapter, activity map[string]any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, signedInvokeRequest(t, a.botAuth, a.cfg.AppID, activity))
	return rec.Code
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func stopDispatching(a *TeamsAdapter) {
	a.server.Close()
	a.cancel()
	a.server.Wait()
}

func allowKey() string { return inboundMessageKey(twoInstanceConversation, allowClickID) }

func TestTwoInstances_AllowFinishedByAnotherInstanceAfterTheFirstStops(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	first, survivor := inst.adapters[0], inst.adapters[1]
	session := newUploadSession(t)
	activity := inst.allowClick(t, session)
	arrived, release := session.hold(t)

	if code := click(t, first, activity); code != http.StatusOK {
		t.Fatalf("the Allow was answered %d, want 200", code)
	}
	<-arrived
	stopDispatching(first)
	release()
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Fatalf("the stopped instance posted %q, want nothing", posted)
	}

	lapsed, err := store.NewEntrypointInboundRepo(survivor.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10)
	if err != nil || len(lapsed) != 1 {
		t.Fatalf("TakeLapsed = %+v, %v; want the Allow", lapsed, err)
	}
	survivor.ReplayInbound(t.Context(), entrypoint.InboundMessage{
		ID: lapsed[0].MessageID, Payload: lapsed[0].Payload, Token: lapsed[0].Token, Attempts: lapsed[0].Attempts,
	})

	if got := inst.bot.posted(); !slices.Equal(got, []string{fileUploaded}) {
		t.Errorf("posted = %q, want only %q", got, fileUploaded)
	}
	if state := inboundRow(t, survivor, allowKey()); state != store.InboundDone {
		t.Errorf("record state = %q, want done", state)
	}
}

func TestTwoInstances_CopyOfAnAllowUploadsNothing(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	session := newUploadSession(t)
	activity := inst.allowClick(t, session)

	if code := deliver(t, inst.adapters[0], activity); code != http.StatusOK {
		t.Fatalf("the Allow was answered %d, want 200", code)
	}
	if code := deliver(t, inst.adapters[1], activity); code != http.StatusOK {
		t.Fatalf("the copy was answered %d, want 200", code)
	}

	if n := session.puts.Load(); n != 1 {
		t.Errorf("uploads = %d, want 1", n)
	}
	if got := inst.bot.posted(); !slices.Equal(got, []string{fileUploaded}) {
		t.Errorf("posted = %q, want only %q", got, fileUploaded)
	}
	if state := inboundRow(t, inst.adapters[0], allowKey()); state != store.InboundDone {
		t.Errorf("record state = %q, want done", state)
	}
}

func TestAbandonInbound_AllowTellsTheUserTheFileWasNotUploaded(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	payload, err := json.Marshal(inst.allowClick(t, newUploadSession(t)))
	if err != nil {
		t.Fatal(err)
	}

	if !a.AbandonInbound(t.Context(), entrypoint.InboundMessage{ID: allowKey(), Payload: string(payload)}) {
		t.Fatal("the notice did not reach Teams")
	}

	want := "Sorry, I couldn't upload the file '" + allowedFile + "'. Please try again."
	waitUntil(t, "the notice is posted", func() bool { return slices.Contains(inst.bot.posted(), want) })
}

func TestTwoInstances_AllowNotRecordedIsRefused(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	broken, healthy := inst.adapters[0], inst.adapters[1]
	session := newUploadSession(t)
	activity := inst.allowClick(t, session)
	_ = broken.core.Store().Close()

	if code := deliver(t, broken, activity); code != http.StatusServiceUnavailable {
		t.Fatalf("an Allow that was not recorded was answered %d, want 503", code)
	}
	if n := session.puts.Load(); n != 0 {
		t.Fatalf("an Allow that was not recorded uploaded %d times", n)
	}

	if code := deliver(t, healthy, activity); code != http.StatusOK {
		t.Fatalf("the retry was answered %d, want 200", code)
	}
	if n := session.puts.Load(); n != 1 {
		t.Errorf("uploads after the retry = %d, want 1", n)
	}
	if got := inst.bot.posted(); !slices.Equal(got, []string{fileUploaded}) {
		t.Errorf("posted = %q, want only %q", got, fileUploaded)
	}
}
