// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const offeredFile = "report.pdf"

func (inst *teamsInstances) agentReturnsFile(t *testing.T, taskID, filename string) {
	t.Helper()
	part := a2a.Part{Kind: "file", File: &a2a.FileContent{
		Name: filename, MIMEType: "application/pdf",
		Bytes: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 report")),
	}}
	inst.agentSends(t, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{part},
		}},
	})
}

func (f *fakeBotConnector) consentCards() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var posters []int
	for i, p := range f.posts {
		for _, att := range p.Attachments {
			if att.ContentType == "application/vnd.microsoft.teams.card.file.consent" {
				posters = append(posters, f.posters[i])
			}
		}
	}
	return posters
}

func (f *fakeBotConnector) waitConsentCard(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cards := f.consentCards(); len(cards) > 0 {
			return cards[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no consent card posted; posted = %q", f.posted())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (inst *teamsInstances) waitOffered(t *testing.T, taskID, filename string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, ok, _ := inst.adapters[0].items.Get(t.Context(), taskID, itemFileOfferedPrefix+filename)
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("offer of %q not recorded", filename)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (inst *teamsInstances) finishAndRequireOneCard(t *testing.T, taskID, statusID string) {
	t.Helper()
	inst.agentCompletes(t, taskID)
	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: statusID})
	if cards := inst.bot.consentCards(); len(cards) != 1 {
		t.Errorf("consent cards posted by instances %v, want one", cards)
	}
}

func TestTwoInstances_TakeoverDoesNotOfferAFileAgain(t *testing.T) {
	inst := newTeamsInstances(t, 3)
	taskID := submitDM(t, inst.adapters[2])
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	_ = inst.clients[2].Disconnect(t.Context())

	inst.agentReturnsFile(t, taskID, offeredFile)
	poster := inst.bot.waitConsentCard(t)
	inst.waitOffered(t, taskID, offeredFile)
	_ = inst.clients[poster].Disconnect(t.Context())

	inst.agentReturnsFile(t, taskID, offeredFile)
	inst.finishAndRequireOneCard(t, taskID, statusID)
}

func TestTwoInstances_SubmitterTakingTheEventsBackDoesNotOfferAFileAgain(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	const submitter = 1
	taskID := submitDM(t, inst.adapters[submitter])
	statusID := inst.bot.postedID(t, twoInstanceStatus)

	inst.agentReturnsFile(t, taskID, offeredFile)
	if poster := inst.bot.waitConsentCard(t); poster == submitter {
		t.Fatal("the submitter received the events; this test needs the other instance to")
	}
	inst.waitOffered(t, taskID, offeredFile)
	_ = inst.clients[0].Disconnect(t.Context())

	inst.agentReturnsFile(t, taskID, offeredFile)
	inst.finishAndRequireOneCard(t, taskID, statusID)
}
