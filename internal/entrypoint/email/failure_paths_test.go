// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/hmacutil"
)

var errItemsDown = errors.New("items store down")

type downItems struct{}

func (downItems) Put(context.Context, string, string, string) error { return errItemsDown }
func (downItems) Claim(context.Context, string, string, string) (bool, error) {
	return false, errItemsDown
}
func (downItems) Get(context.Context, string, string) (string, bool, error) {
	return "", false, errItemsDown
}
func (downItems) List(context.Context, string) (map[string]string, error) { return nil, errItemsDown }

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTaskItems_WithoutAStore(t *testing.T) {
	t.Parallel()
	items := taskItems{logger: quietLogger()}
	if items.enabled() {
		t.Fatal("enabled without a repo")
	}
	if err := items.writeDestination(t.Context(), "t", &EmailTaskMeta{}); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	items.put(t.Context(), "t", itemTextStarted, "")
	items.recordArtifact(t.Context(), "t", capturedArtifact{Filename: "a"})
	if !items.claim(t.Context(), "t", itemReplySent) {
		t.Error("claim without a store not won")
	}
	if _, ok, err := items.get(t.Context(), "t", itemDestination); ok || err != nil {
		t.Errorf("get = %v, %v", ok, err)
	}
	if got, err := items.list(t.Context(), "t"); got != nil || err != nil {
		t.Errorf("list = %v, %v", got, err)
	}
}

func TestTaskItems_StoreDown(t *testing.T) {
	t.Parallel()
	items := taskItems{repo: downItems{}, logger: quietLogger()}
	if err := items.writeDestination(t.Context(), "t", &EmailTaskMeta{}); !errors.Is(err, errItemsDown) {
		t.Errorf("writeDestination = %v", err)
	}
	items.put(t.Context(), "t", itemArtifactPrefix+"a@0", "")
	items.recordArtifact(t.Context(), "t", capturedArtifact{Filename: "a"})
	if !items.claim(t.Context(), "t", itemReplySent) {
		t.Error("a failed claim must count as won")
	}
	if _, _, err := items.get(t.Context(), "t", itemDestination); err == nil {
		t.Error("get on a down store")
	}
	if _, err := items.list(t.Context(), "t"); err == nil {
		t.Error("list on a down store")
	}
	if kind := itemKind("reply-sent"); kind != "reply-sent" {
		t.Errorf("itemKind = %q", kind)
	}
}

func TestMetaFromItems(t *testing.T) {
	t.Parallel()
	if meta := metaFromItems(map[string]string{}, quietLogger(), "t"); meta != nil {
		t.Error("meta built without a destination")
	}
	if meta := metaFromItems(map[string]string{itemDestination: "{"}, quietLogger(), "t"); meta != nil {
		t.Error("meta built from an unreadable destination")
	}
	meta := metaFromItems(map[string]string{
		itemDestination:                `{"message_id":"<m@x>","reply_to":"a@x","subject":"s"}`,
		itemArtifactPrefix + "b.pdf@0": `{"filename":"b.pdf","uri":"u2","seq":2}`,
		itemArtifactPrefix + "a.pdf@0": `{"filename":"a.pdf","uri":"u1","seq":1}`,
		itemArtifactPrefix + "c.pdf@0": `{"filename":"c.pdf","uri":"u3","seq":2}`,
		itemArtifactPrefix + "bad@0":   `{`,
	}, quietLogger(), "t")
	if meta == nil || meta.missedStream || meta.textRecorded {
		t.Fatalf("meta = %+v", meta)
	}
	var names []string
	for _, c := range meta.artifacts {
		names = append(names, c.Filename)
	}
	if len(names) != 3 || names[0] != "a.pdf" || names[1] != "b.pdf" || names[2] != "c.pdf" {
		t.Errorf("artifacts = %v, want a b c", names)
	}
}

func TestDispatch_StoreFailuresStopBeforeThePublish(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	n := &NormalizedEmail{From: EmailAddress{Address: "alice@vendor.com"}, Subject: "hi", MessageID: "<m1@vendor.com>", ThreadID: "<m1@vendor.com>", BodyText: "hello", ProviderUID: "1"}
	d := &gatewayDispatcher{adapter: a.adapter}

	repo := a.adapter.items.repo
	a.adapter.items.repo = downItems{}
	if _, err := d.Dispatch(t.Context(), n, auth.ForUserIDOnly("alice@vendor.com"), InternalTrustResult{}); err == nil {
		t.Fatal("Dispatch with no destination store succeeded")
	}
	if c := taskRecordCount(t, a, emailTaskID("test-gw", n.MessageID, "1")); c != 0 {
		t.Errorf("task records after a failed destination write = %d, want 0", c)
	}
	a.adapter.items.repo = repo

	_ = a.core.Store().Close()
	if _, err := d.Dispatch(t.Context(), n, auth.ForUserIDOnly("alice@vendor.com"), InternalTrustResult{}); err == nil {
		t.Fatal("Dispatch on a closed database succeeded")
	}
	if won, err := a.adapter.recordMail(t.Context(), n.MessageID); err == nil || won {
		t.Errorf("recordMail on a closed database = %v, %v", won, err)
	}
	if a.adapter.forgetMail(t.Context(), n.MessageID) {
		t.Error("forgetMail on a closed database reported success")
	}
	if got := r.agentRequests(200 * time.Millisecond); len(got) != 0 {
		t.Errorf("agent requests = %v, want none", got)
	}
}

func TestLoadTask_Paths(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{logger: quietLogger()}
	if err := a.LoadTask(t.Context(), "t"); err != nil {
		t.Fatalf("LoadTask without a store: %v", err)
	}
	a.items = taskItems{repo: downItems{}, logger: quietLogger()}
	if err := a.LoadTask(t.Context(), "t"); err == nil {
		t.Fatal("LoadTask on a down store reported success")
	}
	a.taskMeta.Store("t", &EmailTaskMeta{})
	if err := a.LoadTask(t.Context(), "t"); err != nil {
		t.Fatalf("LoadTask for a held task: %v", err)
	}
}

func TestOnError_ReplyAlreadyClaimed(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	if res := a.adapter.worker.Handle(t.Context(), m); res != HandleProcessed {
		t.Fatalf("handling = %v", res)
	}
	taskID := emailTaskID("test-gw", "<m1@vendor.com>", "1")
	if !a.adapter.items.claim(t.Context(), taskID, itemReplySent) {
		t.Fatal("first claim lost")
	}
	a.adapter.OnError(t.Context(), taskID, &a2a.Error{Message: "boom"}, nil)
	if got := len(a.replies.replies()); got != 0 {
		t.Errorf("replies = %d, want none", got)
	}
}

func TestRunIngress_OpenFailureIsRetriedOnTheNextTick(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, func(a *EmailAdapter) { a.cfg.DeliveryMode = DeliveryModeIdle })
	r.mailbox.mu.Lock()
	r.mailbox.ConnectErrorRemaining = 1
	r.mailbox.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.adapter.RunIngress(ctx, 1)
	a.adapter.runMu.Lock()
	first := a.adapter.run
	a.adapter.runMu.Unlock()
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a loop that could not open the mailbox did not end")
	}
	r.mailbox.mu.Lock()
	r.mailbox.IdleSupported = true
	r.mailbox.mu.Unlock()
	a.adapter.RunIngress(ctx, 1)
	r.waitProcessed("1")
}

func TestRunIngress_AfterStopOrTermEnd(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	a.adapter.RunIngress(ended, 1)
	if a.adapter.run != nil {
		t.Fatal("a loop started for an ended term")
	}
	if err := a.adapter.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.adapter.RunIngress(t.Context(), 2)
	if a.adapter.run != nil {
		t.Fatal("a loop started after Stop")
	}
}

func TestHandleQueued_TermEndsDuringARetry(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t)
	a := r.start(true, func(a *EmailAdapter) {
		a.worker.Dispatcher = &slowDispatcher{next: &gatewayDispatcher{adapter: a}, failID: "<m1@vendor.com>", failLeft: 100}
		a.retry = retryPolicy{initial: time.Hour, max: time.Hour, attempts: 3}
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.adapter.handleQueued(ctx, m)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleQueued did not return when the term ended")
	}
	if r.mailbox.WasProcessed("1") {
		t.Error("a mail left by an ended term was flagged seen")
	}

	cancelled, stop := context.WithCancel(t.Context())
	stop()
	a.adapter.retry = retryPolicy{initial: time.Millisecond, max: time.Millisecond, attempts: 1}
	a.adapter.handleQueued(cancelled, m)
	if r.mailbox.WasProcessed("1") {
		t.Error("a mail handled on an ended term was flagged seen")
	}
}

func TestGiveUp_Paths(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	a.adapter.giveUp(t.Context(), RawMessage{UID: "x", RFC822: []byte("not a mail")}, 1)
	if got := len(a.replies.replies()); got != 0 {
		t.Errorf("replies for an unparseable mail = %d, want none", got)
	}
	a.adapter.replier = failingReplier{}
	a.adapter.giveUp(t.Context(), testMail("1", "<m1@vendor.com>", "", "hi"), 1)
	a.adapter.replier = nil
	a.adapter.giveUp(t.Context(), testMail("1", "<m1@vendor.com>", "", "hi"), 1)
}

type failingReplier struct{}

func (failingReplier) Send(context.Context, ReplyRequest) error { return errors.New("smtp down") }
func (failingReplier) SupportsAttachments() bool                { return true }

func TestHIL_StoreDown(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	s := newHILServer(a.core, []byte("secret"), "https://gw.example.com", "test-gw", quietLogger())
	s.items = taskItems{repo: downItems{}, logger: quietLogger()}
	taskID := "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c46"
	if err := a.core.Tasks().Register(taskID, "Agent", "u1", entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := s.save(t.Context(), "req-3", &pendingHIL{taskID: taskID, expiry: time.Now().Add(time.Minute)}, nil); err == nil {
		t.Error("save on a down store succeeded")
	}
	expiry := time.Now().Add(10 * time.Minute)
	token, err := signHILToken(hilToken{RequestID: "req-3", TaskID: taskID, AgentName: "Agent", UserID: "u1", ExpiresAt: expiry.Unix()}, s.secret)
	if err != nil {
		t.Fatal(err)
	}
	getRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if getRec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET on a down store = %d, want 503", getRec.Code)
	}
	if rec := postForm(t, s.Handler(), token, url.Values{"_btn": {"submit-btn"}, "f0": {"Blue"}}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("POST on a down store = %d, want 503", rec.Code)
	}

	s.items = a.adapter.items
	if err := a.adapter.items.repo.Put(t.Context(), taskID, itemHILPromptPrefix+"req-4", "{"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lookup(t.Context(), taskID, "req-4"); err == nil {
		t.Error("lookup of an unreadable question succeeded")
	}
	if err := a.adapter.items.repo.Put(t.Context(), taskID, itemHILPromptPrefix+"req-5", `{"surface":42}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lookup(t.Context(), taskID, "req-5"); err == nil {
		t.Error("lookup of an unparseable surface succeeded")
	}
}

func TestHILSigningSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET_KEY", "from-env")
	if got := hilSigningSecret(nil); got != "" {
		t.Errorf("secret without a core = %q", got)
	}
	plain := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "n", "gateway_id": "g"})
	if err := plain.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := hilSigningSecret(plain); got != "from-env" {
		t.Errorf("secret from the environment = %q, want from-env", got)
	}
	configured := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "n", "gateway_id": "g", "session_secret_key": "from-config"})
	if err := configured.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := hilSigningSecret(configured); got != "from-config" {
		t.Errorf("secret from the config = %q, want from-config", got)
	}
	raw, err := signHILToken(hilToken{RequestID: "r", TaskID: "t", ExpiresAt: time.Now().Add(time.Hour).Unix()}, []byte("from-config"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyHILToken(raw, hilKey("from-config")); err == nil {
		t.Error("a token signed with the secret itself verified")
	}
	loginSigned := hmacutil.Sum([]byte("from-config"), []byte("sam-email-hil-v1"))
	if bytes.Equal(hilKey("from-config"), loginSigned) {
		t.Error("the magic-link key is a signature the login hands out")
	}
	if bytes.Equal(hilKey("from-config"), hilKey("other")) {
		t.Error("the magic-link key does not depend on the secret")
	}
}

func TestInit_MagicLinkKeyIsDerived(t *testing.T) {
	cfg := adapterTestConfig()
	cfg["external_base_url"] = "https://gw.example.com"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	core := entrypoint.NewGatewayCore(a, map[string]any{"namespace": "n", "gateway_id": "g", "session_secret_key": "from-config"})
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if a.hil == nil {
		t.Fatal("magic links not enabled")
	}
	if !bytes.Equal(a.hil.secret, hilKey("from-config")) {
		t.Error("magic links are not signed with the derived key")
	}
}

func TestOnFinalResponse_NoAnswerText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		state a2a.TaskState
		want  string
	}{
		{a2a.TaskStateCancelled, cancelledReplyText},
		{a2a.TaskStateFailed, errorReplyText},
		{a2a.TaskStateRejected, errorReplyText},
		{a2a.TaskStateCompleted, ""},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			t.Parallel()
			replies := &sentReplies{}
			a := &EmailAdapter{logger: quietLogger(), cfg: &EmailConfig{}, replier: replies}
			a.taskMeta.Store("t", &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}, OriginalMessageID: "<m@x>"})
			final := &a2a.Task{Status: a2a.TaskStatus{State: tc.state, Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("  ")}}}}
			a.OnFinalResponse(t.Context(), "t", final, nil)
			got := replies.replies()
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("sent %d replies for an empty completed answer, want none", len(got))
				}
				return
			}
			if len(got) != 1 || got[0].BodyText != tc.want || got[0].OriginalMessageID != "<m@x>" {
				t.Fatalf("replies = %+v, want one %q in the thread", got, tc.want)
			}
		})
	}
}
