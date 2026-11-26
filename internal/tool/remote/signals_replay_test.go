// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestExecute_AReplayTakesItsHeldTurnOnce(t *testing.T) {
	s := newAuthSearch(t, "web_search_authed", 1)
	sink := &signalSink{}
	turns := &tool.SearchTurns{}
	s.run(t, s.context(sink, turns, "web_search"))
	s.signIn()
	s.run(t, s.context(sink, turns, "web_search"))
	s.run(t, s.context(sink, turns, "web_search"))
	got := turnsOf(t, s.capture.all())
	if len(got) != 3 || got[0] != 0 || got[1] != 0 || got[2] != 1 {
		t.Fatalf("turns = %v want [0 0 1]", got)
	}
}

func TestExecute_AReplayThatNeedsSignInAgainKeepsItsTurn(t *testing.T) {
	s := newAuthSearch(t, "web_search_authed", 2)
	sink := &signalSink{}
	turns := &tool.SearchTurns{}
	for range 3 {
		s.run(t, s.context(sink, turns, "call-1"))
		s.signIn()
	}
	got := turnsOf(t, s.capture.all())
	if len(got) != 3 || got[0] != 0 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("turns = %v want [0 0 0]", got)
	}
	if n := turns.Next("web_search"); n != 1 {
		t.Fatalf("next = %d", n)
	}
}

func TestExecute_APartialResultPublishesItsSignals(t *testing.T) {
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_partial"}, &logs)
	stubSTR(t, brk, "web_search_partial", func(p remotetool.InvokeParams, _ int) map[string]any {
		r := schemaResult(ragSignal(*p.SearchTurn, webSource("s0r0")))
		r["status"] = "partial"
		return r
	})
	sink := &signalSink{}
	if _, err := rt.Execute(t.Context(), map[string]any{}, searchContext(sink)); err != nil {
		t.Fatal(err)
	}
	if n := len(sink.ragUpdates()); n != 1 {
		t.Fatalf("published %d for partial, want 1", n)
	}
}

func TestDrawSearchTurn_IgnoresAHoldForAnEmptyCallID(t *testing.T) {
	turns := &tool.SearchTurns{}
	turns.Hold("", 5)
	rt := NewTool(&ToolConfig{ToolName: "web_search_x"}, nil)
	if got := rt.drawSearchTurn(&tool.Context{SearchTurns: turns}); *got != 0 {
		t.Fatalf("drew %d want fresh 0", *got)
	}
}

func TestHoldSearchTurn_HoldsNothingForAnEmptyCallID(t *testing.T) {
	turns := &tool.SearchTurns{}
	turn := 3
	holdSearchTurn(&tool.Context{SearchTurns: turns}, &turn, toolauth.NewPendingAuthResult("", nil, "k"))
	if _, ok := turns.TakeHeld(""); ok {
		t.Fatal("held for empty id")
	}
}

func TestIsWebURL_RefusesEmptyAndPasswordOnlyUserinfo(t *testing.T) {
	for _, u := range []string{"https://:pw@example.com/p", "https://@example.com/p", "https://:@example.com/p"} {
		if isWebURL(u) {
			t.Errorf("%s accepted", u)
		}
	}
}
