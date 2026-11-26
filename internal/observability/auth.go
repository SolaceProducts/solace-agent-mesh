// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

const (
	AuthResultSuccess = "success"
	AuthResultFailure = "failure"
)

func RecordAuthLogin(gateway, result, reason string) {
	runtime.MetricProvider().Counter("auth.login", 1,
		"gateway", gateway,
		"result", result,
		"reason", reason,
	)
}

func RecordAuthTokenValidationFailure(gateway, reason string) {
	runtime.MetricProvider().Counter("auth.token.validation", 1,
		"gateway", gateway,
		"result", AuthResultFailure,
		"reason", reason,
	)
}
