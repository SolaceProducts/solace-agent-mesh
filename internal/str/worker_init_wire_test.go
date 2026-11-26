// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestPublishToolInits_TheBroadcastCarriesTheWorkersInstanceIDAndTheToolsEffect(t *testing.T) {
	brk := newTestBroker(t)
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools:\n  placeholder:\n    executable: /usr/bin/placeholder\n"), 0o644); err != nil {
		t.Fatalf("write manifest %s: %v", manifestPath, err)
	}
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:      "test-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	})
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	w.manifest.Register("pod_tool", &ManifestEntry{
		ToolName:    "pod_tool",
		Description: "a tool every pod broadcasts",
		Parameters:  map[string]any{"type": "object"},
		Effect:      remotetool.EffectReadOnly,
	})

	queue, err := brk.CreateQueue("pod-init-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := brk.Subscribe(queue, remotetool.InitTopic("test-ns", "pod_tool")); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}

	w.publishToolInits(ctx)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, queue)
	if err != nil || msg == nil {
		t.Fatalf("no init message for pod_tool: msg %v, err %v", msg, err)
	}
	defer msg.Ack()

	var req a2a.Request
	if decodeErr := json.Unmarshal(msg.Payload, &req); decodeErr != nil {
		t.Fatalf("decode envelope %s: %v", msg.Payload, decodeErr)
	}
	paramsJSON, err := json.Marshal(req.Params)
	if err != nil {
		t.Fatalf("re-encode params %v: %v", req.Params, err)
	}
	var params remotetool.InitParams
	if decodeErr := json.Unmarshal(paramsJSON, &params); decodeErr != nil {
		t.Fatalf("decode init params %s: %v", paramsJSON, decodeErr)
	}
	if w.instanceID == "" || params.InstanceID != w.instanceID {
		t.Errorf("broadcast instance_id = %q, want the worker's %q", params.InstanceID, w.instanceID)
	}
	if params.Effect != remotetool.EffectReadOnly {
		t.Errorf("broadcast effect = %q, want read_only", params.Effect)
	}

	_ = brk.DeleteQueue(w.queue.Name())
}

func TestRefreshSchemasOnce_AFailedRefreshPublishesTheDowngradeAtOnce(t *testing.T) {
	for _, tc := range []struct{ probe, wantEffect string }{
		{probeExitsNonZero, remotetool.EffectSideEffecting},
		{probeListsOthers, remotetool.EffectSideEffecting},
		{probeListsOneOther, ""},
		{probeMissing, remotetool.EffectSideEffecting},
	} {
		probe := tc.probe
		t.Run(probe, func(t *testing.T) {
			brk := newTestBroker(t)
			dir := t.TempDir()
			exe := filepath.Join(dir, "lookup")
			writeEffectStub(t, exe, map[string]string{"lookup": remotetool.EffectReadOnly})
			manifestPath := filepath.Join(dir, "manifest.yaml")
			if err := os.WriteFile(manifestPath, []byte("version: 1\ntools:\n  lookup:\n    executable: "+exe+"\n"), 0o644); err != nil {
				t.Fatalf("write manifest %s: %v", manifestPath, err)
			}
			w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
			ctx := t.Context()
			if err := w.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if got := w.manifest.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
				t.Fatalf("before the refresh, effect = %q, want read_only", got)
			}
			queue, err := brk.CreateQueue("downgrade-init-q-"+strings.ReplaceAll(probe, " ", "-"), broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			if subErr := brk.Subscribe(queue, remotetool.InitTopic("test-ns", "lookup")); subErr != nil {
				t.Fatalf("Subscribe: %v", subErr)
			}
			if probe == probeMissing {
				if rmErr := os.Remove(exe); rmErr != nil {
					t.Fatalf("remove %s: %v", exe, rmErr)
				}
			} else {
				prepareProbe(t, dir, probe)
			}

			w.refreshSchemasOnce(ctx, time.Now(), map[string]time.Time{"lookup": time.Now().Add(-time.Hour)})

			recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			msg, err := brk.Receive(recvCtx, queue)
			if err != nil || msg == nil {
				t.Fatalf("no init published after a refresh that downgraded the tool (%s): msg %v, err %v", probe, msg, err)
			}
			defer msg.Ack()
			var req a2a.Request
			if decodeErr := json.Unmarshal(msg.Payload, &req); decodeErr != nil {
				t.Fatalf("decode envelope %s: %v", msg.Payload, decodeErr)
			}
			paramsJSON, err := json.Marshal(req.Params)
			if err != nil {
				t.Fatalf("re-encode params %v: %v", req.Params, err)
			}
			var params remotetool.InitParams
			if decodeErr := json.Unmarshal(paramsJSON, &params); decodeErr != nil {
				t.Fatalf("decode init params %s: %v", paramsJSON, decodeErr)
			}
			if params.Effect != tc.wantEffect {
				t.Errorf("published effect = %q after a refresh that could not confirm read_only (%s), want %q", params.Effect, probe, tc.wantEffect)
			}
		})
	}
}

func TestRefreshSchemasOnce_AFailingRefreshPublishesTheDowngradeOnceAcrossTicks(t *testing.T) {
	brk := newTestBroker(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "lookup")
	writeEffectStub(t, exe, map[string]string{"lookup": remotetool.EffectReadOnly})
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools:\n  lookup:\n    executable: "+exe+"\n"), 0o644); err != nil {
		t.Fatalf("write manifest %s: %v", manifestPath, err)
	}
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	queue, err := brk.CreateQueue("downgrade-storm-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := brk.Subscribe(queue, remotetool.InitTopic("test-ns", "lookup")); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	prepareProbe(t, dir, probeExitsNonZero)

	const ticks = 3
	for range ticks {
		w.refreshSchemasOnce(ctx, time.Now(), map[string]time.Time{"lookup": time.Now().Add(-time.Hour)})
	}

	published := 0
	for {
		recvCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		msg, recvErr := brk.Receive(recvCtx, queue)
		cancel()
		if recvErr != nil || msg == nil {
			break
		}
		msg.Ack()
		published++
	}
	if published != 1 {
		t.Fatalf("%d init messages published over %d ticks of a refresh that keeps failing, want exactly 1 (the downgrade, not one per tick)", published, ticks)
	}
}
