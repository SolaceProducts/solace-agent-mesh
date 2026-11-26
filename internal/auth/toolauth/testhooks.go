// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"testing"
	"time"
)

func TightenDiscoveryRetryForTest(t *testing.T) {
	t.Helper()
	prev := discoveryRetryConfig
	discoveryRetryConfig = retryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	}
	t.Cleanup(func() { discoveryRetryConfig = prev })
}
