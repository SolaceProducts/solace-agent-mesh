// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import "testing"

func TestBuildToolCtxFromState_CarriesProjectID(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubPendingAuthWithCred("cred-x"))
	state := newTestTaskState(agent)
	state.GatewayMetadata = map[string]string{metaProjectID: "proj-42"}
	pca := defaultPauseCallArgs(agent, state)

	toolCtx := agent.buildToolCtxFromState(t.Context(), state, pca.sp)
	gc := gatewayContextFrom(state.GatewayMetadata, state.UserProps, false)
	if toolCtx.ProjectID != "proj-42" || gc.ProjectID != toolCtx.ProjectID {
		t.Errorf("resumed project: tool context %q, gateway context %q, want both %q", toolCtx.ProjectID, gc.ProjectID, "proj-42")
	}
}
