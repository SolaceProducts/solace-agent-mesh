// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type agentSurfaceFixture struct {
	instruction string
	tools       []any
	skills      []any
	extra       map[string]any
	card        *agentCardSurface
	digests     map[string]string
}

func baseAgentSurfaceFixture() agentSurfaceFixture {
	return agentSurfaceFixture{
		instruction: "You are a helpful HR assistant.\n\nAnswer briefly.  ",
		tools: []any{
			map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"},
			map[string]any{
				"tool_type":                 "builtin",
				"tool_name":                 "hr_db_sql_query_abc123",
				"timeout_seconds":           120,
				"source_connector_category": "sql",
				"tool_config": map[string]any{
					"connection_string": "postgresql://hr:s3cret@db.internal:5432/hr",
					"tool_name":         "hr_db_sql_query_abc123",
					"tool_description":  "Query the hr postgresql database.",
					"_str_binary":       "execute_sql_query",
				},
			},
			map[string]any{
				"tool_type":           "mcp",
				"source_connector_id": "11111111-2222-3333-4444-555555555555",
				"connection_params": map[string]any{
					"type":    "streamable-http",
					"url":     "https://mcp.example.com/mcp",
					"headers": map[string]any{"Authorization": "Bearer abc"},
				},
				"allow_list": []any{"search", "fetch"},
				"hil":        map[string]any{"require_approval": []any{"fetch"}},
			},
			map[string]any{
				"tool_type":         "builtin",
				"tool_name":         "weather__forecast",
				"specification_url": "https://specs.example.com/weather.json?token=t-1",
				"tool_config": map[string]any{
					"units":   "metric",
					"api_key": "weather-key-1",
				},
				"auth": map[string]any{
					"type":       "oauth2",
					"credential": map[string]any{"client_id": "client-1"},
				},
				"required_scopes":  []any{"tool:weather:invoke"},
				"volume_bindings":  map[string]any{"workspace": "slot-a"},
				"network_bindings": map[string]any{"base_url": "egress-default"},
			},
		},
		skills: []any{
			map[string]any{"name": "hr-policies"},
			map[string]any{"name": "benefits", "tool_config": map[string]any{"lookup": map[string]any{"token": "t-1"}}},
		},
		extra: map[string]any{
			"namespace":       "prod",
			"agent_name":      "agent_0001",
			"display_name":    "HR Bot",
			"deployment":      map[string]any{"id": "01a0de00-0000-7000-8000-000000000001"},
			"session_service": map[string]any{"type": "sql"},
			"model_provider":  []any{"general"},
		},
		card:    &agentCardSurface{Name: "HR Bot", Description: "Answers HR questions"},
		digests: map[string]string{"hr-policies": "digest-hr-1", "benefits": "digest-benefits-1"},
	}
}

func (f agentSurfaceFixture) payloads() []ControlPayload {
	appConfig := maps.Clone(f.extra)
	appConfig["instruction"] = f.instruction
	appConfig["tools"] = f.tools
	if f.skills != nil {
		appConfig["skills"] = f.skills
	}
	return []ControlPayload{
		{"kind": "model", "name": "model_general", "app_config": map[string]any{"model": "gpt"}},
		{"kind": "agent", "name": "agent_0001", "app_config": appConfig},
	}
}

func (f agentSurfaceFixture) hashes(t *testing.T) AgentSurfaceHashes {
	t.Helper()
	hashes, err := computeAgentSurfaceHashes(t.Context(), f.payloads(), f.card, f.digestsOf())
	if err != nil {
		t.Fatalf("computeAgentSurfaceHashes: %v", err)
	}
	return hashes
}

func (f agentSurfaceFixture) digestsOf() skillDigestsFunc {
	return func(_ context.Context, names []string) (map[string]string, error) {
		found := make(map[string]string, len(names))
		for _, name := range names {
			if digest, ok := f.digests[name]; ok {
				found[name] = digest
			}
		}
		return found, nil
	}
}

func (f agentSurfaceFixture) withExtra(key string, value any) agentSurfaceFixture {
	f.extra = maps.Clone(f.extra)
	f.extra[key] = value
	return f
}

func (f agentSurfaceFixture) withTool(index int, mutate func(tool map[string]any)) agentSurfaceFixture {
	tools := make([]any, len(f.tools))
	for i, tool := range f.tools {
		tools[i] = deepCloneJSONValue(tool)
	}
	mutate(tools[index].(map[string]any))
	f.tools = tools
	return f
}

func deepCloneJSONValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, inner := range typed {
			out[k] = deepCloneJSONValue(inner)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = deepCloneJSONValue(inner)
		}
		return out
	default:
		return v
	}
}

type surfaceSet map[string]bool

func changedSurfaces(before, after AgentSurfaceHashes) surfaceSet {
	changed := surfaceSet{}
	for name, pair := range map[string][2]string{
		"prompt":     {before.Prompt, after.Prompt},
		"toolset":    {before.Toolset, after.Toolset},
		"capability": {before.Capability, after.Capability},
		"behaviour":  {before.Behaviour, after.Behaviour},
		"card":       {before.Card, after.Card},
	} {
		if pair[0] != pair[1] {
			changed[name] = true
		}
	}
	return changed
}

func assertChangedSurfaces(t *testing.T, before, after AgentSurfaceHashes, want ...string) {
	t.Helper()
	got := changedSurfaces(before, after)
	wantSet := surfaceSet{}
	for _, name := range want {
		wantSet[name] = true
	}
	if !maps.Equal(got, wantSet) {
		t.Errorf("changed surfaces = %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(wantSet)))
	}
}

func assertAllSurfacesKnown(t *testing.T, hashes AgentSurfaceHashes) {
	t.Helper()
	for name, value := range map[string]string{
		"prompt": hashes.Prompt, "toolset": hashes.Toolset, "capability": hashes.Capability,
		"behaviour": hashes.Behaviour, "card": hashes.Card,
	} {
		if len(value) != sha256.Size*2 {
			t.Errorf("%s hash = %q, want a hex SHA-256", name, value)
		}
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestB03_AgentSurfaceHashes(t *testing.T) {
	t.Parallel()
	base := baseAgentSurfaceFixture()
	baseHashes := base.hashes(t)

	t.Run("B03_every_surface_is_known_for_a_fully_digested_agent", func(t *testing.T) {
		assertAllSurfacesKnown(t, baseHashes)
	})

	t.Run("B03_canonicalization_v1_is_pinned", func(t *testing.T) {
		wantPrompt := sha256Hex(`{"instruction":"You are a helpful HR assistant.\n\nAnswer briefly.  ","v":1}`)
		if baseHashes.Prompt != wantPrompt {
			t.Errorf("prompt hash = %s, want %s; a canonicalization change must bump the version", baseHashes.Prompt, wantPrompt)
		}
		wantCard := sha256Hex(`{"description":"Answers HR questions","name":"HR Bot","v":1}`)
		if baseHashes.Card != wantCard {
			t.Errorf("card hash = %s, want %s; a canonicalization change must bump the version", baseHashes.Card, wantCard)
		}
		wantBehaviour := sha256Hex(`{"capability":"` + baseHashes.Capability + `","prompt":"` + baseHashes.Prompt + `","toolset":"` + baseHashes.Toolset + `","v":1}`)
		if baseHashes.Behaviour != wantBehaviour {
			t.Errorf("behaviour hash = %s, want the composition of prompt, toolset and capability %s", baseHashes.Behaviour, wantBehaviour)
		}
		wantCapability := sha256Hex(`{"skills":[{"name":"benefits","digest":"digest-benefits-1"},{"name":"hr-policies","digest":"digest-hr-1"}],"v":1}`)
		if baseHashes.Capability != wantCapability {
			t.Errorf("capability hash = %s, want %s; a canonicalization change must bump the version", baseHashes.Capability, wantCapability)
		}
	})

	t.Run("B03_toolset_canonicalization_v1_is_pinned", func(t *testing.T) {
		pinned := base
		pinned.tools = append(slices.Clone(base.tools[:2]), map[string]any{"tool_type": "builtin", "tool_name": "a_first"})
		pinned.extra = map[string]any{"inter_agent_communication": map[string]any{"allow_list": []any{"peer_a"}}}
		want := sha256Hex(`{"peers":{"allow_list":["peer_a"],"deny_list":[]},"tools":[` +
			`{"tool_name":"a_first","tool_type":"builtin"},` +
			`{"group_name":"artifact_management","tool_type":"builtin-group"},` +
			`{"tool_config":{"_str_binary":"execute_sql_query","connection_string":"postgresql://db.internal:5432",` +
			`"keys":["_str_binary","connection_string","tool_description","tool_name"],` +
			`"tool_description":"Query the hr postgresql database.","tool_name":"hr_db_sql_query_abc123"},` +
			`"tool_name":"hr_db_sql_query_abc123","tool_type":"builtin"}],"v":1}`)
		if got := pinned.hashes(t).Toolset; got != want {
			t.Errorf("toolset hash = %s, want %s; a canonicalization change must bump the version", got, want)
		}
	})

	t.Run("B03_reordered_tool_declarations_and_skills_hash_identically", func(t *testing.T) {
		reordered := base
		reordered.tools = slices.Clone(base.tools)
		slices.Reverse(reordered.tools)
		reordered.skills = slices.Clone(base.skills)
		slices.Reverse(reordered.skills)
		if got := reordered.hashes(t); got != baseHashes {
			t.Errorf("reordered declarations changed hashes:\n got %+v\nwant %+v", got, baseHashes)
		}
	})

	t.Run("B03_instruction_change_moves_prompt_and_behaviour_only", func(t *testing.T) {
		changed := base
		changed.instruction = "You are a terse HR assistant."
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "prompt", "behaviour")
	})

	t.Run("B03_instruction_whitespace_is_preserved", func(t *testing.T) {
		changed := base
		changed.instruction = base.instruction + "\n"
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "prompt", "behaviour")
	})

	promptChanges := map[string]agentSurfaceFixture{}
	for _, key := range []string{
		"global_instruction", "inject_system_purpose", "inject_response_format", "inject_user_profile",
		"inject_current_time", "enable_template_instructions", "enable_artifact_content_instruction", "enable_examples",
	} {
		promptChanges[key] = base.withExtra(key, "changed")
	}
	for name, changed := range promptChanges {
		t.Run("B03_prompt_key_change_moves_prompt_and_behaviour_only/"+name, func(t *testing.T) {
			assertChangedSurfaces(t, baseHashes, changed.hashes(t), "prompt", "behaviour")
		})
	}

	toolDeclarationChanges := map[string]agentSurfaceFixture{
		"tool_description": base.withTool(1, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["tool_description"] = "Query payroll."
		}),
		"mcp_allow_list": base.withTool(2, func(tool map[string]any) { tool["allow_list"] = []any{"search"} }),
		"hil_gate":       base.withTool(2, func(tool map[string]any) { delete(tool, "hil") }),
		"required_scopes": base.withTool(3, func(tool map[string]any) {
			tool["required_scopes"] = []any{"tool:weather:admin"}
		}),
		"tool_config_key_set": base.withTool(3, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["region"] = "eu"
		}),
		"builtin_group": base.withTool(0, func(tool map[string]any) { tool["group_name"] = "web" }),
		"mcp_server_host": base.withTool(2, func(tool map[string]any) {
			tool["connection_params"].(map[string]any)["url"] = "https://mcp-2.example.com/mcp"
		}),
		"mcp_transport": base.withTool(2, func(tool map[string]any) {
			tool["connection_params"].(map[string]any)["type"] = "sse"
		}),
		"connector_identity": base.withTool(2, func(tool map[string]any) {
			tool["source_connector_id"] = "99999999-2222-3333-4444-555555555555"
		}),
		"sql_database_host": base.withTool(1, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["connection_string"] = "postgresql://hr:s3cret@db-replica.internal:5432/hr"
		}),
		"openapi_spec_host": base.withTool(3, func(tool map[string]any) {
			tool["specification_url"] = "https://other.example.com/openapi.json"
		}),
		"peer_allow_list": base.withExtra("inter_agent_communication", map[string]any{"allow_list": []any{"peer_b"}}),
		"peer_deny_list":  base.withExtra("inter_agent_communication", map[string]any{"deny_list": []any{"peer_b"}}),
		"peer_allow_none": base.withExtra("inter_agent_communication", map[string]any{"allow_list": []any{}}),
		"mcp_manifest": base.withTool(2, func(tool map[string]any) {
			tool["manifest"] = []any{map[string]any{"name": "search", "description": "Search the index."}}
		}),
		"sql_host_behind_an_env_password": base.withTool(1, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["connection_string"] = "postgresql://hr:${PASSWORD_AB_CD}@db-replica.internal:5432/hr"
		}),
		"auto_injected_tools": base.withExtra("auto_inject_data_analysis_tools", false),
	}
	for name, changed := range toolDeclarationChanges {
		t.Run("B03_tool_declaration_change_moves_toolset_and_behaviour_only/"+name, func(t *testing.T) {
			assertChangedSurfaces(t, baseHashes, changed.hashes(t), "toolset", "behaviour")
		})
	}

	effectKeyFlips := map[string][2]any{
		"effect":            {"side_effecting", "read_only"},
		"trust_annotations": {true, false},
	}
	for key, values := range effectKeyFlips {
		t.Run("B03_flipping_a_tool_effect_key_moves_toolset_and_behaviour_only/"+key, func(t *testing.T) {
			before := base.withTool(2, func(tool map[string]any) { tool[key] = values[0] })
			after := base.withTool(2, func(tool map[string]any) { tool[key] = values[1] })
			assertChangedSurfaces(t, before.hashes(t), after.hashes(t), "toolset", "behaviour")
		})
	}

	t.Run("B03_added_tool_moves_toolset_and_behaviour_only", func(t *testing.T) {
		changed := base
		changed.tools = append(slices.Clone(base.tools), map[string]any{"tool_type": "builtin-group", "group_name": "web"})
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "toolset", "behaviour")
	})

	t.Run("B03_skill_content_change_moves_capability_and_behaviour_only", func(t *testing.T) {
		changed := base
		changed.digests = maps.Clone(base.digests)
		changed.digests["benefits"] = "digest-benefits-2"
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "capability", "behaviour")
	})

	t.Run("B03_skill_set_change_moves_capability_and_behaviour_only", func(t *testing.T) {
		changed := base
		changed.skills = base.skills[:1]
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "capability", "behaviour")
	})

	t.Run("B03_card_name_change_moves_card_only", func(t *testing.T) {
		changed := base
		changed.card = &agentCardSurface{Name: "People Bot", Description: base.card.Description}
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "card")
	})

	t.Run("B03_card_description_change_moves_card_only", func(t *testing.T) {
		changed := base
		changed.card = &agentCardSurface{Name: base.card.Name, Description: "Answers people questions"}
		assertChangedSurfaces(t, baseHashes, changed.hashes(t), "card")
	})

	excludedChanges := map[string]agentSurfaceFixture{
		"connection_string_password": base.withTool(1, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["connection_string"] = "postgresql://hr:rotated@db.internal:5432/hr"
		}),
		"connection_string_database_and_options": base.withTool(1, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["connection_string"] = "postgresql://hr:s3cret@db.internal:5432/payroll?sslmode=require"
		}),
		"mcp_bearer_header": base.withTool(2, func(tool map[string]any) {
			tool["connection_params"].(map[string]any)["headers"] = map[string]any{"Authorization": "Bearer rotated"}
		}),
		"mcp_url_path_and_query_secret": base.withTool(2, func(tool map[string]any) {
			tool["connection_params"].(map[string]any)["url"] = "https://mcp.example.com/s/rotated-secret/mcp?api_key=rotated"
		}),
		"mcp_basic_auth_block": base.withTool(2, func(tool map[string]any) {
			tool["connection_params"].(map[string]any)["auth"] = map[string]any{"type": "basic", "username": "u", "password": "p"}
		}),
		"openapi_spec_query_token": base.withTool(3, func(tool map[string]any) {
			tool["specification_url"] = "https://specs.example.com/weather.json?token=rotated"
		}),
		"tool_config_value": base.withTool(3, func(tool map[string]any) {
			tool["tool_config"].(map[string]any)["units"] = "imperial"
		}),
		"tool_auth_block": base.withTool(3, func(tool map[string]any) {
			tool["auth"] = map[string]any{"type": "bearer", "credential": map[string]any{"client_id": "client-2"}}
		}),
		"openapi_headers": base.withTool(3, func(tool map[string]any) {
			tool["headers"] = map[string]any{"X-Api-Key": "rotated"}
		}),
		"timeout": base.withTool(1, func(tool map[string]any) { tool["timeout_seconds"] = 30 }),
		"volume_and_network_bindings": base.withTool(3, func(tool map[string]any) {
			tool["volume_bindings"] = map[string]any{"workspace": "slot-b"}
			tool["network_bindings"] = map[string]any{"base_url": "egress-strict"}
		}),
		"model_and_limits": base.withExtra("model_provider", []any{"planning"}).withExtra("max_tokens", 4096).withExtra("max_llm_calls_per_task", 7),
	}
	for name, changed := range excludedChanges {
		t.Run("B03_credentials_and_infrastructure_do_not_move_hashes/"+name, func(t *testing.T) {
			assertChangedSurfaces(t, baseHashes, changed.hashes(t))
		})
	}

	t.Run("B03_deployment_metadata_does_not_move_hashes", func(t *testing.T) {
		changed := base
		changed.extra = maps.Clone(base.extra)
		changed.extra["deployment"] = map[string]any{"id": "01a0de00-0000-7000-8000-000000000999"}
		changed.extra["namespace"] = "staging"
		changed.extra["session_service"] = map[string]any{"type": "memory"}
		assertChangedSurfaces(t, baseHashes, changed.hashes(t))
	})

	t.Run("B03_skill_tool_config_secrets_do_not_move_hashes", func(t *testing.T) {
		changed := base
		changed.skills = []any{
			map[string]any{"name": "hr-policies"},
			map[string]any{"name": "benefits", "tool_config": map[string]any{"lookup": map[string]any{"token": "t-2"}}},
		}
		assertChangedSurfaces(t, baseHashes, changed.hashes(t))
	})

	t.Run("B03_legacy_skill_without_digest_leaves_capability_and_behaviour_unknown", func(t *testing.T) {
		legacy := base
		legacy.skills = append(slices.Clone(base.skills), map[string]any{"name": "legacy-filesystem-skill"})
		got := legacy.hashes(t)
		if got.Capability != "" || got.Behaviour != "" {
			t.Errorf("capability=%q behaviour=%q, want both unknown", got.Capability, got.Behaviour)
		}
		if got.Prompt != baseHashes.Prompt || got.Toolset != baseHashes.Toolset || got.Card != baseHashes.Card {
			t.Errorf("an undigestable skill disturbed the other surfaces: %+v", got)
		}
	})

	t.Run("B03_no_skills_is_a_known_empty_capability", func(t *testing.T) {
		none := base
		none.skills = nil
		got := none.hashes(t)
		if got.Capability == "" || got.Behaviour == "" {
			t.Fatalf("capability=%q behaviour=%q, want known", got.Capability, got.Behaviour)
		}
		empty := base
		empty.skills = []any{}
		if again := empty.hashes(t); again.Capability != got.Capability {
			t.Errorf("an empty skill list hashed differently from an absent one")
		}
	})

	t.Run("B03_connector_declarative_value_moves_toolset_and_behaviour_only", func(t *testing.T) {
		topic := func(value string) AgentSurfaceHashes {
			return base.withTool(1, func(tool map[string]any) {
				tool["tool_config"].(map[string]any)["topic"] = value
			}).hashes(t)
		}
		assertChangedSurfaces(t, topic("orders/created"), topic("orders/cancelled"), "toolset", "behaviour")
	})

	t.Run("B03_package_tool_config_values_are_never_captured_by_key_name", func(t *testing.T) {
		described := func(description string) AgentSurfaceHashes {
			return base.withTool(3, func(tool map[string]any) {
				tool["tool_config"].(map[string]any)["description"] = description
			}).hashes(t)
		}
		assertChangedSurfaces(t, described("secret-value-1"), described("secret-value-2"))
	})

	t.Run("B03_env_referenced_password_keeps_the_sql_host", func(t *testing.T) {
		withEnvPassword := func(password string) agentSurfaceFixture {
			return base.withTool(1, func(tool map[string]any) {
				tool["tool_config"].(map[string]any)["connection_string"] = "postgresql://hr:" + password + "@db.internal:5432/hr"
			})
		}
		assertChangedSurfaces(t, baseHashes, withEnvPassword("${PASSWORD_AB_CD}").hashes(t))
		assertChangedSurfaces(t, withEnvPassword("${PASSWORD_AB_CD}").hashes(t), withEnvPassword("${PASSWORD_EF_01}").hashes(t))
	})

	t.Run("B03_peer_lists_are_pattern_sets", func(t *testing.T) {
		peers := func(section any) AgentSurfaceHashes {
			return base.withExtra("inter_agent_communication", section).hashes(t)
		}
		ordered := peers(map[string]any{"allow_list": []any{"a", "b"}, "deny_list": []any{"x", "y"}})
		for name, section := range map[string]any{
			"reordered":    map[string]any{"allow_list": []any{"b", "a"}, "deny_list": []any{"y", "x"}},
			"duplicated":   map[string]any{"allow_list": []any{"a", "b", "a"}, "deny_list": []any{"x", "y", "y"}},
			"string_slice": map[string]any{"allow_list": []string{"b", "a"}, "deny_list": []string{"x", "y"}},
		} {
			if got := peers(section); got != ordered {
				t.Errorf("%s peer lists hashed differently from the ordered ones", name)
			}
		}
		allowAll := peers(map[string]any{"allow_list": []any{"*"}})
		for name, section := range map[string]any{
			"absent_section":    nil,
			"absent_allow_list": map[string]any{"deny_list": []any{}},
			"null_allow_list":   map[string]any{"allow_list": nil},
		} {
			if got := peers(section); got != allowAll {
				t.Errorf("%s did not hash as allow-all", name)
			}
		}
		if denyAll := peers(map[string]any{"allow_list": []any{}}); denyAll == allowAll {
			t.Error("an explicit empty allow_list (deny all) hashed like an absent one (allow all)")
		}
	})

	t.Run("B03_unclassified_tool_key_contributes_its_name_never_its_value", func(t *testing.T) {
		withKey := base.withTool(3, func(tool map[string]any) { tool["future_setting"] = "value-1" })
		rotated := base.withTool(3, func(tool map[string]any) { tool["future_setting"] = "value-2" })
		assertChangedSurfaces(t, baseHashes, withKey.hashes(t), "toolset", "behaviour")
		assertChangedSurfaces(t, withKey.hashes(t), rotated.hashes(t))
	})

	t.Run("B03_an_underivable_surface_leaves_the_others_known", func(t *testing.T) {
		broken := base
		broken.skills = []any{"not-an-object"}
		got, err := computeAgentSurfaceHashes(t.Context(), broken.payloads(), broken.card, broken.digestsOf())
		if err == nil {
			t.Fatal("a malformed skills list reported no error")
		}
		want := AgentSurfaceHashes{Prompt: baseHashes.Prompt, Toolset: baseHashes.Toolset, Card: baseHashes.Card}
		if got != want {
			t.Errorf("hashes = %+v, want capability and behaviour unknown and the rest known %+v", got, want)
		}
	})

	t.Run("B03_payloads_without_an_agent_carry_only_the_card", func(t *testing.T) {
		proxy := []ControlPayload{{"kind": "proxy", "name": "a2a_x", "app_config": map[string]any{"url": "https://remote"}}}
		got, err := computeAgentSurfaceHashes(t.Context(), proxy, base.card, nil)
		if err != nil {
			t.Fatalf("computeAgentSurfaceHashes: %v", err)
		}
		want := AgentSurfaceHashes{Card: baseHashes.Card}
		if got != want {
			t.Errorf("proxy hashes = %+v, want %+v", got, want)
		}
	})

	t.Run("B03_unknown_card_is_left_unknown", func(t *testing.T) {
		noCard := base
		noCard.card = nil
		got := noCard.hashes(t)
		if got.Card != "" {
			t.Errorf("card = %q, want unknown", got.Card)
		}
		assertChangedSurfaces(t, baseHashes, got, "card")
	})
}

func TestB03_ToolConfigSecretValuesNeverMoveHashes(t *testing.T) {
	t.Parallel()
	secretKeys := []string{
		"clientSecret", "accessToken", "privateKey", "bearerToken", "license_key", "signing_key",
		"account_key", "service_account_json", "dsn", "webhook_url", "pwd", "passphrase", "DATABASE_URL",
		"smtp_username", "smtp_password", "slack_bot_token", "aws_secret_access_key", "pat", "key",
	}
	base := baseAgentSurfaceFixture()
	for _, key := range secretKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			set := func(value string) AgentSurfaceHashes {
				return base.withTool(3, func(tool map[string]any) {
					tool["tool_config"].(map[string]any)[key] = value
				}).hashes(t)
			}
			assertChangedSurfaces(t, set("secret-value-1"), set("secret-value-2"))
		})
	}
}

func TestB03_EndpointIdentityKeepsOnlySchemeAndHost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   any
		want any
	}{
		{"postgresql://hr:s3cret@DB.internal:5432/hr?sslmode=require", "postgresql://db.internal:5432"},
		{"https://mcp.example.com/s/secret-in-path/mcp?api_key=k#frag", "https://mcp.example.com"},
		{"HTTPS://Specs.Example.com/openapi.json", "https://specs.example.com"},
		{"objstore://tool-specs/connectors/abc/weather.json?v=1", "objstore://tool-specs/connectors/abc/weather.json"},
		{"postgresql://alice:${PASSWORD_AB_CD}@db-a.internal:5432/app", "postgresql://db-a.internal:5432"},
		{"postgresql://${USER_1}:${PASSWORD_1}@DB-A.internal/app", "postgresql://db-a.internal"},
		{"mongodb://u:p@[::1]:27017/db", "mongodb://[::1]:27017"},
		{"postgres://admin:123456/x@db.internal/app", true},
		{"https://sk-live-4f9a8b/QwErTy@mcp.example.com/mcp", true},
		{"https://user:pa?ss@mcp.example.com/mcp", true},
		{"https://user:pa#ss@mcp.example.com/mcp", true},
		{"mssql://host;Password=Secret123", true},
		{"https://host.example.com:notaport/x", true},
		{"https://${HOST}/x", true},
		{"postgresql://", true},
		{"host=db user=u password=p", true},
		{"${BASE_URL_1234}", true},
		{"", false},
		{nil, false},
	} {
		if got := endpointIdentity(tc.in); got != tc.want {
			t.Errorf("endpointIdentity(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestB03_AgentSurfaceHashes_ApplyToDeploymentRoundTrips(t *testing.T) {
	t.Parallel()
	hashes := AgentSurfaceHashes{Prompt: "p", Toolset: "t", Card: "c"}
	var row store.AgentDeployment
	hashes.applyTo(&row)
	if row.CapabilityHash != nil || row.BehaviourHash != nil {
		t.Errorf("unknown surfaces were written as values: capability=%v behaviour=%v", row.CapabilityHash, row.BehaviourHash)
	}
	if got := agentSurfaceHashesOf(&row); got != hashes {
		t.Errorf("round trip = %+v, want %+v", got, hashes)
	}
}

func TestB03_SkillContentDigests(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)

	digested := &store.Skill{Name: "digested-skill", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	undigested := &store.Skill{Name: "undigested-skill", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	for _, skill := range []*store.Skill{digested, undigested} {
		if err := repo.Create(ctx, skill); err != nil {
			t.Fatalf("create skill %s: %v", skill.Name, err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE skills SET content_hash = ? WHERE name = ?`, "zip-sha", digested.Name); err != nil {
		t.Fatalf("stamp content hash: %v", err)
	}

	digestsOf := skillContentDigests(repo)
	got, err := digestsOf(ctx, []string{"digested-skill", "undigested-skill", "never-registered"})
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if want := map[string]string{"digested-skill": "zip-sha"}; !maps.Equal(got, want) {
		t.Errorf("digests = %v, want %v", got, want)
	}

	builtinNames := builtinskills.Names()
	if len(builtinNames) == 0 {
		t.Skip("no built-in skills bundled")
	}
	_, wantBuiltin, err := builtinskills.HashZip(builtinNames[0])
	if err != nil {
		t.Fatalf("hash builtin: %v", err)
	}
	for _, digests := range []skillDigestsFunc{digestsOf, skillContentDigests(nil)} {
		got, err := digests(ctx, []string{builtinNames[0]})
		if err != nil {
			t.Fatalf("digest builtin: %v", err)
		}
		if got[builtinNames[0]] != wantBuiltin {
			t.Errorf("builtin digest = %q, want the embedded bundle hash %q", got[builtinNames[0]], wantBuiltin)
		}
	}

	if got, err := skillContentDigests(nil)(ctx, []string{"digested-skill"}); err != nil || len(got) != 0 {
		t.Errorf("without a skill repo a platform skill digest = %v, %v; want unknown", got, err)
	}
}
