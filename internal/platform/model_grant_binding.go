// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type OAuthGrantReader interface {
	Get(ctx context.Context, id string) (*store.ModelOAuthGrant, error)
}

func ValidateModelGrant(ctx context.Context, grants OAuthGrantReader, raw map[string]any) error {
	id, named := raw[llm.GrantIDKey].(string)
	if !named {
		return nil
	}
	if grants == nil {
		return grantValidationError("authConfig."+llm.GrantIDKey, "OAuth grants are not available on this deployment")
	}
	apiBase, _ := raw["api_base"].(string)
	model, _ := raw["model"].(string)
	provider, _ := samapp.ParseModelProvider(model)
	var hostErr *llm.GrantHostError
	if errors.As(llm.CheckGrantHost(provider, apiBase, raw), &hostErr) {
		return grantValidationError(grantHostField(hostErr.Field), hostErr.Message)
	}
	grant, err := grants.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("look up OAuth grant: %w", err)
	}
	if grant == nil {
		return grantValidationError("authConfig."+llm.GrantIDKey, llm.GrantIDKey+" names no OAuth grant")
	}
	if !sameOrigin(apiBase, grant.APIBase) {
		return grantValidationError("apiBase", "apiBase must be on the host the OAuth grant was issued for")
	}
	return nil
}

func grantHostField(key string) string {
	switch key {
	case "api_base":
		return "apiBase"
	case "provider":
		return "provider"
	default:
		return "modelParams." + key
	}
}

func grantValidationError(field, message string) *ValidationError {
	return &ValidationError{Message: "invalid " + AuthTypeOAuth2Delegated + " config: " + message, Details: map[string][]string{field: {message}}}
}

func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Hostname(), ub.Hostname()) &&
		portOf(ua) == portOf(ub)
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
