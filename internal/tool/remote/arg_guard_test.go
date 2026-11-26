// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestArgGuardRefusesACallBeforeItIsDispatched(t *testing.T) {
	rt := NewTool(&ToolConfig{
		ToolName: "read_skill_resource",
		ArgGuard: func(args map[string]any) error {
			if args["skill_name"] != "own-docs" {
				return errors.New(`skill "other-docs" is not available to this agent`)
			}
			return nil
		},
	}, nil)

	res, err := rt.Execute(t.Context(), map[string]any{"skill_name": "other-docs"}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError || res.Message != `skill "other-docs" is not available to this agent` {
		t.Errorf("result = %s %q, want the guard's refusal as the tool error", res.Status, res.Message)
	}
}
