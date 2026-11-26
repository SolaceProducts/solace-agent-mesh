// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"testing"
)

func TestRegisterGoRuntimeMetrics(t *testing.T) {
	reg, reader := newTestRegistry(t)
	RegisterGoRuntimeMetrics(reg)

	if dps := collectGauge(t, reader, "test.runtime.go.goroutines"); len(dps) != 1 || dps[0].Value < 1 {
		t.Errorf("runtime.go.goroutines = %+v, want a single value >= 1", dps)
	}
	if dps := collectGauge(t, reader, "test.runtime.go.heap.alloc.bytes"); len(dps) != 1 || dps[0].Value <= 0 {
		t.Errorf("runtime.go.heap.alloc.bytes = %+v, want a single value > 0", dps)
	}
	if dps := collectGauge(t, reader, "test.runtime.go.mem.limit.bytes"); len(dps) != 1 || dps[0].Value <= 0 {
		t.Errorf("runtime.go.mem.limit.bytes = %+v, want a single value > 0", dps)
	}
	for _, name := range []string{
		"test.runtime.go.heap.sys.bytes",
		"test.runtime.go.heap.objects",
		"test.runtime.go.gc.next.bytes",
		"test.runtime.go.gc.count",
	} {
		if dps := collectGauge(t, reader, name); len(dps) != 1 {
			t.Errorf("%s: got %d series, want 1", name, len(dps))
		}
	}
}

func TestRegisterGoRuntimeMetrics_NilRegistryNoPanic(t *testing.T) {
	RegisterGoRuntimeMetrics(nil)
}
