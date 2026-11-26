// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

func knownWorkflowConfig(name string) *workflow.WorkflowConfig {
	switch name {
	case "SimpleTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "SimpleTestWorkflow",
			Namespace:   "test-ns",
			Description: "A simple 2-node workflow for testing",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
				"required": []any{"input_text"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"final_result": map[string]any{"type": "string"},
				},
				"required": []any{"final_result"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input: map[string]any{
						"task_description": "{{workflow.input.input_text}}",
					},
				},
				{
					ID:        "step_2",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"step_1"},
					Input: map[string]any{
						"task_description": "Process the output from step 1",
					},
				},
			},
			OutputMapping: map[string]any{
				"final_result": "{{step_2.output}}",
			},
		}

	case "AskUserTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "AskUserTestWorkflow",
			Namespace:   "test-ns",
			Description: "A one-node workflow whose agent asks the user a question",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
				"required": []any{"input_text"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "ask",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "{{workflow.input.input_text}}"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{ask.output}}",
			},
		}

	case "StructuredTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "StructuredTestWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow with structured input/output schemas for validation testing",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"customer_name": map[string]any{"type": "string"},
					"order_id":      map[string]any{"type": "string"},
					"amount":        map[string]any{"type": "integer"},
				},
				"required": []any{"customer_name", "order_id", "amount"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"customer_name": map[string]any{"type": "string"},
					"order_id":      map[string]any{"type": "string"},
					"amount":        map[string]any{"type": "integer"},
					"status":        map[string]any{"type": "string"},
					"processed":     map[string]any{"type": "boolean"},
				},
				"required": []any{"customer_name", "order_id", "amount", "status", "processed"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "validate_order",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input: map[string]any{
						"customer_name": "{{workflow.input.customer_name}}",
						"order_id":      "{{workflow.input.order_id}}",
						"amount":        "{{workflow.input.amount}}",
					},
					InputSchemaOverride: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"customer_name": map[string]any{"type": "string"},
							"order_id":      map[string]any{"type": "string"},
							"amount":        map[string]any{"type": "integer"},
						},
						"required": []any{"customer_name", "order_id", "amount"},
					},
					OutputSchemaOverride: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"customer_name": map[string]any{"type": "string"},
							"order_id":      map[string]any{"type": "string"},
							"amount":        map[string]any{"type": "integer"},
							"status":        map[string]any{"type": "string"},
						},
						"required": []any{"customer_name", "order_id", "amount", "status"},
					},
				},
				{
					ID:        "process_order",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"validate_order"},
					Input: map[string]any{
						"customer_name": "{{validate_order.output.customer_name}}",
						"order_id":      "{{validate_order.output.order_id}}",
						"amount":        "{{validate_order.output.amount}}",
						"status":        "{{validate_order.output.status}}",
					},
					InputSchemaOverride: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"customer_name": map[string]any{"type": "string"},
							"order_id":      map[string]any{"type": "string"},
							"amount":        map[string]any{"type": "integer"},
							"status":        map[string]any{"type": "string"},
						},
						"required": []any{"customer_name", "order_id", "amount", "status"},
					},
					OutputSchemaOverride: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"customer_name": map[string]any{"type": "string"},
							"order_id":      map[string]any{"type": "string"},
							"amount":        map[string]any{"type": "integer"},
							"status":        map[string]any{"type": "string"},
							"processed":     map[string]any{"type": "boolean"},
						},
						"required": []any{"customer_name", "order_id", "amount", "status", "processed"},
					},
				},
			},
			OutputMapping: map[string]any{
				"customer_name": "{{process_order.output.customer_name}}",
				"order_id":      "{{process_order.output.order_id}}",
				"amount":        "{{process_order.output.amount}}",
				"status":        "{{process_order.output.status}}",
				"processed":     "{{process_order.output.processed}}",
			},
		}

	case "SwitchTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "SwitchTestWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow with switch (multi-way) branching",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string"},
				},
				"required": []any{"action"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"result":       map[string]any{"type": "string"},
					"action_taken": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:   "route_action",
					Type: "switch",
					Cases: []workflow.SwitchCase{
						{Condition: "'{{workflow.input.action}}' == 'create'", Node: "create_handler"},
						{Condition: "'{{workflow.input.action}}' == 'update'", Node: "update_handler"},
						{Condition: "'{{workflow.input.action}}' == 'delete'", Node: "delete_handler"},
					},
					DefaultCase: "default_handler",
				},
				{
					ID:        "create_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					DependsOn: []string{"route_action"},
					Input:     map[string]any{"task": "Create resource"},
				},
				{
					ID:        "update_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"route_action"},
					Input:     map[string]any{"task": "Update resource"},
				},
				{
					ID:        "delete_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentC",
					DependsOn: []string{"route_action"},
					Input:     map[string]any{"task": "Delete resource"},
				},
				{
					ID:        "default_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					DependsOn: []string{"route_action"},
					Input:     map[string]any{"task": "Handle unknown action"},
				},
			},
			OutputMapping: map[string]any{
				"result":       "{{create_handler.output.result}}",
				"action_taken": "{{create_handler.output.action}}",
			},
		}

	case "MapTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "MapTestWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow that iterates over a list of items",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required": []any{"items"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"results": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "object"},
					},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:              "process_items",
					Type:            "map",
					TemplateNode:    "process_single_item",
					ItemsExpression: "{{workflow.input.items}}",
				},
				{
					ID:        "process_single_item",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input: map[string]any{
						"item":  "{{_map_item}}",
						"index": "{{_map_index}}",
					},
				},
			},
			OutputMapping: map[string]any{
				"results": "{{process_items.output}}",
			},
		}

	case "LoopTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "LoopTestWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow with loop iteration",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"max_count": map[string]any{"type": "integer"},
				},
				"required": []any{"max_count"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"final_count": map[string]any{"type": "integer"},
					"iterations":  map[string]any{"type": "integer"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:            "count_loop",
					Type:          "loop",
					BodyNode:      "increment_counter",
					Condition:     "{{increment_counter.output.count}} < {{workflow.input.max_count}}",
					MaxIterations: 10,
				},
				{
					ID:        "increment_counter",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input: map[string]any{
						"task":              "Increment counter",
						"current_iteration": "{{_loop_iteration}}",
					},
				},
			},
			OutputMapping: map[string]any{
				"final_count": "{{increment_counter.output.count}}",
				"iterations":  "{{count_loop.output.iterations_completed}}",
			},
		}

	case "ParallelTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ParallelTestWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow with parallel independent nodes",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
				"required": []any{"input_text"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"result_a": map[string]any{"type": "string"},
					"result_b": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "task_a",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "Process task A: {{workflow.input.input_text}}"},
				},
				{
					ID:        "task_b",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					Input:     map[string]any{"task_description": "Process task B: {{workflow.input.input_text}}"},
				},
				{
					ID:        "combine",
					Type:      "agent",
					AgentName: "TestPeerAgentC",
					DependsOn: []string{"task_a", "task_b"},
					Input: map[string]any{
						"result_a": "{{task_a.output}}",
						"result_b": "{{task_b.output}}",
					},
				},
			},
			OutputMapping: map[string]any{
				"result_a": "{{task_a.output}}",
				"result_b": "{{task_b.output}}",
			},
		}

	case "ConditionalSkipWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ConditionalSkipWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow testing the 'when' conditional skip",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skip_step2": map[string]any{"type": "boolean"},
				},
				"required": []any{"skip_step2"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"result": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "Step 1"},
				},
				{
					ID:        "step_2",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"step_1"},
					When:      "'{{workflow.input.skip_step2}}' != 'true'",
					Input:     map[string]any{"task_description": "Step 2 (conditional)"},
				},
				{
					ID:        "step_3",
					Type:      "agent",
					AgentName: "TestPeerAgentC",
					DependsOn: []string{"step_1"},
					Input:     map[string]any{"task_description": "Step 3 (always runs)"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{step_3.output}}",
			},
		}

	case "NonFailFastWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "NonFailFastWorkflow",
			Namespace:   "test-ns",
			Description: "A workflow with FailFast=false to test cascading skip",
			Version:     "1.0.0",
			FailFast:    false,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"result": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "failing_step",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "This will fail"},
				},
				{
					ID:        "dependent_step",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"failing_step"},
					Input:     map[string]any{"task_description": "This depends on failing_step"},
				},
				{
					ID:        "independent_step",
					Type:      "agent",
					AgentName: "TestPeerAgentC",
					Input:     map[string]any{"task_description": "This runs independently"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{independent_step.output}}",
			},
		}

	case "CancelTestWorkflow":
		return &workflow.WorkflowConfig{
			Name:            "CancelTestWorkflow",
			Namespace:       "test-ns",
			WorkflowTimeout: 30 * time.Second,
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "step_1_agent",
				},
				{
					ID:        "step_2",
					Type:      "agent",
					AgentName: "step_2_agent",
					DependsOn: []string{"step_1"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{step_2.output}}",
			},
		}

	case "ExitHandlerSuccessWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ExitHandlerSuccessWorkflow",
			Namespace:   "test-ns",
			Description: "Tests exit handler execution on workflow success",
			Version:     "1.0.0",
			FailFast:    true,
			OnExit: &workflow.ExitHandler{
				OnSuccess: "success_handler",
			},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "{{workflow.input.input_text}}"},
				},
				{
					ID:        "success_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					Input:     map[string]any{"outcome": "success"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{step_1.output}}",
			},
		}

	case "ExitHandlerFailureWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ExitHandlerFailureWorkflow",
			Namespace:   "test-ns",
			Description: "Tests exit handler execution on workflow failure",
			Version:     "1.0.0",
			FailFast:    true,
			OnExit: &workflow.ExitHandler{
				OnFailure: "failure_handler",
			},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "failing_step",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "This will fail"},
				},
				{
					ID:        "failure_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					Input:     map[string]any{"outcome": "failure"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{failing_step.output}}",
			},
		}

	case "NodeTimeoutWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "NodeTimeoutWorkflow",
			Namespace:   "test-ns",
			Description: "Tests that a workflow node times out when the target agent never replies",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task": "{{workflow.input.input_text}}"},
				},
				{
					ID:        "blocked_step",
					Type:      "agent",
					AgentName: "NoReplyAgentTimeout",
					Timeout:   1 * time.Second,
					DependsOn: []string{"step_1"},
					Input:     map[string]any{"task": "this never gets answered"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{blocked_step.output}}",
			},
		}

	case "ExitHandlerIndependentBudgetWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ExitHandlerIndependentBudgetWorkflow",
			Namespace:   "test-ns",
			Description: "Tests that exit handlers each get their own per-handler timeout budget",
			Version:     "1.0.0",
			FailFast:    true,
			OnExit: &workflow.ExitHandler{
				Always:    "always_blocked_handler",
				OnFailure: "failure_handler",
			},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "failing_step",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task": "{{workflow.input.input_text}}"},
				},
				{
					ID:        "always_blocked_handler",
					Type:      "agent",
					AgentName: "NoReplyAgentExitAlways",
					Timeout:   1 * time.Second,
					Input:     map[string]any{"outcome": "always"},
				},
				{
					ID:        "failure_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					Timeout:   10 * time.Second,
					Input:     map[string]any{"outcome": "failure"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{failing_step.output}}",
			},
		}

	case "WorkflowLevelTimeoutWorkflow":
		return &workflow.WorkflowConfig{
			Name:            "WorkflowLevelTimeoutWorkflow",
			Namespace:       "test-ns",
			Description:     "Tests that the workflow-level WorkflowTimeout terminates a stuck DAG",
			Version:         "1.0.0",
			FailFast:        true,
			WorkflowTimeout: 1 * time.Second,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task": "{{workflow.input.input_text}}"},
				},
				{
					ID:        "blocked_step",
					Type:      "agent",
					AgentName: "NoReplyAgentWorkflowTimeout",
					DependsOn: []string{"step_1"},
					Input:     map[string]any{"task": "this never gets answered"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{blocked_step.output}}",
			},
		}

	case "MapTimeoutWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "MapTimeoutWorkflow",
			Namespace:   "test-ns",
			Description: "Tests that map iteration per-branch timers fire when their template agent never replies",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required": []any{"items"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "prep",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task": "prepare for map"},
				},
				{
					ID:              "process_items",
					Type:            "map",
					TemplateNode:    "process_single_item",
					ItemsExpression: "{{workflow.input.items}}",
					DependsOn:       []string{"prep"},
				},
				{
					ID:        "process_single_item",
					Type:      "agent",
					AgentName: "NoReplyAgentMapItem",
					Timeout:   1 * time.Second,
					Input: map[string]any{
						"item":  "{{_map_item}}",
						"index": "{{_map_index}}",
					},
				},
			},
			OutputMapping: map[string]any{
				"results": "{{process_items.output}}",
			},
		}

	case "NodeTimeoutRetryWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "NodeTimeoutRetryWorkflow",
			Namespace:   "test-ns",
			Description: "Tests that a node timeout triggers a retry, then fails after retries exhaust",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input_text": map[string]any{"type": "string"},
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task": "{{workflow.input.input_text}}"},
				},
				{
					ID:        "blocked_step",
					Type:      "agent",
					AgentName: "NoReplyAgentTimeoutRetry",
					DependsOn: []string{"step_1"},
					Timeout:   500 * time.Millisecond,
					Retry: &workflow.RetryStrategy{
						Limit:       1,
						RetryPolicy: "OnError",
						Backoff: &workflow.BackoffStrategy{
							Duration: 50 * time.Millisecond,
							Factor:   1,
						},
					},
					Input: map[string]any{"task": "this never gets answered"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{blocked_step.output}}",
			},
		}

	case "ToolNodeBuiltinWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolNodeBuiltinWorkflow",
			Namespace:   "test-ns",
			Description: "Workflow that invokes a built-in Go tool directly via a tool node",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{
					"tool_type": "builtin",
					"tool_name": "get_current_time",
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "fetch_time",
					Type:     "tool",
					ToolName: "get_current_time",
				},
			},
			OutputMapping: map[string]any{
				"tool_status":   "{{fetch_time.output.status}}",
				"tool_timezone": "{{fetch_time.output.timezone}}",
			},
		}

	case "ToolNodeCreatedArtifactWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolNodeCreatedArtifactWorkflow",
			Namespace:   "test-ns",
			Description: "Workflow that chains from the artifact a tool node wrote",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "artifact_search_and_replace_regex"},
				map[string]any{"tool_type": "builtin", "tool_name": "load_artifact"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "rewrite",
					Type:     "tool",
					ToolName: "artifact_search_and_replace_regex",
					Input: map[string]any{
						"filename":           "source.txt",
						"search_expression":  "before",
						"replace_expression": "after",
						"new_filename":       "rewritten.txt",
					},
				},
				{
					ID:        "read_back",
					Type:      "tool",
					ToolName:  "load_artifact",
					DependsOn: []string{"rewrite"},
					Input:     map[string]any{"filename": "{{rewrite.output.created_artifact}}"},
				},
			},
			OutputMapping: map[string]any{
				"written_name":      "{{rewrite.output.created_artifact}}",
				"written_bare_name": "{{rewrite.output.created_artifact_name}}",
				"written_version":   "{{rewrite.output.created_artifact_version}}",
				"loaded_name":       "{{read_back.output.filename}}",
				"loaded_version":    "{{read_back.output.version}}",
				"loaded_content":    "{{read_back.output.content}}",
			},
		}

	case "ToolNodeUnknownToolWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolNodeUnknownToolWorkflow",
			Namespace:   "test-ns",
			Description: "Workflow whose tool node references an unknown tool — fails terminally on dispatch",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{
					"tool_type": "builtin",
					"tool_name": "get_current_time",
				},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "missing",
					Type:     "tool",
					ToolName: "this_tool_does_not_exist",
				},
			},
			OutputMapping: map[string]any{
				"unused": "{{missing.output.message}}",
			},
		}

	case "ToolThenAgentWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolThenAgentWorkflow",
			Namespace:   "test-ns",
			Description: "tool node output feeds a downstream agent node's input",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "fetch_time",
					Type:     "tool",
					ToolName: "get_current_time",
				},
				{
					ID:          "consume",
					Type:        "agent",
					AgentName:   "TestPeerAgentA",
					DependsOn:   []string{"fetch_time"},
					Instruction: "tool_tz_marker_{{fetch_time.output.timezone}}",
				},
			},
			OutputMapping: map[string]any{
				"tool_status": "{{fetch_time.output.status}}",
				"consumed":    "{{consume.output.consumed}}",
			},
		}

	case "AgentThenToolWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "AgentThenToolWorkflow",
			Namespace:   "test-ns",
			Description: "an agent node's output feeds a tool node's input",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "produce_city",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task": "pick a city"},
				},
				{
					ID:        "weather",
					Type:      "tool",
					ToolName:  "get_weather_tool",
					DependsOn: []string{"produce_city"},
					Input: map[string]any{
						"location": "{{produce_city.output.city}}",
					},
				},
			},
			OutputMapping: map[string]any{
				"temp":        "{{weather.output.data.temperature}}",
				"condition":   "{{weather.output.data.condition}}",
				"tool_status": "{{weather.output.status}}",
			},
		}

	case "ToolSwitchWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolSwitchWorkflow",
			Namespace:   "test-ns",
			Description: "a tool node's output drives a switch branch selection",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "classify",
					Type:     "tool",
					ToolName: "get_weather_tool",
					Input:    map[string]any{"location": "London"},
				},
				{
					ID:   "route",
					Type: "switch",
					Cases: []workflow.SwitchCase{
						{Condition: "'{{classify.output.data.temperature}}' == '22'", Node: "warm_branch"},
					},
					DefaultCase: "cold_branch",
					DependsOn:   []string{"classify"},
				},
				{
					ID:        "warm_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					DependsOn: []string{"route"},
					Input:     map[string]any{"task": "handle warm"},
				},
				{
					ID:        "cold_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"route"},
					Input:     map[string]any{"task": "handle cold"},
				},
			},
			OutputMapping: map[string]any{
				"branch": "{{warm_branch.output.branch}}",
				"temp":   "{{classify.output.data.temperature}}",
			},
		}

	case "DecisionRoutingWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "DecisionRoutingWorkflow",
			Namespace:   "test-ns",
			Description: "a decision node routes a ticket to one team or escalates it",
			Version:     "1.0.0",
			FailFast:    true,
			Decision: &workflow.DecisionProviderConfig{
				Provider: workflow.DecisionProviderTypeSafe,
				APIKey:   "test-key",
				Model:    "jev-1.13.0",
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:    "triage",
					Type:  "decision",
					Input: map[string]any{"ticket": "{{workflow.input.input_text}}"},
					Questions: []decision.Question{{
						Key:          "department",
						Type:         decision.TypeChoice,
						Instructions: "Which team owns `ticket`?",
						Criteria: []decision.Criterion{
							{Key: "billing", Description: "Payment, refund or subscription issues"},
							{Key: "technical", Description: "Bugs, outages or integration problems"},
						},
					}},
					RouteOn:        "department",
					Routes:         map[string]string{"billing": "billing_branch", "technical": "technical_branch"},
					MinConfidence:  0.8,
					Escalate:       "review_branch",
					EscalationMode: workflow.EscalationModeHandoff,
				},
				{
					ID:        "billing_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					DependsOn: []string{"triage"},
					Input:     map[string]any{"task": "handle billing"},
				},
				{
					ID:        "technical_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"triage"},
					Input:     map[string]any{"task": "handle technical"},
				},
				{
					ID:        "review_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"triage"},
					Input:     map[string]any{"task": "review the triage"},
				},
			},
			OutputMapping: map[string]any{
				"branch": map[string]any{"coalesce": []any{
					"{{billing_branch.output.branch}}",
					"{{technical_branch.output.branch}}",
					"{{review_branch.output.branch}}",
				}},
				"department": "{{triage.output.answers.department.choice}}",
				"escalated":  "{{triage.output.escalated}}",
				"selected":   "{{triage.output.selected_branch}}",
				"routed":     "{{triage.output.routed_branch}}",
			},
		}

	case "DecisionRoutingWorkflowOpenAI":
		cfg := knownWorkflowConfig("DecisionRoutingWorkflow")
		cfg.Name = name
		cfg.Description = "a decision node on OpenAI's Decisions API routes a ticket to one team or escalates it"
		cfg.Decision = &workflow.DecisionProviderConfig{
			Provider: workflow.DecisionProviderOpenAI,
			APIKey:   "test-key",
			Model:    "gpt-6-luna",
		}
		cfg.Nodes[0].OnProviderError = "escalate"
		return cfg

	case "DecisionImageWorkflowOpenAI":
		cfg := knownWorkflowConfig("DecisionRoutingWorkflowOpenAI")
		cfg.Name = name
		cfg.Description = "a decision node on OpenAI's Decisions API routes a ticket and its photos to one team or escalates it"
		cfg.Decision.MaxImages = 2
		cfg.Nodes[0].Images = []string{"{{workflow.input.photos}}"}
		return cfg

	case "DecisionRoutingWorkflowGemini":
		cfg := knownWorkflowConfig("DecisionRoutingWorkflow")
		cfg.Name = name
		cfg.Description = "a decision node on Gemini's Decisions API routes a ticket to one team or escalates it"
		cfg.Decision = &workflow.DecisionProviderConfig{
			Provider: workflow.DecisionProviderGoogleAIStudio,
			APIKey:   "test-key",
			Model:    "gemini-decision-1",
		}
		cfg.Nodes[0].OnProviderError = "escalate"
		cfg.Nodes[0].Questions[0].Criteria[1].Description = ""
		return cfg

	case "DecisionImageWorkflowGemini":
		cfg := knownWorkflowConfig("DecisionRoutingWorkflowGemini")
		cfg.Name = name
		cfg.Description = "a decision node on Gemini's Decisions API routes a ticket and its photos to one team or escalates it"
		cfg.Decision.MaxImages = 2
		cfg.Nodes[0].Images = []string{"{{workflow.input.photos}}"}
		return cfg

	case "DecisionMediaWorkflowGemini":
		cfg := knownWorkflowConfig("DecisionImageWorkflowGemini")
		cfg.Name = name
		cfg.Description = "a decision node on Gemini's Decisions API routes a ticket with its photos, documents, call recordings and clips"
		cfg.Decision.MaxMedia = 2
		cfg.Nodes[0].Documents = []string{"{{workflow.input.documents}}"}
		cfg.Nodes[0].Audio = []string{"{{workflow.input.calls}}"}
		cfg.Nodes[0].Video = []string{"{{workflow.input.clips}}"}
		cfg.Nodes[0].VideoResolution = "low"
		return cfg

	case "DecisionImageWorkflowOpenRouter":
		cfg := knownWorkflowConfig("DecisionRoutingWorkflow")
		cfg.Name = name
		cfg.Description = "a decision node on GPT-6 Luna Decisions through OpenRouter routes a ticket and its photos to one team"
		cfg.Decision.Model = "openai/gpt-6-luna-decisions"
		cfg.Decision.MaxImages = 2
		cfg.Nodes[0].Images = []string{"{{workflow.input.photos}}"}
		return cfg

	case "DecisionEscalationRoutingWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "DecisionEscalationRoutingWorkflow",
			Namespace:   "test-ns",
			Description: "a decision node escalates an unsure ticket and routes on the escalation's answer",
			Version:     "1.0.0",
			FailFast:    true,
			Decision: &workflow.DecisionProviderConfig{
				Provider: workflow.DecisionProviderTypeSafe,
				APIKey:   "test-key",
				Model:    "jev-1.13.0",
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:    "triage",
					Type:  "decision",
					Input: map[string]any{"ticket": "{{workflow.input.input_text}}"},
					Questions: []decision.Question{{
						Key:          "department",
						Type:         decision.TypeChoice,
						Instructions: "Which team owns `ticket`?",
						Criteria: []decision.Criterion{
							{Key: "billing", Description: "Payment, refund or subscription issues"},
							{Key: "technical", Description: "Bugs, outages or integration problems"},
						},
					}},
					RouteOn:       "department",
					Routes:        map[string]string{"billing": "billing_branch", "technical": "technical_branch"},
					MinConfidence: 0.8,
					Escalate:      "reclassify",
				},
				{
					ID:        "reclassify",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"triage"},
					Input:     map[string]any{"task": "reclassify the ticket"},
				},
				{
					ID:        "billing_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					DependsOn: []string{"triage"},
					Input:     map[string]any{"task": "handle billing"},
				},
				{
					ID:        "technical_branch",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					DependsOn: []string{"triage"},
					Input:     map[string]any{"task": "handle technical"},
				},
			},
			OutputMapping: map[string]any{
				"branch": map[string]any{"coalesce": []any{
					"{{billing_branch.output.branch}}",
					"{{technical_branch.output.branch}}",
				}},
				"department":   "{{triage.output.answers.department.choice}}",
				"escalated":    "{{triage.output.escalated}}",
				"reclassified": "{{reclassify.output.department}}",
				"selected":     "{{triage.output.selected_branch}}",
				"routed":       "{{triage.output.routed_branch}}",
			},
		}

	case "DecisionMapNestedWorkflow":
		return mustParseWorkflowYAML(name, decisionMapNestedWorkflowYAML)

	case "DecisionMapNestedItemWorkflow":
		return mustParseWorkflowYAML(name, decisionMapNestedItemWorkflowYAML)

	case "ToolMapWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolMapWorkflow",
			Namespace:   "test-ns",
			Description: "a map whose per-item template invokes a child workflow containing a tool node",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"items"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:              "process_items",
					Type:            "map",
					TemplateNode:    "item_proc",
					ItemsExpression: "{{workflow.input.items}}",
				},
				{
					ID:           "item_proc",
					Type:         "workflow",
					WorkflowName: "MapItemToolWorkflow",
					Input:        map[string]any{"item": "{{_map_item}}"},
				},
			},
			OutputMapping: map[string]any{
				"results": "{{process_items.output}}",
			},
		}

	case "MapItemToolWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "MapItemToolWorkflow",
			Namespace:   "test-ns",
			Description: "child workflow that runs a tool node over one map item",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"item": map[string]any{"type": "string"}},
			},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "item_weather",
					Type:     "tool",
					ToolName: "get_weather_tool",
					Input:    map[string]any{"location": "{{workflow.input.item}}"},
				},
			},
			OutputMapping: map[string]any{
				"item_temp":   "{{item_weather.output.data.temperature}}",
				"item_status": "{{item_weather.output.status}}",
			},
		}

	case "ToolLoopWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolLoopWorkflow",
			Namespace:   "test-ns",
			Description: "a loop whose body invokes a child workflow containing a tool node",
			Version:     "1.0.0",
			FailFast:    true,
			Nodes: []*workflow.NodeConfig{
				{
					ID:            "tick_loop",
					Type:          "loop",
					BodyNode:      "tick",
					Condition:     "'{{tick.output.body_status}}' == 'success'",
					MaxIterations: 3,
				},
				{
					ID:           "tick",
					Type:         "workflow",
					WorkflowName: "LoopBodyToolWorkflow",
				},
			},
			OutputMapping: map[string]any{
				"iterations":  "{{tick_loop.output.iterations_completed}}",
				"last_status": "{{tick.output.body_status}}",
			},
		}

	case "LoopBodyToolWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "LoopBodyToolWorkflow",
			Namespace:   "test-ns",
			Description: "child workflow that runs a tool node as a loop body",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "fetch",
					Type:     "tool",
					ToolName: "get_current_time",
				},
			},
			OutputMapping: map[string]any{
				"body_status": "{{fetch.output.status}}",
			},
		}

	case "BareToolMapWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "BareToolMapWorkflow",
			Namespace:   "test-ns",
			Description: "a map whose template is a bare tool node (no child-workflow wrapper)",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"items"},
			},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:              "process_items",
					Type:            "map",
					TemplateNode:    "item_weather",
					ItemsExpression: "{{workflow.input.items}}",
				},
				{
					ID:       "item_weather",
					Type:     "tool",
					ToolName: "get_weather_tool",
					Input:    map[string]any{"location": "{{_map_item}}"},
				},
			},
			OutputMapping: map[string]any{
				"results": "{{process_items.output.results}}",
			},
		}

	case "PreviewBytesMapWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "PreviewBytesMapWorkflow",
			Namespace:   "test-ns",
			Description: "a map over a data tool's result_preview with a raised preview byte cap",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin-group", "group_name": "data_analysis"},
				map[string]any{
					"tool_type":   "builtin",
					"tool_name":   "transform_data_with_jmespath",
					"tool_config": map[string]any{"max_result_preview_bytes": 16384},
				},
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "select_accounts",
					Type:     "tool",
					ToolName: "transform_data_with_jmespath",
					Input: map[string]any{
						"input_filename":      "accounts.json",
						"jmespath_expression": "[*]",
					},
				},
				{
					ID:              "assess_accounts",
					Type:            "map",
					TemplateNode:    "assess_account",
					ItemsExpression: "{{select_accounts.output.result_preview}}",
					DependsOn:       []string{"select_accounts"},
				},
				{
					ID:       "assess_account",
					Type:     "tool",
					ToolName: "get_current_time",
				},
			},
			OutputMapping: map[string]any{
				"result_rows":      "{{select_accounts.output.result_rows}}",
				"result_truncated": "{{select_accounts.output.result_truncated}}",
			},
		}

	case "BareToolLoopWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "BareToolLoopWorkflow",
			Namespace:   "test-ns",
			Description: "a loop whose body is a bare tool node (no child-workflow wrapper)",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:            "tick_loop",
					Type:          "loop",
					BodyNode:      "fetch",
					Condition:     "'{{fetch.output.status}}' == 'success'",
					MaxIterations: 3,
				},
				{
					ID:       "fetch",
					Type:     "tool",
					ToolName: "get_current_time",
				},
			},
			OutputMapping: map[string]any{
				"iterations":  "{{tick_loop.output.iterations_completed}}",
				"last_status": "{{fetch.output.status}}",
			},
		}

	case "BareToolMapTimeoutWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "BareToolMapTimeoutWorkflow",
			Namespace:   "test-ns",
			Description: "a map whose bare tool template never replies; the per-branch timer fails the branch and the workflow",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"items"},
			},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "never_responds_remote_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:              "process_items",
					Type:            "map",
					TemplateNode:    "blocked",
					ItemsExpression: "{{workflow.input.items}}",
				},
				{
					ID:       "blocked",
					Type:     "tool",
					ToolName: "never_responds_remote_tool",
					Timeout:  1 * time.Second,
				},
			},
			OutputMapping: map[string]any{
				"unused": "{{process_items.output.results}}",
			},
		}

	case "ParentNestedToolWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ParentNestedToolWorkflow",
			Namespace:   "test-ns",
			Description: "parent workflow invoking a child workflow that contains a tool node",
			Version:     "1.0.0",
			FailFast:    true,
			Nodes: []*workflow.NodeConfig{
				{
					ID:           "call_child",
					Type:         "workflow",
					WorkflowName: "ChildToolWorkflow",
					Input:        map[string]any{"task": "run the child tool"},
				},
			},
			OutputMapping: map[string]any{
				"nested_status":   "{{call_child.output.child_status}}",
				"nested_timezone": "{{call_child.output.child_timezone}}",
			},
		}

	case "ChildToolWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ChildToolWorkflow",
			Namespace:   "test-ns",
			Description: "child workflow whose only node is a tool node",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "child_fetch",
					Type:     "tool",
					ToolName: "get_current_time",
				},
			},
			OutputMapping: map[string]any{
				"child_status":   "{{child_fetch.output.status}}",
				"child_timezone": "{{child_fetch.output.timezone}}",
			},
		}

	case "ToolExitHandlerSuccessWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolExitHandlerSuccessWorkflow",
			Namespace:   "test-ns",
			Description: "tool node success triggers the on_exit success handler",
			Version:     "1.0.0",
			FailFast:    true,
			OnExit:      &workflow.ExitHandler{OnSuccess: "success_handler"},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "main_tool",
					Type:     "tool",
					ToolName: "get_current_time",
				},
				{
					ID:        "success_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					Input:     map[string]any{"outcome": "success"},
				},
			},
			OutputMapping: map[string]any{
				"tool_status": "{{main_tool.output.status}}",
			},
		}

	case "ToolExitHandlerFailureWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolExitHandlerFailureWorkflow",
			Namespace:   "test-ns",
			Description: "terminal tool failure triggers the on_exit failure handler",
			Version:     "1.0.0",
			FailFast:    true,
			OnExit:      &workflow.ExitHandler{OnFailure: "failure_handler"},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "failing_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "main_tool",
					Type:     "tool",
					ToolName: "failing_tool",
				},
				{
					ID:        "failure_handler",
					Type:      "agent",
					AgentName: "TestPeerAgentB",
					Input:     map[string]any{"outcome": "failure"},
				},
			},
			OutputMapping: map[string]any{
				"unused": "{{main_tool.output.message}}",
			},
		}

	case "ToolNodePerNodeTimeoutWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolNodePerNodeTimeoutWorkflow",
			Namespace:   "test-ns",
			Description: "a tool node whose remote tool never replies times out via the per-node timer",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "never_responds_remote_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "blocked_tool",
					Type:     "tool",
					ToolName: "never_responds_remote_tool",
					Timeout:  1 * time.Second,
				},
			},
			OutputMapping: map[string]any{
				"unused": "{{blocked_tool.output.status}}",
			},
		}

	case "ToolNodeWorkflowTimeoutWorkflow":
		return &workflow.WorkflowConfig{
			Name:            "ToolNodeWorkflowTimeoutWorkflow",
			Namespace:       "test-ns",
			Description:     "a stuck tool node is terminated by the workflow-level timeout",
			Version:         "1.0.0",
			FailFast:        true,
			WorkflowTimeout: 1 * time.Second,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "never_responds_remote_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "blocked_tool",
					Type:     "tool",
					ToolName: "never_responds_remote_tool",
				},
			},
			OutputMapping: map[string]any{
				"unused": "{{blocked_tool.output.status}}",
			},
		}

	case "ToolFailNoRetryWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolFailNoRetryWorkflow",
			Namespace:   "test-ns",
			Description: "a failing tool node does not retry even with a retry strategy configured",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "failing_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "doomed_tool",
					Type:     "tool",
					ToolName: "failing_tool",
					Retry: &workflow.RetryStrategy{
						Limit:       3,
						RetryPolicy: "OnFailure",
						Backoff:     &workflow.BackoffStrategy{Duration: 50 * time.Millisecond, Factor: 1},
					},
				},
			},
			OutputMapping: map[string]any{
				"unused": "{{doomed_tool.output.message}}",
			},
		}

	case "ToolConditionalSkipWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "ToolConditionalSkipWorkflow",
			Namespace:   "test-ns",
			Description: "a tool node is skipped when its when-guard is false; the workflow still completes",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"skip_tool": map[string]any{"type": "boolean"}},
				"required":   []any{"skip_tool"},
			},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "Step 1"},
				},
				{
					ID:        "maybe_tool",
					Type:      "tool",
					ToolName:  "get_current_time",
					DependsOn: []string{"step_1"},
					When:      "'{{workflow.input.skip_tool}}' != 'true'",
				},
				{
					ID:        "step_3",
					Type:      "agent",
					AgentName: "TestPeerAgentC",
					DependsOn: []string{"step_1"},
					Input:     map[string]any{"task_description": "Step 3 (always runs)"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{step_3.output}}",
			},
		}

	case "OptionalInputGateWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "OptionalInputGateWorkflow",
			Namespace:   "test-ns",
			Description: "a tool node gated on optional inputs is skipped when the caller supplies neither (#4403)",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"call_id":  map[string]any{"type": "string"},
					"text":     map[string]any{"type": "string"},
					"segments": map[string]any{"type": "array"},
				},
			},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:        "step_1",
					Type:      "agent",
					AgentName: "TestPeerAgentA",
					Input:     map[string]any{"task_description": "Step 1"},
				},
				{
					ID:        "maybe_tool",
					Type:      "tool",
					ToolName:  "get_current_time",
					DependsOn: []string{"step_1"},
					When:      "'{{workflow.input.call_id}}' != '' or '{{workflow.input.text}}' != ''",
				},
				{
					ID:        "step_3",
					Type:      "agent",
					AgentName: "TestPeerAgentC",
					DependsOn: []string{"step_1"},
					Input:     map[string]any{"task_description": "Step 3 (always runs)"},
				},
			},
			OutputMapping: map[string]any{
				"result": "{{step_3.output}}",
			},
		}

	case "InputSchemaTripWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "InputSchemaTripWorkflow",
			Namespace:   "test-ns",
			Description: "a workflow whose input_schema requires a destination and a hotel flag (#4844)",
			Version:     "1.0.0",
			FailFast:    true,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"destination": map[string]any{"type": "string", "pattern": "^[A-Za-z]+$"},
					"needsHotel":  map[string]any{"type": "boolean"},
				},
				"required": []any{"destination", "needsHotel"},
			},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "lookup",
					Type:     "tool",
					ToolName: "get_weather_tool",
					Input:    map[string]any{"location": "{{workflow.input.destination}}"},
				},
				{
					ID:   "route",
					Type: "switch",
					Cases: []workflow.SwitchCase{
						{Condition: "'{{workflow.input.needsHotel}}' == 'true'", Node: "book_hotel"},
					},
					DefaultCase: "skip_hotel",
					DependsOn:   []string{"lookup"},
				},
				{
					ID:        "book_hotel",
					Type:      "tool",
					ToolName:  "get_weather_tool",
					DependsOn: []string{"route"},
					Input:     map[string]any{"location": "Paris"},
				},
				{
					ID:        "skip_hotel",
					Type:      "tool",
					ToolName:  "get_weather_tool",
					DependsOn: []string{"route"},
					Input:     map[string]any{"location": "London"},
				},
			},
			OutputMapping: map[string]any{
				"destination_temp": "{{lookup.output.data.temperature}}",
			},
		}

	case "InputSchemaTripParentWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "InputSchemaTripParentWorkflow",
			Namespace:   "test-ns",
			Description: "a parent workflow whose sub-workflow node sends input the child's input_schema rejects (#4844)",
			Version:     "1.0.0",
			FailFast:    true,
			Nodes: []*workflow.NodeConfig{
				{
					ID:           "trip",
					Type:         "workflow",
					WorkflowName: "InputSchemaTripWorkflow",
					Input: map[string]any{
						"destination": "{{workflow.input.destination}}",
						"needsHotel":  "{{workflow.input.needsHotel}}",
					},
				},
			},
			OutputMapping: map[string]any{
				"destination_temp": "{{trip.output.destination_temp}}",
			},
		}

	case "InputSchemaTripExitHandlerParentWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "InputSchemaTripExitHandlerParentWorkflow",
			Namespace:   "test-ns",
			Description: "a parent whose on_exit handler calls a sub-workflow that rejects its input (#4844)",
			Version:     "1.0.0",
			FailFast:    true,
			OnExit:      &workflow.ExitHandler{Always: "cleanup"},
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{ID: "main", Type: "tool", ToolName: "get_weather_tool", Input: map[string]any{"location": "London"}},
				{
					ID:           "cleanup",
					Type:         "workflow",
					WorkflowName: "InputSchemaTripWorkflow",
					Input: map[string]any{
						"destination": "{{workflow.input.destination}}",
						"needsHotel":  "{{workflow.input.needsHotel}}",
					},
				},
			},
			OutputMapping: map[string]any{"temp": "{{main.output.data.temperature}}"},
		}

	case "InputSchemaTripRetryParentWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "InputSchemaTripRetryParentWorkflow",
			Namespace:   "test-ns",
			Description: "a parent whose retried sub-workflow node is rejected by the child's input_schema (#4844)",
			Version:     "1.0.0",
			FailFast:    true,
			Nodes: []*workflow.NodeConfig{
				{
					ID:           "trip",
					Type:         "workflow",
					WorkflowName: "InputSchemaTripWorkflow",
					Input: map[string]any{
						"destination": "{{workflow.input.destination}}",
						"needsHotel":  "{{workflow.input.needsHotel}}",
					},
					Retry: &workflow.RetryStrategy{
						Limit:       1,
						RetryPolicy: "Always",
						Backoff:     &workflow.BackoffStrategy{Duration: 20 * time.Millisecond, Factor: 1},
					},
				},
			},
			OutputMapping: map[string]any{"destination_temp": "{{trip.output.destination_temp}}"},
		}

	case "NoInputSchemaTextWorkflow":
		return &workflow.WorkflowConfig{
			Name:        "NoInputSchemaTextWorkflow",
			Namespace:   "test-ns",
			Description: "a workflow with no input_schema reads a plain-text message as its text field (#4844)",
			Version:     "1.0.0",
			FailFast:    true,
			Tools: []any{
				map[string]any{"tool_type": "builtin", "tool_name": "get_weather_tool"},
			},
			Nodes: []*workflow.NodeConfig{
				{
					ID:       "lookup",
					Type:     "tool",
					ToolName: "get_weather_tool",
					Input:    map[string]any{"location": "{{workflow.input.text}}"},
				},
			},
			OutputMapping: map[string]any{
				"temp": "{{lookup.output.data.temperature}}",
			},
		}

	default:
		return nil
	}
}

const decisionMapNestedWorkflowYAML = `
description: map over an inline account list, classifying each account in a child workflow
input_schema:
  type: object
  properties:
    notes: {type: object}
  required: [notes]
output_mapping:
  results: "{{assess_accounts.output.results}}"
nodes:
  - id: assess_accounts
    type: map
    node: assess_account
    # MockDecisionServer answers in request order, so items must run one at a
    # time for each primed response to reach the item it was written for.
    concurrency_limit: 1
    with_items:
      - {account: Acme Corp, notes: "{{workflow.input.notes.acme}}"}
      - {account: Globex, notes: "{{workflow.input.notes.globex}}"}
      - {account: Initech, notes: "{{workflow.input.notes.initech}}"}
  - id: assess_account
    type: workflow
    workflow_name: DecisionMapNestedItemWorkflow
    input: {account: "{{_map_item.account}}", notes: "{{_map_item.notes}}"}
`

const decisionMapNestedItemWorkflowYAML = `
description: classify one account's health, and write it up only when it needs attention
decision: {api_key: test-key, model: jev-1.13.0}
input_schema:
  type: object
  properties:
    account: {type: string}
    notes: {type: string}
  required: [account, notes]
output_mapping:
  account: "{{workflow.input.account}}"
  health_score: "{{health.output.answers.health.score}}"
  churn_risk: "{{health.output.answers.churn_risk.value}}"
  driver: "{{health.output.answers.driver.choice}}"
  driver_confidence: "{{health.output.answers.driver.confidence}}"
  write_up:
    coalesce: ["{{writeup.output.summary}}", "not_needed"]
nodes:
  - id: health
    type: decision
    input: {account: "{{workflow.input.account}}", notes: "{{workflow.input.notes}}"}
    questions:
      health:
        type: score
        instructions: "How healthy is ` + "`account`" + `, judging by ` + "`notes`" + `?"
        criteria:
          - critical: Usage collapsing or an executive escalation open
          - at_risk: Declining usage or unresolved escalations
          - stable: Flat usage and no open escalations
          - healthy: Growing usage and engaged sponsors
      churn_risk:
        type: boolean
        instructions: "` + "`notes`" + ` signal that ` + "`account`" + ` may not renew"
        criteria: {"true": A renewal is in doubt, "false": No renewal concern}
      driver:
        type: choice
        instructions: "What mostly drives ` + "`account`" + `'s health?"
        criteria: {product: Product fit or adoption, support: Support experience, commercial: Pricing or contract terms}
  - id: writeup
    type: agent
    agent_name: TestPeerAgentA
    depends_on: [health]
    when: "{{health.output.answers.health.score}} <= 1 || '{{health.output.answers.churn_risk.value}}' == 'true' || {{health.output.answers.driver.confidence}} < 0.7"
    instruction: "Write up the account health of {{workflow.input.account}}."
    input:
      account: "{{workflow.input.account}}"
      health_score: "{{health.output.answers.health.score}}"
      churn_risk: "{{health.output.answers.churn_risk.value}}"
      driver: "{{health.output.answers.driver.choice}}"
`

func mustParseWorkflowYAML(name, doc string) *workflow.WorkflowConfig {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(doc), &raw); err != nil {
		panic(fmt.Sprintf("known workflow %q: unmarshal YAML: %v", name, err))
	}
	cfg, err := workflow.ParseWorkflowConfig(name, "test-ns", raw)
	if err != nil {
		panic(fmt.Sprintf("known workflow %q: %v", name, err))
	}
	return cfg
}
