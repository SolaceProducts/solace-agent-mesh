// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func HTTPFailureCode(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return DispatchErrorCode(taxonomy.DispatchClassAuthRequired)
	case status == http.StatusForbidden:
		return taxonomy.ToolFailurePermissionDenied
	case status == http.StatusRequestTimeout, status == http.StatusGatewayTimeout:
		return DispatchErrorCode(taxonomy.DispatchClassTimeout)
	case status == http.StatusTooManyRequests:
		return taxonomy.ToolFailureRateLimited
	case status >= 400 && status <= 499:
		return DispatchErrorCode(taxonomy.DispatchClassBackend4xx)
	case status >= 500 && status <= 599:
		return DispatchErrorCode(taxonomy.DispatchClassBackend5xx)
	}
	return ""
}
