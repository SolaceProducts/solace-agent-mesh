// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	metricDispatchDuration = "str.dispatch.duration"

	metricSemaphoreWait = "str.semaphore.wait"

	metricExecDuration = "str.exec.duration"

	metricSetupDuration = "str.setup.duration"

	metricInflight = "str.invocations.inflight"

	metricComputeBudget = "str.admission.compute_budget"

	metricComputeYields = "str.admission.compute_yields"

	metricMemoryKills = "str.memory_guard.kills"

	metricMemoryPressure = "str.memory_guard.pressure"

	metricCPUThrottledRatio = "str.cgroup.cpu_throttled_ratio"

	metricIsolatedJobDuration = "str.isolated.job.duration"

	metricErrors = "str.errors"

	metricCancellations = "str.cancellations"

	metricReconcileRuns = "str.reconcile.runs"

	metricReconcileDuration = "str.reconcile.duration"

	metricReconcileDrift = "str.reconcile.drift"
)

type strMetrics struct {
	m        runtime.Metrics
	inflight atomic.Int64
}

func newSTRMetrics(m runtime.Metrics) *strMetrics {
	return &strMetrics{m: runtime.MetricsOrNoop(m)}
}

func (s *strMetrics) invocationStarted(tool string) func(status string) {
	if s == nil {
		return func(string) {}
	}
	start := time.Now()
	n := s.inflight.Add(1)
	s.m.Gauge(metricInflight, float64(n))
	return func(status string) {
		s.m.Histogram(metricDispatchDuration, time.Since(start),
			"tool", tool, "status", status)
		s.m.Gauge(metricInflight, float64(s.inflight.Add(-1)))
	}
}

func (s *strMetrics) recordComputeYield() {
	if s == nil {
		return
	}
	s.m.Counter(metricComputeYields, 1)
}

func (s *strMetrics) recordMemoryKill(tool string) {
	if s == nil {
		return
	}
	s.m.Counter(metricMemoryKills, 1, "tool", tool)
}

func (s *strMetrics) recordMemoryPressure(on bool) {
	if s == nil {
		return
	}
	v := 0.0
	if on {
		v = 1
	}
	s.m.Gauge(metricMemoryPressure, v)
}

func (s *strMetrics) recordComputeBudget(limit int, throttledRatio float64) {
	if s == nil {
		return
	}
	s.m.Gauge(metricComputeBudget, float64(limit))
	s.m.Gauge(metricCPUThrottledRatio, throttledRatio)
}

func (s *strMetrics) recordSemaphoreWait(d time.Duration, backend string) {
	if s == nil {
		return
	}
	s.m.Histogram(metricSemaphoreWait, d, "backend", backend)
}

func (s *strMetrics) recordSetupDuration(d time.Duration, tool string) {
	if s == nil {
		return
	}
	s.m.Histogram(metricSetupDuration, d, "tool", tool)
}

func (s *strMetrics) recordExecDuration(d time.Duration, tool, status string) {
	if s == nil {
		return
	}
	s.m.Histogram(metricExecDuration, d, "tool", tool, "status", status)
}

func (s *strMetrics) recordIsolatedJob(phase string, d time.Duration, tool string) {
	if s == nil {
		return
	}
	s.m.Histogram(metricIsolatedJobDuration, d, "tool", tool, "phase", phase)
}

func (s *strMetrics) recordError(tool, code string) {
	if s == nil {
		return
	}
	s.m.Counter(metricErrors, 1, "tool", tool, "code", code)
}

func (s *strMetrics) recordCancellation(tool string) {
	if s == nil {
		return
	}
	s.m.Counter(metricCancellations, 1, "tool", tool)
}

func (s *strMetrics) recordReconcileRun(d time.Duration, reason, result string) {
	if s == nil {
		return
	}
	s.m.Counter(metricReconcileRuns, 1, "reason", reason, "result", result)
	s.m.Histogram(metricReconcileDuration, d, "reason", reason, "result", result)
}

func (s *strMetrics) recordReconcileDrift(reason string) {
	if s == nil {
		return
	}
	s.m.Counter(metricReconcileDrift, 1, "reason", reason)
}
