// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import "testing"

func TestPlan_EveryKindIsIdempotent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind string
		comp BuildComponentInput
	}{
		{kindConnector, connectorComponent("idem-connector")},
		{kindAgent, agentComponent("idem_agent")},
		{kindA2AAgent, BuildComponentInput{
			Kind: kindA2AAgent,
			Name: "idem_proxy",
			Content: `{"name":"idem_proxy","description":"A remote agent used in the idempotence test.",` +
				`"agentUrl":"https://remote.example.com","agentCardLocation":"well_known",` +
				`"agentAuthConfig":{"type":"none"}}`,
		}},
		{kindA2AAgent + " (auth omitted)", BuildComponentInput{
			Kind: kindA2AAgent,
			Name: "idem_bare_proxy",
			Content: `{"name":"idem_bare_proxy","description":"A remote agent with no auth block.",` +
				`"agentUrl":"https://remote.example.com","agentCardLocation":"well_known"}`,
		}},
		{kindWorkflow, BuildComponentInput{
			Kind: kindWorkflow,
			Name: "IdemWorkflow",
			Content: `
apps:
  - name: idem_workflow
    app_config:
      name: IdemWorkflow
      agent_type: workflow
      workflow:
        output_mapping:
          result: "{{step.output}}"
        agent_card:
          description: "A workflow used in the idempotence test."
        nodes:
          - id: step
            type: agent
            agent_name: Helper
            input:
              text: "{{workflow.input.text}}"
`,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			first, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{tc.comp}}, testActor)
			if err != nil {
				t.Fatalf("first apply: %v", err)
			}
			if first.Summary.Failed != 0 {
				t.Fatalf("first apply failed: %+v", first.Operations)
			}

			second, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{tc.comp}}, testActor)
			if err != nil {
				t.Fatalf("second apply: %v", err)
			}
			op := second.Operations[0]
			if op.Action != actionUnchanged {
				t.Errorf("re-applying an identical %s planned %q (changed=%v); desired and actual hash differently, so every save rewrites it",
					tc.kind, op.Action, op.ChangedFields)
			}
		})
	}
}

func TestPlan_AgentWithUnsortedToolsetsIsIdempotent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	comp := BuildComponentInput{
		Kind: kindAgent,
		Name: "multi_toolset_agent",
		Content: `
apps:
  - name: multi_toolset_agent
    app_config:
      agent_name: multi_toolset_agent
      instruction: An agent whose toolset groups are deliberately not alphabetical.
      tools:
        - tool_type: builtin-group
          group_name: web_tools
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "Checks toolset ordering does not break convergence."
`,
	}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if op := second.Operations[0]; op.Action != actionUnchanged {
		t.Errorf("Action = %q (changed=%v), want unchanged", op.Action, op.ChangedFields)
	}
}
