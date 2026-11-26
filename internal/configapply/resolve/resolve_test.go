// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
)

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, contents := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

func TestResolveModels_BasicAndVarSubstitution(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/planning.yaml": `
kind: model
name: planning
description: "Reasoning model"
spec:
  provider: anthropic
  modelName: ${MODEL_NAME}
  authConfig:
    apiKey: ${API_KEY, fallback}
`,
	})
	m := &manifest.Manifest{
		Variables: map[string]string{
			"MODEL_NAME": "claude-opus-4-7",
			"API_KEY":    "sk-test",
		},
		Resources: map[string][]manifest.ResourceEntry{
			"models": {{Name: "planning"}},
		},
	}
	got, err := ResolveModels(root, m)
	if err != nil {
		t.Fatalf("ResolveModels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources; want 1", len(got))
	}
	r := got[0]
	if r.Name != "planning" || r.Kind != "model" {
		t.Errorf("identity = %s/%s", r.Kind, r.Name)
	}
	if r.Spec["modelName"] != "claude-opus-4-7" {
		t.Errorf("modelName not substituted: %v", r.Spec["modelName"])
	}
	auth, _ := r.Spec["authConfig"].(map[string]any)
	if auth["apiKey"] != "sk-test" {
		t.Errorf("apiKey = %v", auth["apiKey"])
	}
}

func TestResolveModels_DefaultUsedWhenVarMissing(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/m.yaml": `
kind: model
name: m
spec:
  apiBase: ${MISSING, https://default}
  provider: x
  modelName: y
  authConfig: {}
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "m"}}}}
	got, err := ResolveModels(root, m)
	if err != nil {
		t.Fatalf("ResolveModels: %v", err)
	}
	if got[0].Spec["apiBase"] != "https://default" {
		t.Errorf("default not used: %v", got[0].Spec["apiBase"])
	}
}

func TestResolveModels_MissingFile(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/other.yaml": "kind: model\nname: other\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "missing"}}}}
	if _, err := ResolveModels(root, m); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing-file error, got %v", err)
	}
}

func TestResolveModels_KindMismatch(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/wrong.yaml": "kind: agent\nname: wrong\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "wrong"}}}}
	if _, err := ResolveModels(root, m); err == nil {
		t.Fatal("expected error for kind mismatch")
	}
}

func TestResolveModels_DuplicateName(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/a.yaml":     "kind: model\nname: dup\nspec: {}\n",
		"models/sub/b.yaml": "kind: model\nname: dup\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "dup"}}}}
	if _, err := ResolveModels(root, m); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}

func TestResolveModels_NamelessKindFileNamedWhenEntryNotFound(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/example.yaml": "kind: model\nspec:\n  alias: example_model\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "example_model"}}}}
	_, err := ResolveModels(root, m)
	if err == nil {
		t.Fatal("expected not-found error for an entry whose file has no top-level name")
	}
	for _, want := range []string{`"example_model" not found`, "no top-level name", filepath.Join("models", "example.yaml")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveModels_UnreferencedNamelessKindFileSkipped(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/planning.yaml": "kind: model\nname: planning\nspec: {}\n",
		"models/draft.yaml":    "kind: model\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "planning"}}}}
	got, err := ResolveModels(root, m)
	if err != nil {
		t.Fatalf("ResolveModels: %v", err)
	}
	if len(got) != 1 || got[0].Name != "planning" {
		t.Fatalf("got %+v; want only planning", got)
	}
}

func TestResolveModels_FileWithoutKindSkipped(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/planning.yaml":      "kind: model\nname: planning\nspec: {}\n",
		"models/shared/params.yaml": "temperature: 0.2\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "planning"}}}}
	got, err := ResolveModels(root, m)
	if err != nil {
		t.Fatalf("ResolveModels: %v", err)
	}
	if len(got) != 1 || got[0].Name != "planning" {
		t.Fatalf("got %+v; want only planning", got)
	}
}

func TestResolveModels_NameMismatchBetweenManifestAndFile(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/planning.yaml": "kind: model\nname: planning\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "general"}}}}
	if _, err := ResolveModels(root, m); err == nil {
		t.Fatal("expected error: file declares 'planning' but manifest asks for 'general'")
	}
}

func TestResolveModels_ImportRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/planning.yaml": "kind: model\nname: planning\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "planning", Source: "remote"}}}}
	_, err := ResolveModels(root, m)
	if err == nil {
		t.Fatal("expected import-not-supported error")
	}
	if !strings.Contains(err.Error(), "resolve.New") {
		t.Errorf("error should direct callers to resolve.New, got %v", err)
	}
}

func TestResolveConnectors_BasicAndKindMatch(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"connectors/ops-slack.yaml": `
kind: connector
name: ops-slack
description: "Slack workspace connector for ops alerts."
spec:
  type: messaging
  subtype: slack
  values:
    slack_bot_token: ${SLACK_TOKEN}
    slack_channel: "#ops-alerts"
`,
	})
	m := &manifest.Manifest{
		Variables: map[string]string{
			"SLACK_TOKEN": "xoxb-test",
		},
		Resources: map[string][]manifest.ResourceEntry{
			"connectors": {{Name: "ops-slack"}},
		},
	}
	got, err := ResolveConnectors(root, m)
	if err != nil {
		t.Fatalf("ResolveConnectors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources; want 1", len(got))
	}
	r := got[0]
	if r.Kind != "connector" || r.Name != "ops-slack" {
		t.Errorf("identity = %s/%s", r.Kind, r.Name)
	}
	if r.Spec["type"] != "messaging" || r.Spec["subtype"] != "slack" {
		t.Errorf("type/subtype not parsed: %+v", r.Spec)
	}
	values, ok := r.Spec["values"].(map[string]any)
	if !ok {
		t.Fatalf("values missing or wrong type: %+v", r.Spec["values"])
	}
	if values["slack_bot_token"] != "xoxb-test" {
		t.Errorf("slack_bot_token not substituted: %v", values["slack_bot_token"])
	}
}

func TestResolveConnectors_NoEntries(t *testing.T) {
	root := writeRepo(t, map[string]string{})
	m := &manifest.Manifest{}
	got, err := ResolveConnectors(root, m)
	if err != nil {
		t.Fatalf("ResolveConnectors: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result, got %+v", got)
	}
}

func TestResolveConnectors_KindMismatch(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"connectors/wrong.yaml": "kind: model\nname: wrong\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"connectors": {{Name: "wrong"}}}}
	if _, err := ResolveConnectors(root, m); err == nil {
		t.Fatal("expected kind-mismatch error")
	}
}

func TestResolveDatasets_Basic(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"datasets/acme.yaml": `kind: dataset
name: acme
description: ACME corpus
spec: {}
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"datasets": {{Name: "acme"}}}}
	got, err := ResolveDatasets(root, m)
	if err != nil {
		t.Fatalf("ResolveDatasets: %v", err)
	}
	if len(got) != 1 || got[0].Name != "acme" || got[0].Description != "ACME corpus" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveEvaluators_Basic(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"evaluators/judge.yaml": `kind: evaluator
name: judge
description: A judge
spec:
  type: llm_judge
  model: openai/gpt-4o-mini
  promptTemplate: "Score: {{ Response }}"
  choiceScores:
    yes: 1
    no: 0
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"evaluators": {{Name: "judge"}}}}
	got, err := ResolveEvaluators(root, m)
	if err != nil {
		t.Fatalf("ResolveEvaluators: %v", err)
	}
	if len(got) != 1 || got[0].Name != "judge" {
		t.Errorf("got %+v", got)
	}
	if got[0].Spec["type"] != "llm_judge" {
		t.Errorf("type not parsed: %v", got[0].Spec["type"])
	}
}

func TestResolveExperiments_Basic(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"experiments/nightly.yaml": `kind: experiment
name: nightly
description: Nightly run
spec:
  datasetId: acme
  evaluatorIds: [judge]
  primaryEvaluatorId: judge
  targetAgent: Orchestrator
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"experiments": {{Name: "nightly"}}}}
	got, err := ResolveExperiments(root, m)
	if err != nil {
		t.Fatalf("ResolveExperiments: %v", err)
	}
	if len(got) != 1 || got[0].Name != "nightly" {
		t.Errorf("got %+v", got)
	}
	if got[0].Spec["datasetId"] != "acme" {
		t.Errorf("datasetId not parsed: %v", got[0].Spec["datasetId"])
	}
}

func TestResolveModels_StrictEnvAggregatesAcrossFiles(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/a.yaml": `
kind: model
name: a
spec:
  apiBase: ${MISSING_A}
  authConfig:
    apiKey: ${ALSO_MISSING}
  provider: x
  modelName: y
`,
		"models/b.yaml": `
kind: model
name: b
spec:
  authConfig:
    apiKey: ${MISSING_B}
  modelName: ${OPTIONAL, default}
  provider: x
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"models": {{Name: "a"}, {Name: "b"}},
	}}
	_, err := ResolveModels(root, m)
	if err == nil {
		t.Fatal("expected UnresolvedVarsError, got nil")
	}
	uve, ok := err.(*UnresolvedVarsError)
	if !ok {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	if len(uve.Files) != 2 {
		t.Fatalf("Files keys = %v, want 2 file paths", uve.Files)
	}
	var aFile, bFile []string
	for path, vars := range uve.Files {
		if strings.HasSuffix(path, "a.yaml") {
			aFile = vars
		}
		if strings.HasSuffix(path, "b.yaml") {
			bFile = vars
		}
	}
	if !slices.Equal(aFile, []string{"ALSO_MISSING", "MISSING_A"}) {
		t.Errorf("a.yaml missing = %v, want [ALSO_MISSING MISSING_A]", aFile)
	}
	if !slices.Equal(bFile, []string{"MISSING_B"}) {
		t.Errorf("b.yaml missing = %v, want [MISSING_B] (OPTIONAL has default; must not appear)", bFile)
	}
	msg := err.Error()
	for _, v := range []string{"ALSO_MISSING", "MISSING_A", "MISSING_B"} {
		if !strings.Contains(msg, v) {
			t.Errorf("Error() text missing %q: %s", v, msg)
		}
	}
	if strings.Contains(msg, "OPTIONAL") {
		t.Errorf("Error() text wrongly mentions OPTIONAL (has default): %s", msg)
	}
}

func TestResolveToolsets_StrictEnvSurfacesMissing(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/foo.yaml": `
kind: toolset
name: foo
description: needs creds
spec:
  apiKey: ${TOOLSET_CRED}
`,
		"toolsets/foo/src/main.py": "# placeholder\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "foo"}},
	}}
	_, err := ResolveToolsets(root, m)
	if err == nil {
		t.Fatal("expected UnresolvedVarsError, got nil")
	}
	uve, ok := err.(*UnresolvedVarsError)
	if !ok {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	var vars []string
	for _, vs := range uve.Files {
		vars = append(vars, vs...)
	}
	if !slices.Equal(vars, []string{"TOOLSET_CRED"}) {
		t.Errorf("missing = %v, want [TOOLSET_CRED]", vars)
	}
}

func TestResolver_Models_StrictEnvAggregatesAcrossFiles(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/a.yaml": `
kind: model
name: a
spec:
  apiBase: ${MISSING_A}
  provider: x
  modelName: y
`,
		"models/b.yaml": `
kind: model
name: b
spec:
  authConfig:
    apiKey: ${MISSING_B}
  modelName: ${OPTIONAL, default}
  provider: x
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"models": {{Name: "a"}, {Name: "b"}},
	}}
	r := New(root, m, Options{})
	_, err := r.Models(context.Background())
	if err == nil {
		t.Fatal("expected UnresolvedVarsError, got nil")
	}
	uve, ok := err.(*UnresolvedVarsError)
	if !ok {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	if len(uve.Files) != 2 {
		t.Fatalf("Files keys = %v, want 2 file paths", uve.Files)
	}
	msg := err.Error()
	for _, v := range []string{"MISSING_A", "MISSING_B"} {
		if !strings.Contains(msg, v) {
			t.Errorf("Error() text missing %q: %s", v, msg)
		}
	}
	if strings.Contains(msg, "OPTIONAL") {
		t.Errorf("Error() wrongly mentions OPTIONAL (has default): %s", msg)
	}
}

func TestUnresolvedVarsError_Render(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string][]string
		wantHead  string
		wantVars  []string
		wantPaths []string
	}{
		{
			name:      "singular noun for 1 var",
			files:     map[string][]string{"/a.yaml": {"X"}},
			wantHead:  "config error: 1 unresolved environment variable reference:",
			wantVars:  []string{"X"},
			wantPaths: []string{"/a.yaml"},
		},
		{
			name: "plural noun for multiple vars",
			files: map[string][]string{
				"/b.yaml": {"FOO", "BAR"},
				"/a.yaml": {"BAZ"},
			},
			wantHead:  "config error: 3 unresolved environment variable references:",
			wantVars:  []string{"FOO", "BAR", "BAZ"},
			wantPaths: []string{"/a.yaml", "/b.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &UnresolvedVarsError{Files: tt.files}
			got := e.Error()
			if !strings.HasPrefix(got, tt.wantHead) {
				t.Errorf("header = %q, want prefix %q", got, tt.wantHead)
			}
			for _, v := range tt.wantVars {
				if !strings.Contains(got, v) {
					t.Errorf("missing var %q in body:\n%s", v, got)
				}
			}
			for _, p := range tt.wantPaths {
				if !strings.Contains(got, p) {
					t.Errorf("missing path %q in body:\n%s", p, got)
				}
			}
			if !strings.Contains(got, "set the variables, or use ${VAR, default}") {
				t.Errorf("missing opt-in hint:\n%s", got)
			}
		})
	}

	e := &UnresolvedVarsError{Files: map[string][]string{
		"/z.yaml": {"Z1"},
		"/a.yaml": {"A1", "A2"},
	}}
	got := e.Error()
	if idxA, idxZ := strings.Index(got, "/a.yaml"), strings.Index(got, "/z.yaml"); idxA >= idxZ {
		t.Errorf("expected /a.yaml before /z.yaml in body:\n%s", got)
	}

	if msg := (&UnresolvedVarsError{}).Error(); msg != "no unresolved variables" {
		t.Errorf("empty Error() = %q", msg)
	}
	var nilE *UnresolvedVarsError
	if msg := nilE.Error(); msg != "no unresolved variables" {
		t.Errorf("nil Error() = %q", msg)
	}
}

func TestResolveModels_VaultURIsResolved(t *testing.T) {
	stub := &stubVaultFetcher{
		secrets: map[string]map[string]any{
			"kv/data/myapp/llm": {
				"data": map[string]any{
					"data": map[string]any{
						"api_key":  "sk-from-vault",
						"endpoint": "https://lite-llm.example.com/v1",
					},
				},
			},
		},
	}
	root := writeRepo(t, map[string]string{
		"models/planning.yaml": `
kind: model
name: planning
spec:
  provider: ${PROVIDER}
  modelName: claude-opus-4-7
  apiBase: vault://kv/data/myapp/llm#endpoint
  authConfig:
    type: apikey
    api_key: vault://kv/data/myapp/llm#api_key
`,
	})
	m := &manifest.Manifest{
		Variables: map[string]string{"PROVIDER": "anthropic"},
		Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "planning"}}},
	}
	r := New(root, m, Options{VaultFetcher: stub})
	got, err := r.resolveKindLocalOnly("models", "model")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Spec["apiBase"] != "https://lite-llm.example.com/v1" {
		t.Errorf("apiBase not substituted: %v", got[0].Spec["apiBase"])
	}
	auth, _ := got[0].Spec["authConfig"].(map[string]any)
	if auth["api_key"] != "sk-from-vault" {
		t.Errorf("api_key not substituted: %v", auth["api_key"])
	}
	if stub.calls["kv/data/myapp/llm"] != 1 {
		t.Errorf("expected 1 fetch for the shared path, got %d", stub.calls["kv/data/myapp/llm"])
	}
}

func TestResolveModels_EmptyVaultFieldFailsPlan(t *testing.T) {
	stub := &stubVaultFetcher{
		secrets: map[string]map[string]any{
			"kv/data/llm": {"data": map[string]any{"data": map[string]any{"api_key": ""}}},
		},
	}
	root := writeRepo(t, map[string]string{
		"models/planning.yaml": `
kind: model
name: planning
spec:
  provider: anthropic
  modelName: claude-opus-4-7
  authConfig:
    type: apikey
    api_key: vault://kv/data/llm#api_key
`,
	})
	m := &manifest.Manifest{
		Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "planning"}}},
	}
	r := New(root, m, Options{VaultFetcher: stub})
	_, err := r.resolveKindLocalOnly("models", "model")
	if err == nil {
		t.Fatal("resolve succeeded; an empty vault field must abort the plan")
	}
	var unresolved *UnresolvedVarsError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Errorf("message = %q, want it to say the field is empty", err.Error())
	}
	if !strings.Contains(err.Error(), "vault://kv/data/llm#api_key") {
		t.Errorf("message = %q, want it to name the reference", err.Error())
	}
}

func TestResolveModels_VaultErrorsAggregateWithEnvErrors(t *testing.T) {
	stub := &stubVaultFetcher{
		secrets: map[string]map[string]any{},
		failOn: map[string]error{
			"kv/data/x": errVaultStub("HTTP 403 permission denied"),
		},
	}
	root := writeRepo(t, map[string]string{
		"models/a.yaml": `
kind: model
name: a
spec:
  provider: anthropic
  modelName: ${MISSING_MODEL}
  apiBase: vault://kv/data/x#endpoint
  authConfig:
    api_key: vault://kv/data/x#api_key
`,
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "a"}}}}
	r := New(root, m, Options{VaultFetcher: stub})
	_, err := r.resolveKindLocalOnly("models", "model")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	uve, ok := err.(*UnresolvedVarsError)
	if !ok {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	if len(uve.Files) != 1 {
		t.Errorf("Files = %+v, want 1 file with MISSING_MODEL", uve.Files)
	}
	if len(uve.VaultErrors) != 1 {
		t.Errorf("VaultErrors = %+v, want 1 file with two URIs", uve.VaultErrors)
	}
	var vaultURIs []string
	for _, vs := range uve.VaultErrors {
		for _, v := range vs {
			vaultURIs = append(vaultURIs, v.Ref.URI)
		}
	}
	wantURIs := []string{"vault://kv/data/x#api_key", "vault://kv/data/x#endpoint"}
	if !slices.Equal(vaultURIs, wantURIs) {
		t.Errorf("vault URIs = %v, want %v", vaultURIs, wantURIs)
	}

	msg := err.Error()
	for _, expect := range []string{
		"MISSING_MODEL",
		"vault://kv/data/x#api_key",
		"vault://kv/data/x#endpoint",
		"HTTP 403 permission denied",
	} {
		if !strings.Contains(msg, expect) {
			t.Errorf("Error() missing %q:\n%s", expect, msg)
		}
	}
}

func TestResolveSkills_VaultURIInFrontmatter(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	root := writeRepo(t, map[string]string{
		"skills/web/SKILL.md": `---
name: web
description: ${DESC}
tools:
  - tool_type: builtin
    tool_name: web_search_google
    tool_config:
      google_search_api_key: vault://kv/data/google#api_key
---

skill body
`,
	})
	m := &manifest.Manifest{
		Variables: map[string]string{"DESC": "fetch URLs"},
		Resources: map[string][]manifest.ResourceEntry{"skills": {{Name: "web"}}},
	}
	_, err := ResolveSkills(root, m)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	uve, ok := err.(*UnresolvedVarsError)
	if !ok {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	if len(uve.VaultErrors) != 1 {
		t.Errorf("VaultErrors = %+v, want one SKILL.md entry", uve.VaultErrors)
	}
	if !strings.Contains(err.Error(), "vault://kv/data/google#api_key") {
		t.Errorf("Error() missing URI:\n%s", err)
	}
}

type stubVaultFetcher struct {
	secrets map[string]map[string]any
	calls   map[string]int
	failOn  map[string]error
}

func (s *stubVaultFetcher) Fetch(path string) (map[string]any, error) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[path]++
	if err, ok := s.failOn[path]; ok {
		return nil, err
	}
	v, ok := s.secrets[path]
	if !ok {
		return nil, errVaultStub("404 not found")
	}
	return v, nil
}

type stubErr string

func (s stubErr) Error() string { return string(s) }

func errVaultStub(s string) error { return stubErr(s) }

func TestResolveSkills_FullBodySubstitution(t *testing.T) {
	stub := &stubVaultFetcher{
		secrets: map[string]map[string]any{
			"kv/data/myapp/gemini": {
				"data": map[string]any{
					"data": map[string]any{"api_key": "AIza-real-gemini"},
				},
			},
		},
	}
	root := writeRepo(t, map[string]string{
		"skills/image-tools/SKILL.md": `---
name: image-tools
description: ${DESC}
tools:
  - tool_type: builtin
    tool_name: generate_image_with_gemini
    tool_config:
      gemini_api_key: vault://kv/data/myapp/gemini#api_key
      model: ${IMAGE_MODEL}
---

Body text references ${IMAGE_MODEL} and vault://kv/data/myapp/gemini#api_key as well.
`,
	})
	m := &manifest.Manifest{
		Variables: map[string]string{
			"DESC":        "image work",
			"IMAGE_MODEL": "gemini-3.1-flash-image-preview",
		},
		Resources: map[string][]manifest.ResourceEntry{"skills": {{Name: "image-tools"}}},
	}
	r := New(root, m, Options{VaultFetcher: stub})
	got, err := r.Skills(context.Background())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	resolved := string(got[0].ResolvedSkillMD)
	if resolved == "" {
		t.Fatal("ResolvedSkillMD is empty — reconciler would bundle the raw file and ship literal placeholders to the platform")
	}
	for _, expect := range []string{
		"gemini_api_key: AIza-real-gemini",
		"model: gemini-3.1-flash-image-preview",
		"description: image work",
		"Body text references gemini-3.1-flash-image-preview and AIza-real-gemini as well.",
	} {
		if !strings.Contains(resolved, expect) {
			t.Errorf("resolved SKILL.md missing %q:\n---\n%s\n---", expect, resolved)
		}
	}
	for _, forbidden := range []string{
		"${IMAGE_MODEL}",
		"${DESC}",
		"vault://kv/data/myapp/gemini",
	} {
		if strings.Contains(resolved, forbidden) {
			t.Errorf("resolved SKILL.md still contains raw %q:\n---\n%s\n---", forbidden, resolved)
		}
	}
}

func TestResolveModels_UnknownTopLevelKeyRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/m.yaml": "kind: model\nname: m\nslug: helpdesk\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "m"}}}}
	_, err := ResolveModels(root, m)
	if err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
	for _, want := range []string{filepath.Join("models", "m.yaml"), `"slug"`, "spec.slug", "kind, name, description, spec"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveModels_UnreferencedFileWithUnknownKeyIgnored(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"models/ok.yaml":    "kind: model\nname: ok\nspec: {}\n",
		"models/draft.yaml": "kind: model\nname: draft\nslug: x\nspec: {}\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": {{Name: "ok"}}}}
	if _, err := ResolveModels(root, m); err != nil {
		t.Fatalf("unreferenced draft file broke resolve: %v", err)
	}
}
