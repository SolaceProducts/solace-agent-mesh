// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type authSearch struct {
	rt      *Tool
	capture *invokeCapture
	credSvc *mockCredentialService
	signIns int
}

func newAuthSearch(t *testing.T, name string, authRequired int) *authSearch {
	t.Helper()
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{
		ToolName: name,
		Auth: &toolauth.AuthConfig{
			Type:          "oauth2",
			CredentialKey: "search_auth",
			Scheme: &toolauth.OAuthScheme{
				AuthorizationURL: "https://auth.example.com/authorize",
				TokenURL:         "https://auth.example.com/token",
			},
			Credential: &toolauth.AuthCredential{ClientID: "cid"},
		},
	}, &logs)
	capture := stubSTR(t, brk, name, func(p remotetool.InvokeParams, call int) map[string]any {
		turn := *p.SearchTurn
		result := schemaResult(ragSignal(turn, webSource("s"+strconv.Itoa(turn)+"r0")))
		if call < authRequired {
			result["status"], result["error_code"] = "error", "AUTH_REQUIRED"
		}
		return result
	})
	s := &authSearch{rt: rt, capture: capture, credSvc: newMockCredentialService()}
	s.signIn()
	return s
}

func (s *authSearch) signIn() {
	s.signIns++
	token, _ := json.Marshal(&toolauth.OAuthToken{AccessToken: "t" + strconv.Itoa(s.signIns), TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	_ = s.credSvc.Set(context.Background(), testAgent, testAgent, "user1", "search_auth", string(token))
}

func (s *authSearch) context(sink *signalSink, turns *tool.SearchTurns, callID string) *tool.Context {
	return &tool.Context{
		AgentName: testAgent, UserID: "user1", ClientID: "client1", ParentTaskID: "task-1",
		ToolCallID: callID, CredentialService: s.credSvc, SearchTurns: turns, SendSignal: sink.send,
	}
}

func (s *authSearch) run(t *testing.T, toolCtx *tool.Context) *tool.Result {
	t.Helper()
	result, err := s.rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExecute_AuthReplaysReuseTheirOwnSearchTurns(t *testing.T) {
	s := newAuthSearch(t, "web_search_authed", 2)
	sink := &signalSink{}
	turns := &tool.SearchTurns{}

	for _, callID := range []string{"call-1", "call-2"} {
		if r := s.run(t, s.context(sink, turns, callID)); !toolauth.IsPendingAuthResult(r) {
			t.Fatalf("%s: result = %+v, want a pending sign-in", callID, r)
		}
		s.signIn()
	}
	if n := len(sink.ragUpdates()); n != 0 {
		t.Fatalf("published %d citations for calls that answered AUTH_REQUIRED, want 0", n)
	}
	for _, callID := range []string{"call-2", "call-1"} {
		s.run(t, s.context(sink, turns, callID))
	}

	got := turnsOf(t, s.capture.all())
	if len(got) != 4 || got[0] != 0 || got[1] != 1 || got[2] != 1 || got[3] != 0 {
		t.Fatalf("search turns = %v, want [0 1 1 0]: each replay reuses its own call's turn", got)
	}
	updates := sink.ragUpdates()
	if len(updates) != 2 || updates[0].SearchTurn != 1 || updates[1].SearchTurn != 0 {
		t.Fatalf("published %+v, want turns 1 then 0 after sign-in", updates)
	}
	if next := turns.Next("web_search"); next != 2 {
		t.Fatalf("next turn = %d, want 2: the replays must not draw turns", next)
	}
}

func TestExecute_AnotherTaskWithTheSameCallIDDrawsItsOwnTurn(t *testing.T) {
	s := newAuthSearch(t, "web_search_authed", 1)
	sink := &signalSink{}
	taskA, taskB := &tool.SearchTurns{}, &tool.SearchTurns{}
	taskB.Next("web_search")

	s.run(t, s.context(sink, taskA, "web_search"))
	s.signIn()
	s.run(t, s.context(sink, taskB, "web_search"))

	if got := turnsOf(t, s.capture.all()); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("search turns = %v, want task B to draw its own turn 1", got)
	}
	if held, ok := taskA.TakeHeld("web_search"); !ok || held != 0 {
		t.Fatalf("task A's held turn = %d, %v; want 0 still held", held, ok)
	}
}

func TestExecute_ATerminalSignInFailureHoldsNoTurn(t *testing.T) {
	s := newAuthSearch(t, "web_search_authed", 1)
	sink := &signalSink{}
	turns := &tool.SearchTurns{}
	toolCtx := s.context(sink, turns, "call-1")
	toolCtx.AuthGaveUp = func(_, _, _ string) bool { return true }

	if r := s.run(t, toolCtx); toolauth.IsPendingAuthResult(r) {
		t.Fatalf("result = %+v, want a terminal failure", r)
	}
	if _, held := turns.TakeHeld("call-1"); held {
		t.Fatal("a terminal sign-in failure left a held turn")
	}
}

func TestExecute_ACallWithoutAnIDHoldsNoTurn(t *testing.T) {
	s := newAuthSearch(t, "web_search_authed", 1)
	sink := &signalSink{}
	turns := &tool.SearchTurns{}

	s.run(t, s.context(sink, turns, ""))
	s.signIn()
	s.run(t, s.context(sink, turns, ""))

	if got := turnsOf(t, s.capture.all()); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("search turns = %v, want [0 1]: without an id there is no replay to match", got)
	}
}
