// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func newBlockingDrainTool(name string, started chan<- struct{}, release <-chan struct{}) *samtoolsdk.ToolDef {
	return samtoolsdk.NewTool(name, "Blocks until released.",
		func(ctx context.Context, _ struct {
			N string `json:"n"`
		}, _ *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return samtoolsdk.OK("done"), nil
		})
}

func publishDrainInvoke(t *testing.T, ctx context.Context, brk broker.Broker, toolName, reqID string) { //nolint:revive // t before ctx for test helper convention
	t.Helper()
	params := remotetool.InvokeParams{
		ToolName:  toolName,
		TaskID:    "task-" + reqID,
		SessionID: "sess-abc",
		Args:      map[string]any{"n": "x"},
	}
	jsonRPC := a2a.NewRequest(reqID, remotetool.MethodInvoke, params)
	payload, err := json.Marshal(jsonRPC)
	if err != nil {
		t.Fatal(err)
	}
	userProps := map[string]string{
		remotetool.PropReplyTo:  "test-ns/reply/" + toolName,
		remotetool.PropClientID: "test-agent",
		remotetool.PropUserID:   "test-user",
	}
	if pubErr := brk.Publish(ctx, remotetool.InvokeTopic("test-ns", toolName), &broker.Message{
		Payload:        payload,
		UserProperties: userProps,
		CorrelationID:  reqID,
	}); pubErr != nil {
		t.Fatal(pubErr)
	}
}
