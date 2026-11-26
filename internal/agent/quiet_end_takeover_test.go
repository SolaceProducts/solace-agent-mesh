// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestScheduledRunAdoptedAfterAPauseKeepsItsQuietEnd(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	survivorLLM.responses = []*llm.ChatResponse{{
		ToolCalls:    []llm.ToolCall{quietCall("call-quiet", `{"summary": "Nothing needs attention."}`)},
		FinishReason: "tool_calls",
	}}
	state := pausedOnPeerCall(t, store, "task-quiet-adopted", "sub-quiet-adopted")
	state.ReportedFiles = nil
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("tc-1", peerToolPrefix+"analyst", `{}`)
	state.Messages[1] = state.PausedAssistantMsg
	state.PendingCalls["tc-1"].ToolName = peerToolPrefix + "analyst"
	state.QuietEndAllowed = true
	state.UserProps.Trigger = a2a.TriggerScheduled
	state.runFacts.addFile(producedFileOf(artifactCreationRecord{Filename: "deck.pptx", Version: 2}, producedBySourceTool))
	if err := owner.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)
	owner.checkpoints.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-1"], deckPeerResult(t))

	survivor.resumeAbandonedCheckpoints(t.Context(), time.Now())

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "Done.")
	if !offersQuietEnd(survivorLLM.getCalls()[0]) {
		t.Error("the adopted scheduled run was not offered end_run_quietly")
	}
	body, _ := json.Marshal(replyOnTopic(survivorBrk, resumedReplyTopic))
	for _, want := range []string{`"runOutcome":"quiet"`, `deck.pptx`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the adopted run's final response lacks %s: %s", want, body)
		}
	}
}
