// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Monitor struct {
	metrics    runtime.Metrics
	name       string
	labels     []string
	start      time.Time
	classifier func(error) string
}

func newMonitor(metrics runtime.Metrics, name string, classify func(error) string, labels ...string) *Monitor {
	if classify == nil {
		classify = classifyError
	}
	return &Monitor{
		metrics:    runtime.MetricsOrNoop(metrics),
		name:       name,
		labels:     labels,
		start:      time.Now(),
		classifier: classify,
	}
}

func (m *Monitor) Stop(errp *error) {
	if m == nil {
		return
	}
	if r := recover(); r != nil {
		m.emit(m.panicErrorType(r))
		panic(r)
	}
	errorType := ""
	if errp != nil && *errp != nil {
		errorType = m.classifier(*errp)
	}
	m.emit(errorType)
}

func (m *Monitor) emit(errorType string) {
	duration := time.Since(m.start)
	labels := make([]string, len(m.labels), len(m.labels)+2)
	copy(labels, m.labels)
	labels = append(labels, "error.type", errorType)
	m.metrics.Histogram(m.name, duration, labels...)
}

func (m *Monitor) panicErrorType(r any) string {
	if err, ok := r.(error); ok {
		return m.classifier(err)
	}
	return "panic"
}

func (m *Monitor) StopWithErrorType(errorType string) {
	if m == nil {
		return
	}
	m.emit(errorType)
}

func (m *Monitor) Elapsed() time.Duration {
	if m == nil {
		return 0
	}
	return time.Since(m.start)
}
