// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestArtifactBlock_PublishesReceivingAndSavingProgressUpdates(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact +
		" filename=\"poem.md\" description=\"A short poem\"\n" +
		"Roses are red,\n" +
		"Violets are blue.\n" +
		BlockDelimiterClose +
		"\nHere is your poem!"

	agent, _, store := newTestAgent(&llm.ChatResponse{
		Content:      artifactContent,
		FinishReason: "stop",
	})

	brk := newMockBroker()
	memStore := memoryArtifactStore()
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = brk
	svc.arts = &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc

	resp, err := agent.HandleTask(
		context.Background(),
		"task-progress-1",
		newUserMessage("Write me a poem"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var receivingTexts, savingTexts []string
	for _, msg := range brk.getPublished() {
		payload := string(msg.payload)
		if !strings.Contains(payload, `"type":"agent_progress_update"`) {
			continue
		}
		switch {
		case strings.Contains(payload, "Receiving artifact"):
			receivingTexts = append(receivingTexts, payload)
		case strings.Contains(payload, "Saving artifact"):
			savingTexts = append(savingTexts, payload)
		}
	}

	if len(receivingTexts) != 1 {
		t.Fatalf("expected 1 Receiving agent_progress_update, got %d: %v", len(receivingTexts), receivingTexts)
	}
	if !strings.Contains(receivingTexts[0], "`poem.md`") {
		t.Errorf("Receiving event should reference the filename, got: %s", receivingTexts[0])
	}
	if !strings.Contains(receivingTexts[0], "A short poem") {
		t.Errorf("Receiving event should include the description, got: %s", receivingTexts[0])
	}

	if len(savingTexts) != 1 {
		t.Fatalf("expected 1 Saving agent_progress_update, got %d: %v", len(savingTexts), savingTexts)
	}
	if !strings.Contains(savingTexts[0], "`poem.md`") {
		t.Errorf("Saving event should reference the filename, got: %s", savingTexts[0])
	}
	if !strings.Contains(savingTexts[0], "A short poem") {
		t.Errorf("Saving event should include the description, got: %s", savingTexts[0])
	}
}

func TestArtifactBlock_NoStatusTopic_NoProgressUpdates(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact +
		" filename=\"hello.txt\"\n" +
		"Hello\n" +
		BlockDelimiterClose

	agent, _, store := newTestAgent(&llm.ChatResponse{
		Content:      artifactContent,
		FinishReason: "stop",
	})

	brk := newMockBroker()
	memStore := memoryArtifactStore()
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = brk
	svc.arts = &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc

	_, err := agent.HandleTask(
		context.Background(),
		"task-progress-2",
		newUserMessage("save it"),
		a2a.UserProperties{
			ClientID: "user-1",
			UserID:   "user-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, msg := range brk.getPublished() {
		if strings.Contains(string(msg.payload), `"type":"agent_progress_update"`) {
			t.Errorf("expected no agent_progress_update events when status topic is empty, got: %s", msg.payload)
		}
	}
}
