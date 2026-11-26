// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestCheckWorkflowSchemaOutputFieldRefs(t *testing.T) {
	const wfName = "DecisionFlow"
	schemalessDecider := map[string]wfAgentSchema{"Decider": {Name: "Decider"}}
	decideSchemaless := func(key string) string {
		return "output_mapping '" + key + "' references '{{decide.output." + key +
			"}}', but node 'decide' declares no output schema, so its output has no guaranteed fields: declare '" + key +
			"' in the node's output_schema_override (or the output_schema of agent 'Decider') and instruct the agent to return it, " +
			"or map the whole '{{decide.output}}'"
	}
	deciderWithOutputSchema := func(outputSchema map[string]any) map[string]wfAgentSchema {
		return map[string]wfAgentSchema{"Decider": {Name: "Decider", OutputSchema: outputSchema}}
	}

	tests := []struct {
		name         string
		workflow     string
		agents       map[string]wfAgentSchema
		want         []string
		wantWarnings []string
	}{
		{
			name: "output_mapping field of a schemaless session agent node",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          input:
            transaction: "{{workflow.input}}"
      output_mapping:
        action: "{{decide.output.action}}"
        confidence: "{{decide.output.confidence}}"
`,
			agents: schemalessDecider,
			want:   []string{decideSchemaless("action"), decideSchemaless("confidence")},
		},
		{
			name: "nested workflow section is checked the same as the flat shape",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      workflow:
        nodes:
          - id: decide
            type: agent
            agent_name: Decider
        output_mapping:
          action: "{{decide.output.action}}"
`,
			agents: schemalessDecider,
			want:   []string{decideSchemaless("action")},
		},
		{
			name: "coalesce inside output_mapping is traversed",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action:
          coalesce:
            - "{{decide.output.action}}"
            - "hold"
`,
			agents: schemalessDecider,
			want:   []string{decideSchemaless("action")},
		},
		{
			name: "node input field of a schemaless session agent node",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
        - id: notify
          type: agent
          agent_name: Notifier
          depends_on: [decide]
          input:
            message: "{{decide.output.action}}"
      output_mapping:
        result: "{{notify.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'notify' input 'message' references '{{decide.output.action}}', " +
				"but node 'decide' declares no output schema, so its output has no guaranteed fields: declare 'action' " +
				"in the node's output_schema_override (or the output_schema of agent 'Decider') and instruct the agent to return it, " +
				"or map the whole '{{decide.output}}'"},
		},
		{
			name: "node output_schema_override declaring the fields",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          output_schema_override:
            type: object
            properties:
              action: {type: string}
              confidence: {type: number}
      output_mapping:
        action: "{{decide.output.action}}"
        confidence: "{{decide.output.confidence}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "node output_schema_override missing a mapped field",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          output_schema_override:
            type: object
            properties:
              action: {type: string}
      output_mapping:
        action: "{{decide.output.action}}"
        confidence: "{{decide.output.confidence}}"
`,
			agents: schemalessDecider,
			want: []string{"output_mapping 'confidence' references '{{decide.output.confidence}}' " +
				"but 'confidence' not in decide's output schema properties [action]"},
		},
		{
			name: "node output_schema_override without properties",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          output_schema_override:
            type: object
      output_mapping:
        action: "{{decide.output.action}}"
`,
			agents: schemalessDecider,
			want: []string{"output_mapping 'action' references '{{decide.output.action}}', but the " +
				"output_schema_override of node 'decide' declares no properties, so its output has no guaranteed fields: " +
				"add 'action' to it and instruct the agent to return it, or map the whole '{{decide.output}}'"},
		},
		{
			name: "node output_schema_override requiring the field without properties",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          output_schema_override:
            type: object
            required: [action]
      output_mapping:
        action: "{{decide.output.action}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "node output_schema_override requiring other fields without properties",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          output_schema_override:
            type: object
            required: [action]
      output_mapping:
        confidence: "{{decide.output.confidence}}"
`,
			agents: schemalessDecider,
			want: []string{"output_mapping 'confidence' references '{{decide.output.confidence}}' " +
				"but 'confidence' not in decide's output schema required fields [action]"},
		},
		{
			name: "session agent allOf output_schema with a top-level required list",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        confidence: "{{decide.output.confidence}}"
`,
			agents: deciderWithOutputSchema(map[string]any{
				"required": []any{"action"},
				"allOf": []any{map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":     map[string]any{"type": "string"},
						"confidence": map[string]any{"type": "number"},
					},
				}},
			}),
		},
		{
			name: "session agent allOf output_schema declaring the field",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action: "{{decide.output.action}}"
`,
			agents: deciderWithOutputSchema(map[string]any{
				"allOf": []any{map[string]any{
					"type":       "object",
					"properties": map[string]any{"action": map[string]any{"type": "string"}},
					"required":   []any{"action"},
				}},
			}),
		},
		{
			name: "session agent $ref output_schema",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action: "{{decide.output.action}}"
`,
			agents: deciderWithOutputSchema(map[string]any{
				"$ref": "#/$defs/decision",
				"$defs": map[string]any{"decision": map[string]any{
					"type":       "object",
					"properties": map[string]any{"action": map[string]any{"type": "string"}},
				}},
			}),
		},
		{
			name: "map node items referencing a schemaless session agent node",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: split
          type: agent
          agent_name: Decider
        - id: fanout
          type: map
          depends_on: [split]
          items: "{{split.output.items}}"
          node: handle
        - id: handle
          type: agent
          agent_name: PlatformHandler
      output_mapping:
        results: "{{fanout.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'fanout' items references '{{split.output.items}}', " +
				"but node 'split' declares no output schema, so its output has no guaranteed fields: declare 'items' " +
				"in the node's output_schema_override (or the output_schema of agent 'Decider') and instruct the agent to return it"},
		},
		{
			name: "whole node output mapped",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "session agent output_schema declaring the fields",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action: "{{decide.output.action}}"
`,
			agents: map[string]wfAgentSchema{"Decider": {
				Name: "Decider",
				OutputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"action": map[string]any{"type": "string"}},
				},
			}},
		},
		{
			name: "agent outside the build has a schema this check cannot see",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
      output_mapping:
        action: "{{decide.output.action}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "non-agent node without a schema is not judged",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: fanout
          type: map
          agent_name: Decider
          items: "{{workflow.input.items}}"
          node: decide
        - id: decide
          type: agent
          agent_name: PlatformDecider
      output_mapping:
        results: "{{fanout.output.results}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "output_mapping referencing a node that does not exist",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action: "{{ghost.output.action}}"
`,
			agents: schemalessDecider,
			want:   []string{"output_mapping 'action' references non-existent node 'ghost' in '{{ghost.output.action}}'"},
		},
		{
			name: "output_mapping referencing a workflow input the input_schema lacks",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          transaction: {type: object}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
      output_mapping:
        echoed: "{{workflow.input.account}}"
`,
			agents: schemalessDecider,
			wantWarnings: []string{"output_mapping 'echoed' references '{{workflow.input.account}}' " +
				"but 'account' is not in workflow input_schema properties [transaction]"},
		},
		{
			name: "unknown node inside a coalesce in output_mapping is a fallback, not a typo",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
      output_mapping:
        action:
          coalesce:
            - "{{escalate.output.action}}"
            - "{{decide.output}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "unknown node nested below a concat in a node input is a fallback, not a typo",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      workflow:
        nodes:
          - id: decide
            type: agent
            agent_name: PlatformDecider
            input:
              note:
                concat:
                  - "prior: "
                  - text: "{{previous_run.output.summary}}"
        output_mapping:
          decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "operator key beside another key is a plain map, so an unknown node there is a typo",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
      output_mapping:
        action:
          coalesce: ["{{ghost.output.action}}"]
          note: "not an operator"
`,
			agents: schemalessDecider,
			want:   []string{"output_mapping 'action' references non-existent node 'ghost' in '{{ghost.output.action}}'"},
		},
		{
			name: "missing field of an existing node inside a coalesce still fails the run",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action:
          coalesce:
            - "{{decide.output.verdict}}"
            - "hold"
`,
			agents: deciderWithOutputSchema(map[string]any{
				"type":       "object",
				"properties": map[string]any{"action": map[string]any{"type": "string"}},
			}),
			want: []string{"output_mapping 'action' references '{{decide.output.verdict}}' " +
				"but 'verdict' not in decide's output schema properties [action]"},
		},
		{
			name: "switch case condition is offered only the declaration fix",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
        - id: route
          type: switch
          depends_on: [decide]
          cases:
            - condition: "{{decide.output.action}} == 'block'"
              node: block
        - id: block
          type: agent
          agent_name: PlatformBlocker
      output_mapping:
        result: "{{route.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'route' switch case references '{{decide.output.action}}', " +
				"but node 'decide' declares no output schema, so its output has no guaranteed fields: declare 'action' " +
				"in the node's output_schema_override (or the output_schema of agent 'Decider') and instruct the agent to return it"},
		},
		{
			name: "composite output schema is not judged even beside top-level properties",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        confidence: "{{decide.output.confidence}}"
`,
			agents: deciderWithOutputSchema(map[string]any{
				"properties": map[string]any{"action": map[string]any{"type": "string"}},
				"allOf": []any{map[string]any{
					"properties": map[string]any{"confidence": map[string]any{"type": "number"}},
					"required":   []any{"confidence"},
				}},
			}),
		},
		{
			name: "$ref output schema is not judged even beside top-level properties",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        confidence: "{{decide.output.confidence}}"
`,
			agents: deciderWithOutputSchema(map[string]any{
				"properties": map[string]any{"action": map[string]any{"type": "string"}},
				"$ref":       "#/$defs/scored",
			}),
		},
		{
			name: "field named only in required is declared beside properties",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
          output_schema_override:
            type: object
            properties:
              action: {type: string}
            required: [action, confidence]
      output_mapping:
        confidence: "{{decide.output.confidence}}"
        reason: "{{decide.output.reason}}"
`,
			agents: schemalessDecider,
			want: []string{"output_mapping 'reason' references '{{decide.output.reason}}' " +
				"but 'reason' not in decide's output schema properties [action, confidence]"},
		},
		{
			name: "tool node output_schema_override never constrains references",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: fetch
          type: tool
          tool_name: web_request
          output_schema_override:
            type: object
            required: [status]
      output_mapping:
        file: "{{fetch.output.filename}}"
`,
			agents: schemalessDecider,
		},
		{
			name: "camelCase outputMapping is checked",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      outputMapping:
        action: "{{decide.output.action}}"
`,
			agents: schemalessDecider,
			want:   []string{decideSchemaless("action")},
		},
		{
			name: "camelCase inputSchema is read",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      inputSchema:
        type: object
        properties:
          transaction: {type: object}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            account: "{{workflow.input.account}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			wantWarnings: []string{"node 'decide' input 'account' references '{{workflow.input.account}}' " +
				"but 'account' is not in workflow input_schema properties [transaction]"},
		},
		{
			name: "misspelled workflow input is an error in the flat shape",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          query: {type: string}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            q: "{{workflow.input.qeury}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'decide' input 'q' references '{{workflow.input.qeury}}' " +
				"but 'qeury' is not in workflow input_schema properties [query]: did you mean 'query'? " +
				"If 'qeury' is a separate input, declare it in input_schema properties"},
		},
		{
			name: "misspelled workflow input is an error in the nested shape too",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      workflow:
        input_schema:
          type: object
          properties:
            transaction: {type: object}
        nodes:
          - id: decide
            type: agent
            agent_name: PlatformDecider
            input:
              txn: "{{workflow.input.Transaction}}"
        output_mapping:
          decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'decide' input 'txn' references '{{workflow.input.Transaction}}' " +
				"but 'Transaction' is not in workflow input_schema properties [transaction]: did you mean 'transaction'? " +
				"If 'Transaction' is a separate input, declare it in input_schema properties"},
		},
		{
			name: "short undeclared workflow input one edit from a declared one is still optional",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          id: {type: string}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            address: "{{workflow.input.ip}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			wantWarnings: []string{"node 'decide' input 'address' references '{{workflow.input.ip}}' " +
				"but 'ip' is not in workflow input_schema properties [id]"},
		},
		{
			name: "workflow_input node output resolves as the workflow input",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          account: {type: string}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            account: "{{workflow_input.output.account}}"
            everything: "{{workflow_input.output}}"
            typo: "{{workflow_input.output.acount}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'decide' input 'typo' references '{{workflow_input.output.acount}}' " +
				"but 'acount' is not in workflow input_schema properties [account]: did you mean 'account'? " +
				"If 'acount' is a separate input, declare it in input_schema properties"},
		},
		{
			name: "a sibling of a declared input is offered both fixes",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          min_price: {type: number}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            ceiling: "{{workflow.input.max_price}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'decide' input 'ceiling' references '{{workflow.input.max_price}}' " +
				"but 'max_price' is not in workflow input_schema properties [min_price]: did you mean 'min_price'? " +
				"If 'max_price' is a separate input, declare it in input_schema properties"},
		},
		{
			name: "undeclared input read through workflow_input.output fails the node when omitted",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          account: {type: string}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            region: "{{workflow_input.output.region}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'decide' input 'region' references '{{workflow_input.output.region}}' " +
				"but 'region' is not in workflow input_schema properties [account], and read through workflow_input.output " +
				"an input the caller omits fails the node: declare it in input_schema properties and required if callers " +
				"must supply it, or reference it as '{{workflow.input.region}}' if it is optional"},
		},
		{
			name: "undeclared input read through workflow_input.output in output_mapping stays as template text",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          account: {type: string}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
      output_mapping:
        region: "{{workflow_input.output.region}}"
`,
			agents: schemalessDecider,
			want: []string{"output_mapping 'region' references '{{workflow_input.output.region}}' " +
				"but 'region' is not in workflow input_schema properties [account], and read through workflow_input.output " +
				"an input the caller omits leaves the template text in the workflow result: declare it in input_schema " +
				"properties and required if callers must supply it, or reference it as '{{workflow.input.region}}' if it is optional"},
		},
		{
			name: "undeclared input read through workflow_input.output in a switch case offers no optional rewrite",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          account: {type: string}
      nodes:
        - id: route
          type: switch
          cases:
            - condition: "'{{workflow_input.output.region}}' != 'eu'"
              node: decide
        - id: decide
          type: agent
          agent_name: PlatformDecider
          depends_on: [route]
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'route' switch case references '{{workflow_input.output.region}}' " +
				"but 'region' is not in workflow input_schema properties [account], and read through workflow_input.output " +
				"an input the caller omits makes the case not match: declare it in input_schema properties and required " +
				"if callers must supply it, or declare it in properties alone if it is optional, so the case does not match " +
				"when it is omitted"},
		},
		{
			name: "undeclared input read through workflow_input.output in map items offers no optional form",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          account: {type: string}
      nodes:
        - id: fanout
          type: map
          items: "{{workflow_input.output.urls}}"
          node: fetch
        - id: fetch
          type: agent
          agent_name: PlatformFetcher
      output_mapping:
        results: "{{fanout.output}}"
`,
			agents: schemalessDecider,
			want: []string{"node 'fanout' items references '{{workflow_input.output.urls}}' " +
				"but 'urls' is not in workflow input_schema properties [account], and read through workflow_input.output " +
				"an input the caller omits fails the node: declare it in input_schema properties and required if callers " +
				"must supply it"},
		},
		{
			name: "multi-byte short name is held to the short-name typo budget",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      input_schema:
        type: object
        properties:
          ea: {type: string}
      nodes:
        - id: decide
          type: agent
          agent_name: PlatformDecider
          input:
            accent: "{{workflow.input.éa}}"
      output_mapping:
        decision: "{{decide.output}}"
`,
			agents: schemalessDecider,
			wantWarnings: []string{"node 'decide' input 'accent' references '{{workflow.input.éa}}' " +
				"but 'éa' is not in workflow input_schema properties [ea]"},
		},
		{
			name: "whole-output input counts a field the upstream names only in required",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
        - id: act
          type: agent
          agent_name: Actor
          depends_on: [decide]
          input:
            verdict: "{{decide.output}}"
      output_mapping:
        result: "{{act.output}}"
`,
			agents: map[string]wfAgentSchema{
				"Decider": {Name: "Decider", OutputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"action": map[string]any{"type": "string"}},
					"required":   []any{"action", "confidence"},
				}},
				"Actor": {Name: "Actor", InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{"verdict": map[string]any{
						"type":     "object",
						"required": []any{"confidence", "reason"},
					}},
				}},
			},
			want: []string{"node 'act' input 'verdict' ← decide output missing required field 'reason'"},
		},
		{
			name: "whole-output input from a composite upstream schema is not judged",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
        - id: act
          type: agent
          agent_name: Actor
          depends_on: [decide]
          input:
            verdict: "{{decide.output}}"
      output_mapping:
        result: "{{act.output}}"
`,
			agents: map[string]wfAgentSchema{
				"Decider": {Name: "Decider", OutputSchema: map[string]any{
					"properties": map[string]any{"action": map[string]any{"type": "string"}},
					"allOf":      []any{map[string]any{"required": []any{"reason"}}},
				}},
				"Actor": {Name: "Actor", InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{"verdict": map[string]any{
						"type":     "object",
						"required": []any{"reason"},
					}},
				}},
			},
		},
		{
			name: "map body references to the current item are not node references",
			workflow: `
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: fanout
          type: map
          items: "{{workflow.input.rows}}"
          node: handle
        - id: handle
          type: agent
          agent_name: PlatformHandler
          input:
            id: "{{_map_item.id}}"
            position: "{{_map_index}}"
            attempt: "{{_loop_iteration}}"
      output_mapping:
        results: "{{fanout.output}}"
`,
			agents: schemalessDecider,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appConfig := wfExtractAppConfig([]byte(tt.workflow))
			if appConfig == nil {
				t.Fatal("fixture has no apps[0].app_config")
			}
			findings := checkWorkflowSchema(wfName, appConfig, tt.agents)
			if got := wfMessages(findings.errors); !slices.Equal(got, tt.want) {
				t.Errorf("errors mismatch\n got: %q\nwant: %q", got, tt.want)
			}
			if got := wfMessages(findings.warnings); !slices.Equal(got, tt.wantWarnings) {
				t.Errorf("warnings mismatch\n got: %q\nwant: %q", got, tt.wantWarnings)
			}
		})
	}
}

func TestCheckWorkflowSchemaOutputMappingErrorPath(t *testing.T) {
	appConfig := wfExtractAppConfig([]byte(`
apps:
  - name: DecisionFlow
    app_config:
      nodes:
        - id: decide
          type: agent
          agent_name: Decider
      output_mapping:
        action: "{{decide.output.action}}"
`))
	errs := checkWorkflowSchema("DecisionFlow", appConfig, map[string]wfAgentSchema{"Decider": {Name: "Decider"}}).errors
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	if want := "workflow 'DecisionFlow'.output_mapping.action"; errs[0].Path != want {
		t.Errorf("path = %q, want %q", errs[0].Path, want)
	}
}

func TestCheckWorkflowSchemaEscalationAnswerFields(t *testing.T) {
	const workflow = `
apps:
  - name: TriageFlow
    app_config:
      nodes:
        - id: triage
          type: decision
          input: {ticket: "{{workflow.input.ticket}}"}
          questions:
            department:
              type: choice
              instructions: "Which team owns ticket?"
              criteria: {billing: "Payment issues", other: "Anything else"}
          route_on: department
          routes: {billing: billing_agent}
          default: general_agent
          min_confidence: 0.8
          escalate: reclassify
%s
        - id: reclassify
          type: agent
          agent_name: Classifier
          depends_on: [triage]
          input: {ticket: "{{workflow.input.ticket}}"}
%s
        - id: billing_agent
          type: agent
          agent_name: Billing
          depends_on: [triage]
          input: {ticket: "{{workflow.input.ticket}}"}
        - id: general_agent
          type: agent
          agent_name: General
          depends_on: [triage]
          input: {ticket: "{{workflow.input.ticket}}"}
`
	const declaresDepartment = `          output_schema_override:
            type: object
            properties: {department: {type: string, enum: [billing, other]}}
            required: [department]`
	agents := map[string]wfAgentSchema{
		"Classifier": {Name: "Classifier"},
		"Billing":    {Name: "Billing"},
		"General":    {Name: "General"},
	}
	tests := []struct {
		name     string
		mode     string
		override string
		want     []string
	}{
		{
			name: "rerouting escalate target without an output schema",
			want: []string{"node 'triage' escalation answer references '{{reclassify.output.department}}', " +
				"but node 'reclassify' declares no output schema, so its output has no guaranteed fields: declare 'department' " +
				"in the node's output_schema_override (or the output_schema of agent 'Classifier') and instruct the agent to return it"},
		},
		{
			name:     "rerouting escalate target declaring the field",
			override: declaresDepartment,
		},
		{
			name: "handoff escalate target, whose answer nothing reads",
			mode: "          escalation_mode: handoff",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appConfig := wfExtractAppConfig(fmt.Appendf(nil, workflow, tt.mode, tt.override))
			if appConfig == nil {
				t.Fatal("fixture has no apps[0].app_config")
			}
			findings := checkWorkflowSchema("TriageFlow", appConfig, agents)
			if got := wfMessages(findings.errors); !slices.Equal(got, tt.want) {
				t.Errorf("errors mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func wfMessages(findings []schema.ValidationError) []string {
	var msgs []string
	for _, f := range findings {
		msgs = append(msgs, f.Message)
	}
	return msgs
}
