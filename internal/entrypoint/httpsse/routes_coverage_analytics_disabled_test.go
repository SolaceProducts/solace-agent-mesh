// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package httpsse

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func testProductAnalyticsOption(*testing.T, string) runtime.Option {
	return func(*runtime.Runtime) {}
}
