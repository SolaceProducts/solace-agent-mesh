// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func desiredA2AAgent(componentName, content string) (desiredComponent, error) {
	var create entplatform.CreateRemoteAgentRequest
	if err := decodeJSONStrict([]byte(content), &create); err != nil {
		return desiredComponent{}, fmt.Errorf("parse a2a agent %q: %w", componentName, err)
	}

	create.Name = cmp.Or(create.Name, componentName)
	if create.Name == "" {
		return desiredComponent{}, fmt.Errorf("a2a agent has no name")
	}
	if create.AgentURL == "" {
		return desiredComponent{}, fmt.Errorf("a2a agent %q has no agentUrl", componentName)
	}
	if create.AgentCardLocation == "" {
		return desiredComponent{}, fmt.Errorf("a2a agent %q has no agentCardLocation", componentName)
	}

	authored, err := authoredJSONKeys(content, "name")
	if err != nil {
		return desiredComponent{}, fmt.Errorf("parse a2a agent %q: %w", componentName, err)
	}

	return desiredComponent{planProto: a2aViewFromCreate(create), create: create, authored: authored}, nil
}

func defaultA2AAuthForCreate(create entplatform.CreateRemoteAgentRequest) entplatform.CreateRemoteAgentRequest {
	if create.AgentAuthConfig == nil {
		create.AgentAuthConfig = map[string]any{"type": "none"}
	}
	return create
}
