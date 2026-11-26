// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type RunResult struct {
	ID                string                   `json:"id" example:"0190f5f4-9d0e-7f1a-8b2c-3d4e5f6a7b8c"`
	Type              string                   `json:"type" example:"runResult"`
	RunID             string                   `json:"runId" example:"0190f5f1-4a2b-7c3d-8e9f-0a1b2c3d4e5f"`
	ExampleID         string                   `json:"exampleId" example:"0190f5e9-8a9b-7c0d-9e1f-2a3b4c5d6e7f"`
	TrialIndex        int                      `json:"trialIndex" example:"0"`
	InputPrompt       *string                  `json:"inputPrompt" example:"What is 2 + 2?"`
	ExpectedResponse  *string                  `json:"expectedResponse" example:"4"`
	TargetAgent       *string                  `json:"targetAgent" example:"support_agent"`
	AgentResponse     *string                  `json:"agentResponse" example:"2 + 2 is 4."`
	ResolvedModelName *string                  `json:"resolvedModelName" example:"gpt-4o"`
	DurationSeconds   *float64                 `json:"durationSeconds" example:"3.2"`
	ErrorMessage      *string                  `json:"errorMessage" example:"The agent did not answer before the deadline."`
	FailureKind       *string                  `json:"failureKind" example:"auth_required"`
	TargetType        string                   `json:"targetType" example:"workflow"`
	TokenUsage        *RunResultTokenUsage     `json:"tokenUsage"`
	TokenBreakdown    *RunResultTokenBreakdown `json:"tokenBreakdown"`
	Cost              *string                  `json:"cost" example:"0.0142000000"`
	Scores            []RunResultScore         `json:"scores"`
	Artifacts         []RunResultArtifact      `json:"artifacts"`
	InputArtifact     *RunResultArtifact       `json:"inputArtifact"`
	ExpectedArtifact  *RunResultArtifact       `json:"expectedArtifact"`
	CreatedTime       Instant                  `json:"createdTime"`
}

type RunResultTokenUsage struct {
	InputTokens       *int64 `json:"inputTokens" example:"1200"`
	CachedInputTokens *int64 `json:"cachedInputTokens" example:"800"`
	OutputTokens      *int64 `json:"outputTokens" example:"150"`
	ReasoningTokens   *int64 `json:"reasoningTokens" example:"40"`
	TotalTokens       *int64 `json:"totalTokens" example:"1350"`
}

type RunResultTokenBreakdown struct {
	TraceID      *string                `json:"traceId" example:"0190f5f4-9d0e-7f1a-8b2c-3d4e5f6a7b8c"`
	Incomplete   bool                   `json:"incomplete" example:"false"`
	ByNode       []RunResultNodeTokens  `json:"byNode"`
	Unattributed RunResultTokenCounts   `json:"unattributed"`
	ByModel      []RunResultModelTokens `json:"byModel"`
}

type RunResultTokenCounts struct {
	InputTokens       int64 `json:"inputTokens" example:"1200"`
	CachedInputTokens int64 `json:"cachedInputTokens" example:"800"`
	OutputTokens      int64 `json:"outputTokens" example:"150"`
	ReasoningTokens   int64 `json:"reasoningTokens" example:"40"`
	TotalTokens       int64 `json:"totalTokens" example:"1350"`
}

type RunResultNodeTokens struct {
	NodeID string               `json:"nodeId" example:"classify"`
	Models []string             `json:"models" example:"[\"claude-sonnet-5-5\"]"`
	Tokens RunResultTokenCounts `json:"tokens"`
}

type RunResultModelTokens struct {
	Model  *string              `json:"model" example:"claude-sonnet-5-5"`
	Tokens RunResultTokenCounts `json:"tokens"`
}

type RunResultScore struct {
	Key         string           `json:"key" example:"0190f5f5-ae1f-7a2b-9c3d-4e5f6a7b8c9d"`
	EvaluatorID string           `json:"evaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	Score       float64          `json:"score" example:"1"`
	Choice      *string          `json:"choice" example:"correct"`
	Passed      *bool            `json:"passed" example:"true"`
	Reasoning   *string          `json:"reasoning" example:"The answer matches the expected response."`
	Details     *RunScoreDetails `json:"details"`
	CreatedTime Instant          `json:"createdTime"`
}

type RunScoreDetails struct {
	DecidedBy        *string         `json:"decidedBy" example:"decision"`
	Model            *string         `json:"model" example:"gpt-5-decisions-2026-09"`
	Answer           *DecisionAnswer `json:"answer"`
	Escalated        bool            `json:"escalated" example:"false"`
	EscalationReason *string         `json:"escalationReason" example:"low_confidence"`
	ProviderError    *string         `json:"providerError" example:"rate_limited"`
	Usage            *JudgeUsage     `json:"usage"`
	DurationMs       *int64          `json:"durationMs" example:"850"`
}

type DecisionAnswer struct {
	Choice         *string                     `json:"choice" example:"correct"`
	Probability    *float64                    `json:"probability" example:"0.93"`
	Score          *float64                    `json:"score" example:"2"`
	Confidence     *float64                    `json:"confidence" example:"0.93"`
	Probabilities  []DecisionOptionProbability `json:"probabilities"`
	ScoringApplied *string                     `json:"scoringApplied" example:"chosen"`
}

type DecisionOptionProbability struct {
	Option      string  `json:"option" example:"correct"`
	Probability float64 `json:"probability" example:"0.93"`
}

type RunResultArtifact struct {
	Filename string  `json:"filename" example:"report.csv"`
	MimeType *string `json:"mimeType" example:"text/csv"`
	Version  int     `json:"version" example:"0"`
	Size     *int64  `json:"size" example:"2048"`
}

type RunResultExecutionData struct {
	RunID           string             `json:"runId" example:"0190f5f1-4a2b-7c3d-8e9f-0a1b2c3d4e5f"`
	ExampleID       string             `json:"exampleId" example:"0190f5e9-8a9b-7c0d-9e1f-2a3b4c5d6e7f"`
	TrialIndex      int                `json:"trialIndex" example:"0"`
	Messages        []RunResultMessage `json:"messages"`
	AgentResponse   *string            `json:"agentResponse" example:"2 + 2 is 4."`
	DurationSeconds float64            `json:"durationSeconds" example:"3.2"`
}

type RunResultMessage struct {
	Topic        string         `json:"topic" example:"sam/a2a/v1/agent/status/support_agent"`
	Payload      map[string]any `json:"payload" example:"{\"jsonrpc\":\"2.0\",\"id\":\"task-1\"}"`
	CapturedTime Instant        `json:"capturedTime"`
}

type RunResultTaskEvents struct {
	Events []map[string]any `json:"events" example:"[{\"event_type\":\"a2a_message\",\"direction\":\"request\",\"task_id\":\"task-1\"}]"`
}

type listRunResultsRequest struct {
	RunID string `path:"runId" json:"-"`
	Page  Page   `json:"-"`
}

type exportRunResultsRequest struct {
	RunID  string `path:"runId" json:"-"`
	Format string `query:"format" json:"-" example:"csv"`
}

type runResultIDRequest struct {
	RunResultID string `path:"runResultId" json:"-"`
}

type runResultArtifactRequest struct {
	RunResultID string `path:"runResultId" json:"-"`
	Filename    string `path:"filename,text" json:"-"`
	Version     string `query:"version" json:"-" example:"0"`
}

const (
	runResultsOfRunPath  = evaluationsPath + "/runs/{runId}/results"
	runResultsExportPath = evaluationsPath + "/runs/{runId}/export"
	runResultPath        = evaluationsPath + "/runResults/{id}"
	runResultByIDPath    = evaluationsPath + "/runResults/{runResultId}"

	formatCSV  = "csv"
	formatJSON = "json"
)

func runResultRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: runResultsOfRunPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listRunResults", Summary: "Get a list of results for an evaluation run",
				Text: "Returns one page of the run's trial results, in dataset order and then trial order, each with its scores.", Tag: runResultsTag},
			Handler: Handle(h.listRunResults, "Unable to list run results")},
		{Method: http.MethodGet, Path: runResultsExportPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "exportRunResults", Summary: "Get an export of the results of an evaluation run",
				Text: "Downloads every result of the run as a file. With format=csv, the default, the file has one row per result, with a score and a passed column for each evaluator. With format=json, the file is an array of run results as GET /evaluations/runs/{runId}/results returns them.", Tag: runResultsTag},
			Handler: Handle(h.exportRunResults, "Unable to export run results")},
		{Method: http.MethodGet, Path: runResultPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getRunResult", Summary: "Get an evaluation run result",
				Text: "Returns one trial's result with its scores and the files attached to it.", Tag: runResultsTag},
			Handler: Handle(h.getRunResult, "Unable to get run result")},
		{Method: http.MethodGet, Path: runResultByIDPath + "/executionData", Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getRunResultExecutionData", Summary: "Get the execution data of an evaluation run result",
				Text: "Returns the event broker messages captured while the trial ran. The messages list is empty when the trial captured nothing, or when Agent Mesh stores no evaluation data because none of EVAL_DATA_BUCKET_NAME, OBJECT_STORAGE_BUCKET_NAME, or OBJECT_STORAGE_FS_ROOT is set.", Tag: runResultsTag},
			Handler: Handle(h.getRunResultExecutionData, "Unable to get run result execution data")},
		{Method: http.MethodGet, Path: runResultByIDPath + "/taskEvents", Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getRunResultTaskEvents", Summary: "Get the task events of an evaluation run result",
				Text: "Returns the whole event stream recorded while the trial ran, in one response rather than in pages. The events list is empty when the trial recorded none, or when Agent Mesh stores no evaluation data because none of EVAL_DATA_BUCKET_NAME, OBJECT_STORAGE_BUCKET_NAME, or OBJECT_STORAGE_FS_ROOT is set.", Tag: runResultsTag},
			Handler: Handle(h.getRunResultTaskEvents, "Unable to get run result task events")},
		{Method: http.MethodGet, Path: runResultByIDPath + "/artifacts/{filename...}", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getRunResultArtifact", Summary: "Get a file the agent produced in a trial",
				Text: "Downloads one of the run result's artifacts as an attachment, in the version given or else the highest recorded one.", Tag: runResultsTag},
			Handler: Handle(h.getRunResultArtifact, "Unable to get run result artifact")},
		{Method: http.MethodGet, Path: runResultByIDPath + "/inputArtifact/{filename...}", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getRunResultInputArtifact", Summary: "Get the input file a trial was given",
				Text: "Downloads the example's input file as the trial received it. If the example's file has been replaced since, the request fails with 404.", Tag: runResultsTag},
			Handler: Handle(h.getRunResultInputArtifact, "Unable to get run result input artifact")},
		{Method: http.MethodGet, Path: runResultByIDPath + "/expectedArtifact/{filename...}", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getRunResultExpectedArtifact", Summary: "Get the expected file a trial was graded against",
				Text: "Downloads the example's expected file as it stood when the trial ran. If the example's file has been replaced since, the request fails with 404.", Tag: runResultsTag},
			Handler: Handle(h.getRunResultExpectedArtifact, "Unable to get run result expected artifact")},
	}
}

func (h *evaluationHandler) listRunResults(ctx context.Context, req listRunResultsRequest) (List[RunResult], error) {
	rows, total, err := h.runs.ListRunResultsPaginated(ctx, req.RunID, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[RunResult]{}, err
	}
	return List[RunResult]{Items: toRunResults(rows), Total: total}, nil
}

func (h *evaluationHandler) exportRunResults(ctx context.Context, req exportRunResultsRequest) (Download, error) {
	format := req.Format
	if format == "" {
		format = formatCSV
	}
	if format != formatCSV && format != formatJSON {
		return Download{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam,
			Details: map[string][]string{"format": {"must be csv or json"}}}
	}
	filename := fmt.Sprintf("run-%s-results.%s", req.RunID, format)
	if format == formatJSON {
		rows, err := h.runs.GetRunResultsForRun(ctx, req.RunID)
		if err != nil {
			return Download{}, err
		}
		body, err := json.MarshalIndent(toRunResults(rows), "", "  ")
		if err != nil {
			return Download{}, fmt.Errorf("encode run results: %w", err)
		}
		return Download{Content: body, MimeType: "application/json", Filename: filename}, nil
	}
	text, err := h.runs.ExportRunResultsCSV(ctx, req.RunID)
	if err != nil {
		return Download{}, err
	}
	return Download{Content: []byte(text), MimeType: "text/csv", Filename: filename}, nil
}

func (h *evaluationHandler) getRunResult(ctx context.Context, req IDRequest) (RunResult, error) {
	row, err := h.runs.GetRunResultByID(ctx, req.ID)
	if err != nil {
		return RunResult{}, err
	}
	return toRunResult(row), nil
}

func (h *evaluationHandler) getRunResultExecutionData(ctx context.Context, req runResultIDRequest) (RunResultExecutionData, error) {
	record, err := h.runs.GetExecutionDataRecord(ctx, req.RunResultID)
	if isNotFound(err) {
		return h.uncapturedExecutionData(ctx, req.RunResultID)
	}
	if err != nil {
		return RunResultExecutionData{}, err
	}
	messages := make([]RunResultMessage, len(record.Messages))
	for i, m := range record.Messages {
		messages[i] = RunResultMessage{Topic: m.Topic, Payload: m.Payload, CapturedTime: instantFromRFC3339(m.WallTime)}
	}
	return RunResultExecutionData{
		RunID:           record.RunID,
		ExampleID:       record.ExampleID,
		TrialIndex:      record.TrialIndex,
		Messages:        messages,
		AgentResponse:   record.AgentResponse,
		DurationSeconds: record.DurationSeconds,
	}, nil
}

func (h *evaluationHandler) getRunResultTaskEvents(ctx context.Context, req runResultIDRequest) (RunResultTaskEvents, error) {
	events, err := h.runs.GetTaskEvents(ctx, req.RunResultID)
	if isNotFound(err) {
		_, err = h.runs.GetRunResultByID(ctx, req.RunResultID)
	}
	if err != nil {
		return RunResultTaskEvents{}, err
	}
	return RunResultTaskEvents{Events: nonNilList(events)}, nil
}

func (h *evaluationHandler) uncapturedExecutionData(ctx context.Context, runResultID string) (RunResultExecutionData, error) {
	row, err := h.runs.GetRunResultByID(ctx, runResultID)
	if err != nil {
		return RunResultExecutionData{}, err
	}
	data := RunResultExecutionData{
		RunID:         row.RunID,
		ExampleID:     row.ExampleID,
		TrialIndex:    row.TrialIndex,
		Messages:      []RunResultMessage{},
		AgentResponse: row.AgentResponse,
	}
	if row.DurationSeconds != nil {
		data.DurationSeconds = *row.DurationSeconds
	}
	return data, nil
}

func isNotFound(err error) bool {
	_, ok := errors.AsType[*apierr.NotFoundError](err)
	return ok
}

func (h *evaluationHandler) getRunResultArtifact(ctx context.Context, req runResultArtifactRequest) (Download, error) {
	return h.downloadRunResultFile(ctx, req, h.runs.GetRunResultArtifact)
}

func (h *evaluationHandler) getRunResultInputArtifact(ctx context.Context, req runResultArtifactRequest) (Download, error) {
	return h.downloadRunResultFile(ctx, req, h.runs.GetRunResultInputArtifact)
}

func (h *evaluationHandler) getRunResultExpectedArtifact(ctx context.Context, req runResultArtifactRequest) (Download, error) {
	return h.downloadRunResultFile(ctx, req, h.runs.GetRunResultExpectedArtifact)
}

func (h *evaluationHandler) downloadRunResultFile(
	ctx context.Context,
	req runResultArtifactRequest,
	load func(ctx context.Context, resultID, filename string, version *int) ([]byte, string, error),
) (Download, error) {
	version, err := optionalVersion(req.Version)
	if err != nil {
		return Download{}, err
	}
	content, mimeType, err := load(ctx, req.RunResultID, req.Filename, version)
	if err != nil {
		return Download{}, err
	}
	return Download{Content: content, MimeType: mimeType, Filename: req.Filename}, nil
}

func optionalVersion(raw string) (*int, error) {
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, &CallerError{Status: http.StatusBadRequest, Message: msgInvalidQueryParam,
			Details: map[string][]string{"version": {"must be an integer"}}}
	}
	if n < 0 {
		return nil, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam,
			Details: map[string][]string{"version": {"must be 0 or more"}}}
	}
	return &n, nil
}

func toRunResults(rows []eval.RunResultResponse) []RunResult {
	items := make([]RunResult, len(rows))
	for i := range rows {
		items[i] = toRunResult(&rows[i])
	}
	return items
}

func toRunResult(r *eval.RunResultResponse) RunResult {
	scores := make([]RunResultScore, len(r.Scores))
	for i, s := range r.Scores {
		scores[i] = RunResultScore{
			Key:         s.ID,
			EvaluatorID: s.EvaluatorID,
			Score:       s.Score,
			Choice:      s.Choice,
			Passed:      s.Passed,
			Reasoning:   s.Reasoning,
			Details:     toRunScoreDetails(s.Details),
			CreatedTime: Instant(s.CreatedTime),
		}
	}
	artifacts := make([]RunResultArtifact, len(r.Artifacts))
	for i := range r.Artifacts {
		artifacts[i] = *toRunResultArtifact(&r.Artifacts[i])
	}
	return RunResult{
		ID:                r.ID,
		Type:              "runResult",
		RunID:             r.RunID,
		ExampleID:         r.ExampleID,
		TrialIndex:        r.TrialIndex,
		InputPrompt:       r.InputPrompt,
		ExpectedResponse:  r.ExpectedResponse,
		TargetAgent:       r.TargetAgent,
		AgentResponse:     r.AgentResponse,
		ResolvedModelName: r.ResolvedModelName,
		DurationSeconds:   r.DurationSeconds,
		ErrorMessage:      r.ErrorMessage,
		FailureKind:       r.FailureKind,
		TargetType:        r.TargetType,
		TokenUsage:        toRunResultTokenUsage(r.TokenUsage),
		TokenBreakdown:    toRunResultTokenBreakdown(r.TokenBreakdown),
		Cost:              r.Cost,
		Scores:            scores,
		Artifacts:         artifacts,
		InputArtifact:     toExampleArtifact(r.InputArtifact),
		ExpectedArtifact:  toExampleArtifact(r.ExpectedArtifact),
		CreatedTime:       Instant(r.CreatedTime),
	}
}

func toRunScoreDetails(d *eval.ScoreDetails) *RunScoreDetails {
	if d == nil {
		return nil
	}
	details := &RunScoreDetails{
		DecidedBy:        NullIfZero(d.DecidedBy),
		Model:            NullIfZero(d.Model),
		Answer:           toDecisionAnswer(d.Answer),
		Escalated:        d.Escalated,
		EscalationReason: NullIfZero(d.EscalationReason),
		ProviderError:    NullIfZero(d.ProviderError),
		DurationMs:       NullIfZero(d.DurationMs),
	}
	if d.Usage != nil {
		usage := toJudgeUsage(*d.Usage)
		details.Usage = &usage
	}
	return details
}

func toDecisionAnswer(a *eval.DecisionAnswerDetails) *DecisionAnswer {
	if a == nil {
		return nil
	}
	probabilities := make([]DecisionOptionProbability, 0, len(a.Probabilities))
	for option, p := range a.Probabilities {
		probabilities = append(probabilities, DecisionOptionProbability{Option: option, Probability: p})
	}
	slices.SortFunc(probabilities, func(x, y DecisionOptionProbability) int { return strings.Compare(x.Option, y.Option) })
	return &DecisionAnswer{
		Choice:         NullIfZero(a.Choice),
		Probability:    a.Probability,
		Score:          a.Score,
		Confidence:     a.Confidence,
		Probabilities:  probabilities,
		ScoringApplied: NullIfZero(a.ScoringApplied),
	}
}

func toRunResultTokenUsage(u *eval.TokenUsage) *RunResultTokenUsage {
	if u == nil {
		return nil
	}
	return &RunResultTokenUsage{
		InputTokens:       u.InputTokens,
		CachedInputTokens: u.CachedInputTokens,
		OutputTokens:      u.OutputTokens,
		ReasoningTokens:   u.ReasoningTokens,
		TotalTokens:       u.TotalTokens,
	}
}

func toRunResultTokenBreakdown(b *eval.TokenBreakdown) *RunResultTokenBreakdown {
	if b == nil {
		return nil
	}
	byNode := make([]RunResultNodeTokens, len(b.ByNode))
	for i, n := range b.ByNode {
		byNode[i] = RunResultNodeTokens{NodeID: n.NodeID, Models: nonNilList(n.Models), Tokens: toRunResultTokenCounts(n.TokenCounts)}
	}
	byModel := make([]RunResultModelTokens, 0, len(b.ByModel))
	for _, model := range slices.Sorted(maps.Keys(b.ByModel)) {
		byModel = append(byModel, RunResultModelTokens{Model: NullIfZero(model), Tokens: toRunResultTokenCounts(b.ByModel[model])})
	}
	return &RunResultTokenBreakdown{
		TraceID:      NullIfZero(b.TraceID),
		Incomplete:   b.Incomplete,
		ByNode:       byNode,
		Unattributed: toRunResultTokenCounts(b.Unattributed),
		ByModel:      byModel,
	}
}

func toRunResultTokenCounts(c eval.TokenCounts) RunResultTokenCounts {
	return RunResultTokenCounts{
		InputTokens:       c.InputTokens,
		CachedInputTokens: c.CachedInputTokens,
		OutputTokens:      c.OutputTokens,
		ReasoningTokens:   c.ReasoningTokens,
		TotalTokens:       c.TotalTokens,
	}
}

func toRunResultArtifact(a *eval.ArtifactResponse) *RunResultArtifact {
	if a == nil {
		return nil
	}
	return &RunResultArtifact{Filename: a.Filename, MimeType: NullIfZero(a.MimeType), Version: a.Version, Size: NullIfZero(a.Size)}
}

func toExampleArtifact(a *eval.ArtifactResponse) *RunResultArtifact {
	artifact := toRunResultArtifact(a)
	if artifact != nil {
		artifact.Size = &a.Size
	}
	return artifact
}

func instantFromRFC3339(s string) Instant {
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return Instant(parsed.UnixMilli())
}
