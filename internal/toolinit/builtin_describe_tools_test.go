// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestDescribeToolsIsABuilderToolNoBuiltAgentCanBind(t *testing.T) {
	if !slices.Contains(BuiltinToolNames(), "describe_tools") {
		t.Fatal("describe_tools has no in-process factory")
	}
	if _, ok := LookupBuiltinTool("describe_tools", nil, Options{}).(*tools.DescribeToolsTool); !ok {
		t.Errorf("the describe_tools factory builds %T, want *tools.DescribeToolsTool", LookupBuiltinTool("describe_tools", nil, Options{}))
	}

	members := LookupBuiltinGroup("builder_tools", Options{})
	if !slices.Contains(members, "describe_tools") {
		t.Errorf("builder_tools = %v, want describe_tools in it", members)
	}
	if !schema.ValidBuiltinToolNames["describe_tools"] {
		t.Error("schema.ValidBuiltinToolNames omits describe_tools")
	}

	for _, name := range members {
		if AgentBindableBuiltin(name) {
			t.Errorf("%s is a builder_tools member but AgentBindableBuiltin reports it bindable", name)
		}
	}
	if AgentBindableBuiltin("describe_tools") {
		t.Error("describe_tools must not be agent-bindable")
	}
}
