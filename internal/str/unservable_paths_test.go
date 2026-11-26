// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func TestInitRequest_UnservableBinaryIsNotAliased(t *testing.T) {
	brk := newTestBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID: "alias", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	const binary = "execute_sql_query"
	const alias = "mydb_sql_query_abc12345"
	w.manifest.Register(binary, &ManifestEntry{
		ToolName:         binary,
		Executable:       filepath.Join(t.TempDir(), "missing", binary),
		Unservable:       true,
		UnservableReason: "executable not found or not executable",
	})

	reply := requestInitDirect(t, w, brk, alias, map[string]any{"_str_binary": binary})
	if reply != nil {
		t.Fatalf("unservable binary answered init_request for its alias: %s", reply)
	}
	if got := w.manifest.GetTool(alias); got != nil {
		t.Errorf("alias %q was registered onto an unservable entry; a later manifest reload would "+
			"preserve this frozen pointer and the alias would stay dead after the binary is fixed", alias)
	}
}

func TestInvoke_UnservableToolReturnsNotFound(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	worker := newTestWorker(t, &testServices{brk: brk}, manifestPath)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	if entry := worker.manifest.GetTool("echo_tool"); entry == nil {
		t.Fatal("precondition: echo_tool missing from the test manifest")
	}
	worker.manifest.MarkUnservable("echo_tool", "executable not found or not executable")

	payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", nil)
	if err != nil {
		t.Fatalf("no reply to an invoke on an unservable tool; the caller would wait out its timeout: %v", err)
	}
	if !isErrorResponse(payload) {
		t.Fatalf("expected an error reply for an unservable tool, got: %s", payload)
	}
	code, _ := parseErrorResponse(t, payload)
	if code != remotetool.ErrToolNotFound {
		t.Errorf("error code = %q, want %q so the agent treats it as recoverable rather than as a "+
			"failed execution", code, remotetool.ErrToolNotFound)
	}
}
