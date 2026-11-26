// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package httpsse

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestActivationRoutesAreScopeGated(t *testing.T) {
	want := map[string]string{
		"GET " + pathActivation:              rbac.ScopeActivationRead,
		"POST " + pathActivationSignIn:       rbac.ScopeActivationUpdate,
		"POST " + pathActivationCancelSignIn: rbac.ScopeActivationUpdate,
		"POST " + pathActivationAccept:       rbac.ScopeActivationUpdate,
		"GET " + pathActivationTerms:         rbac.ScopeActivationRead,
		"GET " + pathFreeModel:               rbac.ScopeActivationRead,
		"POST " + pathFreeModelSignIn:        rbac.ScopeActivationUpdate,
		"POST " + pathFreeModelRefresh:       rbac.ScopeActivationUpdate,
	}

	seen := make(map[string]bool)
	for _, r := range newCoverageAdapter(t).Routes() {
		if !strings.HasPrefix(r.Path, pathActivation) && !strings.HasPrefix(r.Path, pathFreeModel) {
			continue
		}
		key := r.Method + " " + r.Path
		scope, ok := want[key]
		if !ok {
			t.Errorf("unexpected activation route %s — add it here with its scope", key)
			continue
		}
		seen[key] = true
		if r.Scope != scope || r.IsPublic != "" {
			t.Errorf("route %s Scope = %q IsPublic = %q, want Scope %q", key, r.Scope, r.IsPublic, scope)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("route %s not registered", key)
		}
	}
}
