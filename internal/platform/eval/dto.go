// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"math"
	"time"

	apierr "github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

type ValidationError = apierr.ValidationError

type NotFoundError = apierr.NotFoundError

type ConflictError = apierr.ConflictError

type BusinessRuleViolationError = apierr.BusinessRuleViolationError

type Timestamp int64

func (t Timestamp) MarshalJSON() ([]byte, error) {
	ms := int64(t)
	if ms == 0 {
		return json.Marshal("")
	}
	return json.Marshal(time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000000+00:00"))
}

func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*t = Timestamp(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s == "" {
		*t = 0
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04:05.000000+00:00", s)
		if err != nil {
			return err
		}
	}
	*t = Timestamp(parsed.UnixMilli())
	return nil
}

type PaginatedResponse[T any] struct {
	Data []T  `json:"data"`
	Meta Meta `json:"meta"`
}

type Meta struct {
	Pagination PaginationMeta `json:"pagination"`
}

type PaginationMeta struct {
	PageNumber int  `json:"pageNumber"`
	Count      int  `json:"count"`
	PageSize   int  `json:"pageSize"`
	NextPage   *int `json:"nextPage"`
	TotalPages int  `json:"totalPages"`
}

func newPaginationMeta(pageNumber, pageSize, totalCount int) Meta {
	totalPages := 0
	if pageSize > 0 {
		totalPages = int(math.Ceil(float64(totalCount) / float64(pageSize)))
	}
	var nextPage *int
	if pageNumber < totalPages {
		np := pageNumber + 1
		nextPage = &np
	}
	return Meta{Pagination: PaginationMeta{
		PageNumber: pageNumber,
		Count:      totalCount,
		PageSize:   pageSize,
		NextPage:   nextPage,
		TotalPages: totalPages,
	}}
}

type CreateDatasetRequest struct {
	Name         string `json:"name" validate:"required,max=255"`
	Description  string `json:"description" validate:"omitempty,max=1000"`
	ExamplesFile string `json:"-" cfgapply:"local=examples_file"`
}

type UpdateDatasetRequest struct {
	Name           *string `json:"name,omitempty"`
	Description    *string `json:"description,omitempty"`
	DescriptionSet bool    `json:"-"`
}

type CreateExampleRequest struct {
	Prompt           string          `json:"prompt"`
	ExpectedResponse *string         `json:"expectedResponse,omitempty"`
	InputData        json.RawMessage `json:"inputData,omitempty"`
	ExpectedPath     *ExpectedPath   `json:"expectedPath,omitempty"`
	ExpectedFacts    *ExpectedFacts  `json:"expectedFacts,omitempty"`
	SequenceNumber   *int            `json:"sequenceNumber,omitempty"`
}

type UpdateExampleRequest struct {
	Prompt              *string         `json:"prompt,omitempty"`
	ExpectedResponse    *string         `json:"expectedResponse,omitempty"`
	ExpectedResponseSet bool            `json:"-"`
	InputData           json.RawMessage `json:"inputData,omitempty"`
	InputDataSet        bool            `json:"-"`
	ExpectedPath        *ExpectedPath   `json:"expectedPath,omitempty"`
	ExpectedPathSet     bool            `json:"-"`
	ExpectedFacts       *ExpectedFacts  `json:"expectedFacts,omitempty"`
	ExpectedFactsSet    bool            `json:"-"`
}

func (r UpdateEvaluatorRequest) setsMoreThanModel() bool {
	return r.NameSet || r.DescriptionSet || r.PromptTemplateSet || r.ChoiceScoresSet ||
		r.QuestionSet || r.ScoringSet || r.ReadsSet || r.MinConfidenceSet || r.EscalateToEvaluatorIDSet
}

func (r UpdateExampleRequest) MarshalJSON() ([]byte, error) {
	var body struct {
		Prompt           *string         `json:"prompt,omitempty"`
		ExpectedResponse json.RawMessage `json:"expectedResponse,omitempty"`
		InputData        json.RawMessage `json:"inputData,omitempty"`
		ExpectedPath     json.RawMessage `json:"expectedPath,omitempty"`
		ExpectedFacts    json.RawMessage `json:"expectedFacts,omitempty"`
	}
	body.Prompt = r.Prompt
	body.InputData = r.InputData
	if r.InputDataSet && r.InputData == nil {
		body.InputData = json.RawMessage("null")
	}
	var err error
	if body.ExpectedResponse, err = sentWhenSet(r.ExpectedResponseSet || r.ExpectedResponse != nil, r.ExpectedResponse); err != nil {
		return nil, err
	}
	if body.ExpectedPath, err = sentWhenSet(r.ExpectedPathSet || r.ExpectedPath != nil, r.ExpectedPath); err != nil {
		return nil, err
	}
	if body.ExpectedFacts, err = sentWhenSet(r.ExpectedFactsSet || r.ExpectedFacts != nil, r.ExpectedFacts); err != nil {
		return nil, err
	}
	return json.Marshal(body)
}

func sentWhenSet[T any](set bool, v *T) (json.RawMessage, error) {
	if !set {
		return nil, nil
	}
	return json.Marshal(v)
}

type CreateExperimentRequest struct {
	Name               string           `json:"name" validate:"required,max=255"`
	Description        *string          `json:"description,omitempty"`
	DatasetID          string           `json:"datasetId" cfgapply:"xref=dataset" validate:"required,max=255"`
	TargetAgent        string           `json:"targetAgent,omitempty" cfgapply:"xref=agent" validate:"omitempty,max=255"`
	TargetWorkflow     string           `json:"targetWorkflow,omitempty" cfgapply:"xref=workflow" validate:"omitempty,max=255"`
	Models             []map[string]any `json:"models,omitempty" validate:"omitempty,max=3"`
	RunsPerExample     *int             `json:"runsPerExample,omitempty"`
	MaxWorkers         *int             `json:"maxWorkers,omitempty"`
	EvaluatorIDs       []string         `json:"evaluatorIds,omitempty" cfgapply:"xref=evaluator" validate:"required,min=1,max=5"`
	PrimaryEvaluatorID *string          `json:"primaryEvaluatorId,omitempty" cfgapply:"xref=evaluator"`
	RunAs              *string          `json:"runAs,omitempty"`
}

type UpdateExperimentRequest struct {
	Name                  *string          `json:"name,omitempty"`
	NameSet               bool             `json:"-"`
	Description           *string          `json:"description,omitempty"`
	DescriptionSet        bool             `json:"-"`
	DatasetID             *string          `json:"datasetId,omitempty" cfgapply:"xref=dataset"`
	TargetAgent           *string          `json:"targetAgent,omitempty" cfgapply:"xref=agent"`
	TargetWorkflow        *string          `json:"targetWorkflow,omitempty" cfgapply:"xref=workflow"`
	Models                []map[string]any `json:"models,omitempty"`
	ModelsSet             bool             `json:"-"`
	RunsPerExample        *int             `json:"runsPerExample,omitempty"`
	MaxWorkers            *int             `json:"maxWorkers,omitempty"`
	EvaluatorIDs          []string         `json:"evaluatorIds,omitempty" cfgapply:"xref=evaluator"`
	EvaluatorIDsSet       bool             `json:"-"`
	PrimaryEvaluatorID    *string          `json:"primaryEvaluatorId,omitempty" cfgapply:"xref=evaluator"`
	PrimaryEvaluatorIDSet bool             `json:"-"`
	RunAs                 *string          `json:"runAs,omitempty"`
	RunAsSet              bool             `json:"-"`
}

type CreateEvaluatorRequest struct {
	Name                  string             `json:"name" validate:"required,max=255"`
	Description           string             `json:"description" validate:"required,min=1,max=1000"`
	Type                  string             `json:"type,omitempty" cfgapply:"discriminator" validate:"omitempty,oneof=llm_judge decision"`
	Model                 *string            `json:"model,omitempty"`
	PromptTemplate        *string            `json:"promptTemplate,omitempty"`
	ChoiceScores          map[string]any     `json:"choiceScores,omitempty" validate:"omitempty,min=2,max=26"`
	Question              *EvaluatorQuestion `json:"question,omitempty"`
	Scoring               string             `json:"scoring,omitempty" validate:"omitempty,oneof=chosen expected"`
	Reads                 []string           `json:"reads,omitempty" validate:"omitempty,dive,oneof=prompt response expected_response transcript artifacts"`
	MinConfidence         *float64           `json:"minConfidence,omitempty" validate:"omitempty,gt=0,lte=1"`
	EscalateToEvaluatorID *string            `json:"escalateToEvaluatorId,omitempty" cfgapply:"xref=evaluator"`
}

type EvaluatorQuestion struct {
	Type         string               `json:"type" validate:"required,oneof=boolean choice score"`
	Instructions string               `json:"instructions" validate:"required"`
	Criteria     []EvaluatorCriterion `json:"criteria,omitempty"`
}

type EvaluatorCriterion struct {
	Key         string `json:"key" validate:"required"`
	Description string `json:"description,omitempty"`
}

type UpdateEvaluatorRequest struct {
	Name                     *string            `json:"name,omitempty"`
	NameSet                  bool               `json:"-"`
	Description              *string            `json:"description,omitempty"`
	DescriptionSet           bool               `json:"-"`
	Model                    *string            `json:"model,omitempty"`
	ModelSet                 bool               `json:"-"`
	PromptTemplate           *string            `json:"promptTemplate,omitempty"`
	PromptTemplateSet        bool               `json:"-"`
	ChoiceScores             map[string]any     `json:"choiceScores,omitempty"`
	ChoiceScoresSet          bool               `json:"-"`
	Question                 *EvaluatorQuestion `json:"question,omitempty"`
	QuestionSet              bool               `json:"-"`
	Scoring                  *string            `json:"scoring,omitempty"`
	ScoringSet               bool               `json:"-"`
	Reads                    []string           `json:"reads,omitempty"`
	ReadsSet                 bool               `json:"-"`
	MinConfidence            *float64           `json:"minConfidence,omitempty"`
	MinConfidenceSet         bool               `json:"-"`
	EscalateToEvaluatorID    *string            `json:"escalateToEvaluatorId,omitempty"`
	EscalateToEvaluatorIDSet bool               `json:"-"`
}

func (r UpdateEvaluatorRequest) MarshalJSON() ([]byte, error) {
	body := map[string]any{}
	put := func(key string, set bool, isNil bool, v any) {
		if set || !isNil {
			body[key] = v
		}
	}
	put("name", r.NameSet, r.Name == nil, r.Name)
	put("description", r.DescriptionSet, r.Description == nil, r.Description)
	put("model", r.ModelSet, r.Model == nil, r.Model)
	put("promptTemplate", r.PromptTemplateSet, r.PromptTemplate == nil, r.PromptTemplate)
	put("choiceScores", r.ChoiceScoresSet, r.ChoiceScores == nil, r.ChoiceScores)
	put("question", r.QuestionSet, r.Question == nil, r.Question)
	put("scoring", r.ScoringSet, r.Scoring == nil, r.Scoring)
	put("reads", r.ReadsSet, r.Reads == nil, r.Reads)
	put("minConfidence", r.MinConfidenceSet, r.MinConfidence == nil, r.MinConfidence)
	put("escalateToEvaluatorId", r.EscalateToEvaluatorIDSet, r.EscalateToEvaluatorID == nil, r.EscalateToEvaluatorID)
	return json.Marshal(body)
}

type DatasetResponse struct {
	ID                    string    `json:"id" cfgapply:"read-only"`
	Name                  string    `json:"name"`
	Description           *string   `json:"description"`
	IsDefault             bool      `json:"isDefault" cfgapply:"read-only"`
	ExampleCount          int       `json:"exampleCount" cfgapply:"read-only"`
	ExperimentCount       int       `json:"experimentCount" cfgapply:"read-only"`
	InputDataExampleCount int       `json:"-"`
	CreatedBy             string    `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy             string    `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime           Timestamp `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime           Timestamp `json:"updatedAt" cfgapply:"read-only"`
}

type ExampleResponse struct {
	ID               string                   `json:"id"`
	DatasetID        string                   `json:"datasetId"`
	SequenceNumber   int                      `json:"sequenceNumber"`
	Prompt           string                   `json:"prompt"`
	ExpectedResponse *string                  `json:"expectedResponse"`
	InputData        json.RawMessage          `json:"inputData"`
	ExpectedPath     *ExpectedPath            `json:"expectedPath"`
	ExpectedFacts    *ExpectedFacts           `json:"expectedFacts"`
	InputArtifact    *ExampleArtifactResponse `json:"inputArtifact"`
	ExpectedArtifact *ExampleArtifactResponse `json:"expectedArtifact"`
	CreatedTime      Timestamp                `json:"createdAt"`
	UpdatedTime      Timestamp                `json:"updatedAt"`
}

type ExampleArtifactResponse struct {
	Filename    string `json:"filename"`
	MimeType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Version     int    `json:"version"`
	ContentHash string `json:"contentHash,omitempty"`
}

type EvaluatorResponse struct {
	ID                    string             `json:"id" cfgapply:"read-only"`
	Name                  string             `json:"name"`
	Description           *string            `json:"description"`
	Type                  string             `json:"type" cfgapply:"discriminator"`
	HeuristicType         *string            `json:"heuristicType" cfgapply:"discriminator"`
	IsDefault             bool               `json:"isDefault" cfgapply:"read-only"`
	Model                 *string            `json:"model"`
	PromptTemplate        *string            `json:"promptTemplate"`
	ChoiceScores          map[string]any     `json:"choiceScores"`
	Question              *EvaluatorQuestion `json:"question,omitempty"`
	Scoring               string             `json:"scoring,omitempty"`
	Reads                 []string           `json:"reads,omitempty"`
	MinConfidence         *float64           `json:"minConfidence,omitempty"`
	EscalateToEvaluatorID *string            `json:"escalateToEvaluatorId,omitempty" cfgapply:"xref=evaluator"`
	ExperimentCount       int                `json:"experimentCount" cfgapply:"read-only"`
	CreatedBy             string             `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy             string             `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime           Timestamp          `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime           Timestamp          `json:"updatedAt" cfgapply:"read-only"`
}

type ExperimentResponse struct {
	ID                 string           `json:"id" cfgapply:"read-only"`
	Name               string           `json:"name"`
	Description        *string          `json:"description"`
	DatasetID          *string          `json:"datasetId" cfgapply:"xref=dataset"`
	DatasetName        *string          `json:"datasetName" cfgapply:"read-only"`
	TargetType         string           `json:"targetType" cfgapply:"read-only"`
	TargetAgent        string           `json:"targetAgent" cfgapply:"xref=agent"`
	TargetWorkflow     *string          `json:"targetWorkflow" cfgapply:"xref=workflow"`
	Models             []map[string]any `json:"models"`
	RunsPerExample     int              `json:"runsPerExample"`
	MaxWorkers         int              `json:"maxWorkers"`
	PrimaryEvaluatorID *string          `json:"primaryEvaluatorId" cfgapply:"xref=evaluator"`
	EvaluatorIDs       []string         `json:"evaluatorIds" cfgapply:"xref=evaluator"`
	IsDefault          bool             `json:"isDefault" cfgapply:"read-only"`
	RunAs              *string          `json:"runAs"`
	LatestRunStatus    *string          `json:"latestRunStatus" cfgapply:"read-only"`
	LatestRunTime      *Timestamp       `json:"latestRunTime" cfgapply:"read-only"`
	DatasetUpdatedTime *Timestamp       `json:"datasetUpdatedTime" cfgapply:"read-only"`
	CreatedBy          string           `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy          string           `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime        Timestamp        `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime        Timestamp        `json:"updatedAt" cfgapply:"read-only"`
}

type ImportExamplesResult struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
	Dropped int `json:"dropped"`
	Limit   int `json:"limit"`
}

type TriggerRunRequest struct {
	RunName *string `json:"runName,omitempty"`
}

type GenerateDatasetRequest struct {
	TargetAgent string  `json:"targetAgent"`
	NumExamples int     `json:"numExamples"`
	Focus       *string `json:"focus,omitempty"`
}

type GenerateExamplesRequest struct {
	TargetAgent string  `json:"targetAgent"`
	NumExamples int     `json:"numExamples"`
	Focus       *string `json:"focus,omitempty"`
}

type ScoreResponse struct {
	ID          string        `json:"id"`
	EvaluatorID string        `json:"evaluatorId"`
	Score       float64       `json:"score"`
	Choice      *string       `json:"choice"`
	Passed      *bool         `json:"passed"`
	Reasoning   *string       `json:"reasoning"`
	Details     *ScoreDetails `json:"details,omitempty"`
	CreatedTime Timestamp     `json:"createdAt"`
}

type ArtifactResponse struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimeType,omitempty"`
	Version  int    `json:"version"`
	Size     int64  `json:"size,omitempty"`
}

type TokenUsage struct {
	InputTokens       *int64 `json:"inputTokens"`
	CachedInputTokens *int64 `json:"cachedInputTokens"`
	OutputTokens      *int64 `json:"outputTokens"`
	ReasoningTokens   *int64 `json:"reasoningTokens"`
	TotalTokens       *int64 `json:"totalTokens"`
}

type RunResultResponse struct {
	ID                string             `json:"id"`
	RunID             string             `json:"runId"`
	ExampleID         string             `json:"exampleId"`
	TrialIndex        int                `json:"trialIndex"`
	InputPrompt       *string            `json:"inputPrompt"`
	ExpectedResponse  *string            `json:"expectedResponse"`
	TargetAgent       *string            `json:"targetAgent"`
	AgentResponse     *string            `json:"agentResponse"`
	ResolvedModelName *string            `json:"resolvedModelName"`
	ExecutionDataKey  *string            `json:"executionDataKey"`
	TaskEventsKey     *string            `json:"taskEventsKey"`
	DurationSeconds   *float64           `json:"durationSeconds"`
	ErrorMessage      *string            `json:"errorMessage"`
	FailureKind       *string            `json:"failureKind"`
	TargetType        string             `json:"targetType"`
	TokenUsage        *TokenUsage        `json:"tokenUsage"`
	TokenBreakdown    *TokenBreakdown    `json:"tokenBreakdown"`
	Cost              *string            `json:"cost"`
	Scores            []ScoreResponse    `json:"scores"`
	Artifacts         []ArtifactResponse `json:"artifacts"`
	InputArtifact     *ArtifactResponse  `json:"inputArtifact"`
	ExpectedArtifact  *ArtifactResponse  `json:"expectedArtifact"`
	CreatedTime       Timestamp          `json:"createdAt"`
}

type RunGroupResponse struct {
	ID              string        `json:"id"`
	ExperimentID    string        `json:"experimentId"`
	RunName         *string       `json:"runName"`
	Status          string        `json:"status"`
	ProgressCurrent int           `json:"progressCurrent"`
	ProgressTotal   int           `json:"progressTotal"`
	StartedAt       Timestamp     `json:"startedAt"`
	CompletedAt     *Timestamp    `json:"completedAt"`
	Runs            []RunResponse `json:"runs"`
	ConfigOutdated  bool          `json:"configOutdated"`
}

type ModelTokenSummary struct {
	UsageResultCount      int      `json:"usageResultCount"`
	MeanInputTokens       *float64 `json:"meanInputTokens"`
	MeanCachedInputTokens *float64 `json:"meanCachedInputTokens"`
	MeanOutputTokens      *float64 `json:"meanOutputTokens"`
	MeanReasoningTokens   *float64 `json:"meanReasoningTokens"`
	MeanTotalTokens       *float64 `json:"meanTotalTokens"`
}

type RunGroupResultsResponse struct {
	GroupID             string                         `json:"groupId"`
	ResultsByModel      map[string][]RunResultResponse `json:"resultsByModel"`
	TokenSummaryByModel map[string]ModelTokenSummary   `json:"tokenSummaryByModel"`
}

type SetWatchlistRequest struct {
	AgentNames []string `json:"agentNames"`
}

type WatchlistEntryResponse struct {
	AgentName             string `json:"agentName"`
	TargetType            string `json:"targetType"`
	ResolvedModelProvider string `json:"resolvedModelProvider"`
	IsBuiltIn             bool   `json:"isBuiltIn"`
	Position              int    `json:"position"`
}

type WatchlistChartPoint struct {
	RunGroupID string    `json:"runGroupId"`
	StartedAt  Timestamp `json:"startedAt"`
	Score      *float64  `json:"score"`
}

type WatchlistExperimentSeries struct {
	ExperimentID         string                `json:"experimentId"`
	ExperimentName       string                `json:"experimentName"`
	PrimaryEvaluatorID   string                `json:"primaryEvaluatorId"`
	PrimaryEvaluatorName *string               `json:"primaryEvaluatorName"`
	Points               []WatchlistChartPoint `json:"points"`
}

type WatchlistAgentChartResponse struct {
	AgentName             string                      `json:"agentName"`
	TargetType            string                      `json:"targetType"`
	ResolvedModelProvider string                      `json:"resolvedModelProvider"`
	IsBuiltIn             bool                        `json:"isBuiltIn"`
	Position              int                         `json:"position"`
	Experiments           []WatchlistExperimentSeries `json:"experiments"`
}

type RunGroupSummaryResponse struct {
	RunGroupID      string     `json:"runGroupId"`
	ExperimentID    string     `json:"experimentId"`
	ExperimentName  string     `json:"experimentName"`
	TargetAgent     string     `json:"targetAgent"`
	TargetType      string     `json:"targetType"`
	RunName         *string    `json:"runName"`
	StartedAt       Timestamp  `json:"startedAt"`
	CompletedAt     *Timestamp `json:"completedAt"`
	RunCount        int        `json:"runCount"`
	Status          string     `json:"status"`
	DurationSeconds *float64   `json:"durationSeconds"`
}
