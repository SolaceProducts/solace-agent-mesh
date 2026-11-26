// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

type EvalDataset struct {
	ID          string  `db:"id"`
	Name        string  `db:"name"`
	Description *string `db:"description"`
	IsDefault   bool    `db:"is_default"`
	CreatedBy   string  `db:"created_by"`
	UpdatedBy   string  `db:"updated_by"`
	CreatedTime int64   `db:"created_time"`
	UpdatedTime int64   `db:"updated_time"`
}

type EvalExample struct {
	ID               string                 `db:"id"`
	DatasetID        string                 `db:"dataset_id"`
	SequenceNumber   int                    `db:"sequence_number"`
	Prompt           string                 `db:"prompt"`
	ExpectedResponse *string                `db:"expected_response"`
	InputData        *string                `db:"input_data"`
	ExpectedPath     *string                `db:"expected_path"`
	ExpectedFacts    *string                `db:"expected_facts"`
	CreatedTime      int64                  `db:"created_time"`
	UpdatedTime      int64                  `db:"updated_time"`
	Artifacts        []*EvalExampleArtifact `db:"-"`
}

func (e *EvalExample) ArtifactByRole(role string) *EvalExampleArtifact {
	for _, a := range e.Artifacts {
		if a != nil && a.Role == role {
			return a
		}
	}
	return nil
}

const (
	EvalArtifactRoleInput    = "input"
	EvalArtifactRoleExpected = "expected"
)

type EvalExampleArtifact struct {
	ID          string `db:"id"`
	ExampleID   string `db:"example_id"`
	Role        string `db:"role"`
	Filename    string `db:"filename"`
	MIMEType    string `db:"mime_type"`
	SizeBytes   int64  `db:"size_bytes"`
	Version     int    `db:"version"`
	SessionID   string `db:"artifact_session_id"`
	ContentHash string `db:"content_hash"`
	CreatedTime int64  `db:"created_time"`
	UpdatedTime int64  `db:"updated_time"`
}

type EvalEvaluator struct {
	ID             string  `db:"id"`
	Name           string  `db:"name"`
	Description    *string `db:"description"`
	Type           string  `db:"type"`
	HeuristicType  *string `db:"heuristic_type"`
	IsDefault      bool    `db:"is_default"`
	Model          *string `db:"model"`
	PromptTemplate *string `db:"prompt_template"`
	ChoiceScores   *string `db:"choice_scores"`
	DecisionConfig *string `db:"decision_config"`
	CreatedBy      string  `db:"created_by"`
	UpdatedBy      string  `db:"updated_by"`
	CreatedTime    int64   `db:"created_time"`
	UpdatedTime    int64   `db:"updated_time"`
}

type EvalExperiment struct {
	ID                 string  `db:"id"`
	Name               string  `db:"name"`
	Description        *string `db:"description"`
	DatasetID          *string `db:"dataset_id"`
	TargetAgent        string  `db:"target_agent"`
	TargetType         string  `db:"target_type"`
	Models             string  `db:"models"`
	RunsPerExample     int     `db:"runs_per_example"`
	MaxWorkers         int     `db:"max_workers"`
	PrimaryEvaluatorID *string `db:"primary_evaluator_id"`
	IsDefault          bool    `db:"is_default"`
	RunAs              *string `db:"run_as"`
	CreatedBy          string  `db:"created_by"`
	UpdatedBy          string  `db:"updated_by"`
	CreatedTime        int64   `db:"created_time"`
	UpdatedTime        int64   `db:"updated_time"`
}

const (
	EvalTargetTypeAgent    = "agent"
	EvalTargetTypeWorkflow = "workflow"
)

const EvalFailureKindPartialFailure = "partial_failure"

func (e *EvalExperiment) IsWorkflowTarget() bool {
	return e.TargetType == EvalTargetTypeWorkflow
}

type EvalRun struct {
	ID                   string  `db:"id"`
	ExperimentID         string  `db:"experiment_id"`
	RunGroupID           string  `db:"run_group_id"`
	Status               string  `db:"status"`
	ProgressCurrent      int     `db:"progress_current"`
	ProgressTotal        int     `db:"progress_total"`
	ErrorMessage         *string `db:"error_message"`
	SummaryScores        *string `db:"summary_scores"`
	EvaluatorStats       *string `db:"evaluator_stats"`
	Model                string  `db:"model"`
	ConfigSnapshot       *string `db:"config_snapshot"`
	EvaluatorSnapshot    *string `db:"evaluator_snapshot"`
	RunName              *string `db:"run_name"`
	Source               string  `db:"source"`
	RunAs                *string `db:"run_as"`
	StartedBy            string  `db:"started_by"`
	StartedTime          int64   `db:"started_time"`
	CompletedTime        *int64  `db:"completed_time"`
	CoordinatorStartedAt *int64  `db:"coordinator_started_at"`
	Takeovers            int     `db:"takeovers"`
}

type EvalRunResult struct {
	ID                       string  `db:"id"`
	RunID                    string  `db:"run_id"`
	ExampleID                string  `db:"example_id"`
	TrialIndex               int     `db:"trial_index"`
	InputPrompt              *string `db:"input_prompt"`
	ExpectedResponse         *string `db:"expected_response"`
	TargetAgent              *string `db:"target_agent"`
	TargetType               *string `db:"target_type"`
	AgentResponse            *string `db:"agent_response"`
	ResolvedModelName        *string `db:"resolved_model_name"`
	ExecutionDataKey         *string `db:"execution_data_key"`
	TaskEventsKey            *string `db:"task_events_key"`
	ArtifactsMetadata        *string `db:"artifacts_metadata"`
	InputArtifactMetadata    *string `db:"input_artifact_metadata"`
	ExpectedArtifactMetadata *string `db:"expected_artifact_metadata"`

	DurationSeconds   *float64 `db:"duration_seconds"`
	ErrorMessage      *string  `db:"error_message"`
	FailureKind       *string  `db:"failure_kind"`
	CreatedTime       int64    `db:"created_time"`
	InputTokens       *int64   `db:"input_tokens"`
	OutputTokens      *int64   `db:"output_tokens"`
	CachedInputTokens *int64   `db:"cached_input_tokens"`
	ReasoningTokens   *int64   `db:"reasoning_tokens"`
	TotalTokens       *int64   `db:"total_tokens"`
	TokenBreakdown    *string  `db:"token_breakdown"`
}

func (r *EvalRunResult) ArtifactMetadataByRole(role string) *string {
	switch role {
	case EvalArtifactRoleInput:
		return r.InputArtifactMetadata
	case EvalArtifactRoleExpected:
		return r.ExpectedArtifactMetadata
	default:
		return nil
	}
}

type EvalAgentWatchlist struct {
	ID          string `db:"id"`
	UserID      string `db:"user_id"`
	AgentName   string `db:"agent_name"`
	Position    int    `db:"position"`
	CreatedTime int64  `db:"created_time"`
}

type EvalRunScore struct {
	ID          string  `db:"id"`
	RunResultID string  `db:"run_result_id"`
	EvaluatorID string  `db:"evaluator_id"`
	Score       float64 `db:"score"`
	Choice      *string `db:"choice"`
	Passed      *bool   `db:"passed"`
	Reasoning   *string `db:"reasoning"`
	Details     *string `db:"details"`
	Errored     *bool   `db:"errored"`
	CreatedTime int64   `db:"created_time"`
}
