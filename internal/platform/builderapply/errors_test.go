// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestOperationError_DoesNotLeakInternalCauses(t *testing.T) {
	t.Parallel()
	svc := &Service{logger: discardLogger()}
	row := BuildOperationResult{Kind: kindConnector, Name: "c", Action: actionCreate}

	leaky := fmt.Errorf("create connector: %w",
		errors.New("failed to connect to host=db.internal user=admin database=platform: connection refused"))

	got := svc.operationError(leaky, row, false)

	if strings.Contains(got.Message, "host=") || strings.Contains(got.Message, "user=") {
		t.Errorf("Message leaks connection details: %q", got.Message)
	}
	if got.Message != "internal error" {
		t.Errorf("Message = %q, want the generic message", got.Message)
	}
	if got.ErrorID == "" {
		t.Error("an errorId is required to join the response to the logged cause")
	}
}

func TestOperationError_PassesThroughActionableMessages(t *testing.T) {
	t.Parallel()
	svc := &Service{logger: discardLogger()}
	row := BuildOperationResult{Kind: kindAgent, Name: "a", Action: actionCreate}

	got := svc.operationError(&entplatform.ValidationError{
		Message: "instruction is required",
		Details: map[string][]string{"systemPrompt": {"instruction is required"}},
	}, row, false)

	if got.Message != "instruction is required" {
		t.Errorf("Message = %q, want the validation message preserved", got.Message)
	}
	if len(got.ValidationDetails) == 0 {
		t.Error("validationDetails were dropped; the frontend renders per-field errors from them")
	}
}

func TestOperationError_LogsWhetherItWasAPreview(t *testing.T) {
	t.Parallel()
	for _, dryRun := range []bool{false, true} {
		var logged strings.Builder
		svc := &Service{logger: slog.New(slog.NewTextHandler(&logged, nil))}

		svc.operationError(&entplatform.ValidationError{Message: "rejected"},
			BuildOperationResult{Kind: kindConnector, Name: "c", Action: actionCreate}, dryRun)

		if want := fmt.Sprintf("dryRun=%t", dryRun); !strings.Contains(logged.String(), want) {
			t.Errorf("log = %q, want it to contain %q", logged.String(), want)
		}
	}
}

func TestOperationError_PassesThroughConflict(t *testing.T) {
	t.Parallel()
	svc := &Service{logger: discardLogger()}
	got := svc.operationError(&entplatform.ConflictError{Message: "a connector with this name already exists"},
		BuildOperationResult{Kind: kindConnector, Name: "c", Action: actionCreate}, false)
	if got.Message != "a connector with this name already exists" {
		t.Errorf("Message = %q, want the conflict message preserved", got.Message)
	}
}

func TestPlan_ArtifactErrorsAreValidationErrors(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	for name, comp := range map[string]BuildComponentInput{
		"unparseable yaml": {Kind: kindAgent, Name: "broken", Content: "\t\tnot: [yaml"},
		"unknown tool": {Kind: kindAgent, Name: "broken", Content: `
apps:
  - name: broken
    app_config:
      agent_name: broken
      instruction: Uses a tool that does not exist.
      tools:
        - tool_type: builtin
          tool_name: no_such_tool
`},
		"missing required field":              {Kind: kindA2AAgent, Name: "broken", Content: `{"name":"broken"}`},
		"workflow tool node without a source": {Kind: kindWorkflow, Name: "time_flow", Content: timeWorkflowArtifact("")},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.Plan(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
			if err == nil {
				t.Fatal("expected an error")
			}
			if _, ok := errors.AsType[*entplatform.ValidationError](err); !ok {
				t.Errorf("error is %T, want *ValidationError so it surfaces as 422 with the message intact: %v", err, err)
			}
		})
	}
}
