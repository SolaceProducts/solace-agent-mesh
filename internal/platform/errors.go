// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

type (
	ValidationError            = apierr.ValidationError
	NotFoundError              = apierr.NotFoundError
	ConflictError              = apierr.ConflictError
	BusinessRuleViolationError = apierr.BusinessRuleViolationError
	ForbiddenError             = apierr.ForbiddenError
	ExternalServiceError       = apierr.ExternalServiceError
	UnavailableError           = apierr.UnavailableError
)

func RequireAPIBase(apiBase string) error {
	if strings.TrimSpace(apiBase) == "" {
		return &ValidationError{Message: "apiBase is required", Field: "apiBase"}
	}
	if !ValidAPIBaseStructure(apiBase) {
		return &ValidationError{Message: "apiBase is not a valid URL (expected http(s)://host)", Field: "apiBase"}
	}
	return nil
}
