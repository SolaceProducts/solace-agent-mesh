// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

var ErrReauthorizationRequired = errors.New("model OAuth grant needs reauthorization")

type GrantToken struct {
	AccessToken string
	ExpiresAt   time.Time
}

type GrantTokens interface {
	GrantToken(ctx context.Context, grantID, rejectedFingerprint string) (GrantToken, error)
}

func TokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const GrantIDKey = "grant_id"

const FreeModelGrantID = "1c58a105-c275-4401-b9af-9b1af81a80af"

const ErrorTypeReauthorizationRequired = "reauthorization_required"

const ReauthorizationRequiredUserMessage = "The sign-in this model uses has expired. Sign in again to keep using it."

const ErrorTypeGrantTokenUnavailable = "grant_token_unavailable"

const GrantTokenUnavailableUserMessage = "This model's sign-in service can't be reached right now. Try again in a few minutes."

const grantCacheMargin = 5 * time.Minute

type tokenSource interface {
	GetToken(ctx context.Context) (string, error)
	Invalidate()
}

type grantTokenSource struct {
	grantID string
	grants  GrantTokens
	now     func() time.Time

	mu       sync.Mutex
	cached   GrantToken
	rejected string
}

func newGrantTokenSource(grantID string, grants GrantTokens) *grantTokenSource {
	return &grantTokenSource{grantID: grantID, grants: grants, now: time.Now}
}

func (t *grantTokenSource) GetToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	if t.cached.AccessToken != "" && t.cached.ExpiresAt.Sub(t.now()) > grantCacheMargin {
		token := t.cached.AccessToken
		t.mu.Unlock()
		return token, nil
	}
	rejected := t.rejected
	t.mu.Unlock()

	fresh, err := t.grants.GrantToken(ctx, t.grantID, rejected)
	if err != nil {
		return "", err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.cached = fresh
	if t.rejected == rejected {
		t.rejected = ""
	}
	return fresh.AccessToken, nil
}

func (t *grantTokenSource) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cached.AccessToken != "" {
		t.rejected = TokenFingerprint(t.cached.AccessToken)
	}
	t.cached = GrantToken{}
}

func checkGrantConfig(cfg BifrostConfig) error {
	if cfg.GrantID == "" {
		return nil
	}
	if cfg.Grants == nil {
		return fmt.Errorf("model calls with OAuth grant %s, but this process has no source of grant tokens", cfg.GrantID)
	}
	if cfg.OAuth2 != nil {
		return errors.New("a model cannot call with both OAuth2 client credentials and an OAuth grant")
	}
	if cfg.AzureWorkloadIdentity != nil {
		return errors.New("a model cannot call with both an Azure workload identity and an OAuth grant")
	}
	if err := CheckGrantHost(cfg.Provider, cfg.BaseURL, cfg.ProviderConfig); err != nil {
		return err
	}
	return nil
}

var grantHostOverrideKeys = []string{"endpoint", "url"}

var providersWithOwnHost = []string{"vertex", "bedrock"}

type GrantHostError struct {
	Field   string
	Message string
}

func (e *GrantHostError) Error() string { return e.Field + ": " + e.Message }

func CheckGrantHost(provider, baseURL string, providerConfig map[string]any) error {
	if strings.TrimSpace(baseURL) == "" {
		return &GrantHostError{Field: "api_base", Message: "a model that calls with an OAuth grant needs an api_base"}
	}
	if slices.Contains(providersWithOwnHost, provider) {
		return &GrantHostError{Field: "provider", Message: "provider " + provider + " does not call api_base, so it cannot call with an OAuth grant"}
	}
	for _, key := range grantHostOverrideKeys {
		if value, set := providerConfig[key]; set && value != nil && value != "" {
			return &GrantHostError{Field: key, Message: key + " cannot be set on a model that calls with an OAuth grant, whose token goes only to api_base"}
		}
	}
	return nil
}

func grantTokenError(provider string, err error) error {
	switch {
	case errors.Is(err, ErrReauthorizationRequired):
		return &Error{Type: ErrorTypeReauthorizationRequired, Message: ErrReauthorizationRequired.Error(), Provider: provider}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("oauth grant token: %w", err)
	}
	return &Error{Type: ErrorTypeGrantTokenUnavailable, Message: "oauth grant token: " + err.Error(), Provider: provider}
}

func (e *Error) IsGrantTokenUnavailable() bool {
	return e != nil && e.Type == ErrorTypeGrantTokenUnavailable
}

func (c *BifrostClient) grantTokenRejected(bfErr *schemas.BifrostError) bool {
	if c.cfg.GrantID == "" || c.tokenMgr == nil || bfErr == nil || bfErr.StatusCode == nil {
		return false
	}
	return *bfErr.StatusCode == http.StatusUnauthorized || bifrostErrorToError(bfErr, c.cfg.Provider).IsTermsAcceptanceRequired()
}

func termsRefusalOutlivesRefresh(provider string, refusal *schemas.BifrostError, refreshErr error) bool {
	llmErr, typed := errors.AsType[*Error](refreshErr)
	return typed && llmErr.IsGrantTokenUnavailable() &&
		bifrostErrorToError(refusal, provider).IsTermsAcceptanceRequired()
}

func (e *Error) IsReauthorizationRequired() bool {
	return e != nil && e.Type == ErrorTypeReauthorizationRequired
}
