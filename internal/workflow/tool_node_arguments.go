// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

var BuiltinTools = sync.OnceValue(func() *tool.Set {
	ts := tool.NewSet()
	tools.RegisterAll(ts)
	return ts
})

func ToolNodeArgumentProblems(name string, appConfig map[string]any, opts StaticCheckOptions) ([]StaticProblem, error) {
	body, nested := appConfig["workflow"].(map[string]any)
	if !nested || body == nil {
		body, nested = appConfig, false
	}
	cfg, err := ParseWorkflowConfig(name, "", WithConnectorNodePlaceholders(body))
	if err != nil || cfg == nil {
		return nil, err
	}
	if nested {
		appTools, _ := appConfig["tools"].([]any)
		appConnectors, _ := appConfig["connectors"].([]any)
		cfg.MergeAppConfigToolsAndConnectors(appTools, appConnectors)
	}
	return StaticToolNodeProblems(cfg, BuiltinTools(), opts), nil
}
