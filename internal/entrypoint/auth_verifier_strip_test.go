// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	verifierCanary = "CANARY-pkce-verifier-must-not-leave-the-gateway"
	stripTestState = "STATE-STRIP"
	stripTestURI   = "https://idp.example/authorize?code_challenge=C1&state=" + stripTestState
)

type activityRecordingAdapter struct {
	*recordingAdapter
	mu       sync.Mutex
	activity [][]byte
}

func (a *activityRecordingAdapter) OnActivityEvent(_ context.Context, _, _ string, payload map[string]any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activity = append(a.activity, encoded)
}

func authRequiredBrokerMessage(t *testing.T, taskID string) *broker.Message {
	t.Helper()
	authData := map[string]any{
		"type":                          "authentication_required",
		"target_agent":                  "agent-x",
		"target_agent_task_id":          taskID,
		"user_id":                       "u1",
		"client_id":                     "test-gw",
		"auth_uri":                      stripTestURI,
		"auth_request_function_call_id": "call-1",
		"credential_key":                "tool__oauth2",
		"code_verifier":                 verifierCanary,
	}
	event := a2a.NewStatusUpdateEvent(taskID, "ctx-1", []a2a.Part{a2a.NewDataPart(authData)}, false, nil)
	event.Status.State = a2a.TaskStateInputNeeded
	payload, err := json.Marshal(a2a.NewResponse(taskID, event))
	if err != nil {
		t.Fatal(err)
	}
	return &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), Payload: payload}
}

func TestHandleStatusEvent_AuthRequiredKeepsVerifierOffEveryFanOut(t *testing.T) {
	const taskID = "task-strip"
	core, _, recording := setupTestGateway(t)
	adapter := &activityRecordingAdapter{recordingAdapter: recording}
	core.adapter = adapter
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})

	core.handleStatusEvent(t.Context(), authRequiredBrokerMessage(t, taskID))

	recording.mu.Lock()
	forwarded := recording.statusRawPayloads
	recording.mu.Unlock()
	adapter.mu.Lock()
	activity := adapter.activity
	adapter.mu.Unlock()

	if len(forwarded) != 1 || len(activity) != 1 {
		t.Fatalf("fan-outs: status=%d activity=%d, want 1 each", len(forwarded), len(activity))
	}
	fanOuts := map[string][]byte{"status update": forwarded[0], "activity event": activity[0]}
	for name, payload := range fanOuts {
		if strings.Contains(string(payload), verifierCanary) || strings.Contains(string(payload), `"code_verifier"`) {
			t.Errorf("%s carries the PKCE code_verifier", name)
		}
		if !strings.Contains(string(payload), stripTestState) {
			t.Errorf("%s lost auth_uri; the client needs it to open the consent page", name)
		}
	}

	cached, ok := core.TakeToolAuthState(t.Context(), stripTestState)
	if !ok {
		t.Fatal("callback state was not cached")
	}
	if cached.CodeVerifier != verifierCanary {
		t.Error("the callback cache must keep the verifier; the token exchange needs it")
	}
}
