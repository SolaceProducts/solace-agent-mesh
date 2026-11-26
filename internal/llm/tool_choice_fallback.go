// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"log/slog"
)

type ToolChoicePolicy struct {
	refusing modelMemo
}

func (p *ToolChoicePolicy) Refuses(key string) bool {
	return p != nil && p.refusing.has(key)
}

func (p *ToolChoicePolicy) Mark(key string) bool {
	if p == nil {
		return true
	}
	return p.refusing.add(key)
}

func ChatCompletionWithToolChoiceFallback(
	ctx context.Context,
	client Client,
	req *ChatRequest,
	choices *ToolChoicePolicy,
	temperatures *TemperaturePolicy,
	memoKey string,
	logger *slog.Logger,
) (*ChatResponse, error) {
	if req == nil {
		return nil, errors.New("llm: nil chat request")
	}
	if choices.Refuses(memoKey) || ModelRefusesForcedToolChoice(memoKey) {
		req.ToolChoice = ""
	}
	resp, err := ChatCompletionWithTemperatureFallback(ctx, client, req, temperatures, memoKey, logger)
	if err == nil || req.ToolChoice == "" || !RefusesToolChoice(err) {
		return resp, err
	}
	if choices.Mark(memoKey) && logger != nil {
		logger.Warn("model refused a forced tool choice; leaving the choice to it",
			"policyKey", memoKey, "toolChoice", req.ToolChoice, "error", err)
	}
	req.ToolChoice = ""
	retryResp, retryErr := ChatCompletionWithTemperatureFallback(ctx, client, req, temperatures, memoKey, logger)
	if retryErr != nil {
		return nil, errors.Join(err, retryErr)
	}
	return retryResp, nil
}
