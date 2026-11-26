// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type queryParams struct {
	Query string `json:"query" desc:"query"`
}

func newCitingTool() *ToolDef {
	return NewTool("web_search_test", "Search.",
		func(_ context.Context, p queryParams, tc *ToolContext) (*Result, error) {
			turn, ok := tc.SearchTurn()
			if !ok {
				return OK("searched"), nil
			}
			return OK("searched", WithSignals(Signal{
				Type: SignalRAGInfoUpdate,
				Data: map[string]any{"query": p.Query, "search_turn": turn},
			})), nil
		})
}

func wireSignals(t *testing.T, wire any) []any {
	t.Helper()
	m, ok := wire.(map[string]any)
	if !ok {
		t.Fatalf("wire result is %T, want map", wire)
	}
	signals, _ := m["signals"].([]any)
	return signals
}

func assertCitesTurn(t *testing.T, signals []any, turn int) {
	t.Helper()
	if len(signals) != 1 {
		t.Fatalf("signals = %v, want one", signals)
	}
	s := signals[0].(map[string]any)
	data := s["data"].(map[string]any)
	if s["type"] != SignalRAGInfoUpdate || data["search_turn"] != float64(turn) || data["query"] != "go" {
		t.Fatalf("signal = %v, want a rag_info_update for turn %d", s, turn)
	}
}

func TestInvokeInProcess_CarriesTheSearchTurnAndReturnsSignals(t *testing.T) {
	turn := 4
	wire, err := InvokeInProcess(t.Context(), []*ToolDef{newCitingTool()}, "web_search_test",
		map[string]any{"query": "go"}, nil, InProcessBindings{SearchTurn: &turn})
	if err != nil {
		t.Fatal(err)
	}
	assertCitesTurn(t, wireSignals(t, wire), turn)
}

func TestInvokeInProcess_OmitsSignalsWithoutASearchTurn(t *testing.T) {
	wire, err := InvokeInProcess(t.Context(), []*ToolDef{newCitingTool()}, "web_search_test",
		map[string]any{"query": "go"}, nil, InProcessBindings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := wire.(map[string]any)["signals"]; present {
		t.Fatalf("wire = %v, want no signals key", wire)
	}
}

func TestExecute_CarriesTheSearchTurnAndReturnsSignals(t *testing.T) {
	tmpDir := t.TempDir()
	resultFile := filepath.Join(tmpDir, "result.json")
	turn := 0
	argsData, _ := json.Marshal(runnerArgs{
		ToolName:   "web_search_test",
		Args:       map[string]any{"query": "go"},
		ResultFile: resultFile,
		OutputDir:  tmpDir,
		SearchTurn: &turn,
	})
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execute([]*ToolDef{newCitingTool()}, argsFile); err != nil {
		t.Fatal(err)
	}
	resultData, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	var result resultJSON
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatal(err)
	}
	assertCitesTurn(t, wireSignals(t, result.Result), turn)
}
