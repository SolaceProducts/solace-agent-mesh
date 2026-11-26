// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package samcli

import "testing"

func TestAnalyticsCommandIsAbsentWithoutTheCollectionPath(t *testing.T) {
	if _, _, err := newRootCmd().Find([]string{"analytics"}); err == nil {
		t.Error("sam analytics is registered in a build with no collection path")
	}
}
