// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	authCardText       = "Authorization required to continue."
	authorizedText     = "Authorized"
	authIncompleteText = "Authorization didn't complete"
)

func authLinkFor(callID string) string {
	return "https://idp.example/authorize?state=" + callID
}

func authPromptItemFor(callID string) string {
	return authPromptItem(callID, authLinkFor(callID))
}

func (inst *teamsInstances) agentRequestsAuth(t *testing.T, taskID, callID string) {
	t.Helper()
	inst.agentRequestsAuthAt(t, taskID, callID, authLinkFor(callID))
}

func (inst *teamsInstances) agentRequestsAuthAt(t *testing.T, taskID, callID, authURI string) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateInputNeeded, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":                          "authentication_required",
				"target_agent":                  twoInstanceAgent,
				"auth_uri":                      authURI,
				"auth_request_function_call_id": callID,
			})},
		}},
	})
}

func TestAuthPrompt_RetryOfTheSameCallWithAFreshLinkPostsANewCard(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	taskID := submitDM(t, inst.adapters[0])
	inst.agentRequestsAuth(t, taskID, "call-1")
	inst.bot.waitAuthCards(t, 1)

	inst.agentRequestsAuthAt(t, taskID, "call-1", "https://idp.example/authorize?state=retry-of-call-1")
	ids, _ := inst.bot.waitAuthCards(t, 2)

	inst.agentReportsAuth(t, taskID, true)
	for _, id := range ids {
		inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: id, Text: authorizedText})
	}
}

func (inst *teamsInstances) agentReportsAuth(t *testing.T, taskID string, success bool) {
	t.Helper()
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type": "authentication_result", "target_agent": twoInstanceAgent, "target_agent_task_id": taskID,
				"success": success,
			})},
		}},
	})
}

func (f *fakeBotConnector) authCards() (ids []string, posters []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.posts {
		if p.Text == authCardText {
			ids = append(ids, postedActivityID(i+1))
			posters = append(posters, f.posters[i])
		}
	}
	return ids, posters
}

func (f *fakeBotConnector) waitAuthCards(t *testing.T, n int) ([]string, []int) {
	t.Helper()
	waitUntil(t, "the Authorize cards are posted", func() bool {
		ids, _ := f.authCards()
		return len(ids) >= n
	})
	return f.authCards()
}

func (f *fakeBotConnector) edits(id string) int {
	n := 0
	for _, c := range f.changed() {
		if c.Method == http.MethodPut && c.ID == id {
			n++
		}
	}
	return n
}

func (inst *teamsInstances) waitItemGone(t *testing.T, taskID, item string) {
	t.Helper()
	waitUntil(t, "the task's "+itemKind(item)+" item is deleted", func() bool {
		_, ok, _ := inst.adapters[0].items.Get(t.Context(), taskID, item)
		return !ok
	})
}

func TestTwoInstances_RedeliveredAuthPromptPostsOneCard(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	_ = inst.clients[2].Disconnect(t.Context())

	inst.agentRequestsAuth(t, taskID, "call-1")
	ids, posters := inst.bot.waitAuthCards(t, 1)
	inst.waitRecorded(t, taskID, authPromptItemFor("call-1"))
	_ = inst.clients[posters[0]].Disconnect(t.Context())

	inst.agentRequestsAuth(t, taskID, "call-1")
	inst.agentReportsAuth(t, taskID, true)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: ids[0], Text: authorizedText})
	inst.agentCompletes(t, taskID)
	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: inst.bot.postedID(t, twoInstanceStatus)})
	if posted, _ := inst.bot.authCards(); len(posted) != 1 {
		t.Errorf("Authorize cards posted = %d, want 1", len(posted))
	}
	if n := inst.bot.edits(ids[0]); n != 1 {
		t.Errorf("card edited %d times, want once", n)
	}
}

func TestTwoInstances_AuthCardsRetiredByAnInstanceThatDidNotPostThem(t *testing.T) {
	for name, callIDs := range map[string][]string{
		"tool call ids":    {"call-1", "call-2"},
		"no tool call ids": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			inst := newTeamsInstances(t, 3)
			taskID := submitDM(t, inst.adapters[2])
			_ = inst.clients[2].Disconnect(t.Context())

			for _, callID := range callIDs {
				inst.agentRequestsAuth(t, taskID, callID)
			}
			ids, posters := inst.bot.waitAuthCards(t, 2)
			items := make([]string, len(ids))
			for i, id := range ids {
				items[i] = itemAuthPromptMsgPrefix + id
				if callIDs[i] != "" {
					items[i] = authPromptItemFor(callIDs[i])
				}
				inst.waitItem(t, taskID, items[i], id)
			}
			_ = inst.clients[posters[0]].Disconnect(t.Context())

			inst.agentReportsAuth(t, taskID, true)
			for _, id := range ids {
				inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: id, Text: authorizedText})
			}
			for _, item := range items {
				inst.waitItemGone(t, taskID, item)
			}

			inst.agentCompletes(t, taskID)
			inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: inst.bot.postedID(t, twoInstanceStatus)})
			for _, id := range ids {
				if n := inst.bot.edits(id); n != 1 {
					t.Errorf("card %s edited %d times, want once", id, n)
				}
			}
		})
	}
}

func TestTwoInstances_SubmitterTakingTheEventsBackRetiresTheAuthCard(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	const submitter = 1
	taskID := submitDM(t, inst.adapters[submitter])

	inst.agentRequestsAuth(t, taskID, "call-1")
	ids, posters := inst.bot.waitAuthCards(t, 1)
	if posters[0] == submitter {
		t.Fatal("the submitter received the events; this test needs the other instance to")
	}
	inst.waitRecorded(t, taskID, authPromptItemFor("call-1"))
	_ = inst.clients[posters[0]].Disconnect(t.Context())

	inst.agentReportsAuth(t, taskID, false)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: ids[0], Text: authIncompleteText})
}

func TestAuthPrompt_RefusedRetirementIsRetriedAtTheNextResolution(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)
	inst.agentRequestsAuth(t, taskID, "call-1")
	ids, _ := inst.bot.waitAuthCards(t, 1)
	item := authPromptItemFor("call-1")
	inst.waitItem(t, taskID, item, ids[0])

	inst.bot.setFailEdits(true)
	inst.agentReportsAuth(t, taskID, true)
	waitUntil(t, "the card's edit is refused", func() bool { return inst.bot.refusedEdits(ids[0]) == 1 })
	inst.bot.setFailEdits(false)
	if _, ok, err := a.items.Get(t.Context(), taskID, item); err != nil || !ok {
		t.Fatalf("auth-prompt item after the refused edit = (%v, %v), want kept", ok, err)
	}

	inst.agentCompletes(t, taskID)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: ids[0], Text: authorizedText})
	inst.waitItemGone(t, taskID, item)
}

func TestAuthPrompt_RecordedCardAdoptedByAnInstanceHoldingTheTask(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	taskID := submitDM(t, inst.adapters[0])
	inst.agentStreams(t, taskID, answerStart)
	inst.bot.waitPosted(t, answerStart)
	const otherCard = "posted-by-another-instance"
	if err := inst.adapters[0].items.Put(t.Context(), taskID, authPromptItemFor("call-1"), otherCard); err != nil {
		t.Fatal(err)
	}

	inst.agentRequestsAuth(t, taskID, "call-1")
	inst.agentReportsAuth(t, taskID, true)

	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: otherCard, Text: authorizedText})
	if ids, _ := inst.bot.authCards(); len(ids) != 0 {
		t.Errorf("Authorize cards posted = %d, want 0", len(ids))
	}
}
