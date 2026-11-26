// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var emailDBTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL("", "email", func(u string) error {
		db, err := store.Open(u, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		return db.Migrate(context.Background())
	})
})

type emailRig struct {
	t       *testing.T
	brk     *broker.DevBroker
	dbURL   string
	mailbox *FakeProvider
	agent   broker.Queue
}

type emailInstance struct {
	client  *broker.DevBrokerClient
	core    *entrypoint.GatewayCore
	adapter *EmailAdapter
	replies *sentReplies
}

func newEmailRig(t *testing.T, mail ...RawMessage) *emailRig {
	t.Helper()
	tmpl, err := emailDBTemplate()
	if err != nil {
		t.Fatalf("email db template: %v", err)
	}
	brk := broker.NewDevBroker()
	if connErr := brk.Connect(t.Context()); connErr != nil {
		t.Fatalf("connect dev broker: %v", connErr)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	agentQ, err := brk.CreateQueue("email-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err := brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Agent")); err != nil {
		t.Fatalf("subscribe agent topic: %v", err)
	}
	return &emailRig{t: t, brk: brk, dbURL: tmpl.Clone(t), mailbox: NewFakeProvider(mail), agent: agentQ}
}

func (r *emailRig) start(leader bool, tune func(*EmailAdapter)) *emailInstance {
	r.t.Helper()
	client := r.brk.NewClient()
	if err := client.Connect(r.t.Context()); err != nil {
		r.t.Fatalf("connect client: %v", err)
	}
	cfg := adapterTestConfig()
	cfg["default_agent_name"] = "Agent"
	cfg["worker_count"] = 4
	cfg["queue_capacity"] = 16
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		r.t.Fatalf("NewEmailAdapter: %v", err)
	}
	a.provider = r.mailbox
	a.retry = retryPolicy{initial: 5 * time.Millisecond, max: 20 * time.Millisecond, attempts: 3}
	core := entrypoint.NewGatewayCore(a, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":             "sql",
			"database_url":     r.dbURL,
			"default_behavior": "PERSISTENT",
		},
	})
	core.SetServices(runtime.NewRuntime(client, nil).Services())
	if leader {
		core.UseLeaderIngress()
	}
	if err := core.Init(r.t.Context(), nil); err != nil {
		r.t.Fatalf("core.Init: %v", err)
	}
	inst := &emailInstance{client: client, core: core, adapter: a, replies: &sentReplies{}}
	a.replier = inst.replies
	if tune != nil {
		tune(a)
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Agent"})
	if err := core.Start(r.t.Context()); err != nil {
		r.t.Fatalf("core.Start: %v", err)
	}
	r.t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return inst
}

func (r *emailRig) agentRequests(within time.Duration) []string {
	r.t.Helper()
	deadline := time.Now().Add(within)
	var got []string
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return got
		}
		ctx, cancel := context.WithTimeout(r.t.Context(), remaining)
		msg, err := r.brk.Receive(ctx, r.agent)
		cancel()
		if err != nil || msg == nil {
			return got
		}
		msg.Ack()
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			r.t.Fatalf("decode agent request: %v", err)
		}
		got = append(got, req.ID)
	}
}

func (r *emailRig) waitProcessed(uid ProviderUID) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !r.mailbox.WasProcessed(uid) {
		if time.Now().After(deadline) {
			r.t.Fatalf("mail %s not flagged seen", uid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testMail(uid, messageID, inReplyTo, body string) RawMessage {
	var b strings.Builder
	b.WriteString("From: \"Alice Smith\" <alice@vendor.com>\n")
	b.WriteString("To: sam-email-entrypoint@example.org\n")
	b.WriteString("Subject: Order request\n")
	b.WriteString("Message-ID: " + messageID + "\n")
	if inReplyTo != "" {
		b.WriteString("In-Reply-To: " + inReplyTo + "\n")
		b.WriteString("References: " + inReplyTo + "\n")
	}
	b.WriteString("Date: Sun, 27 Apr 2026 10:00:00 +0000\n")
	b.WriteString("Authentication-Results: protection.outlook.com; spf=pass smtp.mailfrom=vendor.com; dkim=pass header.d=vendor.com; dmarc=pass header.from=vendor.com\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\n\n")
	b.WriteString(body + "\n")
	return RawMessage{UID: ProviderUID(uid), RFC822: []byte(b.String()), FetchedAt: time.Now()}
}

type sentReplies struct {
	mu   sync.Mutex
	sent []ReplyRequest
}

func (f *sentReplies) Send(_ context.Context, req ReplyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	return nil
}

func (f *sentReplies) SupportsAttachments() bool { return true }

func (f *sentReplies) replies() []ReplyRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ReplyRequest(nil), f.sent...)
}

type slowDispatcher struct {
	next     Dispatcher
	hold     string
	holdFor  time.Duration
	failID   string
	failLeft int
	mu       sync.Mutex
	order    []string
}

func (d *slowDispatcher) Dispatch(ctx context.Context, n *NormalizedEmail, caller *auth.Subject, trust InternalTrustResult) (bool, error) {
	d.mu.Lock()
	if n.MessageID == d.failID && d.failLeft > 0 {
		d.failLeft--
		d.mu.Unlock()
		return false, errors.New("no agent available yet")
	}
	d.mu.Unlock()
	if n.MessageID == d.hold {
		time.Sleep(d.holdFor)
	}
	submitted, err := d.next.Dispatch(ctx, n, caller, trust)
	d.mu.Lock()
	d.order = append(d.order, n.MessageID)
	d.mu.Unlock()
	return submitted, err
}

type meeting struct {
	mu   sync.Mutex
	seen map[string]int
	both map[string]chan struct{}
}

func newMeeting() *meeting {
	return &meeting{seen: map[string]int{}, both: map[string]chan struct{}{}}
}

func (m *meeting) wait(id string) {
	m.mu.Lock()
	ch, ok := m.both[id]
	if !ok {
		ch = make(chan struct{})
		m.both[id] = ch
	}
	m.seen[id]++
	if m.seen[id] == 2 {
		close(ch)
	}
	m.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
	}
}

func (m *meeting) met(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen[id] >= 2
}

type meetRecords struct {
	mailRecordRepo
	meet *meeting
}

func (m meetRecords) Recorded(ctx context.Context, gatewayID, messageID string) (bool, error) {
	seen, err := m.mailRecordRepo.Recorded(ctx, gatewayID, messageID)
	m.meet.wait(messageID)
	return seen, err
}

func taskRecordCount(t *testing.T, i *emailInstance, taskID string) int {
	t.Helper()
	var n int
	if err := i.core.Store().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM task_records WHERE task_id = ?`, taskID).Scan(&n); err != nil {
		t.Fatalf("count task records: %v", err)
	}
	return n
}

func TestLeaderIngress_OnlyTheLeaderFetches(t *testing.T) {
	r := newEmailRig(t, testMail("1", "<m1@vendor.com>", "", "hello"))
	leaderInst := r.start(true, nil)
	r.start(true, nil)

	if got := r.agentRequests(300 * time.Millisecond); len(got) != 0 {
		t.Fatalf("published %v before any instance led", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	leaderInst.adapter.RunIngress(ctx, 1)
	r.waitProcessed("1")
	if got := r.agentRequests(300 * time.Millisecond); len(got) != 1 {
		t.Fatalf("agent requests = %v, want one", got)
	}
}

func TestLeaderIngress_TwoLeadersPublishOnce(t *testing.T) {
	var mail []RawMessage
	for i := range 6 {
		mail = append(mail, testMail(fmt.Sprint(i+1), fmt.Sprintf("<m%d@vendor.com>", i+1), "", "hello"))
	}
	r := newEmailRig(t, mail...)
	meet := newMeeting()
	tune := func(a *EmailAdapter) { a.inbound = meetRecords{mailRecordRepo: a.inbound, meet: meet} }
	a := r.start(true, tune)
	b := r.start(true, tune)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.adapter.RunIngress(ctx, 1)
	b.adapter.RunIngress(ctx, 7)
	for i := range mail {
		r.waitProcessed(mail[i].UID)
	}
	for _, m := range mail {
		if !meet.met(fmt.Sprintf("<m%s@vendor.com>", m.UID)) {
			t.Fatalf("mail %s was not dispatched by both leaders", m.UID)
		}
	}
	got := r.agentRequests(500 * time.Millisecond)
	if len(got) != len(mail) {
		t.Fatalf("agent requests = %d, want %d: %v", len(got), len(mail), got)
	}
	for i, m := range mail {
		want := emailTaskID("test-gw", fmt.Sprintf("<m%d@vendor.com>", i+1), m.UID)
		found := false
		for _, id := range got {
			found = found || id == want
		}
		if !found {
			t.Errorf("mail %s published under no task id %s: %v", m.UID, want, got)
		}
	}
}

func TestLeaderIngress_RefetchAfterPublishIsNotRepublished(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	if res := a.adapter.worker.Handle(t.Context(), m); res != HandleProcessed {
		t.Fatalf("first handling = %v, want processed", res)
	}
	if got := r.agentRequests(300 * time.Millisecond); len(got) != 1 {
		t.Fatalf("first handling published %v, want one", got)
	}

	b := r.start(true, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b.adapter.RunIngress(ctx, 2)
	r.waitProcessed("1")
	if got := r.agentRequests(300 * time.Millisecond); len(got) != 0 {
		t.Fatalf("refetched mail published again: %v", got)
	}
}

func TestLeaderIngress_CrashBeforeTheMailRecordReusesTheTask(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	taskID := emailTaskID("test-gw", "<m1@vendor.com>", "1")
	if err := a.core.Tasks().Register(taskID, "Agent", "alice@vendor.com", entrypoint.TaskIdentity{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := a.adapter.items.writeDestination(t.Context(), taskID, &EmailTaskMeta{OriginalMessageID: "<m1@vendor.com>", ReplyTo: EmailAddress{Address: "alice@vendor.com"}}); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	_ = a.client.Disconnect(context.Background())

	b := r.start(true, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b.adapter.RunIngress(ctx, 2)
	r.waitProcessed("1")
	if got := r.agentRequests(300 * time.Millisecond); len(got) != 1 || got[0] != taskID {
		t.Fatalf("agent requests = %v, want one for %s", got, taskID)
	}
	if n := taskRecordCount(t, b, taskID); n != 1 {
		t.Fatalf("task records for %s = %d, want 1", taskID, n)
	}
}

func TestLeaderIngress_CrashAfterTheMailRecordPublishesNothing(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	if won, err := a.adapter.recordMail(t.Context(), "<m1@vendor.com>"); err != nil || !won {
		t.Fatalf("recordMail = %v, %v", won, err)
	}

	b := r.start(true, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b.adapter.RunIngress(ctx, 2)
	r.waitProcessed("1")
	if got := r.agentRequests(300 * time.Millisecond); len(got) != 0 {
		t.Fatalf("agent requests = %v, want none", got)
	}
}

func TestDispatch_FailedSubmitForgetsTheMailRecord(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	_ = a.client.Disconnect(context.Background())
	n := &NormalizedEmail{From: EmailAddress{Address: "alice@vendor.com"}, Subject: "hi", MessageID: "<m1@vendor.com>", ThreadID: "<m1@vendor.com>", BodyText: "hello", ProviderUID: "1"}
	if _, err := (&gatewayDispatcher{adapter: a.adapter}).Dispatch(t.Context(), n, auth.ForUserIDOnly("alice@vendor.com"), InternalTrustResult{}); err == nil {
		t.Fatal("Dispatch on a disconnected broker succeeded")
	}
	if seen, err := a.adapter.mailRecorded(t.Context(), "<m1@vendor.com>"); err != nil || seen {
		t.Fatalf("mail recorded after a failed submit = %v, %v; want false", seen, err)
	}
	if n := taskRecordCount(t, a, emailTaskID("test-gw", "<m1@vendor.com>", "1")); n != 0 {
		t.Fatalf("task records after a failed submit = %d, want 0", n)
	}
}

func TestLeaderIngress_ThreadKeepsItsOrder(t *testing.T) {
	first := testMail("1", "<t1@vendor.com>", "", "first")
	second := testMail("2", "<t2@vendor.com>", "<t1@vendor.com>", "second")
	other := testMail("3", "<o1@vendor.com>", "", "other thread")
	r := newEmailRig(t, first, second, other)
	var slow *slowDispatcher
	a := r.start(true, func(a *EmailAdapter) {
		slow = &slowDispatcher{next: &gatewayDispatcher{adapter: a}, hold: "<t1@vendor.com>", holdFor: 300 * time.Millisecond}
		a.worker.Dispatcher = slow
	})
	if a.adapter.newQueue().worker(first) != a.adapter.newQueue().worker(second) {
		t.Fatal("two mails of one thread map to different workers")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.adapter.RunIngress(ctx, 1)
	for _, uid := range []ProviderUID{"1", "2", "3"} {
		r.waitProcessed(uid)
	}
	slow.mu.Lock()
	order := append([]string(nil), slow.order...)
	slow.mu.Unlock()
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos["<t1@vendor.com>"] > pos["<t2@vendor.com>"] {
		t.Fatalf("dispatch order %v: the reply in the thread went before its first mail", order)
	}
}

func TestLeaderIngress_RetryHoldsTheThread(t *testing.T) {
	first := testMail("1", "<t1@vendor.com>", "", "first")
	second := testMail("2", "<t2@vendor.com>", "<t1@vendor.com>", "second")
	r := newEmailRig(t, first, second)
	var slow *slowDispatcher
	a := r.start(true, func(a *EmailAdapter) {
		slow = &slowDispatcher{next: &gatewayDispatcher{adapter: a}, failID: "<t1@vendor.com>", failLeft: 2}
		a.worker.Dispatcher = slow
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.adapter.RunIngress(ctx, 1)
	r.waitProcessed("1")
	r.waitProcessed("2")
	slow.mu.Lock()
	order := append([]string(nil), slow.order...)
	slow.mu.Unlock()
	if len(order) != 2 || order[0] != "<t1@vendor.com>" {
		t.Fatalf("dispatch order = %v, want the retried mail first", order)
	}
	if got := len(a.replies.replies()); got != 0 {
		t.Fatalf("replies = %d, want none", got)
	}
}

func TestLeaderIngress_RetriesExhaustedTellsTheSender(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, func(a *EmailAdapter) {
		a.worker.Dispatcher = &slowDispatcher{next: &gatewayDispatcher{adapter: a}, failID: "<m1@vendor.com>", failLeft: 100}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.adapter.RunIngress(ctx, 1)
	r.waitProcessed("1")
	replies := a.replies.replies()
	if len(replies) != 1 || replies[0].BodyText != giveUpReplyBodyText || replies[0].OriginalMessageID != "<m1@vendor.com>" {
		t.Fatalf("replies = %+v, want one give-up notice in the thread", replies)
	}
	if got := r.agentRequests(200 * time.Millisecond); len(got) != 0 {
		t.Fatalf("agent requests = %v, want none", got)
	}
}

type failingRecords struct{ mailRecordRepo }

func (failingRecords) Record(context.Context, string, string) (bool, error) {
	return false, errors.New("mail record write failed")
}

func TestLeaderIngress_GiveUpIsTheOnlyNotice(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, func(a *EmailAdapter) { a.inbound = failingRecords{a.inbound} })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.adapter.RunIngress(ctx, 1)
	r.waitProcessed("1")
	taskID := emailTaskID("test-gw", "<m1@vendor.com>", "1")
	if c := taskRecordCount(t, a, taskID); c != 1 {
		t.Fatalf("task records = %d, want the registered one kept", c)
	}

	a.adapter.OnError(t.Context(), taskID, &a2a.Error{Message: "task timed out"}, nil)
	replies := a.replies.replies()
	if len(replies) != 1 || replies[0].BodyText != giveUpReplyBodyText {
		t.Fatalf("replies = %+v, want only the give-up notice", replies)
	}
}

func TestRunIngress_NewTermWaitsForTheLastOne(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	ctx1, cancel1 := context.WithCancel(t.Context())
	a.adapter.RunIngress(ctx1, 1)
	a.adapter.RunIngress(ctx1, 1)
	a.adapter.runMu.Lock()
	first := a.adapter.run
	a.adapter.runMu.Unlock()
	cancel1()

	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	a.adapter.RunIngress(ctx2, 2)
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the first term's loop did not stop")
	}
	r.mailbox.mu.Lock()
	r.mailbox.pending = append(r.mailbox.pending, testMail("9", "<m9@vendor.com>", "", "late"))
	r.mailbox.mu.Unlock()
	r.waitProcessed("9")
	if got := r.agentRequests(300 * time.Millisecond); len(got) != 1 {
		t.Fatalf("agent requests = %v, want one from the second term", got)
	}
}

func TestReply_FromAnInstanceThatNeverSawTheMail(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	if res := a.adapter.worker.Handle(t.Context(), m); res != HandleProcessed {
		t.Fatalf("handling = %v", res)
	}
	taskID := emailTaskID("test-gw", "<m1@vendor.com>", "1")

	if err := a.adapter.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	a.adapter.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("Hel")}}}}, nil)
	a.adapter.OnArtifactUpdate(t.Context(), taskID, &a2a.TaskArtifactUpdateEvent{Artifact: a2a.Artifact{ArtifactID: "f1", Name: "report.pdf", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{Name: "report.pdf", MIMEType: "application/pdf", URI: "artifact://Agent/alice/s/report.pdf?version=0"}}}}}, nil)

	b := r.start(true, nil)
	if err := b.adapter.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	v, ok := b.adapter.taskMeta.Load(taskID)
	if !ok {
		t.Fatal("b has no state for the task after LoadTask")
	}
	meta := v.(*EmailTaskMeta)
	if meta.ReplyTo.Address != "alice@vendor.com" || meta.OriginalMessageID != "<m1@vendor.com>" || !meta.missedStream {
		t.Fatalf("loaded meta = %+v", meta)
	}
	if len(meta.artifacts) != 1 || meta.artifacts[0].Filename != "report.pdf" {
		t.Fatalf("loaded artifacts = %+v, want report.pdf", meta.artifacts)
	}
	b.adapter.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("lo")}}}}, nil)

	final := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("Hello")}}}}
	b.adapter.OnFinalResponse(t.Context(), taskID, final, nil)
	replies := b.replies.replies()
	if len(replies) != 1 {
		t.Fatalf("b sent %d replies, want 1", len(replies))
	}
	if !strings.HasPrefix(replies[0].BodyText, "Hello") || replies[0].OriginalMessageID != "<m1@vendor.com>" || replies[0].To != "alice@vendor.com" {
		t.Fatalf("reply = %+v, want the full answer threaded under the mail", replies[0])
	}

	a.adapter.OnFinalResponse(t.Context(), taskID, final, nil)
	if got := len(a.replies.replies()); got != 0 {
		t.Fatalf("a sent %d replies after b sent the reply, want 0", got)
	}
}

func TestReply_EventsBackOnTheInstanceThatTookTheMail(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	b := r.start(true, nil)
	if res := a.adapter.worker.Handle(t.Context(), m); res != HandleProcessed {
		t.Fatalf("handling = %v", res)
	}
	taskID := emailTaskID("test-gw", "<m1@vendor.com>", "1")

	if err := b.adapter.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask on b: %v", err)
	}
	b.adapter.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("Hel")}}}}, nil)
	b.adapter.OnArtifactUpdate(t.Context(), taskID, &a2a.TaskArtifactUpdateEvent{Artifact: a2a.Artifact{ArtifactID: "f1", Name: "report.pdf", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{Name: "report.pdf", MIMEType: "application/pdf", URI: "artifact://Agent/alice/s/report.pdf?version=0"}}}}}, nil)

	if err := a.adapter.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask on a: %v", err)
	}
	v, ok := a.adapter.taskMeta.Load(taskID)
	if !ok {
		t.Fatal("a has no state for the task after LoadTask")
	}
	if meta := v.(*EmailTaskMeta); !meta.missedStream || len(meta.artifacts) != 1 {
		t.Fatalf("a's meta = %+v, want the stream marked missed and b's file", meta)
	}
	a.adapter.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("lo")}}}}, nil)
	final := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("Hello")}}}}
	a.adapter.OnFinalResponse(t.Context(), taskID, final, nil)

	replies := a.replies.replies()
	if len(replies) != 1 {
		t.Fatalf("a sent %d replies, want 1", len(replies))
	}
	if !strings.HasPrefix(replies[0].BodyText, "Hello") {
		t.Fatalf("reply body = %q, want the full answer", replies[0].BodyText)
	}
}

func TestOnError_WithNoEventReceived(t *testing.T) {
	m := testMail("1", "<m1@vendor.com>", "", "hello")
	r := newEmailRig(t, m)
	a := r.start(true, nil)
	if res := a.adapter.worker.Handle(t.Context(), m); res != HandleProcessed {
		t.Fatalf("handling = %v", res)
	}
	taskID := emailTaskID("test-gw", "<m1@vendor.com>", "1")

	b := r.start(true, nil)
	b.adapter.OnError(t.Context(), taskID, &a2a.Error{Message: "task timed out"}, nil)
	replies := b.replies.replies()
	if len(replies) != 1 || replies[0].BodyText != errorReplyText || replies[0].To != "alice@vendor.com" || replies[0].OriginalMessageID != "<m1@vendor.com>" {
		t.Fatalf("replies = %+v, want one error reply threaded under the mail", replies)
	}
}

func TestHIL_AnsweredThroughAnotherInstance(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	b := r.start(true, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sa := newHILServer(a.core, []byte("secret"), "https://gw.example.com", "test-gw", logger)
	sa.items = a.adapter.items
	sb := newHILServer(b.core, []byte("secret"), "https://gw.example.com", "test-gw", logger)
	sb.items = b.adapter.items

	taskID := "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c44"
	if err := a.core.Tasks().Register(taskID, "Agent", "u1", entrypoint.TaskIdentity{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	expiry := time.Now().Add(10 * time.Minute)
	if err := sa.save(t.Context(), "req-1", &pendingHIL{surface: emailSurfaceT(t), taskID: taskID, agentName: "Agent", userID: "u1", expiry: expiry}, json.RawMessage(emailAskSurfaceJSON)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := sa.peek("req-1"); ok {
		t.Fatal("with a gateway store the question is held in memory too")
	}
	token, err := signHILToken(hilToken{RequestID: "req-1", TaskID: taskID, AgentName: "Agent", UserID: "u1", ExpiresAt: expiry.Unix()}, sb.secret)
	if err != nil {
		t.Fatal(err)
	}

	getRec := httptest.NewRecorder()
	sb.Handler().ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), `type="radio"`) {
		t.Fatalf("GET on the other instance = %d, want the form", getRec.Code)
	}

	form := url.Values{"_btn": {"submit-btn"}, "f0": {"Blue"}}
	if rec := postForm(t, sb.Handler(), token, form); rec.Code != http.StatusOK {
		t.Fatalf("POST on the other instance = %d (body=%s), want 200", rec.Code, rec.Body.String())
	}
	uir := emailRecv(t, r.brk, r.agent)
	if uir["status"] != "answered" {
		t.Fatalf("status = %v, want answered", uir["status"])
	}
	for _, s := range []*hilServer{sa, sb} {
		if rec := postForm(t, s.Handler(), token, form); rec.Code != http.StatusGone {
			t.Errorf("replay = %d, want 410", rec.Code)
		}
	}
	getRec = httptest.NewRecorder()
	sa.Handler().ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if getRec.Code != http.StatusGone {
		t.Errorf("GET after the answer = %d, want 410", getRec.Code)
	}
}

func TestHIL_EndedTaskIsNoLongerWaiting(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newHILServer(a.core, []byte("secret"), "https://gw.example.com", "test-gw", logger)
	s.items = a.adapter.items
	taskID := "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"
	expiry := time.Now().Add(10 * time.Minute)
	token, err := signHILToken(hilToken{RequestID: "req-2", TaskID: taskID, AgentName: "Agent", UserID: "u1", ExpiresAt: expiry.Unix()}, s.secret)
	if err != nil {
		t.Fatal(err)
	}
	rec := postForm(t, s.Handler(), token, url.Values{"_btn": {"submit-btn"}, "f0": {"Blue"}})
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "No longer waiting") {
		t.Fatalf("POST for an ended task = %d %q, want 410 no longer waiting", rec.Code, rec.Body.String())
	}
}

func TestHIL_AnswerCarriesTheUsersIdentity(t *testing.T) {
	r := newEmailRig(t)
	a := r.start(true, nil)
	a.core.SetScopeResolver(platformUserScopeResolver{scopes: []string{rbac.AgentInvokeScope("Agent")}})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newHILServer(a.core, []byte("secret"), "https://gw.example.com", "test-gw", logger)
	s.items = a.adapter.items
	taskID := "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c47"
	if err := a.core.Tasks().Register(taskID, "Agent", "alice@vendor.com", entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(10 * time.Minute)
	if err := s.save(t.Context(), "req-9", &pendingHIL{surface: emailSurfaceT(t), taskID: taskID, agentName: "Agent", userID: "alice@vendor.com", expiry: expiry}, json.RawMessage(emailAskSurfaceJSON)); err != nil {
		t.Fatal(err)
	}
	token, err := signHILToken(hilToken{RequestID: "req-9", TaskID: taskID, AgentName: "Agent", UserID: "alice@vendor.com", ExpiresAt: expiry.Unix()}, s.secret)
	if err != nil {
		t.Fatal(err)
	}
	if rec := postForm(t, s.Handler(), token, url.Values{"_btn": {"submit-btn"}, "f0": {"Blue"}}); rec.Code != http.StatusOK {
		t.Fatalf("POST = %d (body=%s), want 200", rec.Code, rec.Body.String())
	}
	if uir := emailRecv(t, r.brk, r.agent); uir["status"] != "answered" {
		t.Fatalf("status = %v, want answered", uir["status"])
	}
}
