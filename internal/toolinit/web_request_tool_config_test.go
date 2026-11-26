// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestLookupBuiltinTool_WebRequestHonorsToolConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	toolMap := map[string]any{
		"tool_config": map[string]any{"allow_loopback": true},
	}
	tl := LookupBuiltinTool("web_request", toolMap, Options{})
	if tl == nil {
		t.Fatal("LookupBuiltinTool(web_request) = nil")
	}

	toolCtx := &tool.Context{ToolConfig: map[string]any{}}
	result, err := tl.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q; allow_loopback from tool_config was ignored on the agent path",
			result.Status, result.Message)
	}
}
