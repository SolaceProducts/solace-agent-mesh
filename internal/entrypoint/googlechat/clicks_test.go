// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	clickNamespace     = "test-ns"
	requesterName      = "users/1"
	requesterEmail     = "ada@example.com"
	bystanderName      = "users/bystander"
	bystanderEmail     = "bob@example.com"
	noAgentRequestWait = 300 * time.Millisecond
)

type agentRequest struct {
	method          string
	userID          string
	cancelErrorCode string
	contextID       string
}

type clickRig struct {
	brk   *broker.DevBroker
	agent broker.Queue
}

func newClickRig(t *testing.T) *clickRig {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	q, err := brk.CreateQueue("click-agent", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic(clickNamespace, dmMeta().AgentName)); err != nil {
		t.Fatal(err)
	}
	return &clickRig{brk: brk, agent: q}
}

func (r *clickRig) replica(t *testing.T, db *store.DB, chat *fakeChat, authorizer auth.SenderAuthorizer) *Adapter {
	t.Helper()
	a := newTestAdapter(t, db, chat)
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": clickNamespace, "gateway_id": a.gatewayID})
	core.SetServices(runtime.NewRuntime(r.brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	core.SetIdentityAuthorizer(authorizer)
	a.core = core
	return a
}

func (r *clickRig) nextRequest(t *testing.T, wait time.Duration) (agentRequest, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	msg, err := r.brk.Receive(ctx, r.agent)
	if err != nil || msg == nil {
		return agentRequest{}, false
	}
	msg.Ack()
	var req struct {
		Method string `json:"method"`
		Params struct {
			Metadata map[string]any `json:"metadata"`
			Message  struct {
				ContextID string `json:"contextId"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("agent request is not JSON-RPC: %v", err)
	}
	code, _ := req.Params.Metadata[a2a.CancelErrorCodeKey].(string)
	return agentRequest{
		method: req.Method, userID: a2a.DecodeUserProperties(msg.UserProperties).UserID,
		cancelErrorCode: code, contextID: req.Params.Message.ContextID,
	}, true
}

func (r *clickRig) mustReceive(t *testing.T, method string) agentRequest {
	t.Helper()
	req, ok := r.nextRequest(t, 5*time.Second)
	if !ok {
		t.Fatalf("the agent received no %s request", method)
	}
	if req.method != method {
		t.Fatalf("the agent received %q, want %q", req.method, method)
	}
	return req
}

func (r *clickRig) mustReceiveNothing(t *testing.T) {
	t.Helper()
	if req, ok := r.nextRequest(t, noAgentRequestWait); ok {
		t.Fatalf("the agent received %+v, want nothing", req)
	}
}

func sharedMeta() taskMeta {
	return taskMeta{taskDestination: taskDestination{
		SpaceName: "spaces/S1", ThreadName: "spaces/S1/threads/T1", SenderName: requesterName,
		AgentName: dmMeta().AgentName, RunAsSwapped: true,
	}}
}

func clickBy(meta taskMeta, sender, onMessage string, action map[string]string) *inboundEvent {
	email := requesterEmail
	if sender != requesterName {
		email = bystanderEmail
	}
	return &inboundEvent{
		Kind: kindCardClick, Addressed: true, IsDM: meta.IsDM,
		SpaceName: meta.SpaceName, ThreadName: meta.ThreadName, MessageName: onMessage,
		SenderName: sender, SenderEmail: email, Action: action,
	}
}

func cancelClick(taskID string) map[string]string {
	return map[string]string{actionMethodParam: actionCancelTask, paramTaskID: taskID, paramAgentName: dmMeta().AgentName}
}

func hilClick(function, taskID string) map[string]string {
	return map[string]string{actionMethodParam: function, paramTaskID: taskID, paramAgentName: dmMeta().AgentName, paramRequestID: "req-1"}
}

func postCard(t *testing.T, chat *fakeChat, meta taskMeta) string {
	t.Helper()
	name, err := chat.CreateMessage(t.Context(), meta.SpaceName, meta.ThreadName, messageContent{FallbackText: hilDefaultTitle, Cards: card("sam-hil-req-1")})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestCancelClickKeepsTheStreamedAnswer(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	submitForTest(t, a, tid("task-s"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name

	a.OnStatusUpdate(t.Context(), tid("task-s"), textEvent("The first part of the answer"), nil)
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
		return len(texts) == 1 && texts[0] == "The first part of the answer"
	})

	if res := a.process(t.Context(), clickBy(dmMeta(), requesterName, status, cancelClick(tid("task-s")))); res.outcome != outcomeSubmitted {
		t.Fatalf("cancel outcome = %v, want submitted", res.outcome)
	}
	if req := rig.mustReceive(t, "tasks/cancel"); req.cancelErrorCode != "" {
		t.Errorf("the requester's stop named error code %q, want none: it is the user's own stop", req.cancelErrorCode)
	}
	writes, texts := chat.waitFor(t, func(w []fakeWrite, texts []string) bool {
		return controlStripped(w, status) && anyTextContains(texts, msgCancelling)
	})
	for _, w := range writes {
		if w.name == status && w.op == "update" && w.text != "" && w.text != "The first part of the answer" {
			t.Errorf("cancel rewrote the status message with %q, want only its control removed", w.text)
		}
	}
	if want := []string{"The first part of the answer", msgCancelling}; !slices.Equal(texts, want) {
		t.Errorf("messages = %q, want the streamed answer untouched and the acknowledgement after it, %q", texts, want)
	}
}

func controlStripped(writes []fakeWrite, name string) bool {
	return slices.ContainsFunc(writes, func(w fakeWrite) bool {
		return w.name == name && w.op == "update" && w.replacedCards && w.cards == 0 && w.text == ""
	})
}

func TestBystanderClickIsRefusedAndNeverReachesTheAgent(t *testing.T) {
	for name, action := range map[string]map[string]string{
		"cancel":        cancelClick(tid("task-b")),
		"answer":        hilClick(actionHILSubmit, tid("task-b")),
		"cancel answer": hilClick(actionHILCancel, tid("task-b")),
	} {
		t.Run(name, func(t *testing.T) {
			rig := newClickRig(t)
			chat := newFakeChat()
			a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
			submitForTest(t, a, tid("task-b"), sharedMeta())
			card := postCard(t, chat, sharedMeta())

			if res := a.process(t.Context(), clickBy(sharedMeta(), bystanderName, card, action)); res.outcome != outcomeDeclined {
				t.Fatalf("bystander outcome = %v, want declined", res.outcome)
			}
			chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgNotYourTask) })
			rig.mustReceiveNothing(t)
		})
	}
}

func TestClickOnAnotherReplicaIsAuthorizedFromTheTaskItems(t *testing.T) {
	const taskID = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a71"
	rig := newClickRig(t)
	db := store.TestDB(t)
	chat := newFakeChat()
	submitter := rig.replica(t, db, chat, matchedAuthorizer())
	other := rig.replica(t, db, chat, matchedAuthorizer())
	submitForTest(t, submitter, taskID, sharedMeta())
	card := postCard(t, chat, sharedMeta())

	if res := other.process(t.Context(), clickBy(sharedMeta(), bystanderName, card, hilClick(actionHILSubmit, taskID))); res.outcome != outcomeDeclined {
		t.Fatalf("bystander outcome on the other replica = %v, want declined", res.outcome)
	}
	rig.mustReceiveNothing(t)

	if res := other.process(t.Context(), clickBy(sharedMeta(), requesterName, card, hilClick(actionHILSubmit, taskID))); res.outcome != outcomeSubmitted {
		t.Fatalf("requester outcome on the other replica = %v, want submitted", res.outcome)
	}
	rig.mustReceive(t, "message/send")
}

func TestAnswerIsSentAsTheTaskRunAsIdentity(t *testing.T) {
	ownIdentityInSpace := sharedMeta()
	ownIdentityInSpace.RunAsSwapped = false
	for name, tc := range map[string]struct {
		meta  taskMeta
		runAs string
		want  string
	}{
		"shared space":                    {meta: sharedMeta(), want: rbac.SystemUserPrincipal("")},
		"shared space, configured run_as": {meta: sharedMeta(), runAs: "svc-bot", want: rbac.SystemUserPrincipal("svc-bot")},
		"shared space, user identity":     {meta: ownIdentityInSpace, runAs: "svc-bot", want: requesterEmail},
		"direct message":                  {meta: dmMeta(), runAs: "svc-bot", want: requesterEmail},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newClickRig(t)
			chat := newFakeChat()
			a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
			a.cfg.RunAs = tc.runAs
			submitForTest(t, a, tid("task-r"), tc.meta)
			card := postCard(t, chat, tc.meta)

			if res := a.process(t.Context(), clickBy(tc.meta, requesterName, card, hilClick(actionHILSubmit, tid("task-r")))); res.outcome != outcomeSubmitted {
				t.Fatalf("answer outcome = %v, want submitted", res.outcome)
			}
			if got := rig.mustReceive(t, "message/send").userID; got != tc.want {
				t.Errorf("answer sent as %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnswerAfterARunAsChangeIsAttributedToTheTaskUser(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	a.cfg.RunAs = ""
	submitForTest(t, a, tid("task-ra"), sharedMeta())
	if err := a.core.Tasks().Register(tid("task-ra"), sharedMeta().AgentName, rbac.SystemUserPrincipal(""), entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	card := postCard(t, chat, sharedMeta())
	a.cfg.RunAs = "svc-bot"

	if res := a.process(t.Context(), clickBy(sharedMeta(), bystanderName, card, hilClick(actionHILSubmit, tid("task-ra")))); res.outcome != outcomeDeclined {
		t.Fatalf("bystander outcome = %v, want declined", res.outcome)
	}
	rig.mustReceiveNothing(t)

	if res := a.process(t.Context(), clickBy(sharedMeta(), requesterName, card, hilClick(actionHILSubmit, tid("task-ra")))); res.outcome != outcomeSubmitted {
		t.Fatalf("asker outcome = %v, want submitted", res.outcome)
	}
	if got := rig.mustReceive(t, "message/send").userID; got != rbac.SystemUserPrincipal("") {
		t.Errorf("answer sent as %q, want the task's user %q", got, rbac.SystemUserPrincipal(""))
	}
}

func TestAnswerFromAnotherSpaceIsNotAttributedToTheTaskUser(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	submitForTest(t, a, tid("task-sp"), sharedMeta())
	if err := a.core.Tasks().Register(tid("task-sp"), sharedMeta().AgentName, "owner@dev.local", entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	card := postCard(t, chat, sharedMeta())
	elsewhere := sharedMeta()
	elsewhere.SpaceName = "spaces/S2"

	if res := a.process(t.Context(), clickBy(elsewhere, requesterName, card, hilClick(actionHILSubmit, tid("task-sp")))); res.outcome == outcomeSubmitted {
		t.Fatalf("a click from another space was submitted (outcome %v)", res.outcome)
	}
	rig.mustReceiveNothing(t)
}

func TestAnswerGoesToTheTaskAgentNotTheButtons(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	submitForTest(t, a, tid("task-g"), dmMeta())
	card := postCard(t, chat, dmMeta())
	action := hilClick(actionHILSubmit, tid("task-g"))
	action[paramAgentName] = "some_other_agent"

	if res := a.process(t.Context(), clickBy(dmMeta(), requesterName, card, action)); res.outcome != outcomeSubmitted {
		t.Fatalf("answer outcome = %v, want submitted", res.outcome)
	}
	rig.mustReceive(t, "message/send")
}

func TestCancelGoesToTheTaskAgentNotTheButtons(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	submitForTest(t, a, tid("task-gc"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	action := cancelClick(tid("task-gc"))
	action[paramAgentName] = "some_other_agent"

	if res := a.process(t.Context(), clickBy(dmMeta(), requesterName, writes[0].name, action)); res.outcome != outcomeSubmitted {
		t.Fatalf("cancel outcome = %v, want submitted", res.outcome)
	}
	rig.mustReceive(t, "tasks/cancel")
}

func TestClickedCardReplaceFailureIsWarnedUnlessGone(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := jsonLogsOf(a)
	card := postCard(t, chat, dmMeta())

	a.replaceClickedCard(&inboundEvent{SpaceName: dmMeta().SpaceName, MessageName: dmMeta().SpaceName + "/messages/deleted"}, msgHILSubmitted, nil)
	a.bgWG.Wait()
	if strings.Contains(logs.String(), "clicked card could not be replaced") {
		t.Fatalf("a deleted message was warned about:\n%s", logs.String())
	}

	chat.mu.Lock()
	chat.failures = []error{&permanentError{err: errors.New("bad request")}}
	chat.mu.Unlock()
	a.replaceClickedCard(&inboundEvent{SpaceName: dmMeta().SpaceName, MessageName: card}, msgHILSubmitted, nil)
	a.bgWG.Wait()
	if !strings.Contains(logs.String(), `"level":"WARN","msg":"google chat clicked card could not be replaced"`) {
		t.Errorf("a rejected card replacement was not warned about:\n%s", logs.String())
	}
}

func TestRedeliveredClickIsHandledOnce(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	submitForTest(t, a, tid("task-d"), dmMeta())
	card := postCard(t, chat, dmMeta())

	payload, err := json.Marshal(map[string]any{
		"type":    eventTypeCardClicked,
		"space":   map[string]any{"name": "spaces/DM1", "type": legacySpaceTypeDM, "spaceType": spaceTypeDM, "singleUserBotDm": true},
		"user":    map[string]any{"name": requesterName, "email": requesterEmail, "type": "HUMAN"},
		"message": map[string]any{"name": card, "thread": map[string]any{"name": dmMeta().ThreadName}},
		"common":  map[string]any{"invokedFunction": actionHILSubmit, "parameters": hilClick(actionHILSubmit, tid("task-d"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		ev, err := parseEvent(payload)
		if err != nil {
			t.Fatal(err)
		}
		job := ingressJob{msg: pulledMessage{AckID: "click#" + strconv.Itoa(attempt), MessageID: "click", Data: payload}, ev: ev}
		if got := runIngressJob(t, a, job); !got.acked {
			t.Fatalf("delivery %d settled %+v, want ack", attempt, got)
		}
	}

	rig.mustReceive(t, "message/send")
	rig.mustReceiveNothing(t)
}

func TestRequesterClickAfterTheTaskEndedSaysItEnded(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	logs := jsonLogsOf(a)
	st := submitForTest(t, a, tid("task-e"), dmMeta())
	writes, before := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	card := postCard(t, chat, dmMeta())
	st.terminal.Store(true)

	if res := a.process(t.Context(), clickBy(dmMeta(), requesterName, card, hilClick(actionHILSubmit, tid("task-e")))); res.outcome != outcomeDeclined {
		t.Fatalf("answer outcome = %v, want declined", res.outcome)
	}
	if res := a.process(t.Context(), clickBy(dmMeta(), requesterName, status, cancelClick(tid("task-e")))); res.outcome != outcomeDeclined {
		t.Fatalf("cancel outcome = %v, want declined", res.outcome)
	}
	writes, texts := chat.waitFor(t, func(w []fakeWrite, texts []string) bool {
		return strings.Count(strings.Join(texts, "\n"), msgRequestEnded) == 2 && controlStripped(w, status)
	})
	a.bgWG.Wait()
	rig.mustReceiveNothing(t)

	if anyTextContains(texts, msgNotYourTask) {
		t.Errorf("messages = %q, want the requester told the request ended, not refused", texts)
	}
	if texts[0] != before[0] {
		t.Errorf("status message = %q, want it kept as %q", texts[0], before[0])
	}
	if !slices.ContainsFunc(writes, func(w fakeWrite) bool { return w.name == card && w.text == msgRequestEnded }) {
		t.Errorf("the question card was not retired: %+v", writes)
	}
	if strings.Contains(logs.String(), "clicker did not start the task") {
		t.Errorf("a click on an ended task was logged as a refused clicker:\n%s", logs.String())
	}
}

func TestClickOnATaskWithNoTaskItemsLeavesTheCard(t *testing.T) {
	rig := newClickRig(t)
	db := store.TestDB(t)
	chat := newFakeChat()
	submitter := rig.replica(t, db, chat, matchedAuthorizer())
	other := rig.replica(t, db, chat, matchedAuthorizer())
	logs := recordLogs(other)
	submitForTest(t, submitter, tid("task-n"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	card := postCard(t, chat, dmMeta())
	if err := store.NewEntrypointTaskItemRepo(db).Delete(t.Context(), tid("task-n"), itemDestination); err != nil {
		t.Fatal(err)
	}

	for _, click := range []*inboundEvent{
		clickBy(dmMeta(), requesterName, card, hilClick(actionHILSubmit, tid("task-n"))),
		clickBy(dmMeta(), requesterName, status, cancelClick(tid("task-n"))),
	} {
		if res := other.process(t.Context(), click); res.outcome != outcomeDeclined {
			t.Fatalf("%s outcome = %v, want declined", click.Action[actionMethodParam], res.outcome)
		}
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
		return strings.Count(strings.Join(texts, "\n"), msgRequestNotFound) == 2
	})
	other.bgWG.Wait()
	rig.mustReceiveNothing(t)

	writes, _ = chat.snapshot()
	for _, w := range writes {
		if (w.name == card || w.name == status) && w.op == "update" {
			t.Errorf("a card was rewritten (%+v) for a task that may still be live", w)
		}
	}
	if n := logs.count(slog.LevelWarn, "google chat card click on a task with no task items"); n != 2 {
		t.Errorf("no-items warnings = %d, want one per click", n)
	}
}

func TestUnresolvedClickStandingIsRefused(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	var unresolved clickStanding

	res, refused := a.refuseClick(clickBy(dmMeta(), requesterName, "", cancelClick(tid("task-0"))), tid("task-0"), unresolved)
	if !refused || res.outcome != outcomeDeclined {
		t.Errorf("unresolved standing: refused=%v outcome=%v, want a declined refusal", refused, res.outcome)
	}
}

func TestClickWithNoSenderIsRefused(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	meta := dmMeta()
	meta.SenderName = ""
	submitForTest(t, a, tid("task-0"), meta)
	card := postCard(t, chat, meta)
	click := clickBy(meta, requesterName, card, hilClick(actionHILSubmit, tid("task-0")))
	click.SenderName = ""

	if res := a.process(t.Context(), click); res.outcome != outcomeDeclined {
		t.Fatalf("outcome = %v, want declined", res.outcome)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgNotYourTask) })
	rig.mustReceiveNothing(t)
}

func TestAnswerRefusedByAuthorizationIsAnAuthorizationOutcome(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	unscoped := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := rig.replica(t, store.TestDB(t), chat, unscoped)
	a.core.SetAuthzEnabled(true)
	submitForTest(t, a, tid("task-z"), dmMeta())
	card := postCard(t, chat, dmMeta())

	res := a.process(t.Context(), clickBy(dmMeta(), requesterName, card, hilClick(actionHILSubmit, tid("task-z"))))
	if res.outcome != outcomeUnauthorized {
		t.Fatalf("outcome = %v, want unauthorized", res.outcome)
	}
	if res.decline == "" || res.decline == msgHILSubmitFailed || res.decline == entrypoint.SubmitFailedText {
		t.Errorf("decline = %q, want the refusal wording, not a try-again failure", res.decline)
	}
	rig.mustReceiveNothing(t)
}

func TestUndecodableFormClickIsDropped(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replica(t, store.TestDB(t), chat, matchedAuthorizer())
	logs := jsonLogsOf(a)
	submitForTest(t, a, tid("task-u"), dmMeta())
	card := postCard(t, chat, dmMeta())
	action := hilClick(actionHILSubmit, tid("task-u"))
	action[paramFields] = "{not json"

	if res := a.process(t.Context(), clickBy(dmMeta(), requesterName, card, action)); res.outcome != outcomeIgnored {
		t.Fatalf("outcome = %v, want ignored", res.outcome)
	}
	rig.mustReceiveNothing(t)
	a.bgWG.Wait()
	writes, _ := chat.snapshot()
	for _, w := range writes {
		if w.name == card && w.op == "update" {
			t.Errorf("the card was rewritten (%+v) for a click that was dropped", w)
		}
	}
	if !strings.Contains(logs.String(), "undecodable form description") {
		t.Errorf("the dropped click was not logged:\n%s", logs.String())
	}
}
