// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSentinelToolMetadata(t *testing.T) {
	analytics := []*SentinelAnalyticsTool{
		NewQueryTaskHealthTool(),
		NewQueryFailedTasksTool(),
		NewQuerySlowTasksTool(),
		NewQueryAgentPerformanceTool(),
		NewQueryTokenUsageTool(),
		NewQueryScheduledTaskHealthTool(),
		NewQueryFeedbackSummaryTool(),
	}
	for _, tl := range analytics {
		if tl.Name() == "" || !strings.HasPrefix(tl.Name(), "query_") {
			t.Errorf("analytics tool name = %q, want query_*", tl.Name())
		}
		if tl.Description() == "" {
			t.Errorf("%s: empty description", tl.Name())
		}
		p := tl.Parameters()
		if p == nil || p.Type != "object" {
			t.Fatalf("%s: bad parameters schema", tl.Name())
		}
		if _, ok := p.Properties["window"]; !ok {
			t.Errorf("%s: missing 'window' param", tl.Name())
		}
	}

	type ddtool interface {
		Name() string
		Description() string
		Parameters() *tool.JSONSchema
		RequiredScopes() []string
	}
	ddTools := []ddtool{
		NewDatadogQueryLogsTool(nil),
		NewDatadogGetErrorsTool(nil),
		NewCorrelateTaskWithLogsTool(nil),
	}
	for _, tl := range ddTools {
		if tl.Name() == "" {
			t.Error("datadog tool has empty name")
		}
		if tl.Description() == "" {
			t.Errorf("%s: empty description", tl.Name())
		}
		scopes := tl.RequiredScopes()
		if len(scopes) != 1 || scopes[0] != rbac.ScopeToolDatadogLogs {
			t.Errorf("%s: RequiredScopes = %v, want [%s]", tl.Name(), scopes, rbac.ScopeToolDatadogLogs)
		}
		if p := tl.Parameters(); p == nil || p.Type != "object" {
			t.Errorf("%s: bad parameters schema", tl.Name())
		}
	}
}
