// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import "testing"

func TestScopeSourceAdaptersReturnANilInterfaceWhenNothingIsWired(t *testing.T) {
	if got := NewScopeExpander(nil); got != nil {
		t.Errorf("NewScopeExpander(nil) = %#v, want a nil interface", got)
	}
	if got := lookupFor(nil); got != nil {
		t.Errorf("lookupFor(nil) = %#v, want a nil interface", got)
	}

	svc := &AuthorizationService{}
	if NewScopeExpander(svc) == nil || lookupFor(svc) == nil {
		t.Error("a wired service must yield a usable scope source")
	}
}
