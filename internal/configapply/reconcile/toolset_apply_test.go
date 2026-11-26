// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func serveNoToolsetConfig(w http.ResponseWriter, _ *http.Request) {
	writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: map[string]any{}})
}

func TestExecuteUpdateToolset_PatchesConfigWhenChanged(t *testing.T) {
	var patchCalls atomic.Int32
	var got map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "demo", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{{
				ToolName:     "echo",
				ConfigSchema: []any{map[string]any{"key": "api_base"}, map[string]any{"key": "api_key"}},
			}},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", serveNoToolsetConfig)
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		patchCalls.Add(1)
		var body wire.UpdateToolsetConfigurationRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body.Values.Value
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: body.Values.Value})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name:          "demo",
		ID:            "id-1",
		Verb:          "update",
		UploadNeeded:  false,
		ConfigChanged: true,
		Desired: &toolsetDesired{
			name:   "demo",
			create: wire.CreateToolsetRequest{Name: "demo"},
			config: map[string]any{"api_base": "https://api.openai.com/v1", "api_key": "sk-real"},
		},
	}

	if err := executeUpdateToolset(context.Background(), c, op); err != nil {
		t.Fatalf("executeUpdateToolset: %v", err)
	}
	if patchCalls.Load() != 1 {
		t.Errorf("config PATCH calls = %d; want 1", patchCalls.Load())
	}
	echoVals, _ := got["echo"].(map[string]any)
	if echoVals["api_base"] != "https://api.openai.com/v1" || echoVals["api_key"] != "sk-real" {
		t.Errorf("PATCH body = %v; want nested values under echo", got)
	}
}

func TestExecuteUpdateToolset_ReuploadFansConfigToNewTool(t *testing.T) {
	var patchCalls atomic.Int32
	var got map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "celebrate"})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "celebrate", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{
				{ToolName: "list_employees", ConfigSchema: []any{map[string]any{"key": "database_url"}}},
				{ToolName: "admin_repair_employee", ConfigSchema: []any{map[string]any{"key": "database_url"}}},
			},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"values":{"list_employees":{"database_url":"<REDACTED>"}}}}`))
	})
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		patchCalls.Add(1)
		var body wire.UpdateToolsetConfigurationRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body.Values.Value
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: body.Values.Value})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name:          "celebrate",
		ID:            "id-1",
		Verb:          "update",
		UploadNeeded:  true,
		ConfigChanged: false,
		Desired: &toolsetDesired{
			name:        "celebrate",
			create:      wire.CreateToolsetRequest{Name: "celebrate"},
			zipFilename: "celebrate.zip",
			bundleData:  []byte("zip-bytes"),
			config:      map[string]any{"database_url": "postgres://real"},
		},
	}

	if err := executeUpdateToolset(context.Background(), c, op); err != nil {
		t.Fatalf("executeUpdateToolset: %v", err)
	}
	if patchCalls.Load() != 1 {
		t.Fatalf("config PATCH calls = %d; want 1 (re-fan to cover new tool)", patchCalls.Load())
	}
	newTool, _ := got["admin_repair_employee"].(map[string]any)
	if newTool["database_url"] != "postgres://real" {
		t.Errorf("PATCH body = %v; want database_url fanned out to admin_repair_employee", got)
	}
}

func TestExecuteUpdateToolset_ReuploadSkipsConfigWhenToolSetUnchanged(t *testing.T) {
	var patchCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "celebrate"})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "celebrate", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{
				{ToolName: "list_employees", ConfigSchema: []any{map[string]any{"key": "database_url"}}},
			},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"values":{"list_employees":{"database_url":"<REDACTED>"}}}}`))
	})
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, _ *http.Request) {
		patchCalls.Add(1)
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: map[string]any{}})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name:          "celebrate",
		ID:            "id-1",
		Verb:          "update",
		UploadNeeded:  true,
		ConfigChanged: false,
		Desired: &toolsetDesired{
			name:        "celebrate",
			create:      wire.CreateToolsetRequest{Name: "celebrate"},
			zipFilename: "celebrate.zip",
			bundleData:  []byte("zip-bytes"),
			config:      map[string]any{"database_url": "postgres://real"},
		},
	}

	if err := executeUpdateToolset(context.Background(), c, op); err != nil {
		t.Fatalf("executeUpdateToolset: %v", err)
	}
	if patchCalls.Load() != 0 {
		t.Errorf("config PATCH calls = %d; want 0 (tool set unchanged, no redundant redeploy)", patchCalls.Load())
	}
}

func TestExecuteUpdateToolset_PatchConfigErrorSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "demo", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{{
				ToolName: "echo", ConfigSchema: []any{map[string]any{"key": "api_base"}},
			}},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", serveNoToolsetConfig)
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "boom", nil)
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name: "demo", ID: "id-1", Verb: "update", ConfigChanged: true,
		Desired: &toolsetDesired{name: "demo", create: wire.CreateToolsetRequest{Name: "demo"}, config: map[string]any{"api_base": "https://x"}},
	}
	err := executeUpdateToolset(context.Background(), c, op)
	if err == nil || !strings.Contains(err.Error(), "patch config") {
		t.Fatalf("want wrapped patch-config error; got %v", err)
	}
}

func TestExecuteUpdateToolset_DiscoveryFailureSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "demo", DiscoveryStatus: new("failed"),
			DiscoveryErrors: []string{"binary rejected"},
		})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name: "demo", ID: "id-1", Verb: "update", ConfigChanged: true,
		Desired: &toolsetDesired{name: "demo", create: wire.CreateToolsetRequest{Name: "demo"}, config: map[string]any{"api_base": "https://x"}},
	}
	err := executeUpdateToolset(context.Background(), c, op)
	if err == nil || !strings.Contains(err.Error(), "wait for discovery") {
		t.Fatalf("want wrapped discovery error; got %v", err)
	}
}

func TestExecuteUpdateToolset_CompareConfigErrorSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "demo"})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "demo", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{{
				ToolName: "echo", ConfigSchema: []any{map[string]any{"key": "api_base"}},
			}},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "boom", nil)
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name: "demo", ID: "id-1", Verb: "update", UploadNeeded: true, ConfigChanged: false,
		Desired: &toolsetDesired{
			name: "demo", create: wire.CreateToolsetRequest{Name: "demo"},
			zipFilename: "demo.zip", bundleData: []byte("zip-bytes"),
			config: map[string]any{"api_base": "https://x"},
		},
	}
	err := executeUpdateToolset(context.Background(), c, op)
	if err == nil || !strings.Contains(err.Error(), "compare config") {
		t.Fatalf("want wrapped compare-config error; got %v", err)
	}
}

func TestExecuteUpdateToolset_NestConfigErrorSurfaces(t *testing.T) {
	var getCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		if getCalls.Add(1) == 1 {
			writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "demo", DiscoveryStatus: new("ready")})
			return
		}
		writeV2Error(w, http.StatusInternalServerError, "boom", nil)
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name: "demo", ID: "id-1", Verb: "update", ConfigChanged: true,
		Desired: &toolsetDesired{name: "demo", create: wire.CreateToolsetRequest{Name: "demo"}, config: map[string]any{"api_base": "https://x"}},
	}
	err := executeUpdateToolset(context.Background(), c, op)
	if err == nil || !strings.Contains(err.Error(), "nest config") {
		t.Fatalf("want wrapped nest-config error; got %v", err)
	}
}

func TestExecuteUpdateToolset_UndeclaredConfigKeyFailsBeforePatch(t *testing.T) {
	var patchCalls atomic.Int32
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "ledger"})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-1", Name: "ledger", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{{ToolName: "post_entry", ConfigSchema: []any{map[string]any{"key": "api_base"}}}},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", serveNoToolsetConfig)
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		patchCalls.Add(1)
		var body wire.UpdateToolsetConfigurationRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body.Values.Value
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: body.Values.Value})
	})
	srv := newV2Platform(t, mux)
	c := newClientForTest(t, srv.URL)

	opWith := func(config map[string]any) toolsetOp {
		return toolsetOp{
			Name: "ledger", ID: "id-1", Verb: "update", UploadNeeded: true, ConfigChanged: true,
			Desired: &toolsetDesired{
				name: "ledger", create: wire.CreateToolsetRequest{Name: "ledger"},
				zipFilename: "ledger.zip", bundleData: []byte("zip-bytes"), config: config,
			},
		}
	}

	err := executeUpdateToolset(context.Background(), c, opWith(map[string]any{"api_base": "https://x", "ledger_api_key": "sk"}))
	if err == nil || !strings.Contains(err.Error(), "ledger_api_key") || !strings.Contains(err.Error(), "post_entry") {
		t.Fatalf("want undeclared-key error naming the key and discovered tools; got %v", err)
	}
	if patchCalls.Load() != 0 {
		t.Fatalf("config PATCH calls = %d; want 0 for an undeclared key", patchCalls.Load())
	}

	auth := map[string]any{"credential": map[string]any{"client_id": "cid"}}
	if err := executeUpdateToolset(context.Background(), c, opWith(map[string]any{"api_base": "https://x", "auth": auth})); err != nil {
		t.Fatalf("declared + reserved keys: %v", err)
	}
	if patchCalls.Load() != 1 {
		t.Fatalf("config PATCH calls = %d; want 1", patchCalls.Load())
	}
	if _, ok := got["auth"]; !ok {
		t.Errorf("reserved auth block must pass through at the top level; PATCH body = %v", got)
	}
	if vals, _ := got["post_entry"].(map[string]any); vals["api_base"] != "https://x" {
		t.Errorf("declared key must nest under its tool; PATCH body = %v", got)
	}
}

func TestGetToolsetConfig_DecodesResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"values":{"api_base":"https://api.openai.com/v1","api_key":"<REDACTED>"}}}`))
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	resp, err := c.GetToolsetConfig(context.Background(), "id-1")
	if err != nil {
		t.Fatalf("GetToolsetConfig: %v", err)
	}
	if resp.Values["api_base"] != "https://api.openai.com/v1" {
		t.Errorf("api_base = %v", resp.Values["api_base"])
	}
	if resp.Values["api_key"] != "<REDACTED>" {
		t.Errorf("expected redacted secret on the wire; got %v", resp.Values["api_key"])
	}
}

func TestPatchToolsetConfig_SendsValues(t *testing.T) {
	var got wire.UpdateToolsetConfigurationRequest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", serveNoToolsetConfig)
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: map[string]any{"k": "v"}})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	values := map[string]any{"k": "v"}
	if err := c.PatchToolsetConfig(context.Background(), "id-1", nil, values); err != nil {
		t.Fatalf("PatchToolsetConfig: %v", err)
	}
	if got.Values.Value["k"] != "v" {
		t.Errorf("PATCH body = %v; want k=v", got.Values.Value)
	}
}

func TestExecuteCreateToolset_PatchesConfigWhenPresent(t *testing.T) {
	var patchCalls atomic.Int32
	var got map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/toolsets", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusCreated, wire.ToolsetResponse{ID: "id-new", Name: "demo"})
	})
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-new", Name: "demo"})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID: "id-new", Name: "demo", DiscoveryStatus: new("ready"),
			Tools: []apiv2.ToolsetTool{{
				ToolName:     "echo",
				ConfigSchema: []any{map[string]any{"key": "api_key"}},
			}},
		})
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}/config", serveNoToolsetConfig)
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		patchCalls.Add(1)
		var body wire.UpdateToolsetConfigurationRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = body.Values.Value
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: body.Values.Value})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name:          "demo",
		Verb:          "create",
		UploadNeeded:  true,
		ConfigChanged: true,
		Desired: &toolsetDesired{
			name:        "demo",
			create:      wire.CreateToolsetRequest{Name: "demo"},
			zipFilename: "demo.zip",
			bundleData:  []byte("zip-bytes"),
			config:      map[string]any{"api_key": "sk-real"},
		},
	}

	if err := executeCreateToolset(context.Background(), c, op); err != nil {
		t.Fatalf("executeCreateToolset: %v", err)
	}
	if patchCalls.Load() != 1 {
		t.Errorf("config PATCH calls = %d; want 1", patchCalls.Load())
	}
	echoVals, _ := got["echo"].(map[string]any)
	if echoVals["api_key"] != "sk-real" {
		t.Errorf("PATCH body = %v; want api_key=sk-real nested under echo", got)
	}
}

func TestExecuteUpdateToolset_UploadNeverAppendsForceQuery(t *testing.T) {
	var uploadCalls atomic.Int32
	var uploadQuery atomic.Pointer[string]

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		uploadCalls.Add(1)
		q := r.URL.RawQuery
		uploadQuery.Store(&q)
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "demo"})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name:         "demo",
		ID:           "id-1",
		Verb:         "update",
		UploadNeeded: true,
		Desired: &toolsetDesired{
			name:        "demo",
			create:      wire.CreateToolsetRequest{Name: "demo"},
			zipFilename: "demo.zip",
			bundleData:  []byte("zip-bytes"),
		},
	}

	if err := executeUpdateToolset(context.Background(), c, op); err != nil {
		t.Fatalf("executeUpdateToolset: %v", err)
	}
	if uploadCalls.Load() != 1 {
		t.Fatalf("upload calls = %d; want 1", uploadCalls.Load())
	}
	if q := uploadQuery.Load(); q != nil && *q != "" {
		t.Errorf("upload query = %q; want empty (no ?force gate)", *q)
	}
}

func TestWaitToolsetReady_SurfacesDiscoveryErrors(t *testing.T) {
	const diag = "binary cannot run on this host: it's Mach-O 64-bit executable arm64, but STR is running on linux/arm64. Re-upload the toolset with a binary built for linux/arm64 (Go example: `GOOS=linux GOARCH=arm64 go build`)"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID:              "id-bad",
			Name:            "demo",
			DiscoveryStatus: new("failed"),
			DiscoveryErrors: []string{diag},
		})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	err := c.waitToolsetReady(context.Background(), "id-bad", 200*1000)
	if err == nil {
		t.Fatal("expected error for discoveryStatus=failed")
	}
	if !strings.Contains(err.Error(), diag) {
		t.Errorf("error should include the platform diagnostic verbatim.\n  got: %v\n want substring: %q", err, diag)
	}
}

func TestWaitToolsetReady_FailedWithNoDiagnostics(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{
			ID:              "id-bad",
			Name:            "demo",
			DiscoveryStatus: new("failed"),
		})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	err := c.waitToolsetReady(context.Background(), "id-bad", 200*1000)
	if err == nil {
		t.Fatal("expected error for discoveryStatus=failed")
	}
	if !strings.Contains(err.Error(), "no diagnostic details") {
		t.Errorf("expected error to note empty diagnostic; got: %v", err)
	}
}

func TestExecuteUpdateToolset_SkipsConfigWhenUnchanged(t *testing.T) {
	var metadataPatches []wire.UpdateToolsetRequest
	var configPatchCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body wire.UpdateToolsetRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		metadataPatches = append(metadataPatches, body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "id-1", Name: "demo"})
	})
	mux.HandleFunc("PATCH /api/v2/platform/toolsets/{id}/config", func(w http.ResponseWriter, _ *http.Request) {
		configPatchCalls.Add(1)
		writeV2Single(w, http.StatusOK, wire.ToolsetConfiguration{Values: map[string]any{}})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	op := toolsetOp{
		Name:          "demo",
		ID:            "id-1",
		Verb:          "update",
		ConfigChanged: false,
		Update:        wire.UpdateToolsetRequest{Description: wire.Patch[string]{Set: true, Value: "updated"}},
		Desired: &toolsetDesired{
			name:   "demo",
			create: wire.CreateToolsetRequest{Name: "demo", Description: "updated"},
			config: map[string]any{"api_base": "https://api.openai.com/v1"},
		},
	}

	if err := executeUpdateToolset(context.Background(), c, op); err != nil {
		t.Fatalf("executeUpdateToolset: %v", err)
	}
	if len(metadataPatches) != 1 || metadataPatches[0].Description.Value != "updated" {
		t.Errorf("metadata PATCHes = %+v; want one setting description to %q", metadataPatches, "updated")
	}
	if configPatchCalls.Load() != 0 {
		t.Errorf("config PATCH calls = %d; want 0 (unchanged)", configPatchCalls.Load())
	}
}
