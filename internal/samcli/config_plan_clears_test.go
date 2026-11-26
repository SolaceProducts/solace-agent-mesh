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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const storedTriageAgent = `{"id":"0192a0c4-0000-7000-8000-000000000002","name":"triage",` +
	`"description":"Triages incoming support tickets.","systemPrompt":"You triage tickets.",` +
	`"toolsets":["builtin_web_request_tools"],` +
	`"toolsetConfigs":[{"toolsetName":"builtin_web_request_tools","configValues":{"hil":{"web_request":{"require_approval":true}}}}]}`

func newV2AgentPlatform(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s: plan must not write", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, platform.APIV2Prefix+"/") {
			http.NotFound(w, r)
			return
		}
		data := "[]"
		if r.URL.Path == platform.APIV2Prefix+"/agents" {
			data = "[" + storedTriageAgent + "]"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + data + `,"meta":{"pagination":{"pageNumber":1,"pageSize":100}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestConfigPlanJSON_ReportsSettingsAV2UpdateClears(t *testing.T) {
	rerouteHome(t)
	isolateGatewayRecording(t)
	t.Setenv(platformTokenEnv, "platform-token")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := "kind: agent\nname: triage\ndescription: Triages incoming support tickets.\n" +
		"spec:\n  systemPrompt: You triage tickets.\n  toolsets: [web_tools]\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "triage.yaml"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := filepath.Join(root, "manifest.yaml")
	manifestBody := "kind: manifest\nname: test\ntarget:\n  url: " + newV2AgentPlatform(t) +
		"\n  auth:\n    type: bearer_token\n    env_var: " + platformTokenEnv + "\nresources:\n  agents: [triage]\n"
	if err := os.WriteFile(mf, []byte(manifestBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"config", "plan", "-m", mf, "--format", "json", "--no-interactive", "--no-dotenv"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan: %v; stderr: %s", err, stderr)
	}

	var doc planReportDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if len(doc.Changes) != 1 || doc.Changes[0].Name != "triage" || doc.Changes[0].Action != reconcile.ActionUpdate {
		t.Fatalf("changes = %+v, want an update of agent triage", doc.Changes)
	}
	want := reconcile.Warning(`agent "triage": apply clears the stored toolsetConfigs, which the YAML no longer sets`)
	found := false
	for _, d := range doc.Diagnostics {
		found = found || d == want
	}
	if !found {
		t.Errorf("diagnostics = %+v, want %+v", doc.Diagnostics, want)
	}
}
