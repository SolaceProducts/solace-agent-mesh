// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package rbac

func init() {
	taggedScopeSources = append(taggedScopeSources, "product_analytics_scope.go")
}
