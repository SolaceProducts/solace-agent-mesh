// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func registerHILTasks(a *Adapter, phone string, taskIDs ...string) {
	for _, id := range taskIDs {
		_ = a.core.Tasks().Register(id, "Builder", "user-1", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata(id, &taskMeta{FromPhone: phone, SessionID: "sess"})
	}
}

func TestAdapter_NonContentTypesIgnored(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	for _, typ := range []string{"reaction", "system", "ephemeral"} {
		t.Run(typ, func(t *testing.T) {
			a, mock := newHILRig(t)
			seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{TaskID: taskT1, RequestID: "req-1"})

			a.deliver(&webhookChangeValue{}, &inboundMessage{From: phone, ID: "wamid." + typ, Type: typ})

			if n := len(mock.all()); n != 0 {
				t.Fatalf("%s must be ignored silently, got %d outbound sends", typ, n)
			}
			if pendingPromptRow(t, a, phone) == nil {
				t.Errorf("%s must not consume the pending prompt", typ)
			}
		})
	}
}

func TestAdapter_InteractiveWithoutPending_ExpiredNotice(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)

	value, msg := interactiveReply(phone, "wamid.stale", "opt:0")
	a.deliver(value, msg)

	bodies := mock.all()
	if len(bodies) != 1 {
		t.Fatalf("expected one expired-prompt notice, got %d", len(bodies))
	}
	if !strings.Contains(string(bodies[0]), "expired") {
		t.Fatalf("notice should say the prompt expired, got %s", bodies[0])
	}
}

func TestAdapter_ClearPendingHIL_TaskScoped(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, _ := newHILRig(t)
	registerHILTasks(a, phone, taskTA, taskTB)
	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{TaskID: taskTB, RequestID: "req-B"})

	done := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "task A done"}}}}}
	a.OnFinalResponse(t.Context(), taskTA, done, nil)

	if pendingPromptRow(t, a, phone) == nil {
		t.Fatal("task A finishing must not clear task B's pending prompt")
	}

	a.OnFinalResponse(t.Context(), taskTB, done, nil)
	if pendingPromptRow(t, a, phone) != nil {
		t.Fatal("task B finishing must clear its own pending prompt")
	}
}

func TestAdapter_HILExpiry_SubmitsTimedOut(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)
	registerHILTasks(a, phone, taskT1)

	reqData := a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface:   json.RawMessage(askSurfaceSingleChoiceWA),
		ExpiresAt: time.Now().UTC().Add(200 * time.Millisecond).Format(time.RFC3339Nano),
	}
	a.handleUserInputRequest(taskT1, &reqData)
	waitPendingPrompt(t, a, phone)

	expirePromptNow(t, a, phone)

	a.sweepExpiredPrompts()
	if pendingPromptRow(t, a, phone) != nil {
		t.Fatal("pending prompt should expire")
	}
	waitQueueDrained(t, a)
	bodies := mock.all()
	if len(bodies) < 2 || !strings.Contains(string(bodies[len(bodies)-1]), "timed out") {
		t.Fatalf("expected a timed-out notice after the prompt, got %d sends", len(bodies))
	}
}

func TestAdapter_HILReply_StaleContextKeepsPending(t *testing.T) {
	t.Parallel()
	a, mock := newHILRig(t)
	p := &pendingHIL{requestID: "r", taskID: taskT1, fromPhone: "155", surface: choiceSurface("Yes", "No"),
		promptWamid: "wamid.PROMPT"}

	msg := &inboundMessage{
		Type:        "interactive",
		Interactive: &inboundInteractive{Type: "button_reply", ButtonReply: &replyRef{ID: "opt:0"}},
		Context:     &messageContext{ID: "wamid.OLDER-PROMPT"},
	}
	if a.handleHILReply(p, msg) == outcomeSubmitted {
		t.Fatal("a reply to a different prompt must not resolve the pending one")
	}
	if len(mock.all()) != 1 {
		t.Fatalf("expected a stale-prompt notice, got %d sends", len(mock.all()))
	}

	msg.Context.ID = "wamid.PROMPT"
	if a.handleHILReply(p, msg) != outcomeSubmitted {
		t.Fatal("a reply carrying the matching prompt wamid must resolve")
	}
}

func TestAdapter_HILReply_OutOfRangeKeepsPending(t *testing.T) {
	t.Parallel()
	a, mock := newHILRig(t)
	p := &pendingHIL{requestID: "r", taskID: taskT1, fromPhone: "155", surface: choiceSurface("Yes", "No")}

	msg := &inboundMessage{Interactive: &inboundInteractive{Type: "button_reply", ButtonReply: &replyRef{ID: "opt:9"}}}
	if a.handleHILReply(p, msg) == outcomeSubmitted {
		t.Fatal("an out-of-range option must not silently submit an empty answer")
	}
	if len(mock.all()) != 1 {
		t.Fatalf("expected a couldn't-match notice, got %d sends", len(mock.all()))
	}
}

func TestAdapter_HILText_OtherFreeText(t *testing.T) {
	t.Parallel()
	a, _ := newHILRig(t)
	s := askSurface("Pizza", "Sushi")
	p := &pendingHIL{requestID: "r", taskID: taskT1, fromPhone: "155", surface: s}

	if a.handleHILText(p, "home-made lasagna") != outcomeSubmitted {
		t.Fatal("free text with an Other escape must resolve as the Other answer")
	}
}

func TestAdapter_HILText_OutOfRangeNumberStillRePrompts(t *testing.T) {
	t.Parallel()
	a, mock := newHILRig(t)
	p := &pendingHIL{requestID: "r", taskID: taskT1, fromPhone: "155", surface: askSurface("Pizza", "Sushi")}

	if a.handleHILText(p, "9") == outcomeSubmitted {
		t.Fatal("an out-of-range number must re-prompt, not become the Other answer")
	}
	if len(mock.all()) != 1 {
		t.Fatalf("expected a re-prompt, got %d sends", len(mock.all()))
	}
}

func TestAdapter_HILText_CancelKeyword(t *testing.T) {
	t.Parallel()
	a, _ := newHILRig(t)
	s := choiceSurface("Yes", "No")
	s.Buttons = []a2ui.Button{{Event: "submit"}, {Event: "cancel"}}
	p := &pendingHIL{requestID: "r", taskID: taskT1, fromPhone: "155", surface: s}

	if a.handleHILText(p, " Cancel ") != outcomeSubmitted {
		t.Fatal("typed cancel must resolve when the surface offers a cancel action")
	}

	noCancel := choiceSurface("Yes", "No")
	p2 := &pendingHIL{requestID: "r2", taskID: taskT2, fromPhone: "155", surface: noCancel}
	if a.handleHILText(p2, "cancel") == outcomeSubmitted {
		t.Fatal("typed cancel must NOT cancel a surface with no cancel action")
	}
}

func TestTextPrompt_HidesOtherAndShowsHints(t *testing.T) {
	s := askSurface("Pizza", "Sushi")
	s.Buttons = []a2ui.Button{{Event: "submit"}, {Event: "cancel"}}
	got := textPrompt(s)

	if strings.Contains(got, "3. Other") || strings.Contains(got, "Other\n") {
		t.Fatalf("prompt must not enumerate the Other sentinel:\n%s", got)
	}
	if !strings.Contains(got, "Or reply with your own answer.") {
		t.Fatalf("prompt should hint the free-text escape:\n%s", got)
	}
	if !strings.Contains(got, `Reply "cancel" to cancel.`) {
		t.Fatalf("prompt should hint the cancel keyword:\n%s", got)
	}
}

func TestSurfacePrompt_IncludesQuestion(t *testing.T) {
	cases := []struct {
		name    string
		surface *a2ui.Surface
		field   *a2ui.Field
		want    string
	}{
		{
			name:    "title and question both shown",
			surface: &a2ui.Surface{Title: "Pick one", Question: "Favorite color?"},
			field:   &a2ui.Field{Label: "Color"},
			want:    "Pick one\n\nFavorite color?\n\nColor",
		},
		{
			name:    "label repeating the question is not duplicated",
			surface: &a2ui.Surface{Title: "Pick one", Question: "Favorite color?"},
			field:   &a2ui.Field{Label: "Favorite color?"},
			want:    "Pick one\n\nFavorite color?",
		},
		{
			name:    "title only",
			surface: &a2ui.Surface{Title: "Agent wants to run a tool"},
			want:    "Agent wants to run a tool",
		},
		{
			name:    "question only",
			surface: &a2ui.Surface{Question: "Favorite color?"},
			want:    "Favorite color?",
		},
		{
			name:    "field label only",
			surface: &a2ui.Surface{},
			field:   &a2ui.Field{Label: "Color"},
			want:    "Color",
		},
		{
			name:    "nothing to show falls back",
			surface: &a2ui.Surface{},
			want:    "Please choose an option:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := surfacePrompt(tc.surface, tc.field); got != tc.want {
				t.Errorf("surfacePrompt() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdapter_SecondTaskSupersedesPendingPrompt(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)
	registerHILTasks(a, phone, taskTA, taskTB)

	ask := func(taskID, reqID string) {
		a.handleUserInputRequest(taskID, &a2a.UserInputRequestData{
			Type: "user_input_request", RequestID: reqID,
			Surface: json.RawMessage(askSurfaceSingleChoiceWA),
		})
	}
	ask(taskTA, "req-A")
	ask(taskTB, "req-B")

	waitQueueDrained(t, a)
	pending := waitPendingPrompt(t, a, phone)
	if pending.TaskID != taskTB {
		t.Fatalf("pending prompt task = %q, want tB (the newer one)", pending.TaskID)
	}
	waitMockSent(t, mock, msgPromptSuperseded,
		"the superseded question needs a user-visible notice: it stays in the chat above the new one")

	if _, claimed := a.hilDedup.Get("req-A"); !claimed {
		t.Error("the superseded prompt must have been answered (cancelled) so its agent stops waiting")
	}
}

func TestAdapter_DoubleTapSubmitsOnce(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, _ := newHILRig(t)
	registerHILTasks(a, phone, taskT1)

	p := &pendingHIL{requestID: "req-1", taskID: taskT1, agentName: "Builder", userID: "user-1",
		fromPhone: phone, surface: choiceSurface("Yes", "No")}

	if err := a.submitResponse(p, "answered", nil); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	err := a.submitResponse(p, "answered", nil)
	if !errors.Is(err, errHILAlreadyAnswered) {
		t.Fatalf("second submit err = %v, want errHILAlreadyAnswered", err)
	}
	if a.submitted(p, err) != outcomeSubmitted {
		t.Error("an already-answered prompt must count as resolved")
	}
}

func TestAdapter_HILText_OtherFreeTextOnNonAskSurface(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, _ := newHILRig(t)
	registerHILTasks(a, phone, taskT1)

	surface := &a2ui.Surface{Fields: []a2ui.Field{
		{Kind: a2ui.FieldSingleChoice, Path: "/selected_volume", Options: []a2ui.Option{
			{Label: "vol-a", Value: "vol-a"},
			{Label: "Other (enter ID manually)", Value: a2ui.OtherSentinel},
		}},
		{Kind: a2ui.FieldText, Path: "/manual_volume_id"},
	}}
	p := &pendingHIL{requestID: "req-1", taskID: taskT1, agentName: "Builder", userID: "user-1",
		fromPhone: phone, surface: surface}

	if a.handleHILText(p, "vol-manual-42") != outcomeSubmitted {
		t.Fatal("a manual volume id must be accepted as the Other answer, not re-prompted forever")
	}
}

func TestAdapter_ConcurrentFirstPromptsAllRetired(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	const prompts = 12
	a, mock := newHILRig(t)

	taskIDs := make([]string, prompts)
	for i := range taskIDs {
		taskIDs[i] = fmt.Sprintf("t%02d", i)
	}
	registerHILTasks(a, phone, taskIDs...)

	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(prompts)
	for i, id := range taskIDs {
		go func(taskID string, n int) {
			defer done.Done()
			start.Wait()
			a.handleUserInputRequest(taskID, &a2a.UserInputRequestData{
				Type: "user_input_request", RequestID: fmt.Sprintf("req-%02d", n),
				Surface: json.RawMessage(askSurfaceSingleChoiceWA),
			})
		}(id, i)
	}
	start.Done()
	done.Wait()

	waitQueueDrained(t, a)
	survivor := waitPendingPrompt(t, a, phone).RequestID

	var retired, unaccounted []string
	for i := range prompts {
		req := fmt.Sprintf("req-%02d", i)
		if req == survivor {
			continue
		}
		if _, claimed := a.hilDedup.Get(req); claimed {
			retired = append(retired, req)
			continue
		}
		unaccounted = append(unaccounted, req)
	}
	if len(unaccounted) != 0 {
		t.Errorf("%d displaced prompt(s) vanished with no cancellation and no notice: %v",
			len(unaccounted), unaccounted)
	}
	if len(retired) != prompts-1 {
		t.Errorf("retired = %d, want %d (every prompt but the survivor %s)", len(retired), prompts-1, survivor)
	}
	var notices int
	for _, b := range mock.all() {
		if strings.Contains(string(b), "no longer active") {
			notices++
		}
	}
	if notices != prompts-1 {
		t.Errorf("supersede notices = %d, want %d (each displaced prompt retired exactly once)", notices, prompts-1)
	}
}

func TestAdapter_ResolveDoesNotClearASiblingsPrompt(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, _ := newHILRig(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	registerHILTasks(a, phone, taskTA, taskTB)

	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{
		TaskID: taskTA, RequestID: "req-A", AgentName: "Builder", UserID: "user-1"})
	pA := &pendingHIL{requestID: "req-A", taskID: taskTA, agentName: "Builder", userID: "user-1",
		fromPhone: phone, surface: choiceSurface("Yes", "No")}

	a.handleUserInputRequest(taskTB, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-B",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	waitQueueDrained(t, a)
	if live := waitPendingPrompt(t, a, phone); live.TaskID != taskTB {
		t.Fatalf("precondition: task B's prompt must be live, got %+v", live)
	}

	_, msg := textMessage("wamid.replyA", phone, "Yes")
	a.resolvePendingHIL(pA, msg, true)

	after := pendingPromptRow(t, a, phone)
	if after == nil {
		t.Fatal("resolving task A's reply deleted task B's live prompt: B now hangs with no timeout and the user cannot answer")
	}
	if after.TaskID != taskTB || after.RequestID != "req-B" {
		t.Errorf("live prompt = task %s/%s, want tB/req-B", after.TaskID, after.RequestID)
	}
}
