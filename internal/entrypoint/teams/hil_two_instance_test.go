// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const (
	promptRequestID = "req-approve-1"
	approveClickID  = "1792767299001"
	denyClickID     = "1792767299002"
)

func (inst *teamsInstances) agentAsks(t *testing.T, taskID string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateInputNeeded, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type": "user_input_request", "request_id": promptRequestID, "source": "tool_approval",
				"surface": json.RawMessage(teamsAskSurfaceJSON),
			})},
		}},
	})
}

func (f *fakeBotConnector) promptCards() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var posters []int
	for i, p := range f.posts {
		for _, att := range p.Attachments {
			if att.ContentType == "application/vnd.microsoft.card.adaptive" {
				posters = append(posters, f.posters[i])
			}
		}
	}
	return posters
}

func (inst *teamsInstances) waitRecorded(t *testing.T, taskID, item string) {
	t.Helper()
	waitUntil(t, "the task's "+itemKind(item)+" item is written", func() bool {
		_, ok, _ := inst.adapters[0].items.Get(t.Context(), taskID, item)
		return ok
	})
}

func promptClick(t *testing.T, a *TeamsAdapter, id, taskID, action string) map[string]any {
	t.Helper()
	card, _ := a.buildHILCard(parseTeamsSurfaceT(t), promptRequestID, taskID, twoInstanceAgent, false)
	value := submitActionData(t, card, action)
	if action == HILSubmitAction {
		value["hil_0"] = "o0"
	}
	activity := dmActivity(id)
	delete(activity, "text")
	activity["value"] = value
	return activity
}

func (f *fakeBotConnector) replies(id string) []string {
	var texts []string
	for _, p := range f.postedActivities() {
		if p.ReplyToID == id {
			texts = append(texts, p.Text)
		}
	}
	return texts
}

func TestTwoInstances_RedeliveredPromptPostsOneCard(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	_ = inst.clients[2].Disconnect(t.Context())

	inst.agentAsks(t, taskID)
	waitUntil(t, "the prompt card is posted", func() bool { return len(inst.bot.promptCards()) > 0 })
	inst.waitRecorded(t, taskID, itemHILPromptedPrefix+promptRequestID)
	_ = inst.clients[inst.bot.promptCards()[0]].Disconnect(t.Context())

	inst.agentAsks(t, taskID)
	inst.agentCompletes(t, taskID)
	inst.bot.waitChanged(t, activityChange{Method: "DELETE", ID: statusID})
	if cards := inst.bot.promptCards(); len(cards) != 1 {
		t.Errorf("prompt cards posted by instances %v, want one", cards)
	}
}

func TestTwoInstances_PromptAnsweredOnceAcrossInstances(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1", n)
	}

	deliver(t, inst.adapters[0], promptClick(t, inst.adapters[0], approveClickID, taskID, HILSubmitAction))
	if n := inst.published(t); n != 1 {
		t.Fatalf("answers forwarded after the approval = %d, want 1", n)
	}
	if got := inst.bot.replies(approveClickID); !slices.Equal(got, []string{"✅ Response submitted."}) {
		t.Errorf("replies to the approval = %q", got)
	}

	deliver(t, inst.adapters[1], promptClick(t, inst.adapters[1], denyClickID, taskID, HILCancelAction))
	deliver(t, inst.adapters[0], promptClick(t, inst.adapters[0], denyClickID+"0", taskID, HILCancelAction))
	if n := inst.published(t); n != 0 {
		t.Errorf("answers forwarded after the approval was forwarded = %d, want 0", n)
	}
	for _, id := range []string{denyClickID, denyClickID + "0"} {
		if got := inst.bot.replies(id); !slices.Equal(got, []string{msgHILAlreadyAnswered}) {
			t.Errorf("replies to the later press %s = %q, want only %q", id, got, msgHILAlreadyAnswered)
		}
	}
}

func TestTwoInstances_FailedForwardLetsAnotherInstanceAnswer(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	inst.published(t)
	_ = inst.clients[0].Disconnect(t.Context())

	deliver(t, inst.adapters[0], promptClick(t, inst.adapters[0], approveClickID, taskID, HILSubmitAction))
	if got := inst.bot.replies(approveClickID); !slices.Equal(got, []string{"⚠️ Your response couldn't be submitted — please try again."}) {
		t.Fatalf("replies to the failed approval = %q", got)
	}

	deliver(t, inst.adapters[1], promptClick(t, inst.adapters[1], denyClickID, taskID, HILSubmitAction))
	if n := inst.published(t); n != 1 {
		t.Errorf("answers forwarded = %d, want 1", n)
	}
	if got := inst.bot.replies(denyClickID); !slices.Equal(got, []string{"✅ Response submitted."}) {
		t.Errorf("replies to the retry = %q", got)
	}
}

func TestTwoInstances_ClaimOfAnInstanceThatDiedLapses(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	inst.published(t)
	answerItem := hilAnswerItem(promptRequestID)
	claimedAt := func(age time.Duration) {
		at := strconv.FormatInt(time.Now().Add(-age).UnixMilli(), 10)
		if err := inst.adapters[0].items.Put(t.Context(), taskID, answerItem, at); err != nil {
			t.Fatal(err)
		}
	}

	claimedAt(time.Second)
	deliver(t, inst.adapters[0], promptClick(t, inst.adapters[0], approveClickID, taskID, HILSubmitAction))
	if n := inst.published(t); n != 0 {
		t.Fatalf("answers forwarded under a fresh claim = %d, want 0", n)
	}
	if got := inst.bot.replies(approveClickID); len(got) != 0 {
		t.Errorf("replies under a fresh claim = %q, want none", got)
	}

	claimedAt(hilAnswerLease + time.Second)
	deliver(t, inst.adapters[0], promptClick(t, inst.adapters[0], denyClickID, taskID, HILSubmitAction))
	if n := inst.published(t); n != 1 {
		t.Errorf("answers forwarded after the claim lapsed = %d, want 1", n)
	}
	if held, _, _ := inst.adapters[0].items.Get(t.Context(), taskID, answerItem); held != hilAnswerForwarded {
		t.Errorf("claim after the forward = %q, want %q", held, hilAnswerForwarded)
	}
}

func TestTwoInstances_OwnerCheckOnTheInstanceThatDidNotSubmit(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	inst.published(t)
	other := inst.adapters[0]

	want := entrypoint.ChatPlace{Conversation: twoInstanceConversation, User: "29:user"}
	if got := other.askedPlace(taskID); got != want {
		t.Fatalf("asked place on the other instance = %+v, want %+v", got, want)
	}

	stranger := promptClick(t, other, denyClickID, taskID, HILSubmitAction)
	stranger["from"] = map[string]any{"id": "29:stranger", "name": "Stranger", "aadObjectId": "aad-stranger"}
	deliver(t, other, stranger)
	if got := inst.bot.replies(denyClickID); !slices.Equal(got, []string{msgHILNotYours}) {
		t.Errorf("replies to the stranger = %q, want only %q", got, msgHILNotYours)
	}

	deliver(t, other, promptClick(t, other, approveClickID, taskID, HILSubmitAction))
	if n := inst.published(t); n != 1 {
		t.Errorf("answers forwarded = %d, want only the asker's", n)
	}
}

func TestTwoInstances_PressAfterTheTaskEndedLogsNoClaimFailure(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	logs := &lockedBuffer{}
	late := inst.adapters[0]
	late.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	taskID := submitDM(t, inst.adapters[1])
	inst.published(t)

	deliver(t, inst.adapters[1], promptClick(t, inst.adapters[1], approveClickID, taskID, HILSubmitAction))
	inst.agentCompletes(t, taskID)
	waitUntil(t, "the task's items are deleted", func() bool {
		_, ok, _ := late.items.Get(t.Context(), taskID, itemDestination)
		return !ok
	})

	deliver(t, late, promptClick(t, late, denyClickID, taskID, HILCancelAction))
	if got := inst.bot.replies(denyClickID); !slices.Equal(got, []string{"🛑 Request cancelled."}) {
		t.Errorf("replies to the press after the task ended = %q", got)
	}
	if strings.Contains(logs.String(), "answer claim") {
		t.Errorf("claim logged at WARN for an ended task:\n%s", logs.String())
	}
}

func TestTwoInstances_ClickBeforeTheAnswerDoesNotStaleTheSubmittersTakeover(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	const submitter = 1
	taskID := submitDM(t, inst.adapters[submitter])
	inst.published(t)

	inst.agentAsks(t, taskID)
	waitUntil(t, "the prompt card is posted", func() bool { return len(inst.bot.promptCards()) > 0 })
	holder := inst.bot.promptCards()[0]
	if holder == submitter {
		t.Fatalf("instance %d received the events; this test needs the other instance to", holder)
	}
	deliver(t, inst.adapters[submitter], promptClick(t, inst.adapters[submitter], approveClickID, taskID, HILSubmitAction))
	if n := inst.published(t); n != 1 {
		t.Fatalf("answers forwarded = %d, want 1", n)
	}

	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	answerID := inst.bot.postedID(t, answerStart)
	inst.waitItem(t, taskID, itemAnswerMsg, answerID)
	_ = inst.clients[holder].Disconnect(t.Context())

	inst.agentStreams(t, taskID, " Here's why: 2 + 2 = 4.")
	inst.agentCompletesWith(t, taskID, answerFinal)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: answerID, Text: answerWhole})
	if posted := inst.bot.posted(); slices.Contains(posted, "Here's why: 2 + 2 = 4.") {
		t.Errorf("the submitter started a second answer message: posted %q", posted)
	}
	requireNoPartialEdit(t, inst.bot, answerID)
}
