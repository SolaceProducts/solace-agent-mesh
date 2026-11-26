// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestPortAvailabilityCheck_AllFree(t *testing.T) {
	a, b := freePort(t), freePort(t)
	env := Env{
		FastAPIPort:     fmt.Sprint(a),
		PlatformAPIPort: fmt.Sprint(b),
	}
	check := NewPortAvailabilityCheck()
	check.PIDLookup = func(int) string { return "" }
	got := check.Run(context.Background(), env, ContextLocal)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
}

func TestPortAvailabilityCheck_OneInUse(t *testing.T) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	env := Env{
		FastAPIPort:     fmt.Sprint(port),
		PlatformAPIPort: fmt.Sprint(freePort(t)),
	}
	check := NewPortAvailabilityCheck()
	check.PIDLookup = func(int) string { return " (PID 99: stub)" }
	got := check.Run(context.Background(), env, ContextLocal)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "in use") || !strings.Contains(got.Reason, "(PID 99: stub)") {
		t.Errorf("reason: %q", got.Reason)
	}
	if !strings.Contains(got.Detail, fmt.Sprint(port)) {
		t.Errorf("detail: got %q", got.Detail)
	}
}

func TestPortAvailabilityCheck_DefaultPortsWhenUnset(t *testing.T) {
	check := NewPortAvailabilityCheck()
	check.PIDLookup = func(int) string { return "" }
	got := check.Run(context.Background(), Env{}, ContextLocal)
	for _, want := range []string{"8800", "8001"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail %q missing default port %s", got.Detail, want)
		}
	}
}
