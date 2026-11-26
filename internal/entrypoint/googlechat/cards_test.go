// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func testSurface() *a2ui.Surface {
	return &a2ui.Surface{
		Title:    "Deploy approval",
		Question: "Which environments?",
		Fields: []a2ui.Field{
			{Kind: a2ui.FieldMultiChoice, Path: "/answers/envs", Label: "Environments", Options: []a2ui.Option{
				{Label: "Staging", Value: "staging, eu"}, {Label: "Production", Value: "prod"},
			}},
			{Kind: a2ui.FieldSingleChoice, Path: "/answers/when", Options: []a2ui.Option{{Value: "now"}, {Value: "later"}}},
			{Kind: a2ui.FieldText, Path: "/answers/why", Label: "Reason", Multiline: true},
		},
		Buttons: []a2ui.Button{
			{Label: "Approve", Event: "submit", Context: map[string]any{"answers": map[string]any{"path": "/answers"}}},
			{Event: "cancel"},
		},
	}
}

func buttonParams(t *testing.T, cards []*chat.CardWithId, label string) map[string]string {
	t.Helper()
	for _, sec := range cards[0].Card.Sections {
		for _, w := range sec.Widgets {
			if w.ButtonList == nil {
				continue
			}
			for _, b := range w.ButtonList.Buttons {
				if b.Text != label || b.OnClick == nil || b.OnClick.Action == nil {
					continue
				}
				params := map[string]string{actionMethodParam: b.OnClick.Action.Function}
				for _, p := range b.OnClick.Action.Parameters {
					params[p.Key] = p.Value
				}
				return params
			}
		}
	}
	t.Fatalf("no button %q in card", label)
	return nil
}

func TestHILCard_RoundTripsFormValuesToTheDataModel(t *testing.T) {
	cards, _ := hilCard(testSurface(), "req-1", tid("task-1"), "agent-a")
	if cards[0].Card.Header.Title != "Deploy approval" {
		t.Errorf("header = %+v", cards[0].Card.Header)
	}

	approve := buttonParams(t, cards, "Approve")
	if approve[actionMethodParam] != actionHILSubmit || approve[paramRequestID] != "req-1" || approve[paramTaskID] != tid("task-1") || approve[paramAgentName] != "agent-a" {
		t.Fatalf("approve params = %v", approve)
	}
	if cancel := buttonParams(t, cards, cancelButtonLabel); cancel[actionMethodParam] != actionHILCancel {
		t.Errorf("cancel params = %v", cancel)
	}

	data, err := hilResponseData(approve, map[string][]string{
		"hil_0": {"o0", "o1"},
		"hil_1": {"o1"},
		"hil_2": {"ship it"},
	})
	if err != nil {
		t.Fatalf("hilResponseData: %v", err)
	}
	got, _ := json.Marshal(data)
	want := `{"answers":{"envs":["staging, eu","prod"],"when":"later","why":"ship it"}}`
	if string(got) != want {
		t.Errorf("data = %s, want %s (synthetic values mapped back, commas in option values intact)", got, want)
	}
}

func TestHILResponseData_RejectsTamperedFieldDescription(t *testing.T) {
	if _, err := hilResponseData(map[string]string{paramFields: "{not json"}, nil); err == nil {
		t.Error("an undecodable field description must be rejected")
	}
}

func TestStatusMessageCarriesCancelUntilTheTaskEnds(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-c"), dmMeta())

	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	if writes[0].cards != 1 {
		t.Fatalf("placeholder = %+v, want it posted with the cancel control", writes[0])
	}

	a.OnFinalResponse(t.Context(), tid("task-c"), completedTask("Done."), nil)
	writes, texts := chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
		last := w[len(w)-1]
		return last.replacedCards && last.cards == 0
	})
	if last := writes[len(writes)-1]; last.text != "" {
		t.Errorf("controls cleared with text %q, want a cards-only update", last.text)
	}
	if len(texts) != 1 || texts[0] != "Done." {
		t.Errorf("messages = %q, want the answer kept once the controls are cleared", texts)
	}
}

func TestAddOnTaskOverPubSubGetsNoButtons(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.cfg.FeedbackEnabled = true
	meta := dmMeta()
	meta.SessionID, meta.AddOn = "sess-1", true
	st := &taskState{meta: meta}
	st.queue = a.newQueue(tid("task-a"), st, a.taskControls(tid("task-a"), meta))
	a.trackTask(tid("task-a"), st)
	st.queue.Placeholder(a.cfg.InitialStatusMessage)

	a.OnFinalResponse(t.Context(), tid("task-a"), completedTask("Done."), nil)
	writes, _ := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "Done.") })
	for _, w := range writes {
		if w.cards != 0 {
			t.Errorf("write %+v carries cards, want none for an add-on app over pubsub", w)
		}
	}
}

func finishFeedbackTask(t *testing.T, answer string, failCardAdd error) *fakeChat {
	t.Helper()
	chat := newFakeChat()
	chat.failCardAdd = failCardAdd
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.cfg.FeedbackEnabled = true
	meta := dmMeta()
	meta.SessionID = "sess-1"
	submitForTest(t, a, tid("task-f"), meta)

	a.OnFinalResponse(t.Context(), tid("task-f"), completedTask(answer), nil)
	waitTerminalWritten(t, a)
	return chat
}

func createdNames(writes []fakeWrite) []string {
	var names []string
	for _, w := range writes {
		if w.op == "create" {
			names = append(names, w.name)
		}
	}
	return names
}

func assertFinalCards(t *testing.T, chat *fakeChat, want ...[]string) {
	t.Helper()
	got := chat.cardsByMessage()
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cards on each message = %q, want %q", got, want)
	}
}

func TestFeedbackButtonsRideOnTheAnswerMessage(t *testing.T) {
	chat := finishFeedbackTask(t, "Done.", nil)
	writes, _ := chat.snapshot()

	if created := createdNames(writes); len(created) != 1 {
		t.Fatalf("messages created = %v, want only the answer message", created)
	}
	assertFinalCards(t, chat, []string{feedbackCardID})
	for _, w := range writes {
		if w.fallback == feedbackPrompt {
			t.Errorf("feedback card posted as its own message: %+v", w)
		}
	}
}

func TestFeedbackButtonsRideOnTheLastMessageOfALongAnswer(t *testing.T) {
	long := strings.Repeat("Resilience is a skill built through connection. ", maxMessageRunes/20)
	chat := finishFeedbackTask(t, long, nil)

	cards := chat.cardsByMessage()
	if len(cards) < 2 {
		t.Fatalf("messages = %d, want the answer split across several", len(cards))
	}
	want := make([][]string, len(cards))
	want[len(want)-1] = []string{feedbackCardID}
	assertFinalCards(t, chat, want...)
}

func TestFeedbackButtonsAreAPostedMessageWhenTheAnswerRefusesThem(t *testing.T) {
	long := strings.Repeat("Resilience is a skill built through connection. ", maxMessageRunes/20)
	for name, refusal := range map[string]error{
		"update refused":      &permanentError{err: errors.New("rejected")},
		"answer message gone": fmt.Errorf("%w: m1", errMessageGone),
	} {
		for answerName, tc := range map[string]struct {
			answer      string
			minMessages int
		}{
			"one message":  {"Done.", 2},
			"split answer": {long, 3},
		} {
			t.Run(name+"/"+answerName, func(t *testing.T) {
				chat := finishFeedbackTask(t, tc.answer, refusal)
				writes, _ := chat.snapshot()

				posted := slices.IndexFunc(writes, func(w fakeWrite) bool { return w.op == "create" && w.fallback == feedbackPrompt })
				if posted < 0 || writes[posted].cards != 1 || writes[posted].text != "" {
					t.Fatalf("feedback card was not posted as a card-only message; writes=%+v", writes)
				}
				cards := chat.cardsByMessage()
				if len(cards) < tc.minMessages {
					t.Fatalf("messages = %d, want at least %d", len(cards), tc.minMessages)
				}
				want := make([][]string, len(cards))
				want[len(want)-1] = []string{feedbackCardID}
				assertFinalCards(t, chat, want...)
			})
		}
	}
}

const askSurfaceJSON = `{
  "surfaceId":"req-9",
  "components":[
    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
    {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Color"},"variant":"mutuallyExclusive",
      "options":[{"label":{"literalString":"Red"},"value":"Red"},{"label":{"literalString":"Blue"},"value":"Blue"}],
      "value":{"path":"/answers/q0"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}}
  ],
  "dataModel":{"answers":{"q0":""}}
}`

func TestInputRequestIsPostedAsCard(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-h"), dmMeta())

	event := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
			"type": "user_input_request", "request_id": "req-9", "surface": json.RawMessage(askSurfaceJSON),
		})}},
	}}
	a.OnStatusUpdate(t.Context(), tid("task-h"), event, nil)

	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
		for _, x := range w {
			if x.op == "create" && x.cards == 1 && x.fallback != "" {
				return true
			}
		}
		return false
	})
	if last := writes[len(writes)-1]; last.text != "" || (last.fallback != "Favorite color?" && last.fallback != hilDefaultTitle) {
		t.Errorf("card message text = %q, fallback = %q; want the title only as fallback, so it is not shown twice", last.text, last.fallback)
	}
}

func TestCancelClickFromBystanderIsRefused(t *testing.T) {
	chat := newFakeChat()
	a := adapterWithAuthorizer(t, &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}})
	a.writer = newChatWriter(chat, newSpaceThrottle(0), a.logger)
	shared := dmMeta()
	shared.IsDM = false
	submitForTest(t, a, tid("task-b"), shared)

	click := &inboundEvent{
		Kind: kindCardClick, SpaceName: shared.SpaceName, ThreadName: shared.ThreadName,
		SenderName: "users/bystander", SenderEmail: "bob@example.com",
		Action: map[string]string{actionMethodParam: actionCancelTask, paramTaskID: tid("task-b"), paramAgentName: "agent"},
	}
	if res := a.process(t.Context(), click); res.outcome != outcomeDeclined {
		t.Fatalf("bystander cancel outcome = %v, want declined", res.outcome)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgNotYourTask) })
}

func TestAuthorizeCardOpensTheAuthURI(t *testing.T) {
	const uri = "https://idp.example.com/authorize?state=abc&client_id=sam"
	cards := authorizeCard("Sign in to continue.", uri)
	for _, w := range cards[0].Card.Sections[0].Widgets {
		if w.ButtonList == nil {
			continue
		}
		link := w.ButtonList.Buttons[0].OnClick.OpenLink
		if link == nil || link.Url != uri {
			t.Fatalf("button opens %+v, want %q", link, uri)
		}
		return
	}
	t.Fatal("the sign-in card has no button")
}

func TestFeedbackCardOnlyFollowsACompletedTask(t *testing.T) {
	for name, state := range map[string]a2a.TaskState{"cancelled": a2a.TaskStateCancelled, "failed": a2a.TaskStateFailed} {
		t.Run(name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			a.cfg.FeedbackEnabled = true
			meta := dmMeta()
			meta.SessionID = "sess-1"
			submitForTest(t, a, tid("task-n"), meta)

			a.OnFinalResponse(t.Context(), tid("task-n"), &a2a.Task{Status: a2a.TaskStatus{State: state}}, nil)

			writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
				return len(w) > 0 && w[len(w)-1].replacedCards && w[len(w)-1].cards == 0
			})
			for _, w := range writes {
				if w.fallback == feedbackPrompt {
					t.Errorf("a %s task was followed by the feedback card: %+v", name, w)
				}
			}
		})
	}
}

func TestFeedbackDetailsCardShowsTheDataUseNoticeAndNoCheckbox(t *testing.T) {
	cards := feedbackDetailsCard(tid("task-c"), "s", ratingUp, requesterName)
	var noticeShown bool
	for _, c := range cards {
		for _, sec := range c.Card.Sections {
			for _, w := range sec.Widgets {
				if w.TextParagraph != nil && w.TextParagraph.Text == entrypoint.FeedbackDataUseNotice {
					noticeShown = true
				}
				if w.SelectionInput != nil {
					t.Errorf("the details card has a selection input %q; consent is no longer asked for", w.SelectionInput.Name)
				}
			}
		}
	}
	if !noticeShown {
		t.Errorf("the details card does not show %q", entrypoint.FeedbackDataUseNotice)
	}
}
