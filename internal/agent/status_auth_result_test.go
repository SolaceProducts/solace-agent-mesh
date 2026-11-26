// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func newAuthResultStatusPublisher(t *testing.T) (*StatusPublisher, broker.Broker, broker.Queue) {
	t.Helper()
	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mb.Disconnect(t.Context()) })

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-auth"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: "task-auth", ContextID: "ctx-auth", AgentName: "OAuth Agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("auth-result-signals", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}
	return sp, mb, q
}

func TestPublishAuthenticationResult_Success(t *testing.T) {
	sp, mb, q := newAuthResultStatusPublisher(t)

	sp.PublishAuthenticationResult(t.Context(), a2a.AuthenticationResultData{
		Type:                      "authentication_result",
		TargetAgent:               "OAuth Agent",
		TargetAgentTaskID:         "task-auth",
		Success:                   true,
		CredentialKey:             "cred-1",
		AuthRequestFunctionCallID: "call-1",
		ToolDisplayName:           "Atlassian",
	})

	var got a2a.AuthenticationResultData
	if err := json.Unmarshal(receiveSignalDataPart(t, mb, q), &got); err != nil {
		t.Fatalf("unmarshal AuthenticationResultData: %v", err)
	}
	if got.Type != string(a2a.SignalAuthenticationResult) {
		t.Errorf("Type = %q, want %q", got.Type, a2a.SignalAuthenticationResult)
	}
	if !got.Success {
		t.Error("Success = false, want true")
	}
	if got.TargetAgentTaskID != "task-auth" {
		t.Errorf("TargetAgentTaskID = %q", got.TargetAgentTaskID)
	}
	if got.ToolDisplayName != "Atlassian" {
		t.Errorf("ToolDisplayName = %q", got.ToolDisplayName)
	}
}

func TestPublishAuthenticationResult_Failure(t *testing.T) {
	sp, mb, q := newAuthResultStatusPublisher(t)

	sp.PublishAuthenticationResult(t.Context(), a2a.AuthenticationResultData{
		Type:              "authentication_result",
		TargetAgent:       "OAuth Agent",
		TargetAgentTaskID: "task-auth",
		Success:           false,
	})

	var got a2a.AuthenticationResultData
	if err := json.Unmarshal(receiveSignalDataPart(t, mb, q), &got); err != nil {
		t.Fatalf("unmarshal AuthenticationResultData: %v", err)
	}
	if got.Success {
		t.Error("Success = true, want false")
	}
}

func TestPublishAuthenticationResult_SuccessFieldAlwaysPresent(t *testing.T) {
	sp, mb, q := newAuthResultStatusPublisher(t)

	sp.PublishAuthenticationResult(t.Context(), a2a.AuthenticationResultData{
		Type:              "authentication_result",
		TargetAgentTaskID: "task-auth",
		Success:           false,
	})

	var raw map[string]any
	if err := json.Unmarshal(receiveSignalDataPart(t, mb, q), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["success"]; !ok {
		t.Error("success field missing from wire payload; must be present even when false")
	}
}

func TestTakePendingAuthOutcome(t *testing.T) {
	state := &TaskExecutionState{}

	if got := state.takePendingAuthOutcome(); got != nil {
		t.Fatalf("takePendingAuthOutcome on fresh state = %v, want nil", got)
	}

	state.mu.Lock()
	state.setPendingAuthOutcome(&authOutcome{success: true, credentialKey: "cred-1", functionCallID: "call-1", toolDisplayName: "Atlassian"})
	state.mu.Unlock()

	got := state.takePendingAuthOutcome()
	if got == nil || !got.success || got.credentialKey != "cred-1" {
		t.Fatalf("takePendingAuthOutcome = %+v", got)
	}
	if again := state.takePendingAuthOutcome(); again != nil {
		t.Fatalf("takePendingAuthOutcome after take = %+v, want nil", again)
	}
}
