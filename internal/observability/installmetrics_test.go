// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func installMetrics[T runtime.Metrics](t *testing.T, m T) T {
	t.Helper()
	runtime.Init(m)
	t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })
	return m
}
