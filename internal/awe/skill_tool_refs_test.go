// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestResolveSkillToolEnv(t *testing.T) {
	t.Setenv("SKILL_TEST_CSE_ID", "cse-123")
	t.Setenv("SKILL_TEST_TOKEN", "tok-abc")

	t.Run("an unset bare reference is reported", func(t *testing.T) {
		entry := map[string]any{
			"tool_type":   "builtin",
			"tool_name":   "web_search_google",
			"tool_config": map[string]any{"google_cse_id": "${SKILL_TEST_ABSENT}"},
		}
		missing, _ := resolveSkillToolRefs(entry, nil)
		if len(missing) != 1 || missing[0] != "SKILL_TEST_ABSENT" {
			t.Fatalf("missing = %v, want [SKILL_TEST_ABSENT]", missing)
		}
		got := entry["tool_config"].(map[string]any)["google_cse_id"]
		if got != "" {
			t.Errorf("value = %q; the caller skips the entry, so the blank is never used", got)
		}
	})

	t.Run("a set reference is expanded", func(t *testing.T) {
		entry := map[string]any{"tool_config": map[string]any{"google_cse_id": "${SKILL_TEST_CSE_ID}"}}
		if missing, _ := resolveSkillToolRefs(entry, nil); len(missing) != 0 {
			t.Fatalf("missing = %v, want none", missing)
		}
		if got := entry["tool_config"].(map[string]any)["google_cse_id"]; got != "cse-123" {
			t.Errorf("value = %q, want cse-123", got)
		}
	})

	t.Run("a default form is not reported when unset", func(t *testing.T) {
		entry := map[string]any{"tool_config": map[string]any{"model": "${SKILL_TEST_ABSENT, gpt-4o}"}}
		if missing, _ := resolveSkillToolRefs(entry, nil); len(missing) != 0 {
			t.Fatalf("missing = %v, want none: a default is an explicit opt-in to unset", missing)
		}
		if got := entry["tool_config"].(map[string]any)["model"]; got != "gpt-4o" {
			t.Errorf("value = %q, want gpt-4o", got)
		}
	})

	t.Run("nested maps and slices are reached", func(t *testing.T) {
		entry := map[string]any{
			"connection_params": map[string]any{
				"headers": map[string]any{"Authorization": "Bearer ${SKILL_TEST_TOKEN}"},
				"args":    []any{"--token", "${SKILL_TEST_TOKEN}"},
			},
			"auth": map[string]any{"token": "${SKILL_TEST_ABSENT}"},
		}
		missing, _ := resolveSkillToolRefs(entry, nil)
		if len(missing) != 1 || missing[0] != "SKILL_TEST_ABSENT" {
			t.Fatalf("missing = %v, want [SKILL_TEST_ABSENT]", missing)
		}
		conn := entry["connection_params"].(map[string]any)
		if got := conn["headers"].(map[string]any)["Authorization"]; got != "Bearer tok-abc" {
			t.Errorf("nested header = %q, want the expanded token", got)
		}
		if got := conn["args"].([]any)[1]; got != "tok-abc" {
			t.Errorf("slice element = %q, want the expanded token", got)
		}
	})

	t.Run("identity fields are left alone", func(t *testing.T) {
		entry := map[string]any{
			"tool_type":   "builtin",
			"tool_name":   "${SKILL_TEST_TOKEN}",
			"name":        "${SKILL_TEST_TOKEN}",
			"description": "${SKILL_TEST_TOKEN}",
			"tool_config": map[string]any{"ok": "${SKILL_TEST_TOKEN}"},
		}
		resolveSkillToolRefs(entry, nil)
		for _, key := range []string{"tool_name", "name", "description"} {
			if got := entry[key]; got != "${SKILL_TEST_TOKEN}" {
				t.Errorf("%s = %q, want the reference left literal", key, got)
			}
		}
		if got := entry["tool_config"].(map[string]any)["ok"]; got != "tok-abc" {
			t.Errorf("tool_config value = %q, want it expanded", got)
		}
	})

	t.Run("every configured key is reached", func(t *testing.T) {
		cases := map[string]struct {
			given any
			want  any
		}{
			"tool_config":           {map[string]any{"google_cse_id": "${SKILL_TEST_TOKEN}"}, map[string]any{"google_cse_id": "tok-abc"}},
			"connection_params":     {map[string]any{"headers": map[string]any{"Authorization": "Bearer ${SKILL_TEST_TOKEN}"}}, map[string]any{"headers": map[string]any{"Authorization": "Bearer tok-abc"}}},
			"environment_variables": {map[string]any{"GITHUB_TOKEN": "${SKILL_TEST_TOKEN}"}, map[string]any{"GITHUB_TOKEN": "tok-abc"}},
			"manifest_file":         {"/mcp/${SKILL_TEST_TOKEN}.json", "/mcp/tok-abc.json"},
			"auth":                  {map[string]any{"token": "${SKILL_TEST_TOKEN}"}, map[string]any{"token": "tok-abc"}},
			"headers":               {map[string]any{"X-API-Key": "${SKILL_TEST_TOKEN}"}, map[string]any{"X-API-Key": "tok-abc"}},
			"specification_url":     {"https://${SKILL_TEST_TOKEN}/openapi.json", "https://tok-abc/openapi.json"},
			"specification_file":    {"/specs/${SKILL_TEST_TOKEN}.json", "/specs/tok-abc.json"},
			"base_url":              {"https://${SKILL_TEST_TOKEN}", "https://tok-abc"},
		}

		entry := map[string]any{"tool_type": "builtin"}
		for _, key := range skillToolConfigKeys {
			c, covered := cases[key]
			if !covered {
				t.Fatalf("skillToolConfigKeys contains %q with no case here; add one so the "+
					"key is not listed as resolved while nothing checks that it is", key)
			}
			entry[key] = c.given
		}
		for key := range cases {
			if !slices.Contains(skillToolConfigKeys, key) {
				t.Fatalf("%q has a case here but is missing from skillToolConfigKeys, so "+
					"whichever parser reads it now receives the literal reference", key)
			}
		}

		if missing, _ := resolveSkillToolRefs(entry, nil); len(missing) != 0 {
			t.Fatalf("missing = %v, want none", missing)
		}
		for _, key := range skillToolConfigKeys {
			if got := entry[key]; !reflect.DeepEqual(got, cases[key].want) {
				t.Errorf("%s = %#v, want %#v", key, got, cases[key].want)
			}
		}
	})

	t.Run("an unset variable in environment_variables is reported", func(t *testing.T) {
		entry := map[string]any{
			"tool_type":             "mcp",
			"environment_variables": map[string]any{"GITHUB_TOKEN": "${SKILL_TEST_ABSENT}"},
		}
		missing, _ := resolveSkillToolRefs(entry, nil)
		if len(missing) != 1 || missing[0] != "SKILL_TEST_ABSENT" {
			t.Errorf("missing = %v, want [SKILL_TEST_ABSENT]", missing)
		}
	})

	t.Run("several unresolved names are sorted and deduplicated", func(t *testing.T) {
		entry := map[string]any{
			"tool_config":       map[string]any{"b": "${SKILL_TEST_ZZZ}", "a": "${SKILL_TEST_AAA}"},
			"connection_params": map[string]any{"c": "${SKILL_TEST_ZZZ}"},
		}
		missing, _ := resolveSkillToolRefs(entry, nil)
		if strings.Join(missing, ",") != "SKILL_TEST_AAA,SKILL_TEST_ZZZ" {
			t.Errorf("missing = %v, want the two names sorted and deduplicated", missing)
		}
	})
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
		return nil, errors.New("404 not found")
	}
	return v, nil
}

func kv2(fields map[string]any) map[string]any {
	return map[string]any{"data": map[string]any{"data": fields}}
}

func TestResolveSkillToolVault(t *testing.T) {
	const path = "kv/data/skills/demo"

	t.Run("a reference is resolved", func(t *testing.T) {
		f := &stubVaultFetcher{secrets: map[string]map[string]any{path: kv2(map[string]any{"api_key": "sk-real"})}}
		entry := map[string]any{
			"tool_type":   "builtin",
			"tool_config": map[string]any{"api_key": "vault://" + path + "#api_key"},
		}
		missing, vaultErrs := resolveSkillToolRefs(entry, f)
		if len(missing) != 0 || len(vaultErrs) != 0 {
			t.Fatalf("missing = %v, vaultErrs = %v, want none", missing, vaultErrs)
		}
		if got := entry["tool_config"].(map[string]any)["api_key"]; got != "sk-real" {
			t.Errorf("api_key = %q, want the resolved secret", got)
		}
	})

	t.Run("nested maps and slices are reached", func(t *testing.T) {
		f := &stubVaultFetcher{secrets: map[string]map[string]any{path: kv2(map[string]any{"token": "tok-real"})}}
		ref := "vault://" + path + "#token"
		entry := map[string]any{
			"connection_params": map[string]any{
				"headers": map[string]any{"Authorization": "Bearer " + ref},
				"args":    []any{"--token", ref},
			},
		}
		if _, vaultErrs := resolveSkillToolRefs(entry, f); len(vaultErrs) != 0 {
			t.Fatalf("vaultErrs = %v, want none", vaultErrs)
		}
		conn := entry["connection_params"].(map[string]any)
		if got := conn["headers"].(map[string]any)["Authorization"]; got != "Bearer tok-real" {
			t.Errorf("nested header = %q, want the resolved secret", got)
		}
		if got := conn["args"].([]any)[1]; got != "tok-real" {
			t.Errorf("slice element = %q, want the resolved secret", got)
		}
	})

	t.Run("a variable composes into the vault path", func(t *testing.T) {
		t.Setenv("SKILL_TEST_DEPLOY_ENV", "staging")
		f := &stubVaultFetcher{secrets: map[string]map[string]any{
			"kv/data/staging/api": kv2(map[string]any{"key": "staging-key"}),
		}}
		entry := map[string]any{
			"tool_config": map[string]any{"api_key": "vault://kv/data/${SKILL_TEST_DEPLOY_ENV}/api#key"},
		}
		missing, vaultErrs := resolveSkillToolRefs(entry, f)
		if len(missing) != 0 || len(vaultErrs) != 0 {
			t.Fatalf("missing = %v, vaultErrs = %v, want none", missing, vaultErrs)
		}
		if got := entry["tool_config"].(map[string]any)["api_key"]; got != "staging-key" {
			t.Errorf("api_key = %q, want staging-key: ${VAR} must resolve before vault://", got)
		}
	})

	t.Run("no fetcher is reported and names vault", func(t *testing.T) {
		ref := "vault://" + path + "#api_key"
		entry := map[string]any{"tool_config": map[string]any{"api_key": ref}}
		_, vaultErrs := resolveSkillToolRefs(entry, nil)
		if len(vaultErrs) != 1 {
			t.Fatalf("vaultErrs = %v, want exactly one", vaultErrs)
		}
		msg := vaultErrs[0].Error()
		if !strings.Contains(msg, ref) || !strings.Contains(msg, "vault fetcher not configured") {
			t.Errorf("message = %q, want it to name the reference and Vault", msg)
		}
		if got := entry["tool_config"].(map[string]any)["api_key"]; got != ref {
			t.Errorf("value = %q, want the reference left literal, not blanked", got)
		}
	})

	t.Run("an unreachable vault is reported once per path", func(t *testing.T) {
		f := &stubVaultFetcher{failOn: map[string]error{path: errors.New("dial tcp: connection refused")}}
		ref := "vault://" + path + "#api_key"
		entry := map[string]any{
			"tool_config": map[string]any{"api_key": ref},
			"headers":     map[string]any{"X-Key": ref, "X-Other": "vault://" + path + "#other"},
		}
		_, vaultErrs := resolveSkillToolRefs(entry, f)
		if len(vaultErrs) == 0 {
			t.Fatal("vaultErrs = none, want the fetch failure reported")
		}
		if !strings.Contains(vaultErrs[0].Error(), "connection refused") {
			t.Errorf("message = %q, want the underlying reason", vaultErrs[0].Error())
		}
		if f.calls[path] != 1 {
			t.Errorf("fetched %d times, want 1: failures must be memoised per entry, "+
				"or an unreachable Vault costs one timeout per reference", f.calls[path])
		}
	})

	t.Run("a successful lookup is fetched once per path", func(t *testing.T) {
		f := &stubVaultFetcher{secrets: map[string]map[string]any{path: kv2(map[string]any{"a": "1", "b": "2"})}}
		entry := map[string]any{
			"tool_config": map[string]any{"a": "vault://" + path + "#a"},
			"headers":     map[string]any{"b": "vault://" + path + "#b"},
		}
		if _, vaultErrs := resolveSkillToolRefs(entry, f); len(vaultErrs) != 0 {
			t.Fatalf("vaultErrs = %v, want none", vaultErrs)
		}
		if f.calls[path] != 1 {
			t.Errorf("fetched %d times, want 1: ResolveVaultURIs caches per call, and a "+
				"call is now one leaf, so the memo is what dedupes across leaves", f.calls[path])
		}
	})

	t.Run("a missing field is reported", func(t *testing.T) {
		f := &stubVaultFetcher{secrets: map[string]map[string]any{path: kv2(map[string]any{"api_key": "sk-real"})}}
		entry := map[string]any{"tool_config": map[string]any{"api_key": "vault://" + path + "#absent"}}
		_, vaultErrs := resolveSkillToolRefs(entry, f)
		if len(vaultErrs) != 1 || !strings.Contains(vaultErrs[0].Error(), "absent") {
			t.Fatalf("vaultErrs = %v, want the missing field named", vaultErrs)
		}
	})

	t.Run("identity fields are left alone", func(t *testing.T) {
		f := &stubVaultFetcher{secrets: map[string]map[string]any{path: kv2(map[string]any{"api_key": "sk-real"})}}
		ref := "vault://" + path + "#api_key"
		entry := map[string]any{
			"tool_type":   "builtin",
			"tool_name":   ref,
			"name":        ref,
			"description": ref,
			"tool_config": map[string]any{"ok": ref},
		}
		resolveSkillToolRefs(entry, f)
		for _, key := range []string{"tool_name", "name", "description"} {
			if got := entry[key]; got != ref {
				t.Errorf("%s = %q, want the reference left literal", key, got)
			}
		}
		if got := entry["tool_config"].(map[string]any)["ok"]; got != "sk-real" {
			t.Errorf("tool_config value = %q, want it resolved", got)
		}
	})

	t.Run("every configured key is reached", func(t *testing.T) {
		secrets := map[string]map[string]any{path: kv2(map[string]any{"v": "sk-real"})}
		ref := "vault://" + path + "#v"
		cases := map[string]struct {
			given any
			want  any
		}{
			"tool_config":           {map[string]any{"api_key": ref}, map[string]any{"api_key": "sk-real"}},
			"connection_params":     {map[string]any{"headers": map[string]any{"Authorization": "Bearer " + ref}}, map[string]any{"headers": map[string]any{"Authorization": "Bearer sk-real"}}},
			"environment_variables": {map[string]any{"GITHUB_TOKEN": ref}, map[string]any{"GITHUB_TOKEN": "sk-real"}},
			"manifest_file":         {"/mcp/" + ref, "/mcp/sk-real"},
			"auth":                  {map[string]any{"token": ref}, map[string]any{"token": "sk-real"}},
			"headers":               {map[string]any{"X-API-Key": ref}, map[string]any{"X-API-Key": "sk-real"}},
			"specification_url":     {ref, "sk-real"},
			"specification_file":    {"/specs/" + ref, "/specs/sk-real"},
			"base_url":              {"https://" + ref, "https://sk-real"},
		}

		entry := map[string]any{"tool_type": "builtin"}
		for _, key := range skillToolConfigKeys {
			c, covered := cases[key]
			if !covered {
				t.Fatalf("skillToolConfigKeys contains %q with no case here; add one so the "+
					"key is not listed as resolved while nothing checks that vault:// works there", key)
			}
			entry[key] = c.given
		}
		for key := range cases {
			if !slices.Contains(skillToolConfigKeys, key) {
				t.Fatalf("%q has a case here but is missing from skillToolConfigKeys, so "+
					"whichever parser reads it now receives the literal reference", key)
			}
		}

		missing, vaultErrs := resolveSkillToolRefs(entry, &stubVaultFetcher{secrets: secrets})
		if len(missing) != 0 || len(vaultErrs) != 0 {
			t.Fatalf("missing = %v, vaultErrs = %v, want none", missing, vaultErrs)
		}
		for _, key := range skillToolConfigKeys {
			if got := entry[key]; !reflect.DeepEqual(got, cases[key].want) {
				t.Errorf("%s = %#v, want %#v", key, got, cases[key].want)
			}
		}
	})
}
