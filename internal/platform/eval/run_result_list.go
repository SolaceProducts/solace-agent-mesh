// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *RunService) ListRunResultsPaginated(ctx context.Context, runID string, pageNumber, pageSize int) ([]RunResultResponse, int, error) {
	rows, total, err := s.results.ListByRunPaginated(ctx, runID, pageNumber, pageSize)
	if err != nil {
		return nil, 0, wrapEvalErr(errListRunResults, err)
	}
	if total == 0 {
		if _, err := s.runs.GetByID(ctx, runID); err != nil {
			return nil, 0, mapEvalNotFound(err, "EvalRun", runID)
		}
	}
	out := make([]RunResultResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, runResultResponseFromRow(r, s.logger))
	}
	s.attachTrialCosts(ctx, out)
	return out, total, nil
}

type ExecutionDataRecord struct {
	RunID           string                 `json:"run_id"`
	ExampleID       string                 `json:"example_id"`
	TrialIndex      int                    `json:"trial_index"`
	Messages        []ExecutionDataMessage `json:"messages"`
	AgentResponse   *string                `json:"agent_response"`
	DurationSeconds float64                `json:"duration_seconds"`
}

type ExecutionDataMessage struct {
	Topic    string         `json:"topic"`
	Payload  map[string]any `json:"payload"`
	WallTime string         `json:"wall_time"`
}

func (s *RunService) GetExecutionDataRecord(ctx context.Context, resultID string) (*ExecutionDataRecord, error) {
	blob, err := s.GetExecutionData(ctx, resultID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return nil, fmt.Errorf("encode execution data: %w", err)
	}
	var record ExecutionDataRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode execution data: %w", err)
	}
	return &record, nil
}
