// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"strings"
	"testing"
	"time"
)

func TestGenerate_NamesTheRequester(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind RequesterKind
		who  string
		want string
	}{
		{"an agent", RequesterAgent, "code-worker", `Agent \"code-worker\" wants to run tool: delete_file`},
		{"a workflow", RequesterWorkflow, "nightly-cleanup", `Workflow \"nightly-cleanup\" wants to run tool: delete_file`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surface, err := Generate(Request{
				RequestID:     "req-1",
				ToolName:      "delete_file",
				RequesterKind: tc.kind,
				RequesterName: tc.who,
				ToolArgs:      map[string]any{"path": "/tmp/x"},
				ShowArgs:      true,
				Timeout:       time.Minute,
			})
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if !strings.Contains(string(surface), tc.want) {
				t.Errorf("prompt does not read %q:\n%s", tc.want, surface)
			}
		})
	}
}
