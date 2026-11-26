// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedAgentWithConnector(t *testing.T, db *store.DB, agentID, agentName, connectorID string) {
	t.Helper()
	repo := store.NewAgentRepo(db)
	desc := "deep e2e agent"
	prompt := "deep e2e prompt — long enough to clear validators which require a non-trivial system prompt"
	a := &store.Agent{
		ID:           agentID,
		Name:         agentName,
		Description:  &desc,
		SystemPrompt: &prompt,
		Type:         "standard",
		CreatedBy:    "tester",
		UpdatedBy:    "tester",
	}
	if err := repo.Create(t.Context(), a, nil, nil, nil, []string{connectorID}, nil); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func TestConnectorDeepE2E_FullAgentYAMLPipeline(t *testing.T) {
	for _, tc := range e2eCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := testOpenDB(t)

			connID := createStoredConnector(t, db, CreateConnectorRequest{
				Name: "deploy-" + tc.connectorName, Description: "deploy e2e " + tc.name,
				Type: tc.typ, Subtype: tc.subtype, Values: tc.values,
			})

			seedAgentWithConnector(t, db, deterministicUUID(tc.name), "deploy-agent-"+tc.subtype, connID)

			a, _, _, _, _, err := store.NewAgentRepo(db).GetByID(t.Context(), deterministicUUID(tc.name))
			if err != nil || a == nil {
				t.Fatalf("load seeded agent: %v", err)
			}
			configs, err := resolveConnectorValues(t.Context(), []string{connID}, store.NewConnectorRepo(db), nil)
			if err != nil {
				t.Fatalf("resolveConnectorValues: %v", err)
			}
			if len(configs) != 1 {
				t.Fatalf("expected 1 resolved connector, got %d", len(configs))
			}
			for _, key := range secretKeysForCase(tc) {
				unredacted, ok := configs[0].Values[key].(string)
				if !ok || unredacted == "" {
					t.Fatalf("deploy-time secret %q missing from resolved values; got %v", key, configs[0].Values[key])
				}
				if unredacted == "<REDACTED>" {
					t.Errorf("deploy-time secret %q is <REDACTED> — secrets would never reach the broker", key)
				}
				want := tc.values[key]
				if want != nil && unredacted != want {
					t.Errorf("deploy-time secret %q = %q, want %q", key, unredacted, want)
				}
			}

			yamlSvc := NewYAMLService("test-ns", slog.New(slog.NewTextHandler(io.Discard, nil)))
			cfg, err := yamlSvc.BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, configs)
			if err != nil {
				t.Fatalf("BuildAgentConfig: %v", err)
			}
			tools, err := unwrapTools(cfg)
			if err != nil {
				t.Fatalf("unwrap tools: %v", err)
			}
			if len(tools) == 0 {
				t.Fatal("tools list is empty after BuildAgentConfig")
			}
			var match map[string]any
			for _, tl := range tools {
				name, _ := tl["tool_name"].(string)
				if strings.Contains(name, tc.wantToolNameHas) {
					match = tl
					break
				}
			}
			if match == nil {
				var names []string
				for _, tl := range tools {
					n, _ := tl["tool_name"].(string)
					names = append(names, n)
				}
				t.Fatalf("agent YAML missing connector tool matching %q; have %v", tc.wantToolNameHas, names)
			}
			tcfg, _ := match["tool_config"].(map[string]any)
			if tcfg["_str_binary"] != tc.wantStrBinary {
				t.Errorf("agent YAML tool_config._str_binary = %v, want %q", tcfg["_str_binary"], tc.wantStrBinary)
			}
			if tcfg["backend"] != tc.wantBackend {
				t.Errorf("agent YAML tool_config.backend = %v, want %q", tcfg["backend"], tc.wantBackend)
			}
		})
	}
}

func secretKeysForCase(tc e2eCase) []string {
	info := getSecretRegistry().ConnectorSecretFields(tc.typ, tc.subtype)
	out := make([]string, 0, len(info))
	for k := range info {
		if _, set := tc.values[k]; set {
			out = append(out, k)
		}
	}
	return out
}

func unwrapTools(cfg map[string]any) ([]map[string]any, error) {
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		return nil, fmt.Errorf("apps[] missing or wrong type: %T", cfg["apps"])
	}
	appCfg, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("app_config missing: %T", apps[0]["app_config"])
	}
	switch v := appCfg["tools"].(type) {
	case []map[string]any:
		return v, nil
	case []any:
		out := make([]map[string]any, 0, len(v))
		for i, t := range v {
			m, ok := t.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tools[%d] is %T, want map", i, t)
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tools missing or wrong type: %T", appCfg["tools"])
	}
}

func deterministicUUID(label string) string {
	const seed = "0123456789abcdef"
	var b strings.Builder
	for i, c := range label {
		if i >= 32 {
			break
		}
		b.WriteByte(seed[int(c)%16])
	}
	for b.Len() < 32 {
		b.WriteByte('0')
	}
	s := b.String()
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
