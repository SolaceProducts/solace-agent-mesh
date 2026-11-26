// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func (a *Agent) toolSearchEnabled() bool {
	return a.svc != nil && a.svc.FeatureRegistry() != nil && a.svc.FeatureRegistry().IsEnabled(features.KeyToolSearch)
}

func (a *Agent) markDeferredTools(defs []llm.ToolDefinition) {
	if !a.toolSearchEnabled() {
		return
	}
	for i := range defs {
		defs[i].DeferLoading = !alwaysLoadedTool(defs[i].Name)
	}
}

func alwaysLoadedTool(name string) bool {
	return name == LoadSkillToolName || strings.HasPrefix(name, "_")
}
