// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Run struct {
	ID                string                 `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Type              string                 `json:"type" example:"run"`
	ExperimentID      string                 `json:"experimentId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	RunGroupID        string                 `json:"runGroupId" example:"0190f5f3-7d8e-7f9a-8b0c-1c2d3e4f5a6b"`
	Status            string                 `json:"status" example:"completed"`
	ProgressCurrent   int                    `json:"progressCurrent" example:"3"`
	ProgressTotal     int                    `json:"progressTotal" example:"10"`
	ErrorMessage      *string                `json:"errorMessage" example:"Run exceeded maximum duration."`
	SummaryScores     []RunScore             `json:"summaryScores"`
	EvaluatorStats    []RunEvaluatorStats    `json:"evaluatorStats"`
	Model             *string                `json:"model" example:"0190f5e7-7a8b-7c9d-8e0f-1a2b3c4d5e6f"`
	ConfigSnapshot    *RunConfigSnapshot     `json:"configSnapshot"`
	EvaluatorSnapshot []RunEvaluatorSnapshot `json:"evaluatorSnapshot"`
	RunName           *string                `json:"runName" example:"Support answers, March"`
	Source            string                 `json:"source" example:"manual"`
	RunAs             *string                `json:"runAs" example:"system:nightly-evals"`
	CreatedBy         string                 `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime       Instant                `json:"createdTime"`
	CompletedTime     Instant                `json:"completedTime"`
}

type RunScore struct {
	EvaluatorID string  `json:"evaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	Score       float64 `json:"score" example:"0.85"`
}

type RunEvaluatorStats struct {
	EvaluatorID      string     `json:"evaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	Scored           int        `json:"scored" example:"10"`
	Escalated        int        `json:"escalated" example:"2"`
	WithConfidence   int        `json:"withConfidence" example:"9"`
	EscalatedPercent *float64   `json:"escalatedPercent" example:"20"`
	MeanConfidence   *float64   `json:"meanConfidence" example:"0.91"`
	JudgeUsage       JudgeUsage `json:"judgeUsage"`
}

type JudgeUsage struct {
	Decision *JudgeTokenUsage `json:"decision"`
	LLMJudge *JudgeTokenUsage `json:"llmJudge"`
}

type JudgeTokenUsage struct {
	InputTokens  int `json:"inputTokens" example:"1200"`
	OutputTokens int `json:"outputTokens" example:"40"`
}

type RunConfigSnapshot struct {
	Experiment *RunExperimentSnapshot `json:"experiment"`
	Dataset    *RunDatasetSnapshot    `json:"dataset"`
	Agent      *RunAgentSnapshot      `json:"agent"`
	Workflow   *RunWorkflowSnapshot   `json:"workflow"`
}

type RunWorkflowSnapshot struct {
	Name         string     `json:"name" example:"order_flow"`
	InstanceName *string    `json:"instanceName" example:"workflow_0190f5ee_1d2e_7f3a_9b4c_5d6e7f8a9b0c"`
	InputSchema  JSONObject `json:"inputSchema" example:"{\"type\":\"object\",\"properties\":{\"orderId\":{\"type\":\"string\"}},\"required\":[\"orderId\"]}"`
}

type RunExperimentSnapshot struct {
	ID          string             `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Name        string             `json:"name" example:"Support answers, March"`
	Description *string            `json:"description" example:"Checks the support agent against last month's tickets."`
	Models      []RunModelSnapshot `json:"models"`
}

type RunModelSnapshot struct {
	ModelConfigID *string                 `json:"modelConfigId" example:"0190f5e7-7a8b-7c9d-8e0f-1a2b3c4d5e6f"`
	Model         string                  `json:"model" example:"gpt-4o"`
	Alias         string                  `json:"alias" example:"general"`
	ResolvedModel *string                 `json:"resolvedModel" example:"gpt-4o-2024-08-06"`
	ModelConfig   *RunModelConfigSnapshot `json:"modelConfig"`
}

type RunModelConfigSnapshot struct {
	ID             *string        `json:"id" example:"0190f5e7-7a8b-7c9d-8e0f-1a2b3c4d5e6f"`
	Alias          *string        `json:"alias" example:"general"`
	Provider       *string        `json:"provider" example:"openai"`
	ModelName      *string        `json:"modelName" example:"gpt-4o"`
	APIBase        *string        `json:"apiBase" example:"https://llm.example.com/v1"`
	ModelParams    map[string]any `json:"modelParams" example:"{\"temperature\":0.2}"`
	MaxInputTokens *int           `json:"maxInputTokens" example:"128000"`
	Description    *string        `json:"description" example:"General-purpose model."`
}

type RunDatasetSnapshot struct {
	ID           string               `json:"id" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	Name         string               `json:"name" example:"Support tickets"`
	ExampleCount int                  `json:"exampleCount" example:"10"`
	Examples     []RunExampleSnapshot `json:"examples"`
}

type RunExampleSnapshot struct {
	ID               string                      `json:"id" example:"0190f5e9-4d5e-7f6a-9b7c-8d9e0f1a2b3c"`
	SequenceNumber   int                         `json:"sequenceNumber" example:"1"`
	Prompt           string                      `json:"prompt" example:"How do I reset my password?"`
	ExpectedResponse *string                     `json:"expectedResponse" example:"Use the Forgot password link on the sign-in page."`
	InputData        JSONObject                  `json:"inputData" example:"{\"orderId\":\"O-5003\",\"rush\":true}"`
	ExpectedPath     *ExpectedPath               `json:"expectedPath"`
	ExpectedFacts    *ExpectedFacts              `json:"expectedFacts"`
	InputArtifact    *RunExampleArtifactSnapshot `json:"inputArtifact"`
	ExpectedArtifact *RunExampleArtifactSnapshot `json:"expectedArtifact"`
}

type RunExampleArtifactSnapshot struct {
	Filename    string  `json:"filename" example:"ticket.pdf"`
	Version     int     `json:"version" example:"0"`
	MimeType    *string `json:"mimeType" example:"application/pdf"`
	Size        *int64  `json:"size" example:"20480"`
	ContentHash *string `json:"contentHash" example:"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"`
}

type RunAgentSnapshot struct {
	Name         string                      `json:"name" example:"support_agent"`
	Description  *string                     `json:"description" example:"Answers customer support questions."`
	AgentType    *string                     `json:"agentType" example:"standard"`
	SystemPrompt *string                     `json:"systemPrompt" example:"You answer support questions politely."`
	Toolsets     []string                    `json:"toolsets" example:"[\"web\"]"`
	Tools        []RunAgentToolSnapshot      `json:"tools"`
	Connectors   []RunAgentConnectorSnapshot `json:"connectors"`
	Skills       []RunAgentSkillSnapshot     `json:"skills"`
	InputModes   []string                    `json:"inputModes" example:"[\"text\"]"`
	OutputModes  []string                    `json:"outputModes" example:"[\"text\"]"`
	ModelConfig  *RunModelConfigSnapshot     `json:"modelConfig"`
	Origin       *string                     `json:"origin" example:"discovered"`
}

type RunAgentToolSnapshot struct {
	Key         *string `json:"key" example:"search_web"`
	Name        string  `json:"name" example:"search_web"`
	Description *string `json:"description" example:"Searches the web."`
}

type RunAgentConnectorSnapshot struct {
	ID            string  `json:"id" example:"0190f5e8-6b7c-7d8e-9f0a-1b2c3d4e5f6a"`
	Name          string  `json:"name" example:"Support wiki"`
	ConnectorType *string `json:"connectorType" example:"openapi"`
	Subtype       *string `json:"subtype" example:"rest"`
}

type RunAgentSkillSnapshot struct {
	ID          *string `json:"id" example:"summarize"`
	Name        string  `json:"name" example:"Summarize"`
	Description *string `json:"description" example:"Summarizes long text."`
}

type RunEvaluatorSnapshot struct {
	ID                string                  `json:"id" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	Name              string                  `json:"name" example:"Answer relevance"`
	Description       *string                 `json:"description" example:"Judges whether the answer addresses the question."`
	EvaluatorType     string                  `json:"evaluatorType" example:"llm_judge"`
	HeuristicType     *string                 `json:"heuristicType" example:"exact_match"`
	IsDefault         bool                    `json:"isDefault" example:"false"`
	Model             *string                 `json:"model" example:"general"`
	PromptTemplate    *string                 `json:"promptTemplate" example:"Rate the answer: {{response}}"`
	ChoiceScores      map[string]float64      `json:"choiceScores" example:"{\"pass\":1,\"fail\":0}"`
	ResolvedModelName *string                 `json:"resolvedModelName" example:"gpt-4o-2024-08-06"`
	ModelConfig       *RunModelConfigSnapshot `json:"modelConfig"`
}

type listExperimentRunsRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
	Page         Page   `json:"-"`
}

type TriggerExperimentRunRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
	RunName      string `json:"runName" example:"Support answers, March"`
}

const (
	experimentIDPath = experimentsPath + "/{experimentId}"
	runsPath         = evaluationsPath + "/runs"
	runPath          = runsPath + "/{id}"
)

func runsRoutes(h *evaluationHandler) []Route {
	return slices.Concat(runRoutes(h), operationRoutes(h), runGroupRoutes(h))
}

func runRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodPut, Path: experimentIDPath + "/trigger", Scope: rbac.ScopeEvaluationInvoke, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "triggerExperimentRun", Summary: "Initiate a run of an evaluation experiment",
				Text: "Starts one run per model of the experiment and returns 202 with an operation for them. Poll the operation at the Location header until its status is succeeded, failed, or cancelled. If a run of the experiment is pending or running, the request fails with 409. If the experiment has no dataset, no evaluators, or a dataset with no examples, the request fails with 422. If the run cannot be started, the request fails with 503.",
				Tag:  runsTag, Errors: []int{http.StatusConflict, http.StatusServiceUnavailable}},
			Handler: Handle(h.triggerRun, "Unable to start run")},
		{Method: http.MethodGet, Path: experimentIDPath + "/runs", Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listExperimentRuns", Summary: "Get a list of an evaluation experiment's runs",
				Text: "Returns one page of the experiment's runs, newest first.", Tag: runsTag},
			Handler: Handle(h.listRuns, "Unable to list runs")},
		{Method: http.MethodGet, Path: runPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getRun", Summary: "Get an evaluation run",
				Text: "Returns one run with its status, progress, scores, and the configuration it started with.", Tag: runsTag},
			Handler: Handle(h.getRun, "Unable to get run")},
		{Method: http.MethodDelete, Path: runPath, Scope: rbac.ScopeEvaluationDelete,
			Stability: StabilityDraft,
			Doc: Doc{ID: "deleteRun", Summary: "Delete an evaluation run",
				Text: "Deletes the run with its results and the execution data stored for them. The run's operation is then derived from the runs left, so its status can change. Answers 422 while the run is pending or running; cancel it first.", Tag: runsTag},
			Handler: Handle(h.deleteRun, "Unable to delete run")},
		{Method: http.MethodPut, Path: runPath + "/cancel", Scope: rbac.ScopeEvaluationInvoke, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "cancelRun", Summary: "Update an evaluation run to cancel it",
				Text: "Stops a pending or running run and returns it. If the run has already finished, the request fails with 422.", Tag: runsTag},
			Handler: Handle(h.cancelRun, "Unable to cancel run")},
	}
}

func (h *evaluationHandler) triggerRun(ctx context.Context, req TriggerExperimentRunRequest) (Accepted, error) {
	rows, err := h.runs.TriggerRunsIfIdle(ctx, req.ExperimentID, entplatform.UserIDFromContext(ctx), req.RunName, "")
	if errors.Is(err, store.ErrEvalRunActive) {
		return Accepted{}, &CallerError{Status: http.StatusConflict, Message: "A run of this experiment is already pending or running. Wait for it to finish or cancel it."}
	}
	if errors.Is(err, eval.ErrRunNotDispatched) {
		return Accepted{}, &CallerError{Status: http.StatusServiceUnavailable, Message: "Unable to start run. Try again later.", Cause: err}
	}
	if err != nil {
		return Accepted{}, evaluationError(err)
	}
	operation := toOperation(rows)
	return Accepted{Operation: operation, Location: operationLocation(req.ExperimentID, operation.ID)}, nil
}

func (h *evaluationHandler) listRuns(ctx context.Context, req listExperimentRunsRequest) (List[Run], error) {
	rows, total, err := h.runs.ListRunsPaginated(ctx, req.ExperimentID, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[Run]{}, err
	}
	return List[Run]{Items: toRuns(rows), Total: total}, nil
}

func (h *evaluationHandler) getRun(ctx context.Context, req IDRequest) (Run, error) {
	row, err := h.runs.GetRunRecord(ctx, req.ID)
	if err != nil {
		return Run{}, err
	}
	return toRun(row), nil
}

func (h *evaluationHandler) deleteRun(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.runs.DeleteRun(ctx, req.ID)
}

func (h *evaluationHandler) cancelRun(ctx context.Context, req IDRequest) (Run, error) {
	row, err := h.runs.CancelRunRecord(ctx, req.ID)
	if err != nil {
		return Run{}, err
	}
	return toRun(row), nil
}

func toRuns(rows []*store.EvalRun) []Run {
	runs := make([]Run, len(rows))
	for i, row := range rows {
		runs[i] = toRun(row)
	}
	return runs
}

func toRun(row *store.EvalRun) Run {
	return Run{
		ID:                row.ID,
		Type:              "run",
		ExperimentID:      row.ExperimentID,
		RunGroupID:        row.RunGroupID,
		Status:            row.Status,
		ProgressCurrent:   row.ProgressCurrent,
		ProgressTotal:     row.ProgressTotal,
		ErrorMessage:      row.ErrorMessage,
		SummaryScores:     toRunScores(row.SummaryScores),
		EvaluatorStats:    toRunEvaluatorStats(row.EvaluatorStats),
		Model:             NullIfZero(row.Model),
		ConfigSnapshot:    toRunConfigSnapshot(row.ConfigSnapshot),
		EvaluatorSnapshot: toRunEvaluatorSnapshots(row.EvaluatorSnapshot),
		RunName:           row.RunName,
		Source:            row.Source,
		RunAs:             row.RunAs,
		CreatedBy:         row.StartedBy,
		CreatedTime:       Instant(row.StartedTime),
		CompletedTime:     InstantOf(row.CompletedTime),
	}
}

func toRunScores(stored *string) []RunScore {
	byEvaluator := eval.DecodeStoredSnapshot[map[string]float64](stored)
	scores := make([]RunScore, 0, len(byEvaluator))
	for evaluatorID, score := range byEvaluator {
		scores = append(scores, RunScore{EvaluatorID: evaluatorID, Score: score})
	}
	slices.SortFunc(scores, func(a, b RunScore) int { return strings.Compare(a.EvaluatorID, b.EvaluatorID) })
	return scores
}

func toRunEvaluatorStats(stored *string) []RunEvaluatorStats {
	byEvaluator := eval.DecodeStoredSnapshot[map[string]eval.EvaluatorRunStats](stored)
	stats := make([]RunEvaluatorStats, 0, len(byEvaluator))
	for evaluatorID, s := range byEvaluator {
		stats = append(stats, RunEvaluatorStats{
			EvaluatorID:      evaluatorID,
			Scored:           s.Scored,
			Escalated:        s.Escalated,
			WithConfidence:   s.WithConfidence,
			EscalatedPercent: s.EscalatedPercent,
			MeanConfidence:   s.MeanConfidence,
			JudgeUsage:       toJudgeUsage(s.JudgeUsage),
		})
	}
	slices.SortFunc(stats, func(a, b RunEvaluatorStats) int { return strings.Compare(a.EvaluatorID, b.EvaluatorID) })
	return stats
}

func toJudgeUsage(u eval.JudgeUsageDetails) JudgeUsage {
	return JudgeUsage{Decision: toJudgeTokenUsage(u.Decision), LLMJudge: toJudgeTokenUsage(u.LLMJudge)}
}

func toJudgeTokenUsage(u *eval.JudgeTokenUsage) *JudgeTokenUsage {
	if u == nil {
		return nil
	}
	return &JudgeTokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
}

func toRunModelConfig(stored *eval.SnapshotModelConfig) *RunModelConfigSnapshot {
	if stored == nil {
		return nil
	}
	params := stored.ModelParams
	if params == nil {
		params = map[string]any{}
	}
	return &RunModelConfigSnapshot{
		ID:             stored.ID,
		Alias:          stored.Alias,
		Provider:       stored.Provider,
		ModelName:      stored.ModelName,
		APIBase:        stored.APIBase,
		ModelParams:    params,
		MaxInputTokens: stored.MaxInputTokens,
		Description:    stored.Description,
	}
}

func toRunConfigSnapshot(stored *string) *RunConfigSnapshot {
	snapshot := eval.DecodeStoredSnapshot[eval.SnapshotConfig](stored)
	if snapshot.Experiment == nil && snapshot.Dataset == nil && snapshot.Agent == nil && snapshot.Workflow == nil {
		return nil
	}
	return &RunConfigSnapshot{
		Experiment: toRunExperimentSnapshot(snapshot.Experiment),
		Dataset:    toRunDatasetSnapshot(snapshot.Dataset),
		Agent:      toRunAgentSnapshot(snapshot.Agent),
		Workflow:   toRunWorkflowSnapshot(snapshot.Workflow),
	}
}

func toRunWorkflowSnapshot(stored *eval.SnapshotWorkflow) *RunWorkflowSnapshot {
	if stored == nil {
		return nil
	}
	return &RunWorkflowSnapshot{Name: stored.Name, InstanceName: stored.InstanceName, InputSchema: storedInputSchema(stored.InputSchema)}
}

func storedInputSchema(stored json.RawMessage) JSONObject {
	schema := storedJSONObject(stored)
	var fields map[string]json.RawMessage
	if schema == nil || json.Unmarshal(schema, &fields) != nil || len(fields) == 0 {
		return nil
	}
	return schema
}

func storedJSONObject(stored json.RawMessage) JSONObject {
	trimmed := bytes.TrimSpace(stored)
	if !bytes.HasPrefix(trimmed, []byte("{")) {
		return nil
	}
	return JSONObject(trimmed)
}

func toRunExperimentSnapshot(stored *eval.SnapshotExperiment) *RunExperimentSnapshot {
	if stored == nil {
		return nil
	}
	models := make([]RunModelSnapshot, len(stored.Models))
	for i, m := range stored.Models {
		models[i] = RunModelSnapshot{
			ModelConfigID: NullIfZero(m.ModelConfigID),
			Model:         m.Model,
			Alias:         m.Alias,
			ResolvedModel: m.ResolvedModel,
			ModelConfig:   toRunModelConfig(m.ModelConfig),
		}
	}
	return &RunExperimentSnapshot{ID: stored.ID, Name: stored.Name, Description: stored.Description, Models: models}
}

func toRunDatasetSnapshot(stored *eval.SnapshotDataset) *RunDatasetSnapshot {
	if stored == nil {
		return nil
	}
	examples := make([]RunExampleSnapshot, len(stored.Examples))
	for i, e := range stored.Examples {
		examples[i] = RunExampleSnapshot{
			ID:               e.ID,
			SequenceNumber:   e.SequenceNumber,
			Prompt:           e.Prompt,
			ExpectedResponse: e.ExpectedResponse,
			InputData:        storedJSONObject(e.InputData),
			ExpectedPath:     fromEvalExpectedPath(e.ExpectedPath),
			ExpectedFacts:    fromEvalExpectedFacts(e.ExpectedFacts),
			InputArtifact:    toRunExampleArtifact(e.Artifact),
			ExpectedArtifact: toRunExampleArtifact(e.ExpectedArtifact),
		}
	}
	return &RunDatasetSnapshot{ID: stored.ID, Name: stored.Name, ExampleCount: stored.ExampleCount, Examples: examples}
}

func toRunExampleArtifact(stored *eval.SnapshotArtifact) *RunExampleArtifactSnapshot {
	if stored == nil {
		return nil
	}
	return &RunExampleArtifactSnapshot{
		Filename:    stored.Filename,
		Version:     stored.Version,
		MimeType:    stored.MIMEType,
		Size:        stored.SizeBytes,
		ContentHash: stored.ContentHash,
	}
}

func toRunAgentSnapshot(stored *eval.SnapshotAgent) *RunAgentSnapshot {
	if stored == nil {
		return nil
	}
	tools := make([]RunAgentToolSnapshot, len(stored.Tools))
	for i, t := range stored.Tools {
		tools[i] = RunAgentToolSnapshot{Key: t.ID, Name: t.Name, Description: t.Description}
	}
	connectors := make([]RunAgentConnectorSnapshot, len(stored.Connectors))
	for i, c := range stored.Connectors {
		connectors[i] = RunAgentConnectorSnapshot{ID: c.ID, Name: c.Name, ConnectorType: c.Type, Subtype: c.Subtype}
	}
	skills := make([]RunAgentSkillSnapshot, len(stored.Skills))
	for i, s := range stored.Skills {
		skills[i] = RunAgentSkillSnapshot{ID: s.ID, Name: s.Name, Description: s.Description}
	}
	return &RunAgentSnapshot{
		Name:         stored.Name,
		Description:  stored.Description,
		AgentType:    stored.Type,
		SystemPrompt: stored.SystemPrompt,
		Toolsets:     nonNilList(stored.Toolsets),
		Tools:        tools,
		Connectors:   connectors,
		Skills:       skills,
		InputModes:   nonNilList(stored.InputModes),
		OutputModes:  nonNilList(stored.OutputModes),
		ModelConfig:  toRunModelConfig(stored.ModelConfig),
		Origin:       stored.Origin,
	}
}

func toRunEvaluatorSnapshots(stored *string) []RunEvaluatorSnapshot {
	evaluators := eval.DecodeStoredSnapshot[[]eval.SnapshotEvaluator](stored)
	out := make([]RunEvaluatorSnapshot, len(evaluators))
	for i, e := range evaluators {
		choices := e.ChoiceScores
		if choices == nil {
			choices = map[string]float64{}
		}
		out[i] = RunEvaluatorSnapshot{
			ID:                e.ID,
			Name:              e.Name,
			Description:       e.Description,
			EvaluatorType:     e.Type,
			HeuristicType:     e.HeuristicType,
			IsDefault:         e.IsDefault,
			Model:             e.Model,
			PromptTemplate:    e.PromptTemplate,
			ChoiceScores:      choices,
			ResolvedModelName: e.ResolvedModelName,
			ModelConfig:       toRunModelConfig(e.ModelConfig),
		}
	}
	return out
}
