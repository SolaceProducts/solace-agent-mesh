// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestGoogleChatAnswersExcludeSignInResponses(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "agent")); err != nil {
		t.Fatal(err)
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	signIn := a2a.Message{Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(a2a.AuthResponseData{Type: string(a2a.SignalAuthResponse)})}}
	if err := core.SubmitAuthResponse(t.Context(), "agent", "task-1", signIn, "u1"); err != nil {
		t.Fatal(err)
	}
	answer := a2a.UserInputResponseData{Type: string(a2a.SignalUserInputResponse), RequestID: "req-1", Status: "answered"}
	if err := core.SubmitUserInputResponse(t.Context(), "agent", "task-1", answer, auth.NewTestSubject(auth.SubjectFields{UserID: "u1"})); err != nil {
		t.Fatal(err)
	}

	for _, want := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var req googleChatAgentRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}
		if got := req.answersAQuestion(); got != want {
			t.Errorf("answersAQuestion = %v for %s, want %v", got, msg.Payload, want)
		}
	}
}

func TestLoadScenario_RejectsAnUnknownGoogleChatDelivery(t *testing.T) {
	load := func(delivery string) error {
		path := filepath.Join(t.TempDir(), "scenario.yaml")
		body := "name: gc\ngooglechat_config:\n  delivery: " + delivery + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadScenario(path)
		return err
	}
	if err := load("htps"); err == nil || !strings.Contains(err.Error(), "htps") {
		t.Errorf("LoadScenario = %v, want the unknown delivery refused", err)
	}
	for _, delivery := range []string{GoogleChatDeliveryPubSub, GoogleChatDeliveryHTTPS, GoogleChatDeliveryUnset} {
		if err := load(delivery); err != nil {
			t.Errorf("LoadScenario(delivery %q) = %v", delivery, err)
		}
	}
}
