// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
)

type AllowanceStatus string

const (
	AllowanceAvailable               AllowanceStatus = "available"
	AllowanceReauthorizationRequired AllowanceStatus = "reauthorization_required"
	AllowancePaused                  AllowanceStatus = "paused"
	AllowanceUnavailable             AllowanceStatus = "unavailable"
)

const (
	allowancePath    = "/v1/allowance"
	allowanceTimeout = 10 * time.Second
	maxAllowanceBody = 64 << 10
)

type Allowance struct {
	Status           AllowanceStatus
	RemainingPercent *float64
	UpstreamStatus   int
}

type Allowances struct {
	cfg    Config
	tokens llm.GrantTokens
	client *http.Client

	modelsMu sync.Mutex
	models   []ModelDisplay
}

func NewAllowances(cfg Config, tokens llm.GrantTokens) *Allowances {
	return &Allowances{
		cfg:    cfg,
		tokens: tokens,
		client: &http.Client{Timeout: allowanceTimeout, CheckRedirect: refuseRedirect},
	}
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (a *Allowances) Get(ctx context.Context) (Allowance, error) {
	answer, err := a.fetch(ctx, allowancePath)
	var grantErr *grantTokenError
	if errors.As(err, &grantErr) {
		return grantFailure(grantErr.err)
	}
	if err != nil {
		return Allowance{Status: AllowanceUnavailable}, fmt.Errorf("read the free model allowance: %w", err)
	}
	return parseAllowance(answer)
}

type grantTokenError struct{ err error }

func (e *grantTokenError) Error() string { return e.err.Error() }
func (e *grantTokenError) Unwrap() error { return e.err }

var errRefreshedTokenRejected = errors.New("the free LLM rejected a refreshed token")

type freeLLMAnswer struct {
	status int
	body   []byte
}

func (a *Allowances) fetch(ctx context.Context, path string) (freeLLMAnswer, error) {
	token, err := a.tokens.GrantToken(ctx, GrantID, "")
	if err != nil {
		return freeLLMAnswer{}, &grantTokenError{err}
	}
	answer, err := a.get(ctx, path, token.AccessToken)
	if err != nil || answer.status != http.StatusUnauthorized {
		return answer, err
	}
	token, err = a.tokens.GrantToken(ctx, GrantID, llm.TokenFingerprint(token.AccessToken))
	if err != nil {
		return freeLLMAnswer{}, &grantTokenError{err}
	}
	return a.get(ctx, path, token.AccessToken)
}

func RefreshFailure(err error) Allowance {
	allowance, _ := grantFailure(err)
	return allowance
}

func grantFailure(err error) (Allowance, error) {
	if errors.Is(err, llm.ErrReauthorizationRequired) || errors.Is(err, tokenbroker.ErrNotFound) {
		return Allowance{Status: AllowanceReauthorizationRequired}, nil
	}
	return Allowance{Status: AllowanceUnavailable}, fmt.Errorf("free model access token: %w", err)
}

func (a *Allowances) get(ctx context.Context, path, accessToken string) (freeLLMAnswer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.BaseURL+path, nil)
	if err != nil {
		return freeLLMAnswer{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return freeLLMAnswer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAllowanceBody))
	if err != nil {
		return freeLLMAnswer{}, err
	}
	return freeLLMAnswer{status: resp.StatusCode, body: body}, nil
}

func parseAllowance(answer freeLLMAnswer) (Allowance, error) {
	switch {
	case answer.status == http.StatusUnauthorized:
		return Allowance{Status: AllowanceUnavailable, UpstreamStatus: http.StatusUnauthorized}, errRefreshedTokenRejected
	case answer.status == http.StatusServiceUnavailable && errorType(answer.body) == llm.ErrorTypeFreeTierPaused:
		return Allowance{Status: AllowancePaused}, nil
	case answer.status == http.StatusNotFound:
		untouched := 100.0
		return Allowance{Status: AllowanceAvailable, RemainingPercent: &untouched}, nil
	case answer.status != http.StatusOK:
		return Allowance{Status: AllowanceUnavailable, UpstreamStatus: answer.status}, errors.New("the free LLM refused the allowance request")
	}
	var parsed struct {
		RemainingPercent *float64 `json:"remainingPercent"`
	}
	if err := json.Unmarshal(answer.body, &parsed); err != nil {
		return Allowance{Status: AllowanceUnavailable, UpstreamStatus: answer.status}, fmt.Errorf("decode the free model allowance: %w", err)
	}
	if parsed.RemainingPercent == nil {
		return Allowance{Status: AllowanceAvailable}, nil
	}
	remaining := min(max(*parsed.RemainingPercent, 0), 100)
	return Allowance{Status: AllowanceAvailable, RemainingPercent: &remaining}, nil
}

func errorType(body []byte) string {
	var answer struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &answer)
	return answer.Error.Type
}
