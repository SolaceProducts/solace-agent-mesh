// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

var (
	evalDatasetsResource    = platformResource{path: "/evaluations/datasets"}
	evalExamplesResource    = platformResource{path: "/evaluations/examples"}
	evalEvaluatorsResource  = platformResource{path: "/evaluations/evaluators"}
	evalExperimentsResource = platformResource{path: "/evaluations/experiments"}
	evalRunsResource        = platformResource{path: "/evaluations/runs"}
	evalRunResultsResource  = platformResource{path: "/evaluations/runResults"}
	evalRunGroupsResource   = platformResource{path: "/evaluations/runGroups"}
)

var errNoCapturedData = errors.New("run result captured no data")

func (c *Client) ListDatasets(ctx context.Context) ([]wire.DatasetResponse, error) {
	return listResource[wire.DatasetResponse](ctx, c, evalDatasetsResource)
}

func (c *Client) CreateDataset(ctx context.Context, req wire.CreateDatasetRequest) (*wire.DatasetResponse, error) {
	var resp wire.DatasetResponse
	if err := c.callResource(ctx, evalDatasetsResource, http.MethodPost, "", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateDataset(ctx context.Context, id string, update wire.UpdateDatasetRequest) (*wire.DatasetResponse, error) {
	var resp wire.DatasetResponse
	if err := c.callResource(ctx, evalDatasetsResource, http.MethodPatch, "/"+url.PathEscape(id), update, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteDataset(ctx context.Context, id string) error {
	return c.callResource(ctx, evalDatasetsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListAllExamples(ctx context.Context, datasetID string) ([]wire.ExampleResponse, error) {
	return listAllPages[wire.ExampleResponse](ctx, c, evalDatasetsResource.apiPath()+"/"+url.PathEscape(datasetID)+"/examples")
}

func (c *Client) CreateExample(ctx context.Context, datasetID string, req wire.CreateExampleRequest) (*wire.ExampleResponse, error) {
	var resp wire.ExampleResponse
	if err := c.callResource(ctx, evalDatasetsResource, http.MethodPost, "/"+url.PathEscape(datasetID)+"/examples", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateExample(ctx context.Context, exampleID string, update wire.UpdateExampleRequest) (*wire.ExampleResponse, error) {
	var resp wire.ExampleResponse
	if err := c.callResource(ctx, evalExamplesResource, http.MethodPatch, "/"+url.PathEscape(exampleID), update, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteExample(ctx context.Context, exampleID string) error {
	return c.callResource(ctx, evalExamplesResource, http.MethodDelete, "/"+url.PathEscape(exampleID), nil, nil)
}

func (c *Client) ListEvaluators(ctx context.Context) ([]wire.EvaluatorResponse, error) {
	return listResource[wire.EvaluatorResponse](ctx, c, evalEvaluatorsResource)
}

func (c *Client) CreateEvaluator(ctx context.Context, req wire.CreateEvaluatorRequest) (*wire.EvaluatorResponse, error) {
	var resp wire.EvaluatorResponse
	if err := c.callResource(ctx, evalEvaluatorsResource, http.MethodPost, "", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateEvaluator(ctx context.Context, id string, update wire.UpdateEvaluatorRequest) (*wire.EvaluatorResponse, error) {
	var resp wire.EvaluatorResponse
	if err := c.callResource(ctx, evalEvaluatorsResource, http.MethodPatch, "/"+url.PathEscape(id), update, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteEvaluator(ctx context.Context, id string) error {
	return c.callResource(ctx, evalEvaluatorsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListExperiments(ctx context.Context) ([]wire.ExperimentResponse, error) {
	return listResource[wire.ExperimentResponse](ctx, c, evalExperimentsResource)
}

func (c *Client) CreateExperiment(ctx context.Context, req wire.CreateExperimentRequest) (*wire.ExperimentResponse, error) {
	var resp wire.ExperimentResponse
	if err := c.callResource(ctx, evalExperimentsResource, http.MethodPost, "", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateExperiment(ctx context.Context, id string, update wire.UpdateExperimentRequest) (*wire.ExperimentResponse, error) {
	var resp wire.ExperimentResponse
	if err := c.callResource(ctx, evalExperimentsResource, http.MethodPatch, "/"+url.PathEscape(id), update, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func experimentModelMaps(models []apiv2.ExperimentModel) []map[string]any {
	if models == nil {
		return nil
	}
	out := make([]map[string]any, len(models))
	for i, m := range models {
		entry := map[string]any{}
		if id := ptrString(m.ModelConfigID); id != "" {
			entry["modelConfigId"] = id
		}
		if m.Model != "" {
			entry["model"] = m.Model
		}
		if m.Alias != "" {
			entry["alias"] = m.Alias
		}
		out[i] = entry
	}
	return out
}

func (c *Client) DeleteExperiment(ctx context.Context, id string) error {
	return c.callResource(ctx, evalExperimentsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

type EvalTrigger struct {
	Operation apiv2.Operation
	Runs      []evalplatform.RunResponse
}

func (c *Client) TriggerExperimentRun(ctx context.Context, experimentID string, runName string) (*EvalTrigger, error) {
	body := evalplatform.TriggerRunRequest{}
	if runName != "" {
		body.RunName = &runName
	}
	var operation apiv2.Operation
	if err := c.callResource(ctx, evalExperimentsResource, http.MethodPut, "/"+url.PathEscape(experimentID)+"/trigger", body, &operation); err != nil {
		return nil, err
	}
	group, err := c.getRunGroup(ctx, operation.ID)
	if err != nil {
		return nil, fmt.Errorf("the trigger started the runs of operation %s, but reading their ids failed, so they may still be running: %w", operation.ID, err)
	}
	runs := make([]evalplatform.RunResponse, len(group.Runs))
	for i, r := range group.Runs {
		runs[i] = v1Run(r)
	}
	return &EvalTrigger{Operation: operation, Runs: runs}, nil
}

func (c *Client) getRunGroup(ctx context.Context, operationID string) (apiv2.RunGroup, error) {
	path := evalRunGroupsResource.apiPath() + "/" + url.PathEscape(operationID)
	var group apiv2.RunGroup
	err := retryHTTP(ctx, "GET "+path, func(ctx context.Context) error {
		err := c.doSingle(ctx, http.MethodGet, path, nil, &group)
		if isTransientHTTP(err) {
			return transientErr{err: err}
		}
		return err
	})
	return group, err
}

func isTransientHTTP(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= http.StatusInternalServerError
	}
	var netErr *url.Error
	return errors.As(err, &netErr)
}

func (c *Client) GetEvalOperation(ctx context.Context, experimentID, operationID string) (*apiv2.Operation, error) {
	var operation apiv2.Operation
	subPath := "/" + url.PathEscape(experimentID) + "/operations/" + url.PathEscape(operationID)
	if err := c.callResource(ctx, evalExperimentsResource, http.MethodGet, subPath, nil, &operation); err != nil {
		return nil, err
	}
	return &operation, nil
}

func (c *Client) GetEvalRun(ctx context.Context, runID string) (*evalplatform.RunResponse, error) {
	return c.evalRunCall(ctx, http.MethodGet, "/"+url.PathEscape(runID))
}

func (c *Client) CancelEvalRun(ctx context.Context, runID string) (*evalplatform.RunResponse, error) {
	return c.evalRunCall(ctx, http.MethodPut, "/"+url.PathEscape(runID)+"/cancel")
}

func (c *Client) evalRunCall(ctx context.Context, method, subPath string) (*evalplatform.RunResponse, error) {
	var run apiv2.Run
	if err := c.callResource(ctx, evalRunsResource, method, subPath, nil, &run); err != nil {
		return nil, err
	}
	resp := v1Run(run)
	return &resp, nil
}

func v1Run(run apiv2.Run) evalplatform.RunResponse {
	out := evalplatform.RunResponse{
		ID:              run.ID,
		ExperimentID:    run.ExperimentID,
		RunGroupID:      run.RunGroupID,
		Status:          run.Status,
		ProgressCurrent: run.ProgressCurrent,
		ProgressTotal:   run.ProgressTotal,
		ErrorMessage:    run.ErrorMessage,
		SummaryScores:   map[string]float64{},
		Model:           ptrString(run.Model),
		RunName:         run.RunName,
		Source:          run.Source,
		RunAs:           run.RunAs,
		StartedBy:       run.CreatedBy,
		StartedAt:       evalplatform.Timestamp(run.CreatedTime),
	}
	for _, s := range run.SummaryScores {
		out.SummaryScores[s.EvaluatorID] = s.Score
	}
	out.ConfigSnapshot = v1RunShape(run)
	if run.CompletedTime != 0 {
		completed := evalplatform.Timestamp(run.CompletedTime)
		out.CompletedAt = &completed
	}
	return out
}

func v1RunShape(run apiv2.Run) map[string]any {
	if run.ConfigSnapshot == nil || run.ConfigSnapshot.Dataset == nil || run.ConfigSnapshot.Dataset.ExampleCount <= 0 {
		return nil
	}
	examples := run.ConfigSnapshot.Dataset.ExampleCount
	snapshot := map[string]any{"dataset": map[string]any{"example_count": float64(examples)}}
	if run.ProgressTotal%examples == 0 {
		snapshot["experiment"] = map[string]any{"runs_per_example": float64(run.ProgressTotal / examples)}
	}
	return snapshot
}

func (c *Client) GetEvalRunResults(ctx context.Context, runID string) ([]evalplatform.RunResultResponse, error) {
	subPath := "/" + url.PathEscape(runID) + "/results"
	rows, err := listPagesUpTo[json.RawMessage](ctx, c, evalRunsResource.apiPath()+subPath, maxRunResultPages())
	if err != nil {
		return nil, err
	}
	out := make([]evalplatform.RunResultResponse, len(rows))
	for i, raw := range rows {
		if err := v1RunResult(raw, &out[i]); err != nil {
			return nil, fmt.Errorf("GET %s: %w", subPath, err)
		}
	}
	return out, nil
}

func maxRunResultPages() int {
	rows := evalplatform.MaxExamplesPerDataset * evalplatform.MaxRunsPerExample
	return (rows + listPageSize - 1) / listPageSize
}

func v1RunResult(raw json.RawMessage, into *evalplatform.RunResultResponse) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var breakdown *apiv2.RunResultTokenBreakdown
	if err := json.Unmarshal(cmpOrNull(fields["tokenBreakdown"]), &breakdown); err != nil {
		return err
	}
	delete(fields, "tokenBreakdown")
	into.TokenBreakdown = v1TokenBreakdown(breakdown)
	var scores []map[string]json.RawMessage
	if err := json.Unmarshal(cmpOrNull(fields["scores"]), &scores); err != nil {
		return err
	}
	for _, score := range scores {
		renameKey(score, "key", "id")
		renameKey(score, "createdTime", "createdAt")
	}
	encoded, err := json.Marshal(scores)
	if err != nil {
		return err
	}
	fields["scores"] = encoded
	renameKey(fields, "createdTime", "createdAt")
	converted, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(converted, into)
}

func v1TokenBreakdown(b *apiv2.RunResultTokenBreakdown) *evalplatform.TokenBreakdown {
	if b == nil {
		return nil
	}
	out := &evalplatform.TokenBreakdown{
		TraceID:      ptrString(b.TraceID),
		Incomplete:   b.Incomplete,
		ByNode:       make([]evalplatform.NodeTokenUsage, len(b.ByNode)),
		Unattributed: evalplatform.TokenCounts(b.Unattributed),
		ByModel:      make(map[string]evalplatform.TokenCounts, len(b.ByModel)),
	}
	for i, n := range b.ByNode {
		out.ByNode[i] = evalplatform.NodeTokenUsage{NodeID: n.NodeID, Models: n.Models, TokenCounts: evalplatform.TokenCounts(n.Tokens)}
	}
	for _, m := range b.ByModel {
		out.ByModel[ptrString(m.Model)] = evalplatform.TokenCounts(m.Tokens)
	}
	return out
}

func renameKey(fields map[string]json.RawMessage, from, to string) {
	if v, ok := fields[from]; ok {
		delete(fields, from)
		fields[to] = v
	}
}

func cmpOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func (c *Client) GetEvalRunResultExecutionData(ctx context.Context, resultID string) (map[string]any, error) {
	subPath := "/" + url.PathEscape(resultID) + "/executionData"
	var resp map[string]any
	if err := c.callResource(ctx, evalRunResultsResource, http.MethodGet, subPath, nil, &resp); err != nil {
		return nil, err
	}
	if messages, _ := resp["messages"].([]any); len(messages) == 0 {
		return nil, errNoCapturedData
	}
	return resp, nil
}

func (c *Client) GetEvalRunResultTaskEvents(ctx context.Context, resultID string) ([]map[string]any, error) {
	subPath := "/" + url.PathEscape(resultID) + "/taskEvents"
	var resp apiv2.RunResultTaskEvents
	if err := c.callResource(ctx, evalRunResultsResource, http.MethodGet, subPath, nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Events) == 0 {
		return nil, errNoCapturedData
	}
	return resp.Events, nil
}
