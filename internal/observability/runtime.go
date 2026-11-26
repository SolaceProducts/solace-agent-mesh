// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"runtime"
	"runtime/debug"
	"time"
)

const memStatsTTL = 2 * time.Second

func RegisterGoRuntimeMetrics(reg *Registry) {
	if reg == nil {
		return
	}

	memStats := NewTTLMemo(memStatsTTL, func() (*runtime.MemStats, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return &m, nil
	})
	read := func() *runtime.MemStats {
		m, _ := memStats.Value()
		return m
	}

	reg.ObservableValue("runtime.go.goroutines", func() float64 {
		return float64(runtime.NumGoroutine())
	})
	reg.ObservableValue("runtime.go.heap.alloc.bytes", func() float64 {
		return float64(read().HeapAlloc)
	})
	reg.ObservableValue("runtime.go.heap.sys.bytes", func() float64 {
		return float64(read().HeapSys)
	})
	reg.ObservableValue("runtime.go.heap.objects", func() float64 {
		return float64(read().HeapObjects)
	})
	reg.ObservableValue("runtime.go.gc.next.bytes", func() float64 {
		return float64(read().NextGC)
	})
	reg.ObservableValue("runtime.go.gc.count", func() float64 {
		return float64(read().NumGC)
	})
	reg.ObservableValue("runtime.go.mem.limit.bytes", func() float64 {
		return float64(debug.SetMemoryLimit(-1))
	})
}
