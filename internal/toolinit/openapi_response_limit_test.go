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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type executor interface {
	Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error)
}

func sizedSpecServer(t *testing.T, bodySize int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewUnstartedServer(mux)

	mux.HandleFunc("/spec/", func(w http.ResponseWriter, r *http.Request) {
		op := strings.TrimPrefix(r.URL.Path, "/spec/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
  "openapi": "3.0.0",
  "info": {"title": "Sized", "version": "1.0.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/data/%s": {
      "get": {
        "operationId": %q,
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`, srv.URL, op, op)
	})
	mux.HandleFunc("/data/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"payload":%q}`, strings.Repeat("x", bodySize))
	})

	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func runRegisteredTool(t *testing.T, host *testHost, name string) *tool.Result {
	t.Helper()
	registered := host.Tools().GetTool(name)
	if registered == nil {
		t.Fatalf("tool %q was not registered", name)
	}
	ex, ok := registered.(executor)
	if !ok {
		t.Fatalf("tool %q is not executable", name)
	}
	result, err := ex.Execute(t.Context(), nil, nil)
	if err != nil {
		t.Fatalf("Execute(%s): %v", name, err)
	}
	return result
}

func TestRegisterTools_OpenAPIPerToolsetResponseLimit(t *testing.T) {
	t.Parallel()
	srv := sizedSpecServer(t, 4096)
	host := newTestHost("openapi-limit-agent")

	_, entries, err := RegisterTools(host, []any{
		map[string]any{
			"tool_type":         "openapi",
			"specification_url": srv.URL + "/spec/fetchStrict",
			"max_response_size": 512,
		},
		map[string]any{
			"tool_type":         "openapi",
			"specification_url": srv.URL + "/spec/fetchRoomy",
			"max_response_size": 65536,
		},
		map[string]any{
			"tool_type":         "openapi",
			"specification_url": srv.URL + "/spec/fetchDefault",
		},
	}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	for _, e := range entries {
		if e.Status == EntryStatusFailed {
			t.Fatalf("entry failed: %s", e.Error)
		}
	}

	strict := runRegisteredTool(t, host, "fetch_strict")
	if strict.Status != tool.StatusError || !strings.Contains(strict.Message, "exceeds limit 512") {
		t.Errorf("fetch_strict: status=%q message=%q, want rejection at its own 512-byte cap", strict.Status, strict.Message)
	}

	roomy := runRegisteredTool(t, host, "fetch_roomy")
	if roomy.Status == tool.StatusError {
		t.Errorf("fetch_roomy: unexpected error %q, want its own 64 KiB cap to admit a 4 KiB body", roomy.Message)
	}

	deflt := runRegisteredTool(t, host, "fetch_default")
	if deflt.Status == tool.StatusError {
		t.Errorf("fetch_default: unexpected error %q, want the 10 MiB default when the key is unset", deflt.Message)
	}
}
