// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func NewToolMonitor(metrics runtime.Metrics, toolName string) *Monitor {
	return newMonitor(
		metrics,
		metricOperationDuration,
		classifyToolError,
		"type", "tool",
		"component.name", toolName,
		"operation.name", "execute",
	)
}

func NewRemoteToolMonitor(metrics runtime.Metrics, toolName string) *Monitor {
	return newMonitor(
		metrics,
		metricOperationDuration,
		classifyRemoteError,
		"type", "str_tool",
		"component.name", toolName,
		"operation.name", "invoke",
	)
}

func NewRemoteToolMonitorSince(metrics runtime.Metrics, toolName string, start time.Time) *Monitor {
	m := NewRemoteToolMonitor(metrics, toolName)
	m.start = start
	return m
}

func classifyToolError(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "invalid") || strings.Contains(lower, "validation") {
		return "validation_error"
	}
	return classifyError(err)
}
