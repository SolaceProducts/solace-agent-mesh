// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type retryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

var defaultRetryConfig = retryConfig{
	MaxAttempts: 3,
	BaseDelay:   1 * time.Second,
	MaxDelay:    30 * time.Second,
}

type retryResult[T any] struct {
	Value T
	Resp  *http.Response
	Err   error
}

func withRetry[T any](ctx context.Context, cfg retryConfig, fn func(ctx context.Context) retryResult[T]) (T, error) {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	var zero T
	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		res := fn(ctx)

		if res.Err == nil && !shouldRetryResponse(res.Resp) {
			return res.Value, nil
		}

		lastErr = res.Err
		if lastErr == nil && res.Resp != nil {
			lastErr = errors.New(res.Resp.Status)
		}

		retryable := res.Resp == nil || shouldRetryResponse(res.Resp)
		if !retryable {
			return zero, lastErr
		}
		if attempt == cfg.MaxAttempts-1 {
			break
		}
		delay := cfg.BaseDelay * (1 << attempt)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("retry exhausted")
	}
	return zero, lastErr
}

func shouldRetryResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	if resp.StatusCode >= 500 {
		return true
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return false
}
