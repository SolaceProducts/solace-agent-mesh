// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func TestResolveSkillToolRefs_EmptyEnvVarIsUnresolved(t *testing.T) {
	t.Setenv("SKILL_TEST_BLANK", "")
	entry := map[string]any{
		"environment_variables": map[string]any{"GITHUB_TOKEN": "${SKILL_TEST_BLANK}"},
	}
	missing, _ := resolveSkillToolRefs(entry, nil)
	if len(missing) != 1 || missing[0] != "SKILL_TEST_BLANK" {
		t.Fatalf("missing = %v, want the empty variable reported as unresolved", missing)
	}
}

func TestResolveSkillToolRefs_EmptyEnvVarFallsBackToDefault(t *testing.T) {
	t.Setenv("SKILL_TEST_BLANK", "")
	entry := map[string]any{"tool_config": map[string]any{"model": "${SKILL_TEST_BLANK, gpt-4o}"}}
	missing, _ := resolveSkillToolRefs(entry, nil)
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none: the default form opts in to absent", missing)
	}
	if got := entry["tool_config"].(map[string]any)["model"]; got != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", got)
	}
}

func TestSkillToolRegistrar_RefusesStdioMCPFromSkill(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "stdio-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	newRegistrar := func(allow bool, buf *bytes.Buffer) *skillToolRegistrar {
		return &skillToolRegistrar{
			agent:         ag,
			executor:      remote.NewExecutor(svc.Broker(), "", ag.Name()),
			tools:         ag.Tools(),
			allowStdioMCP: allow,
			logger:        captureLogger(buf),
		}
	}
	stdioEntry := func() map[string]any {
		return map[string]any{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type":    "stdio",
				"command": "curl",
				"args":    []any{"https://author.example/collect"},
			},
		}
	}

	var refused bytes.Buffer
	got, retry := newRegistrar(false, &refused).RegisterSkillTools("demo", []map[string]any{
		stdioEntry(),
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	})
	if len(got) != 1 || got[0] != "get_current_time" {
		t.Errorf("got %v, want only [get_current_time]: the stdio entry must be refused "+
			"without taking its sibling down with it", got)
	}
	if len(retry) != 0 {
		t.Errorf("retry = %v, want a refused entry never retried", retry)
	}
	if out := refused.String(); !strings.Contains(out, "stdio mcp transport is not permitted") {
		t.Errorf("want the refusal logged with its reason, got:\n%s", out)
	}
	if out := refused.String(); !strings.Contains(out, "SAM_FEATURE_SKILL_MCP_STDIO") {
		t.Errorf("want the escape hatch named so an operator can act, got:\n%s", out)
	}

	var allowed bytes.Buffer
	newRegistrar(true, &allowed).RegisterSkillTools("demo", []map[string]any{{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "stdio"},
	}})
	out := allowed.String()
	if strings.Contains(out, "stdio mcp transport is not permitted") {
		t.Errorf("the flag must re-enable the path, got:\n%s", out)
	}
	if !strings.Contains(out, "skipping skill tool entry") {
		t.Errorf("want the entry to fail at registration instead, got:\n%s", out)
	}
}

func TestIsStdioMCPEntry(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		entry map[string]any
		want  bool
	}{
		"explicit stdio": {map[string]any{"tool_type": "mcp",
			"connection_params": map[string]any{"type": "stdio"}}, true},
		"absent type is classified as stdio": {map[string]any{"tool_type": "mcp",
			"connection_params": map[string]any{"command": "x"}}, true},
		"empty type is classified as stdio": {map[string]any{"tool_type": "mcp",
			"connection_params": map[string]any{"type": ""}}, true},
		"case does not evade the gate": {map[string]any{"tool_type": "mcp",
			"connection_params": map[string]any{"type": "STDIO"}}, true},
		"http is not stdio": {map[string]any{"tool_type": "mcp",
			"connection_params": map[string]any{"type": "streamable_http", "url": "https://x"}}, false},
		"sse is not stdio": {map[string]any{"tool_type": "mcp",
			"connection_params": map[string]any{"type": "sse", "url": "https://x"}}, false},
		"not an mcp entry":              {map[string]any{"tool_type": "builtin", "tool_name": "x"}, false},
		"mcp with no connection_params": {map[string]any{"tool_type": "mcp"}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isStdioMCPEntry(c.entry); got != c.want {
				t.Errorf("isStdioMCPEntry = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRegisterSkillTools_LogsResolvedVaultPaths(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "vault-audit-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := &skillToolRegistrar{
		agent:    ag,
		executor: remote.NewExecutor(svc.Broker(), "", ag.Name()),
		tools:    ag.Tools(),
		vault: &stubVaultFetcher{secrets: map[string]map[string]any{
			"kv/data/prod/api": kv2(map[string]any{"key": "sk-prod-secret"}),
		}},
		logger: captureLogger(&buf),
	}
	r.RegisterSkillTools("demo", []map[string]any{{
		"tool_type":   "builtin",
		"tool_name":   "get_current_time",
		"tool_config": map[string]any{"k": "vault://kv/data/${SKILL_TEST_NO_ENV, prod}/api#key"},
	}})

	out := buf.String()
	if !strings.Contains(out, "kv/data/prod/api") {
		t.Errorf("want the resolved vault path recorded, got:\n%s", out)
	}
	if strings.Contains(out, "sk-prod-secret") {
		t.Errorf("the value must never be logged, only the path:\n%s", out)
	}
}

func TestResolveSkillToolRefs_InlineDocumentsAreNotResolved(t *testing.T) {
	t.Setenv("SKILL_TEST_LEAK", "sk-should-not-appear")
	f := &stubVaultFetcher{secrets: map[string]map[string]any{
		"kv/db": kv2(map[string]any{"password": "pw-should-not-appear"}),
	}}
	entry := map[string]any{
		"tool_type": "mcp",
		"manifest": []any{map[string]any{
			"name":        "helper",
			"description": "notes ${SKILL_TEST_LEAK} vault://kv/db#password",
		}},
		"specification": "openapi: 3.0.0\nsummary: calls ${SKILL_TEST_LEAK}\n",
	}
	if _, vaultErrs := resolveSkillToolRefs(entry, f); len(vaultErrs) != 0 {
		t.Fatalf("vaultErrs = %v, want none", vaultErrs)
	}
	tool0 := entry["manifest"].([]any)[0].(map[string]any)
	if got := tool0["description"]; got != "notes ${SKILL_TEST_LEAK} vault://kv/db#password" {
		t.Errorf("manifest description = %q, want it left literal — it reaches the model", got)
	}
	if got := entry["specification"]; !strings.Contains(got.(string), "${SKILL_TEST_LEAK}") {
		t.Errorf("specification = %q, want it left literal", got)
	}
	if len(f.calls) != 0 {
		t.Errorf("fetched %v; an inline document must not reach Vault", f.calls)
	}
}

func TestResolveSkillToolRefs_AuthNameStillResolves(t *testing.T) {
	t.Setenv("SKILL_TEST_HEADER", "X-Api-Key")
	entry := map[string]any{
		"auth": map[string]any{"type": "apikey", "name": "${SKILL_TEST_HEADER}", "in": "header"},
	}
	missing, _ := resolveSkillToolRefs(entry, nil)
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if got := entry["auth"].(map[string]any)["name"]; got != "X-Api-Key" {
		t.Errorf("auth.name = %q, want it resolved — it is connection config, not prose", got)
	}
}

func TestRegisterSkillTools_ReportsRefsInInlineDocuments(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "inline-doc-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := &skillToolRegistrar{
		agent:    ag,
		executor: remote.NewExecutor(svc.Broker(), "", ag.Name()),
		tools:    ag.Tools(),
		logger:   captureLogger(&buf),
	}
	entry := map[string]any{
		"tool_type":     "builtin",
		"tool_name":     "get_current_time",
		"specification": "servers:\n  - url: https://${SKILL_TEST_DEPLOY}.api.example.com\n",
	}
	r.RegisterSkillTools("demo", []map[string]any{entry})

	out := buf.String()
	if !strings.Contains(out, "reference in an inline document is not resolved") {
		t.Errorf("want the unresolved inline reference reported, got:\n%s", out)
	}
	if !strings.Contains(out, "specification") || !strings.Contains(out, "${SKILL_TEST_DEPLOY}") {
		t.Errorf("want the key AND the offending reference named, got:\n%s", out)
	}
	if got := entry["specification"]; !strings.Contains(got.(string), "${SKILL_TEST_DEPLOY}") {
		t.Errorf("specification = %q, want it left literal", got)
	}
}

func TestRegisterSkillTools_QuietWhenInlineDocumentIsClean(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "inline-clean-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := &skillToolRegistrar{
		agent:    ag,
		executor: remote.NewExecutor(svc.Broker(), "", ag.Name()),
		tools:    ag.Tools(),
		logger:   captureLogger(&buf),
	}
	r.RegisterSkillTools("demo", []map[string]any{{
		"tool_type":     "builtin",
		"tool_name":     "get_current_time",
		"specification": "servers:\n  - url: https://api.example.com\n",
	}})
	if strings.Contains(buf.String(), "inline document") {
		t.Errorf("a clean document must not warn:\n%s", buf.String())
	}
}

func TestRegisterSkillTools_ReportsVaultRefInInlineManifest(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "inline-manifest-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	f := &stubVaultFetcher{}
	r := &skillToolRegistrar{
		agent:    ag,
		executor: remote.NewExecutor(svc.Broker(), "", ag.Name()),
		tools:    ag.Tools(),
		vault:    f,
		logger:   captureLogger(&buf),
	}
	r.RegisterSkillTools("demo", []map[string]any{{
		"tool_type": "builtin",
		"tool_name": "get_current_time",
		"manifest": []any{map[string]any{
			"name":        "helper",
			"description": "reads vault://kv/data/prod/db#password",
		}},
	}})

	out := buf.String()
	if !strings.Contains(out, "reference in an inline document is not resolved") {
		t.Errorf("want the nested reference reported, got:\n%s", out)
	}
	if !strings.Contains(out, "vault://kv/data/prod/db#password") {
		t.Errorf("want the offending reference named, got:\n%s", out)
	}
	if len(f.calls) != 0 {
		t.Errorf("fetched %v; detection must never resolve", f.calls)
	}
}
