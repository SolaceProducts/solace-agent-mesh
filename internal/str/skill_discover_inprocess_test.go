// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"testing"

	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestSubprocessBundledToolsExcludesInProcess(t *testing.T) {
	inProcTool := samtoolsdk.NewTool("generate_test_plan", "Plan",
		func(context.Context, struct{}, *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			return &samtoolsdk.Result{}, nil
		})

	aliased := &ManifestEntry{
		ToolName:   "generate_test_plan",
		Executable: "/nonexistent/test_harness",
		inProcess:  inProcTool,
	}
	all := map[string]*ManifestEntry{
		"sam-test-harness__generate_test_plan": aliased,
		"generate_test_plan":                   aliased,
		"myskill__subprocess_tool": {
			ToolName:   "subprocess_tool",
			Executable: "/some/path/subprocess_tool",
		},
	}

	got := subprocessBundledTools(all, testLogger())

	if _, present := got["sam-test-harness__generate_test_plan"]; present {
		t.Error("in-process skill tool was written to the bundled manifest; the file entry it " +
			"produces loses the binding and pre-empts the correct registration")
	}
	if _, present := got["generate_test_plan"]; present {
		t.Error("the expose_globally alias of an in-process tool survived; both keys point at " +
			"one entry and must be dropped together")
	}
	if _, present := got["myskill__subprocess_tool"]; !present {
		t.Error("subprocess skill tool was dropped from the bundled manifest")
	}
	if len(got) != 1 {
		t.Errorf("got %d entries, want 1", len(got))
	}
}
