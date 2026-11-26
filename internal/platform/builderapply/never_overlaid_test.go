// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestNeverOverlaid_EveryEntryCanActuallyReachTheOverlay(t *testing.T) {
	t.Parallel()
	pairs := map[string]struct{ create, response any }{
		kindAgent:     {entplatform.CreateAgentRequest{}, entplatform.AgentResponse{}},
		kindGateway:   {entplatform.CreateGatewayRequest{}, entplatform.GatewayResponse{}},
		kindConnector: {entplatform.CreateConnectorRequest{}, entplatform.ConnectorResponse{}},
		kindA2AAgent:  {entplatform.CreateRemoteAgentRequest{}, entplatform.RemoteAgentResponse{}},
		kindWorkflow:  {entplatform.CreateWorkflowRequest{}, entplatform.WorkflowResponse{}},
	}
	for _, kind := range componentKindsInOrder() {
		if _, covered := pairs[kind]; !covered {
			t.Errorf("kind %q has no DTO pair here, so its overlay is unexamined", kind)
		}
	}

	for key := range neverOverlaid {
		var onCreate, onResponse, live []string
		for kind, dto := range pairs {
			hasCreate := slices.Contains(wireFieldNames(dto.create), key)
			hasResponse := slices.Contains(wireFieldNames(dto.response), key)
			if hasCreate {
				onCreate = append(onCreate, kind)
			}
			if hasResponse {
				onResponse = append(onResponse, kind)
			}
			if hasCreate && hasResponse {
				live = append(live, kind)
			}
		}
		slices.Sort(live)
		slices.Sort(onCreate)
		slices.Sort(onResponse)

		switch {
		case len(live) > 0:
			t.Logf("%-18s copyable for %v (on both the create body and the response; whether it then survives is a separate question)", key, live)
		case len(onCreate) == 0 && len(onResponse) == 0:
			t.Errorf("%q is excluded from the overlay but appears on no create body and no response of any kind — it excludes nothing and names nothing", key)
		default:
			t.Logf("%-18s inert (on create for %v, on responses for %v — never both, so the overlay has no source to copy)", key, onCreate, onResponse)
		}
	}
}
