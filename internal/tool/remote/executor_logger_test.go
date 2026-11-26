// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWithLoggerInjectsLoggerWithAgentNameBound(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	e := NewExecutor(nil, "test-ns", "agent-under-test", WithLogger(logger))
	e.logger.Debug("probe")

	out := buf.String()
	if !strings.Contains(out, "probe") {
		t.Errorf("the injected logger was not used: %q", out)
	}
	if !strings.Contains(out, "agentName=agent-under-test") {
		t.Errorf("agentName was not bound, so option ordering regressed: %q", out)
	}
	if !strings.Contains(out, "logger_name=remote-tool") {
		t.Errorf("logger_name was not preserved: %q", out)
	}
}

func TestWithLogger_NilIsIgnored(t *testing.T) {
	e := NewExecutor(nil, "test-ns", "agent", WithLogger(nil))
	if e.logger == nil {
		t.Fatal("a nil logger replaced the default, so every dispatch log line would panic")
	}
}
