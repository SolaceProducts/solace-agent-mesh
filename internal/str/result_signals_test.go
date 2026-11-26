// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestBuildRunnerArgs_CarriesTheSearchTurn(t *testing.T) {
	runner := &SandboxRunner{cfg: SandboxConfig{}, logger: slog.Default()}
	turn := 7
	args := runner.buildRunnerArgs(&remotetool.InvokeParams{SearchTurn: &turn},
		&ManifestEntry{ToolName: "web_search"}, nil, nil, "", "", "", "", nil)
	if args.SearchTurn == nil || *args.SearchTurn != turn {
		t.Fatalf("runner_args search_turn = %v, want %d", args.SearchTurn, turn)
	}
}

func TestInProcessTool_ReceivesTheSearchTurnAndReturnsSignals(t *testing.T) {
	const toolName = "web_search_cite"
	td := samtoolsdk.NewTool(toolName, "Cite.",
		func(_ context.Context, _ struct{}, tc *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			turn, ok := tc.SearchTurn()
			if !ok {
				return samtoolsdk.Error("no search turn"), nil
			}
			return samtoolsdk.OK("ok", samtoolsdk.WithSignals(samtoolsdk.Signal{
				Type: samtoolsdk.SignalRAGInfoUpdate,
				Data: map[string]any{"search_turn": turn},
			})), nil
		})

	brk := newTestBroker(t)
	artSvc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
	w := NewWorker(&testServices{brk: brk, artifacts: artSvc}, WorkerConfig{
		WorkerID: "signals", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
		InProcessTools: []*samtoolsdk.ToolDef{td},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	entry := w.manifest.GetTool(toolName)
	if entry == nil {
		t.Fatalf("tool %q not registered", toolName)
	}

	turn := 2
	result, invokeErr := w.executeInProcessTool(t.Context(), &remotetool.InvokeParams{
		ToolName: toolName, TaskID: "corr", AppName: "agent", UserID: "user", SessionID: "sess",
		Args: map[string]any{}, SearchTurn: &turn,
	}, entry, artSvc, func(string) {})
	if invokeErr != nil {
		t.Fatalf("invoke error: %s %s", invokeErr.Code, invokeErr.Message)
	}
	signals, _ := result.ToolResult["signals"].([]any)
	if len(signals) != 1 {
		t.Fatalf("tool result = %+v, want one signal", result.ToolResult)
	}
	data := signals[0].(map[string]any)["data"].(map[string]any)
	if data["search_turn"] != float64(turn) {
		t.Fatalf("signal data = %v, want search_turn %d", data, turn)
	}
}

func TestSpillOversizedResult_KeepsSignals(t *testing.T) {
	svc, _ := newTestScopedSvc()
	signals := []any{map[string]any{"type": "rag_info_update", "data": map[string]any{"search_turn": 0}}}
	envelope := map[string]any{
		"data":    map[string]any{"summary": strings.Repeat("Z", 5000)},
		"signals": signals,
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 1024, newDiscardLogger())
	if _, spilled := envelope["data"]; spilled {
		t.Fatal("precondition: data was not spilled")
	}
	if got, _ := envelope["signals"].([]any); len(got) != 1 {
		t.Fatalf("signals = %v after spill, want them untouched", envelope["signals"])
	}
}
