// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func runCoder(t *testing.T, o Options) *Outcome {
	t.Helper()
	out, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

func TestACallerCheckFailureGoesBackToTheModelAndTheRunContinues(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", submitResultTool, map[string]any{"verdict": "safe", "reasons": []any{}})),
		toolTurn(call("2", submitResultTool, map[string]any{"verdict": "safe", "reasons": []any{"no API change"}})),
	}}
	var checked []string
	validate := func(raw json.RawMessage) error {
		checked = append(checked, string(raw))
		var v struct{ Reasons []string }
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if len(v.Reasons) == 0 {
			return errors.New("a safe verdict needs at least one reason")
		}
		return nil
	}

	out := runCoder(t, Options{Mode: ModePlan, Dir: ws, Task: "is the bump safe?", OutputSchema: verdictSchema,
		ValidateResult: validate, Chat: m.chat})

	if len(checked) != 2 || len(m.calls) != 2 {
		t.Fatalf("checked %d answers over %d model calls, want 2 and 2", len(checked), len(m.calls))
	}
	if got := toolResults(m.calls[1])["1"]; !strings.Contains(got, "failed the caller's checks") ||
		!strings.Contains(got, "a safe verdict needs at least one reason") {
		t.Errorf("the failing answer returned %q, want the caller's error", got)
	}
	want := map[string]any{"verdict": "safe", "reasons": []any{"no API change"}}
	if fmt.Sprint(out.Result) != fmt.Sprint(want) || out.StopReason != StopCompleted {
		t.Errorf("outcome = %+v, want the corrected answer and a completed run", out)
	}
}

func TestOnTheLastStepASchemaValidAnswerIsKeptThoughTheCallerCheckFails(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "glob", map[string]any{"pattern": "*"})),
		toolTurn(call("2", submitResultTool, map[string]any{"verdict": "safe", "reasons": []any{}})),
	}}
	validate := func(json.RawMessage) error { return errors.New("never good enough") }

	out := runCoder(t, Options{Dir: ws, Task: "assess", OutputSchema: verdictSchema, MaxSteps: 1,
		ValidateResult: validate, Chat: m.chat})

	if out.Result == nil {
		t.Errorf("the last step's schema-valid answer was dropped; outcome = %+v", out)
	}
}
