// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestWebhook_ToAnAgentThatIsNotDeployedAnswers404AndPublishesNothing(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	rig.core.SetTargetLookup(func(context.Context, string) control.Target {
		return control.Target{State: control.TargetNotDeployed}
	})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hello"}`, bearer("dev-webhook-token"))
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["message"] != entrypoint.AgentNotAvailableText {
		t.Errorf("message = %q, want %q", body["message"], entrypoint.AgentNotAvailableText)
	}
	select {
	case req := <-ch:
		t.Fatalf("a refused webhook reached the agent as task %q", req.taskID)
	case <-time.After(300 * time.Millisecond):
	}
}
