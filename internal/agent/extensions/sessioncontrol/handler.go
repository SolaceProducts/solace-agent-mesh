// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sessioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
)

func errorsAsExt(err error) (*extensions.Error, bool) {
	if e, ok := errors.AsType[*extensions.Error](err); ok {
		return e, true
	}
	return nil, false
}

type Service interface {
	CompactSession(ctx context.Context, info extensions.RequestInfo, params CompactParams) (*Result, error)
}

type CompactParams struct {
	SessionID     string `json:"sessionId"`
	TargetPercent int    `json:"targetPercent,omitempty"`
	TaskID        string `json:"taskId,omitempty"`
	TraceID       string `json:"traceId,omitempty"`
	a2a.Origin
}

type Result struct {
	SummarizedMessagesCount       int     `json:"summarizedMessagesCount"`
	TokensFreed                   int     `json:"tokensFreed"`
	PostCompactionRemainingTokens int     `json:"postCompactionRemainingTokens"`
	Summary                       string  `json:"summary"`
	RemainingEvents               int     `json:"remainingEvents"`
	SummarizerCostUsd             float64 `json:"summarizerCostUsd"`
	SummarizerInputTokens         int     `json:"summarizerInputTokens"`
	SummarizerOutputTokens        int     `json:"summarizerOutputTokens"`
}

func HandlerFor(svc Service) extensions.Handler {
	return func(ctx context.Context, info extensions.RequestInfo, raw json.RawMessage) (any, *extensions.Error) {
		var params CompactParams
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, &extensions.Error{
					Code:    -32602,
					Message: fmt.Sprintf("invalid params: %s", err),
				}
			}
		}
		if params.SessionID == "" {
			return nil, &extensions.Error{
				Code:    -32602,
				Message: "invalid params: sessionId is required",
			}
		}
		if params.TargetPercent < 0 || params.TargetPercent > 100 {
			return nil, &extensions.Error{
				Code:    -32602,
				Message: "invalid params: targetPercent must be between 0 and 100",
			}
		}

		result, err := svc.CompactSession(ctx, info, params)
		if err != nil {
			if extErr, ok := errorsAsExt(err); ok {
				return nil, extErr
			}
			return nil, &extensions.Error{
				Code:    extensions.CodeInternal,
				Message: err.Error(),
			}
		}
		return result, nil
	}
}
