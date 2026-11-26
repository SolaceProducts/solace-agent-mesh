// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

type TemperaturePolicy struct {
	rejecting modelMemo
}

func (p *TemperaturePolicy) Rejects(key string) bool {
	return p != nil && p.rejecting.has(key)
}

func (p *TemperaturePolicy) Mark(key string) {
	if p != nil {
		p.rejecting.add(key)
	}
}

func (p *TemperaturePolicy) Forget(key string) {
	if p != nil {
		p.rejecting.remove(key)
	}
}

func (p *TemperaturePolicy) Apply(req *ChatRequest, key string) {
	if req == nil || req.Temperature == nil {
		return
	}
	if p.Rejects(key) {
		req.Temperature = nil
	}
}

func ChatCompletionWithTemperatureFallback(
	ctx context.Context,
	client Client,
	req *ChatRequest,
	policy *TemperaturePolicy,
	memoKey string,
	logger *slog.Logger,
) (*ChatResponse, error) {
	if req == nil {
		return nil, errors.New("llm: nil chat request")
	}
	policy.Apply(req, memoKey)

	resp, err := client.ChatCompletion(ctx, req)
	if err != nil && req.Temperature != nil {
		if llmErr, ok := errors.AsType[*Error](err); ok && llmErr.IsTemperatureUnsupportedError() {
			rejected := *req.Temperature
			if logger != nil {
				logger.Warn("model rejected an explicit temperature; omitting it",
					"policyKey", memoKey, "temperature", rejected)
			}
			policy.Mark(memoKey)
			req.Temperature = nil
			retryResp, retryErr := client.ChatCompletion(ctx, req)
			if retryErr != nil {
				return nil, errors.Join(err, retryErr)
			}
			resp, err = retryResp, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("llm: chat completion for %q returned no response", memoKey)
	}
	return resp, nil
}
