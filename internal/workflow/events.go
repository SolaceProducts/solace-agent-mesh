// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

type WorkflowExecutionStartData struct {
	Type             string         `json:"type"`
	WorkflowName     string         `json:"workflow_name"`
	ExecutionID      string         `json:"execution_id"`
	InputArtifactRef *string        `json:"input_artifact_ref"`
	WorkflowInput    map[string]any `json:"workflow_input"`
}

type WorkflowNodeExecutionStartData struct {
	Type             string          `json:"type"`
	NodeID           string          `json:"node_id"`
	NodeType         string          `json:"node_type"`
	AgentName        *string         `json:"agent_name"`
	InputArtifactRef *string         `json:"input_artifact_ref"`
	IterationIndex   *int            `json:"iteration_index"`
	Condition        *string         `json:"condition"`
	TrueBranch       *string         `json:"true_branch"`
	FalseBranch      *string         `json:"false_branch"`
	TrueBranchLabel  *string         `json:"true_branch_label"`
	FalseBranchLabel *string         `json:"false_branch_label"`
	SubTaskID        *string         `json:"sub_task_id"`
	ParentNodeID     *string         `json:"parent_node_id"`
	Cases            []SwitchCaseRef `json:"cases"`
	DefaultBranch    *string         `json:"default_branch"`
	WaitFor          []string        `json:"wait_for"`
	JoinStrategy     *string         `json:"join_strategy"`
	JoinN            *int            `json:"join_n"`
	MaxIterations    *int            `json:"max_iterations"`
	LoopDelay        *string         `json:"loop_delay"`
	ParallelGroupID  *string         `json:"parallel_group_id"`
	DependsOn        []string        `json:"depends_on"`
	ToolName         *string         `json:"tool_name"`
}

type SwitchCaseRef struct {
	Condition string `json:"condition"`
	Node      string `json:"node"`
}

type WorkflowNodeExecutionResultData struct {
	Type              string         `json:"type"`
	NodeID            string         `json:"node_id"`
	Status            string         `json:"status"`
	OutputArtifactRef *ArtifactRef   `json:"output_artifact_ref"`
	ErrorMessage      *string        `json:"error_message"`
	Metadata          map[string]any `json:"metadata"`
}

type ArtifactRef struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type WorkflowMapProgressData struct {
	Type           string `json:"type"`
	NodeID         string `json:"node_id"`
	TotalItems     int    `json:"total_items"`
	CompletedItems int    `json:"completed_items"`
	Status         string `json:"status"`
}

type WorkflowExecutionResultData struct {
	Type              string         `json:"type"`
	Status            string         `json:"status"`
	OutputArtifactRef *ArtifactRef   `json:"output_artifact_ref"`
	ErrorMessage      *string        `json:"error_message"`
	WorkflowOutput    map[string]any `json:"workflow_output"`
}
