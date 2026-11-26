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
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func deckPeerResultWithInlineFile(t *testing.T, state a2a.TaskState, inlineBytes int) *tool.Result {
	t.Helper()
	artifacts := []any{map[string]any{
		"artifactId": "art-deck", "name": "Deck",
		"parts": []any{map[string]any{"kind": "file", "file": map[string]any{"name": "deck.pptx", "bytes": strings.Repeat("A", inlineBytes)}}},
	}}
	result, err := parsePeerResponse("analyst", delegatedReply(t, state, producedDeck(), artifacts))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	return result
}

func TestAdoptedTaskReturnsTheFileADelegateSavedWhenItsNameIsInTheAnswer(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		inlineBytes int
		state       a2a.TaskState
	}{
		{name: "a peer's result", toolName: peerToolPrefix + "analyst", inlineBytes: 0, state: a2a.TaskStateCompleted},
		{name: "a sub-task's result", toolName: (&subTaskTool{}).Name(), inlineBytes: 0, state: a2a.TaskStateCompleted},
		{name: "a peer's result too large to checkpoint in full", toolName: peerToolPrefix + "analyst", inlineBytes: 2 << 20, state: a2a.TaskStateCompleted},
		{name: "a failed peer task too large to checkpoint in full", toolName: peerToolPrefix + "analyst", inlineBytes: 2 << 20, state: a2a.TaskStateFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			owner, _, _ := replicaOf(t, store, "owner")
			survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
			survivorLLM.responses = []*llm.ChatResponse{{Content: "The analyst built the deck, see deck.pptx.", FinishReason: "stop"}}
			state := pausedOnPeerCall(t, store, "task-delegated-file", "sub-delegated-file")
			state.ReportedFiles = nil
			state.PausedAssistantMsg = assistantMsgWithOneToolCall("tc-1", tc.toolName, `{}`)
			state.Messages[1] = state.PausedAssistantMsg
			state.PendingCalls["tc-1"].ToolName = tc.toolName
			if err := owner.checkpoints.save(t.Context(), state); err != nil {
				t.Fatalf("owner checkpoint: %v", err)
			}
			loseOwner(t, store, state, 1)
			owner.checkpoints.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-1"], deckPeerResultWithInlineFile(t, tc.state, tc.inlineBytes))

			survivor.resumeAbandonedCheckpoints(t.Context(), time.Now())

			assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "Done.")
			if want := `"task_state":"` + string(tc.state) + `"`; !requestContains(survivorLLM.getCalls()[0], want) {
				t.Errorf("the resumed turn's tool result lacks %s, so the model cannot tell how the delegate ended", want)
			}
			final := replyOnTopic(survivorBrk, resumedReplyTopic)
			body, _ := json.Marshal(final)
			if !strings.Contains(string(body), peerDeckEmbed) {
				t.Errorf("the adopted task's answer names deck.pptx but does not return it: %s", body)
			}
		})
	}
}
