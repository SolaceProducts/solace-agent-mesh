// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var replacedTokenCallArgs = map[string]any{"query": "widgets", "limit": "5"}

func TestRemoteToolAuth_RejectedTokenAnotherWriterReplaced(t *testing.T) {
	tests := []struct {
		name            string
		replaceStored   bool
		rejectAll       bool
		wantCalls       int64
		wantMessage     string
		wantPrompt      bool
		wantStored      string
		wantSignInArgs  map[string]any
		wantStatusTexts []string
	}{
		{name: "the call is retried once with the stored token and no sign-in is asked", replaceStored: true, wantCalls: 2, wantMessage: "ITEMS-OK", wantStored: "second-access", wantStatusTexts: []string{"status of call 1", "status of call 2"}},
		{name: "an AUTH_REQUIRED for the stored token itself still asks the user to sign in and clears it", wantCalls: 1, wantMessage: "Pending user authorization.", wantPrompt: true, wantSignInArgs: replacedTokenCallArgs, wantStatusTexts: []string{"status of call 1"}},
		{name: "a replacement token that is also rejected is not retried again", rejectAll: true, wantCalls: 2, wantMessage: "Pending user authorization.", wantPrompt: true, wantStored: "access-3", wantSignInArgs: replacedTokenCallArgs, wantStatusTexts: []string{"status of call 1", "status of call 2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
			brk := broker.NewDevBroker()
			if err := brk.Connect(t.Context()); err != nil {
				t.Fatalf("DevBroker.Connect() error = %v", err)
			}
			exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second), WithInvokeBuffer(2*time.Second))
			startExecutor(t, exe)

			const credKey = "str_replaced"
			rt := NewTool(&ToolConfig{
				ToolName: "str_replaced", TimeoutSeconds: 5,
				Auth: &toolauth.AuthConfig{
					Type: "oauth2", CredentialKey: credKey,
					Scheme:     &toolauth.OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize", TokenURL: "https://auth.example.com/token"},
					Credential: &toolauth.AuthCredential{ClientID: "cid"},
				},
			}, exe)

			credSvc := newMockCredentialService()
			store := func(access string) {
				data, _ := json.Marshal(&toolauth.OAuthToken{AccessToken: access, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).Unix()})
				_ = credSvc.Set(context.Background(), testAgent, testAgent, "user1", credKey, string(data))
			}
			store("first-access")

			var calls atomic.Int64
			var argsMu sync.Mutex
			var argsSeen []map[string]any
			serveSTRByToken(t, brk, "str_replaced", func(token string, args map[string]any) map[string]any {
				n := calls.Add(1)
				argsMu.Lock()
				argsSeen = append(argsSeen, args)
				argsMu.Unlock()
				if tt.rejectAll {
					store(fmt.Sprintf("access-%d", n+1))
					return map[string]any{"status": "error", "message": "Authentication required", "error_code": "AUTH_REQUIRED"}
				}
				if token == "second-access" {
					return map[string]any{"message": "ITEMS-OK"}
				}
				if tt.replaceStored {
					store("second-access")
				}
				return map[string]any{"status": "error", "message": "Authentication required", "error_code": "AUTH_REQUIRED"}
			})

			var prompted atomic.Bool
			var statusMu sync.Mutex
			var statusTexts []string
			result, err := rt.Execute(t.Context(), maps.Clone(replacedTokenCallArgs), &tool.Context{
				AgentName: testAgent, UserID: "user1", ClientID: "client1", ParentTaskID: "task-1", ToolCallID: "call-1",
				CredentialService: credSvc,
				SendSignal:        func(any) error { prompted.Store(true); return nil },
				SendStatus: func(text string) error {
					statusMu.Lock()
					statusTexts = append(statusTexts, text)
					statusMu.Unlock()
					return nil
				},
			})

			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if result.Message != tt.wantMessage || prompted.Load() != tt.wantPrompt {
				t.Errorf("Execute() message = %q, sign-in asked = %v; want %q, %v", result.Message, prompted.Load(), tt.wantMessage, tt.wantPrompt)
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Errorf("STR calls = %d, want %d", got, tt.wantCalls)
			}
			argsMu.Lock()
			defer argsMu.Unlock()
			wantArgsSeen := slices.Repeat([]map[string]any{replacedTokenCallArgs}, int(tt.wantCalls))
			if diff := cmp.Diff(wantArgsSeen, argsSeen); diff != "" {
				t.Errorf("arguments the STR received per call (-want +got):\n%s", diff)
			}
			statusMu.Lock()
			defer statusMu.Unlock()
			if diff := cmp.Diff(tt.wantStatusTexts, statusTexts); diff != "" {
				t.Errorf("status texts forwarded to the task (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantSignInArgs, toolauth.PendingAuthOriginalArgs(result), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("arguments kept for the call after sign-in (-want +got):\n%s", diff)
			}
			var stored toolauth.OAuthToken
			if raw, _ := credSvc.Get(context.Background(), testAgent, testAgent, "user1", credKey); raw != "" {
				_ = json.Unmarshal([]byte(raw), &stored)
			}
			if stored.AccessToken != tt.wantStored {
				t.Errorf("stored access token = %q, want %q", stored.AccessToken, tt.wantStored)
			}
		})
	}
}

const statusSettle = 50 * time.Millisecond

func serveSTRByToken(t *testing.T, brk *broker.DevBroker, toolName string, answer func(token string, args map[string]any) map[string]any) {
	t.Helper()
	q, err := brk.CreateQueue("str-"+toolName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue(%q) error = %v", "str-"+toolName, err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, toolName)); err != nil {
		t.Fatalf("Subscribe(%q) error = %v", toolName, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var callNumber atomic.Int64
	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil {
				return
			}
			msg.Ack()
			var req struct {
				ID     string `json:"id"`
				Params struct {
					ToolOAuthToken string         `json:"tool_oauth_token"`
					Args           map[string]any `json:"args"`
				} `json:"params"`
			}
			_ = json.Unmarshal(msg.Payload, &req)
			corrID := msg.CorrelationID
			if corrID == "" {
				corrID = req.ID
			}
			result := answer(req.Params.ToolOAuthToken, req.Params.Args)
			statusPayload, _ := json.Marshal(a2a.Request{
				JSONRPC: "2.0",
				Method:  remotetool.MethodStatus,
				Params:  remotetool.StatusParams{TaskID: corrID, StatusText: fmt.Sprintf("status of call %d", callNumber.Add(1))},
			})
			_ = brk.Publish(context.Background(), msg.UserProperties[remotetool.PropStatusTo], &broker.Message{Payload: statusPayload})
			time.Sleep(statusSettle)
			payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: result}))
			_ = brk.Publish(context.Background(), msg.UserProperties[remotetool.PropReplyTo], &broker.Message{Payload: payload})
		}
	}()
}
