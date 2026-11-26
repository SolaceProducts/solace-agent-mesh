// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const staleModelSecret = "sk-stale-secret"

func newModelsPlatform(t *testing.T, models ...string) string {
	t.Helper()
	srv := httptest.NewServer(modelsListHandler(t, models...))
	t.Cleanup(srv.Close)
	return srv.URL
}

func modelsListHandler(t *testing.T, models ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s: plan and dry run must not write", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data := "[]"
		if r.URL.Path == platform.APIV2Prefix+"/models" {
			data = "[" + strings.Join(models, ",") + "]"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + data + `,"meta":{"pagination":{"pageNumber":1,"pageSize":100}}}`))
	}
}

func writeModelRepo(t *testing.T, url string, models ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range models {
		body := "kind: model\nname: " + name + "\nspec:\n  provider: anthropic\n  modelName: claude\n" +
			"  apiBase: https://api.anthropic.com\n  authConfig:\n    apiKey: sk-desired-secret\n"
		if err := os.WriteFile(filepath.Join(root, "models", name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mf := filepath.Join(root, "manifest.yaml")
	body := "kind: manifest\nname: test\ntarget:\n  url: " + url + "\n  auth:\n    type: bearer_token\n    env_var: " +
		platformTokenEnv + "\nresources:\n  models: [" + strings.Join(models, ", ") + "]\n"
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return mf
}

type planReportDoc struct {
	Changes     []reconcile.PlanOp     `json:"changes"`
	Summary     reconcile.PlanSummary  `json:"summary"`
	Diagnostics []reconcile.Diagnostic `json:"diagnostics"`
}

func TestConfigPlanJSON_ExitCodeTracksPendingChanges(t *testing.T) {
	stale := `{"id":"0192a0c4-0000-7000-8000-000000000001","alias":"stale","provider":"openai","modelName":"gpt","authConfig":{"apiKey":"` + staleModelSecret + `"}}`
	for _, tc := range []struct {
		name       string
		platform   []string
		declared   []string
		args       []string
		wantExit   int
		wantAction string
	}{
		{name: "pending create exits 0 by default", declared: []string{"m1"}, args: []string{"plan"}, wantExit: 0, wantAction: reconcile.ActionCreate},
		{name: "create is pending", declared: []string{"m1"}, args: []string{"plan", "--detailed-exit-code"}, wantExit: 2, wantAction: reconcile.ActionCreate},
		{name: "nothing to do", args: []string{"plan", "--detailed-exit-code"}, wantExit: 0},
		{name: "delete without prune is not pending", platform: []string{stale}, args: []string{"plan", "--detailed-exit-code"}, wantExit: 0, wantAction: reconcile.ActionDelete},
		{name: "plan prune counts the delete", platform: []string{stale}, args: []string{"plan", "--prune", "--detailed-exit-code"}, wantExit: 2, wantAction: reconcile.ActionDelete},
		{name: "dry-run without prune ignores the delete", platform: []string{stale}, args: []string{"apply", "--dry-run", "--detailed-exit-code"}, wantExit: 0, wantAction: reconcile.ActionDelete},
		{name: "dry-run prune counts the delete", platform: []string{stale}, args: []string{"apply", "--dry-run", "--prune", "--detailed-exit-code"}, wantExit: 2, wantAction: reconcile.ActionDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rerouteHome(t)
			isolateGatewayRecording(t)
			t.Setenv(platformTokenEnv, "platform-token")
			mf := writeModelRepo(t, newModelsPlatform(t, tc.platform...), tc.declared...)

			cmd := newRootCmd()
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			args := append([]string{"config"}, tc.args...)
			cmd.SetArgs(append(args, "-m", mf, "--format", "json", "--no-interactive", "--no-dotenv"))
			err := cmd.Execute()

			if got := exitStatus(err); got != tc.wantExit {
				t.Fatalf("exit = %d (err %v), want %d; stderr: %s", got, err, tc.wantExit, stderr)
			}
			if strings.Contains(stdout.String(), "secret") {
				t.Errorf("JSON output carries a secret value:\n%s", stdout)
			}
			var doc planReportDoc
			if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
			if doc.Changes == nil || doc.Diagnostics == nil {
				t.Errorf("changes/diagnostics must be arrays, never null:\n%s", stdout)
			}
			if tc.wantAction == "" {
				if len(doc.Changes) != 0 {
					t.Errorf("changes = %+v, want none", doc.Changes)
				}
				return
			}
			if len(doc.Changes) != 1 || doc.Changes[0].Kind != "model" || doc.Changes[0].Action != tc.wantAction {
				t.Fatalf("changes = %+v, want one model %s", doc.Changes, tc.wantAction)
			}
			if got := reconcile.Summarize(doc.Changes); got != doc.Summary {
				t.Errorf("summary = %+v, want %+v", doc.Summary, got)
			}
		})
	}
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	return ExitCode(err)
}

func TestConfigPlanAndApply_ForbiddenSecretCompareFails(t *testing.T) {
	rerouteHome(t)
	isolateGatewayRecording(t)
	t.Setenv(platformTokenEnv, "read-only-token")
	stored := `{"id":"0192a0c4-0000-7000-8000-000000000002","alias":"m1","provider":"anthropic","modelName":"claude",` +
		`"apiBase":"https://api.anthropic.com","authConfig":{"apiKey":"<REDACTED>"}}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+platform.APIV2Prefix+"/models/0192a0c4-0000-7000-8000-000000000002/secretsDiff", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusForbidden, "Insufficient permissions")
	})
	mux.Handle("GET /", modelsListHandler(t, stored))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mf := writeModelRepo(t, srv.URL, "m1")

	for _, verb := range []string{"plan", "apply"} {
		t.Run(verb, func(t *testing.T) {
			cmd := newRootCmd()
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs([]string{"config", verb, "-m", mf, "--format", "json", "--no-interactive", "--no-dotenv"})
			err := cmd.Execute()

			if got := exitStatus(err); got != 1 {
				t.Fatalf("exit = %d (err %v), want 1; stdout: %s", got, err, stdout)
			}
			for _, want := range []string{`model "m1"`, "authConfig.apiKey", rbac.ScopeModelConfigUpdate} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}

			var doc planReportDoc
			if jerr := json.Unmarshal(stdout.Bytes(), &doc); jerr != nil {
				t.Fatalf("stdout is not a JSON document: %v\n%s", jerr, stdout)
			}
			if doc.Changes == nil || len(doc.Diagnostics) == 0 {
				t.Fatalf("want empty changes and an error diagnostic, got %+v", doc)
			}
			if last := doc.Diagnostics[len(doc.Diagnostics)-1]; last.Level != reconcile.DiagnosticError || last.Message != err.Error() {
				t.Errorf("last diagnostic = %+v, want error level carrying %q", last, err)
			}
		})
	}
}

func TestConfigApplyJSON_ReportsResults(t *testing.T) {
	rerouteHome(t)
	isolateGatewayRecording(t)
	t.Setenv(platformTokenEnv, "platform-token")
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+platform.APIV2Prefix+"/models", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusCreated, json.RawMessage(`{"id":"0192a0c4-0000-7000-8000-000000000003","alias":"m1","provider":"anthropic","modelName":"claude"}`))
	})
	mux.Handle("GET /", modelsListHandler(t))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mf := writeModelRepo(t, srv.URL, "m1")

	cmd := newRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"config", "apply", "-m", mf, "--format", "json", "--no-interactive", "--no-dotenv"})
	err := cmd.Execute()

	if got := exitStatus(err); got != 0 {
		t.Fatalf("exit = %d (err %v), want 0; stderr: %s", got, err, stderr)
	}
	var doc struct {
		Changes []reconcile.PlanOp        `json:"changes"`
		Results []reconcile.ApplyOpResult `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	want := []reconcile.ApplyOpResult{{Kind: "model", Name: "m1", Status: reconcile.StatusCreated}}
	if !reflect.DeepEqual(doc.Results, want) {
		t.Errorf("results = %+v, want %+v", doc.Results, want)
	}
	if len(doc.Changes) != 1 || doc.Changes[0].Action != reconcile.ActionCreate {
		t.Errorf("changes = %+v, want the planned model create", doc.Changes)
	}
}

func TestConfigApply_DetailedExitCodeNeedsDryRun(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"config", "apply", "--detailed-exit-code", "--no-interactive", "--no-dotenv"})
	err := cmd.Execute()
	if exitStatus(err) != 1 || !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("err = %v (exit %d), want exit 1 naming --dry-run", err, exitStatus(err))
	}
}
