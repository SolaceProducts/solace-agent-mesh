// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import "testing"

func TestOnlyFailuresOfTheProviderOrDeploymentAreSystemic(t *testing.T) {
	for kind, want := range map[FailureKind]bool{
		FailureUnauthorized:    true,
		FailureConfig:          true,
		FailureUnreachable:     true,
		FailureRateLimited:     true,
		FailureTimeout:         true,
		FailureInvalidResponse: false,
		FailureInputTooLarge:   false,
		FailureRefused:         false,
		FailureKind("unknown"): false,
	} {
		if got := kind.Systemic(); got != want {
			t.Errorf("%s.Systemic() = %v, want %v", kind, got, want)
		}
	}
}
