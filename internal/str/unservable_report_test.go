// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestPublishToolInits_ReportsUnservableEntries(t *testing.T) {
	brk := newTestBroker(t)
	dir := t.TempDir()

	writeSchemaStubForManifestTest(t, filepath.Join(dir, "good_tool"), "good_tool", "control")

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := `version: 1
tools:
  pkg__broken_with_schema:
    executable: ./absent_one
    description: "Never installed"
    parameters:
      message:
        type: string
  pkg__broken_no_schema:
    executable: ./absent_two
    description: "Never installed, and never described"
  good_tool:
    executable: ./good_tool
    description: "Installed"
    parameters:
      message:
        type: string
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	failureQueue := func(name, tool string) broker.Queue {
		t.Helper()
		q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
		if err != nil {
			t.Fatal(err)
		}
		if subErr := brk.Subscribe(q, remotetool.FailedDiscoveryTopic("test-ns", tool)); subErr != nil {
			t.Fatal(subErr)
		}
		return q
	}
	withSchemaQ := failureQueue("with-schema-q", "pkg__broken_with_schema")
	noSchemaQ := failureQueue("no-schema-q", "pkg__broken_no_schema")
	goodQ := failureQueue("good-q", "good_tool")

	receive := func(q broker.Queue, wait time.Duration) *broker.Message {
		t.Helper()
		rctx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		msg, err := brk.Receive(rctx, q)
		if err != nil {
			return nil
		}
		if msg != nil {
			msg.Ack()
		}
		return msg
	}

	w.publishToolInits(ctx)

	for _, tc := range []struct {
		name string
		q    broker.Queue
	}{
		{"pkg__broken_with_schema", withSchemaQ},
		{"pkg__broken_no_schema", noSchemaQ},
	} {
		if msg := receive(tc.q, 2*time.Second); msg == nil {
			t.Errorf("%s: no failed-discovery broadcast; the platform is left waiting on a "+
				"sweeper timeout for a cause this worker already knows", tc.name)
		}
	}

	if msg := receive(goodQ, 300*time.Millisecond); msg != nil {
		t.Errorf("good_tool was reported as failed; a servable tool must not be: %s", msg.Payload)
	}

	w.publishToolInits(ctx)
	if msg := receive(withSchemaQ, 300*time.Millisecond); msg != nil {
		t.Errorf("the same failure was republished on the next broadcast: %s", msg.Payload)
	}

	w.manifest.MarkUnservable("pkg__broken_with_schema", "a different cause entirely")
	w.publishToolInits(ctx)
	if msg := receive(withSchemaQ, 2*time.Second); msg == nil {
		t.Error("a changed unservable reason was suppressed as a duplicate; the platform keeps " +
			"the superseded cause")
	}
}

func TestPublishToolInits_ForgetsReportsForRemovedTools(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	const name = "pkg__gone_later"
	w.manifest.Register(name, &ManifestEntry{ToolName: name, Executable: "/nonexistent/absent"})
	w.manifest.MarkUnservable(name, "executable not found or not executable: /nonexistent/absent")

	w.publishToolInits(ctx)
	if _, recorded := w.unservableReported.Load(name); !recorded {
		t.Fatal("precondition: the unservable tool was never reported, so this test cannot tell " +
			"a working prune from a report that never happened")
	}

	w.manifest.Unregister(name)
	if w.manifest.GetTool(name) != nil {
		t.Fatal("precondition: the entry survived Unregister, so nothing was removed to prune")
	}

	w.publishToolInits(ctx)
	if _, stillThere := w.unservableReported.Load(name); stillThere {
		t.Error("the report record outlived the manifest entry; nothing on the per-entry paths " +
			"can ever clear it again")
	}
}
