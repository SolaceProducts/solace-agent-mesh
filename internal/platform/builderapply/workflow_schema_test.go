// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"
)

func warningsFor(t *testing.T, content string) []string {
	t.Helper()
	d, err := desiredWorkflow("wf", content)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	return d.warnings
}

func mentions(warnings []string, want string) bool {
	for _, w := range warnings {
		if strings.Contains(w, want) {
			return true
		}
	}
	return false
}

func TestWorkflowSchema_QuietForTheShapesTheProductProduces(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"flat, as the builder writes it": `
apps:
  - name: flat_flow
    app_config:
      agent_name: flat_flow
      agent_type: workflow
      description: A flat workflow body of the kind the builder emits.
      output_mapping:
        result: "{{step.output}}"
      nodes:
        - id: step
          type: agent
          agent_name: worker
`,
		"nested, as the platform stores it": `
apps:
  - name: nested_flow
    app_config:
      name: nested_flow
      agent_type: workflow
      workflow:
        output_mapping:
          result: "{{step.output}}"
        agent_card:
          description: "A nested workflow body."
        nodes:
          - id: step
            type: agent
            agent_name: worker
`,
		"tool nodes and connector bindings": `
apps:
  - name: tool_flow
    app_config:
      agent_name: tool_flow
      agent_type: workflow
      description: A workflow whose work is done by tool nodes directly.
      output_mapping:
        stamped: "{{stamp.output}}"
      tools:
        - tool_type: builtin
          tool_name: get_current_time
      nodes:
        - id: stamp
          type: tool
          tool_name: get_current_time
        - id: place
          type: tool
          connector: orders_db
          tool: place_order
`,
		"every node type, with control flow": `
apps:
  - name: rich_flow
    app_config:
      agent_name: rich_flow
      agent_type: workflow
      description: A workflow using every node type the engine supports today.
      workflow_timeout: 10m
      retry_strategy:
        limit: 2
        retry_policy: OnFailure
      on_exit:
        always: audit
      output_mapping:
        out: "{{fan.output}}"
      nodes:
        - id: classify
          type: agent
          agent_name: worker
          timeout: 45s
        - id: route
          type: switch
          depends_on: [classify]
          cases:
            - condition: "true"
              node: fan
          default: fan
        - id: body
          type: agent
          agent_name: worker
        - id: fan
          type: map
          node: body
          items: "{{classify.output.lines}}"
          concurrency_limit: 4
        - id: settle
          type: loop
          node: body
          condition: "false"
          max_iterations: 5
        - id: nested
          type: workflow
          workflow_name: child_flow
        - id: audit
          type: agent
          agent_name: worker
`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if w := warningsFor(t, content); len(w) != 0 {
				t.Errorf("warnings = %v, want silence for a shape the product produces", w)
			}
		})
	}
}

func TestWorkflowSchema_ReportsWhatWasBroken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content, want string
	}{
		{
			name: "no output_mapping",
			want: "output_mapping",
			content: `
apps:
  - name: no_mapping
    app_config:
      agent_name: no_mapping
      agent_type: workflow
      description: A workflow with no output mapping at all in it.
      nodes:
        - id: only
          type: agent
          agent_name: worker
`,
		},
		{
			name: "no nodes",
			want: "minItems",
			content: `
apps:
  - name: no_nodes
    app_config:
      agent_name: no_nodes
      agent_type: workflow
      description: A workflow with an empty node list inside it.
      output_mapping:
        out: "{{x.output}}"
      nodes: []
`,
		},
		{
			name: "a node type the engine does not have",
			want: "/nodes/0/type",
			content: `
apps:
  - name: bad_type
    app_config:
      agent_name: bad_type
      agent_type: workflow
      description: A workflow using a node type the engine does not have.
      output_mapping:
        out: "{{only.output}}"
      nodes:
        - id: only
          type: teleport
`,
		},
		{
			name: "a node with no id to address it by",
			want: "id",
			content: `
apps:
  - name: no_id
    app_config:
      agent_name: no_id
      agent_type: workflow
      description: A workflow whose node has no id to address it by.
      output_mapping:
        out: "{{only.output}}"
      nodes:
        - type: agent
          agent_name: worker
`,
		},
		{
			name: "no agent_type discriminator anywhere",
			want: "agent_type",
			content: `
apps:
  - name: no_type
    app_config:
      agent_name: no_type
      description: A workflow body with no agent_type discriminator at all.
      output_mapping:
        out: "{{only.output}}"
      nodes:
        - id: only
          type: agent
          agent_name: worker
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := warningsFor(t, tc.content)
			if len(w) == 0 {
				t.Fatalf("no warning — this body used to save clean and cannot run")
			}
			if !mentions(w, tc.want) {
				t.Errorf("warnings = %v, want one naming %q", w, tc.want)
			}
		})
	}
}

func TestWorkflowSchema_ReachesTheAuthor(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	broken := BuildComponentInput{Kind: kindWorkflow, Name: "broken_flow", Content: `
apps:
  - name: broken_flow
    app_config:
      agent_name: broken_flow
      agent_type: workflow
      description: A workflow with an empty node list inside it.
      output_mapping:
        out: "{{x.output}}"
      nodes: []
`}
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{broken},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindWorkflow, "broken_flow")
	if len(op.Warnings) == 0 {
		t.Fatalf("op = %+v, want the schema violation surfaced to the author", op)
	}
	if !mentions(op.Warnings, "minItems") {
		t.Errorf("warnings = %v, want the empty node list named", op.Warnings)
	}
}

func TestWorkflowSchema_ReportsDanglingNodeReferences(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content, want string
	}{
		{
			name: "depends_on a node that was never defined",
			want: "ghost_node",
			content: `
apps:
  - name: dangling_dep
    app_config:
      agent_name: dangling_dep
      agent_type: workflow
      description: A workflow whose node depends on an id that is absent.
      output_mapping:
        out: "{{only.output}}"
      nodes:
        - id: only
          type: agent
          agent_name: worker
          depends_on: [ghost_node]
`,
		},
		{
			name: "a switch routing to a node that does not exist",
			want: "nowhere",
			content: `
apps:
  - name: bad_switch
    app_config:
      agent_name: bad_switch
      agent_type: workflow
      description: A workflow whose switch routes to an absent node id.
      output_mapping:
        out: "{{only.output}}"
      nodes:
        - id: only
          type: switch
          cases:
            - condition: "true"
              node: nowhere
          default: also_nowhere
`,
		},
		{
			name: "a map whose body node is missing",
			want: "missing_body",
			content: `
apps:
  - name: bad_map
    app_config:
      agent_name: bad_map
      agent_type: workflow
      description: A workflow whose map node names a body that is absent.
      output_mapping:
        out: "{{fan.output}}"
      nodes:
        - id: fan
          type: map
          node: missing_body
          items: "{{workflow.input.rows}}"
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := warningsFor(t, tc.content)
			if !mentions(w, tc.want) {
				t.Errorf("warnings = %v, want the dangling id %q named", w, tc.want)
			}
		})
	}
}

func TestWorkflowSchema_AgentReferencesAreNotDangling(t *testing.T) {
	t.Parallel()
	w := warningsFor(t, `
apps:
  - name: late_bound
    app_config:
      agent_name: late_bound
      agent_type: workflow
      description: A workflow naming an agent that has not been created yet.
      output_mapping:
        out: "{{only.output}}"
      nodes:
        - id: only
          type: agent
          agent_name: an_agent_that_does_not_exist_yet
`)
	if len(w) != 0 {
		t.Errorf("warnings = %v, want silence — agent references resolve at run time", w)
	}
}

func TestWorkflowSchema_ExitHandlerSpellingsAreAllRead(t *testing.T) {
	t.Parallel()
	body := func(exit string) string {
		return `
apps:
  - name: exiting
    app_config:
      agent_name: exiting
      agent_type: workflow
      description: A workflow whose exit handler names an absent node.
      output_mapping:
        out: "{{only.output}}"
` + exit + `
      nodes:
        - id: only
          type: agent
          agent_name: worker
`
	}

	for _, tc := range []struct{ name, exit string }{
		{"bare string, snake_case key", "      on_exit: ghost"},
		{"bare string, camelCase key", "      onExit: ghost"},
		{"snake_case outcome", "      on_exit:\n        on_success: ghost"},
		{"camelCase outcome", "      on_exit:\n        onSuccess: ghost"},
		{"camelCase key and outcome", "      onExit:\n        onFailure: ghost"},
		{"always", "      on_exit:\n        always: ghost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := warningsFor(t, body(tc.exit))
			if !mentions(w, "ghost") {
				t.Errorf("warnings = %v, want the dangling exit handler %q named", w, "ghost")
			}
		})
	}

	t.Run("a handler that resolves raises nothing about its target", func(t *testing.T) {
		for _, exit := range []string{
			"      on_exit: only",
			"      on_exit:\n        on_success: only",
			"      onExit:\n        onSuccess: only",
		} {
			if w := warningsFor(t, body(exit)); mentions(w, "does not define") {
				t.Errorf("warnings = %v, want nothing said about a handler naming a node this workflow defines", w)
			}
		}
	})
}

func TestWorkflowSchema_ExitHandlerReadsOnlyTheKeysTheLoaderReads(t *testing.T) {
	t.Parallel()
	w := warningsFor(t, `
apps:
  - name: annotated_exit
    app_config:
      agent_name: annotated_exit
      agent_type: workflow
      description: A workflow whose exit handler carries a comment key.
      output_mapping:
        out: "{{only.output}}"
      on_exit:
        always: only
        description: cleans up whatever the run left behind
      nodes:
        - id: only
          type: agent
          agent_name: worker
`)
	if mentions(w, "does not define") {
		t.Errorf("warnings = %v, want nothing reported — the loader reads four outcome keys and ignores the rest", w)
	}
}
