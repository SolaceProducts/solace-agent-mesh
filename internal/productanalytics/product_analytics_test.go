// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import "testing"

func TestTokenIsNeverConfiguredFromSource(t *testing.T) {
	if TokenConfigured() {
		t.Fatal("nothing injected a token, so TokenConfigured must be false: check for a hardcoded default")
	}
}
