// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"
)

func TestStartHealthServer_LogsErrorWithHintWhenPortBusy(t *testing.T) {
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupier listen: %v", err)
	}
	t.Cleanup(func() { _ = occupier.Close() })
	busyAddr := occupier.Addr().String()

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	inst := newTestInstance(Config{HealthAddr: busyAddr})
	inst.logger = logger
	setupMinimalRuntime(t, inst)

	inst.startHealthServer()

	got := buf.String()
	if !strings.Contains(got, "level=ERROR") {
		t.Errorf("expected ERROR level, got: %s", got)
	}
	if !strings.Contains(got, "management endpoint failed to bind") {
		t.Errorf("expected diagnostic message, got: %s", got)
	}
	if !strings.Contains(got, "stale sam process") {
		t.Errorf("expected stale-process hint, got: %s", got)
	}
	if !strings.Contains(got, busyAddr) {
		t.Errorf("expected addr %s in log, got: %s", busyAddr, got)
	}
	if !strings.Contains(got, "to check") {
		t.Errorf("expected runbook hint, got: %s", got)
	}
	if !strings.Contains(got, "Continuing startup") {
		t.Errorf("expected non-fatal reassurance, got: %s", got)
	}
	if inst.healthServer != nil {
		t.Error("expected healthServer to remain nil after bind failure")
	}
}
