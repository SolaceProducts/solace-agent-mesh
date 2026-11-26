// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"time"
)

type diagnosticsSnapshot struct {
	Timestamp       time.Time `json:"timestamp"`
	PID             int       `json:"pid"`
	Goroutines      int       `json:"goroutines"`
	HeapAllocBytes  uint64    `json:"heap_alloc_bytes"`
	HeapInUseBytes  uint64    `json:"heap_in_use_bytes"`
	HeapSysBytes    uint64    `json:"heap_sys_bytes"`
	HeapObjects     uint64    `json:"heap_objects"`
	StackInUseBytes uint64    `json:"stack_in_use_bytes"`
	StackSysBytes   uint64    `json:"stack_sys_bytes"`
	TotalAllocBytes uint64    `json:"total_alloc_bytes"`
	NumGC           uint32    `json:"num_gc"`
	GCPauseTotalNs  uint64    `json:"gc_pause_total_ns"`
	LastGCUnixNs    uint64    `json:"last_gc_unix_ns"`
}

func StartDiagnosticsSignalHandler(logger *slog.Logger) (stop func()) {
	ch := make(chan os.Signal, 1)
	notifyDiagSignal(ch)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				DumpDiagnostics(logger)
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

func DumpDiagnostics(logger *slog.Logger) {
	now := time.Now()
	pid := os.Getpid()
	ts := now.Format("20060102T150405")

	runtime.GC()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	snap := diagnosticsSnapshot{
		Timestamp:       now,
		PID:             pid,
		Goroutines:      runtime.NumGoroutine(),
		HeapAllocBytes:  ms.HeapAlloc,
		HeapInUseBytes:  ms.HeapInuse,
		HeapSysBytes:    ms.HeapSys,
		HeapObjects:     ms.HeapObjects,
		StackInUseBytes: ms.StackInuse,
		StackSysBytes:   ms.StackSys,
		TotalAllocBytes: ms.TotalAlloc,
		NumGC:           ms.NumGC,
		GCPauseTotalNs:  ms.PauseTotalNs,
		LastGCUnixNs:    ms.LastGC,
	}

	tmpDir := os.TempDir()
	statsPath := fmt.Sprintf("%s/sam-awe-memstats-%d-%s.json", tmpDir, pid, ts)
	if data, marshalErr := json.MarshalIndent(snap, "", "  "); marshalErr == nil {
		if writeErr := os.WriteFile(statsPath, data, 0644); writeErr != nil {
			logger.Error("failed to write memstats", "path", statsPath, "error", writeErr)
		}
	}

	heapPath := fmt.Sprintf("%s/sam-awe-heap-%d-%s.pprof", tmpDir, pid, ts)
	if f, createErr := os.Create(heapPath); createErr == nil {
		if writeErr := pprof.WriteHeapProfile(f); writeErr != nil {
			logger.Error("failed to write heap profile", "path", heapPath, "error", writeErr)
		}
		_ = f.Close()
	} else {
		logger.Error("failed to create heap profile", "path", heapPath, "error", createErr)
	}

	goroutinePath := fmt.Sprintf("%s/sam-awe-goroutines-%d-%s.txt", tmpDir, pid, ts)
	if f, createErr := os.Create(goroutinePath); createErr == nil {
		if p := pprof.Lookup("goroutine"); p != nil {
			if writeErr := p.WriteTo(f, 1); writeErr != nil {
				logger.Error("failed to write goroutine stacks", "path", goroutinePath, "error", writeErr)
			}
		}
		_ = f.Close()
	} else {
		logger.Error("failed to create goroutine dump", "path", goroutinePath, "error", createErr)
	}

	logger.Info("diagnostics dump complete",
		"goroutines", snap.Goroutines,
		"heap_in_use_mb", fmt.Sprintf("%.1f", float64(snap.HeapInUseBytes)/(1024*1024)),
		"heapObjects", snap.HeapObjects,
		"stats", statsPath,
		"heapProfile", heapPath,
		"goroutineStacks", goroutinePath,
	)
}
