// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	openapitool "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

const specURLSubject = "specification URL"

type OpenAPIAuthDetector struct {
	inner *openapitool.AuthDetector
}

func NewOpenAPIAuthDetector(logger *slog.Logger) *OpenAPIAuthDetector {
	return newOpenAPIAuthDetectorWithClient(logger, httpClientWithSSRFGuard(nil))
}

func newOpenAPIAuthDetectorWithClient(logger *slog.Logger, client *http.Client) *OpenAPIAuthDetector {
	return &OpenAPIAuthDetector{inner: openapitool.NewAuthDetector(logger, client)}
}

func (d *OpenAPIAuthDetector) DetectAuth(ctx context.Context, specURL string, specContent []byte) (*OpenAPIDetectAuthResponse, error) {
	if err := validateOutboundURLField(specURLSubject, specURL); err != nil {
		return nil, err
	}
	raw, err := d.inner.DetectAuth(ctx, specURL, specContent)
	if err != nil {
		return nil, mapDetectError(err)
	}

	resp := &OpenAPIDetectAuthResponse{
		Schemes: make([]SecuritySchemeInfo, len(raw.Schemes)),
	}
	for i, s := range raw.Schemes {
		resp.Schemes[i] = SecuritySchemeInfo{
			SchemeName: s.SchemeName,
			AuthType:   s.AuthType,
			Details:    s.Details,
		}
	}
	if raw.SuggestedAuth != nil {
		resp.SuggestedAuth = &SuggestedAuth{
			AuthType: raw.SuggestedAuth.AuthType,
			Config:   raw.SuggestedAuth.Config,
		}
	}
	return resp, nil
}

func (d *OpenAPIAuthDetector) FetchSpec(ctx context.Context, specURL string) ([]byte, error) {
	if err := validateOutboundURLField(specURLSubject, specURL); err != nil {
		return nil, err
	}
	raw, err := d.inner.FetchSpec(ctx, specURL)
	if err != nil {
		return nil, mapDetectError(err)
	}
	return raw, nil
}

type unreadableSpecError struct{ *ValidationError }

func (e unreadableSpecError) Unwrap() error { return e.ValidationError }

func IsUnreadableSpecification(err error) bool {
	_, ok := errors.AsType[unreadableSpecError](err)
	return ok
}

func mapDetectError(err error) error {
	if verr, ok := errors.AsType[*openapitool.DetectAuthError](err); ok {
		if errors.Is(verr, openapitool.ErrSpecNotJSONOrYAML) {
			return unreadableSpecError{&ValidationError{Message: verr.Message}}
		}
		return &ValidationError{Message: verr.Message}
	}
	fetchErr, ok := errors.AsType[*openapitool.SpecFetchError](err)
	if !ok {
		return err
	}
	if failure, ok := guardedFetchFailure(fetchErr, specURLSubject, specURLSubject); ok {
		return failure
	}
	return &ExternalServiceError{
		Message: fmt.Sprintf("Could not fetch the OpenAPI specification from the URL%s. Verify the URL and that it is reachable from the platform.", statusSuffix(fetchErr.StatusCode)),
		Cause:   newOutboundCause(fetchErr),
	}
}

func statusSuffix(status int) string {
	if status == 0 {
		return ""
	}
	return fmt.Sprintf(" (HTTP %d)", status)
}
