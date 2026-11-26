// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestHashTaskIDDeterministicWorkerIndex(t *testing.T) {
	const poolSize = 8
	ids := []string{
		"task-abc",
		"task-def",
		"0f8e2c1a-1b2c-4d5e-8f90-a1b2c3d4e5f6",
		"",
	}
	for _, id := range ids {
		want := hashTaskID(id) % poolSize
		for range 100 {
			if got := hashTaskID(id) % poolSize; got != want {
				t.Fatalf("taskID %q: index not deterministic, got %d want %d", id, got, want)
			}
		}
	}
}

func TestEventRoutingKey(t *testing.T) {
	t.Run("extracts taskID from last topic segment", func(t *testing.T) {
		msg := &broker.Message{Topic: "default/a2a/v1/gateway/response/gw1/task-xyz"}
		if got := eventRoutingKey(msg); got != "task-xyz" {
			t.Fatalf("got %q, want task-xyz", got)
		}
	})

	t.Run("same taskID on response and status routes identically", func(t *testing.T) {
		resp := &broker.Message{Topic: "default/a2a/v1/gateway/response/gw1/task-xyz"}
		status := &broker.Message{Topic: "default/a2a/v1/gateway/status/gw1/task-xyz"}
		if eventRoutingKey(resp) != eventRoutingKey(status) {
			t.Fatalf("response key %q != status key %q", eventRoutingKey(resp), eventRoutingKey(status))
		}
	})

	t.Run("falls back to whole topic when no taskID", func(t *testing.T) {
		msg := &broker.Message{Topic: "default/a2a/v1/discovery/"}
		if got := eventRoutingKey(msg); got != msg.Topic {
			t.Fatalf("got %q, want whole topic %q", got, msg.Topic)
		}
	})
}

func TestResolveEventWorkers(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		want       int
	}{
		{"explicit passthrough", 8, 8},
		{"explicit one degenerates to serial", 1, 1},
		{"explicit at cap", maxExplicitEventWorkers, maxExplicitEventWorkers},
		{"explicit above cap clamps", 10000, maxExplicitEventWorkers},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveEventWorkers(tt.configured, nil); got != tt.want {
				t.Fatalf("resolveEventWorkers(%d) = %d, want %d", tt.configured, got, tt.want)
			}
		})
	}
}

func TestResolveEventWorkersAutoDerivation(t *testing.T) {
	prev := goruntime.GOMAXPROCS(0)
	t.Cleanup(func() { goruntime.GOMAXPROCS(prev) })

	t.Run("floor when few CPUs", func(t *testing.T) {
		goruntime.GOMAXPROCS(1)
		if got := resolveEventWorkers(0, nil); got != minEventWorkers {
			t.Fatalf("got %d, want floor %d", got, minEventWorkers)
		}
	})
	t.Run("ceiling when many CPUs", func(t *testing.T) {
		goruntime.GOMAXPROCS(64)
		if got := resolveEventWorkers(0, nil); got != 32 {
			t.Fatalf("got %d, want ceiling 32", got)
		}
	})
	t.Run("a 4-core gateway gets enough workers to keep commits in flight", func(t *testing.T) {
		goruntime.GOMAXPROCS(4)
		if got := resolveEventWorkers(0, nil); got != 16 {
			t.Fatalf("got %d, want 16", got)
		}
	})
	t.Run("tracks GOMAXPROCS between floor and ceiling", func(t *testing.T) {
		goruntime.GOMAXPROCS(24)
		if got := resolveEventWorkers(0, nil); got != 24 {
			t.Fatalf("got %d, want 24", got)
		}
	})
}

func TestResolveEventWorkersClampWarns(t *testing.T) {
	t.Run("clamp emits exactly one warn carrying the configured value", func(t *testing.T) {
		lc := newLogCapture()
		if got := resolveEventWorkers(1000, lc.logger); got != maxExplicitEventWorkers {
			t.Fatalf("got %d, want %d", got, maxExplicitEventWorkers)
		}
		warns := 0
		for _, line := range lc.lines() {
			if line["level"] != "WARN" {
				continue
			}
			if msg, _ := line["msg"].(string); !strings.Contains(msg, "clamping") {
				continue
			}
			warns++
			if configured, _ := line["configured"].(float64); int(configured) != 1000 {
				t.Fatalf("warn configured attr = %v, want 1000", line["configured"])
			}
		}
		if warns != 1 {
			t.Fatalf("want exactly 1 clamp warn, got %d", warns)
		}
	})

	t.Run("value within cap does not warn", func(t *testing.T) {
		lc := newLogCapture()
		resolveEventWorkers(8, lc.logger)
		for _, line := range lc.lines() {
			if line["level"] == "WARN" {
				t.Fatalf("unexpected warn for in-range value: %v", line)
			}
		}
	})
}
