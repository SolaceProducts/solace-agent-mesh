// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"path/filepath"
	"testing"

	sqltool "github.com/SolaceProducts/solace-agent-mesh/tools/go/sql/sqltool"
)

func TestInitRequest_UnservableToolGetsNoReply(t *testing.T) {
	brk := newTestBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID: "unservable", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	const toolName = "execute_python_code"
	w.manifest.Register(toolName, &ManifestEntry{
		ToolName:         toolName,
		Executable:       filepath.Join(t.TempDir(), "python", "bin", toolName),
		Unservable:       true,
		UnservableReason: "executable not found or not executable",
	})

	if _, servable := w.manifest.ToolNames()[toolName]; servable {
		t.Fatal("precondition broken: ToolNames must omit an unservable entry")
	}

	if reply := requestInitDirect(t, w, brk, toolName, nil); reply != nil {
		t.Fatalf("unservable tool answered init_request, so the agent will advertise it as healthy "+
			"and every call will hang until the remote-tool timeout: %s", reply)
	}
}

func TestInitRequest_ServableToolStillReplies(t *testing.T) {
	brk := newTestBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID: "servable", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
		InProcessTools: sqltool.Tools(),
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	const toolName = "execute_sql_query"
	if entry := w.manifest.GetTool(toolName); entry == nil || entry.Unservable {
		t.Fatalf("precondition broken: %q must be present and servable", toolName)
	}
	if reply := requestInitDirect(t, w, brk, toolName, nil); reply == nil {
		t.Fatalf("servable in-process tool %q got no init_request reply", toolName)
	}
}
