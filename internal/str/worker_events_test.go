// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestRebroadcast_FiresOnInterval(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	manifestDir := t.TempDir()
	stubExe := manifestDir + "/echo_tool"
	if err := os.WriteFile(stubExe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := NewManifestFromSources(nil, testLogger())
	manifest.Register("echo_tool", &ManifestEntry{
		ToolName:    "echo_tool",
		Parameters:  map[string]any{"type": "object"},
		Description: "echo",
	})

	initTopic := remotetool.InitTopic("test-ns", "echo_tool")
	q, qErr := brk.CreateQueue("rebroadcast-q", broker.QueueOptions{Named: true})
	if qErr != nil {
		t.Fatal(qErr)
	}
	if subErr := brk.Subscribe(q, initTopic); subErr != nil {
		t.Fatal(subErr)
	}

	w := &Worker{
		cfg:      WorkerConfig{Namespace: "test-ns", WorkerID: "str-1", RebroadcastInterval: 100 * time.Millisecond},
		svc:      svc,
		manifest: manifest,
		logger:   testLogger(),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	go w.rebroadcastLoop(ctx)

	received := 0
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		recvCtx, recvCancel := context.WithTimeout(ctx, 50*time.Millisecond)
		msg, err := brk.Receive(recvCtx, q)
		recvCancel()
		if err != nil || msg == nil {
			continue
		}
		msg.Ack()
		received++
	}

	if received < 2 {
		t.Errorf("expected at least 2 rebroadcasts within 600ms at 100ms interval, got %d", received)
	}
}

func TestRebroadcast_StopsOnContextCancel(t *testing.T) {
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	manifest := NewManifestFromSources(nil, testLogger())
	w := &Worker{
		cfg:      WorkerConfig{Namespace: "test-ns", WorkerID: "str-1", RebroadcastInterval: 50 * time.Millisecond},
		svc:      svc,
		manifest: manifest,
		logger:   testLogger(),
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.rebroadcastLoop(ctx)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("rebroadcastLoop did not stop within 500ms after context cancel")
	}
}

func TestSplitPackageToolName(t *testing.T) {
	tests := []struct {
		input    string
		wantPkg  string
		wantBare string
	}{
		{"pkg__tool", "pkg", "tool"},
		{"tool-only", "", "tool-only"},
		{"a__b__c", "a", "b__c"},
		{"__tool", "", "tool"},
	}
	for _, tt := range tests {
		pkg, bare := splitPackageToolName(tt.input)
		if pkg != tt.wantPkg {
			t.Errorf("splitPackageToolName(%q) pkg = %q, want %q", tt.input, pkg, tt.wantPkg)
		}
		if bare != tt.wantBare {
			t.Errorf("splitPackageToolName(%q) bare = %q, want %q", tt.input, bare, tt.wantBare)
		}
	}
}
