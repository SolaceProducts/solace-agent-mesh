// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const (
	builderCard      = `{"name":"Builder","capabilities":{"extensions":[{"uri":"https://solace.com/a2a/extensions/sam/internal","params":{"internal":true}}]}}`
	workflowCard     = `{"name":"flow","capabilities":{"extensions":[{"uri":"https://solace.com/a2a/extensions/agent-type","params":{"type":"workflow"}}]}}`
	orchestratorCard = `{"name":"Orchestrator","capabilities":{}}`
)

func TestAwaitChatAgent(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		lists     []string
		limit     time.Duration
		want      bool
		wantCalls int32
	}{
		{name: "a chat agent is listed at once", status: http.StatusOK, lists: []string{`[` + builderCard + `,` + orchestratorCard + `]`}, limit: time.Minute, want: true, wantCalls: 1},
		{name: "a chat agent is listed after the internal agent", status: http.StatusOK, lists: []string{`[]`, `[` + builderCard + `]`, `[` + builderCard + `,` + orchestratorCard + `]`}, limit: time.Minute, want: true, wantCalls: 3},
		{name: "only internal and workflow agents are listed", status: http.StatusOK, lists: []string{`[` + builderCard + `,` + workflowCard + `]`}, limit: 50 * time.Millisecond, want: false},
		{name: "an app locked until activation refuses the list", status: http.StatusForbidden, lists: []string{`[]`}, limit: time.Minute, want: true, wantCalls: 1},
		{name: "a gateway that needs sign-in refuses the list", status: http.StatusUnauthorized, lists: []string{`[]`}, limit: time.Minute, want: true, wantCalls: 1},
		{name: "the gateway keeps failing", status: http.StatusServiceUnavailable, lists: []string{`[` + orchestratorCard + `]`}, limit: 50 * time.Millisecond, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := int(calls.Add(1))
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"data":` + tt.lists[min(n, len(tt.lists))-1] + `}`))
			}))
			t.Cleanup(srv.Close)

			got := awaitChatAgent(t.Context(), srv.Client(), srv.URL, tt.limit, time.Millisecond)

			if got != tt.want {
				t.Fatalf("awaitChatAgent() = %v, want %v", got, tt.want)
			}
			if tt.wantCalls > 0 && calls.Load() != tt.wantCalls {
				t.Fatalf("agent cards requested %d times, want %d", calls.Load(), tt.wantCalls)
			}
		})
	}
}
