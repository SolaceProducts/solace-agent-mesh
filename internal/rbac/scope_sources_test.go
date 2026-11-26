// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

var taggedScopeSources []string

func scopeSourceFiles() []string {
	return append([]string{"scopes.go"}, taggedScopeSources...)
}
