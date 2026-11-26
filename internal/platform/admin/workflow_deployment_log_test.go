// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestLogDeployFailure_RefusalIsWarnOtherFailuresAreError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantLevel string
		wantMsg   string
	}{
		{"a config refusal", &ValidationError{Message: "no tool source"}, "WARN", "workflow deployment refused"},
		{"a wrapped config refusal", fmt.Errorf("resolve toolset refs: %w", &ValidationError{Message: "unknown toolset"}), "WARN", "workflow deployment refused"},
		{"an operator-facing failure", errors.New("control plane unreachable"), "ERROR", "workflow deployment failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			svc := &WorkflowDeploymentService{logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}

			deployment := &store.WorkflowDeployment{ID: "dep-1", WorkflowID: "wf-1", Action: DeployActionDeploy}
			deployment.ErrorMessage, deployment.FailureReason, deployment.ErrorID = deployFailure(tc.err)
			svc.logDeployFailure(deployment, tc.err)

			var record map[string]any
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("want exactly one JSON log record, got %q: %v", buf.String(), err)
			}
			if record["level"] != tc.wantLevel || record["msg"] != tc.wantMsg {
				t.Fatalf("logged %v %q, want %s %q", record["level"], record["msg"], tc.wantLevel, tc.wantMsg)
			}
			if record["errorId"] != *deployment.ErrorID {
				t.Errorf("errorId = %v, want the one stored on the row, %s", record["errorId"], *deployment.ErrorID)
			}
		})
	}
}
