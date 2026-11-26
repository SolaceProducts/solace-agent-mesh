// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LLMCheck struct {
	HTTPClient *http.Client
}

func NewLLMCheck() *LLMCheck { return &LLMCheck{HTTPClient: &http.Client{Timeout: 5 * time.Second}} }

func (LLMCheck) Name() string { return "llm_connectivity" }

func (LLMCheck) DisplayName() string { return "LLM Connectivity" }

func (c *LLMCheck) Run(ctx context.Context, env Env, _ Context) CheckResult {
	endpoint := strings.TrimRight(env.LLMServiceEndpoint, "/")
	if endpoint == "" {
		return CheckResult{Name: c.DisplayName(), Status: StatusSkip, Reason: "LLM_SERVICE_ENDPOINT not set"}
	}
	if env.LLMServiceAPIKey == "" {
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: endpoint, Reason: "LLM endpoint configured but no API key set"}
	}

	urls := []string{endpoint + "/v1/models", endpoint}
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: endpoint, Reason: fmt.Sprintf("LLM endpoint unreachable: %v", err)}
		}
		req.Header.Set("Authorization", "Bearer "+env.LLMServiceAPIKey)
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if isTimeout(err) {
				return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: endpoint, Reason: "LLM endpoint timed out after 5s"}
			}
			return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: endpoint, Reason: fmt.Sprintf("LLM endpoint unreachable: %v", err)}
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: endpoint}
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: endpoint, Reason: fmt.Sprintf("LLM API key invalid or unauthorized (HTTP %d)", resp.StatusCode)}
		case resp.StatusCode == 404 || resp.StatusCode == 405:
			continue
		default:
			return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: endpoint}
		}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: endpoint}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	type timeoutErr interface{ Timeout() bool }
	var t timeoutErr
	if errors.As(err, &t) && t.Timeout() {
		return true
	}
	return false
}
