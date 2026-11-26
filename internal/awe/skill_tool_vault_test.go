// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveSkillToolVault_EmptyValueIsReported(t *testing.T) {
	f := &stubVaultFetcher{secrets: map[string]map[string]any{"p": kv2(map[string]any{"k": ""})}}
	entry := map[string]any{"tool_config": map[string]any{"api_key": "vault://p#k"}}
	_, vaultErrs := resolveSkillToolRefs(entry, f)
	if len(vaultErrs) != 1 || !strings.Contains(vaultErrs[0].Error(), "empty") {
		t.Fatalf("vaultErrs = %v, want the empty field reported", vaultErrs)
	}
	if got := entry["tool_config"].(map[string]any)["api_key"]; got == "" {
		t.Error("api_key is blank; the entry must be skipped rather than registered with it")
	}
}

func TestResolveSkillToolVault_ErrorsAreStableAndUnique(t *testing.T) {
	entry := func() map[string]any {
		return map[string]any{
			"tool_config": map[string]any{"a": "vault://p/z#f", "b": "vault://p/a#f"},
			"headers":     map[string]any{"c": "vault://p/z#f", "d": "vault://p/z#g"},
			"auth":        map[string]any{"e": "vault://p/z#f"},
		}
	}
	var first string
	for i := range 40 {
		f := &stubVaultFetcher{failOn: map[string]error{
			"p/z": errors.New("connection refused"),
			"p/a": errors.New("connection refused"),
		}}
		_, vaultErrs := resolveSkillToolRefs(entry(), f)
		if len(vaultErrs) != 3 {
			t.Fatalf("vaultErrs = %v, want 3 distinct references (z#f, z#g, a#f)", vaultErrs)
		}
		var b strings.Builder
		for _, e := range vaultErrs {
			b.WriteString(e.Ref.URI + " ")
		}
		if i == 0 {
			first = b.String()
			continue
		}
		if b.String() != first {
			t.Fatalf("order changed between runs: %q then %q", first, b.String())
		}
	}
}

func TestResolveSkillToolVault_CollapsedPathIsReportedNotFetched(t *testing.T) {
	forms := map[string]string{
		"bare":         "vault://kv/data/${SKILL_TEST_NO_SUCH}/api#key",
		"alt form":     "vault://kv/data/${SKILL_TEST_NO_SUCH:+seg}/api#key",
		"default form": "vault://kv/data/${SKILL_TEST_NO_SUCH:-}/api#key",
	}
	for name, ref := range forms {
		t.Run(name, func(t *testing.T) {
			f := &stubVaultFetcher{}
			entry := map[string]any{"tool_config": map[string]any{"k": ref}}
			_, vaultErrs := resolveSkillToolRefs(entry, f)
			if len(vaultErrs) != 1 || !strings.Contains(vaultErrs[0].Error(), "empty segment") {
				t.Fatalf("vaultErrs = %v, want the collapsed path reported", vaultErrs)
			}
			if len(f.calls) != 0 {
				t.Errorf("fetched %v, want no lookup against a collapsed path", f.calls)
			}
		})
	}
}

func TestResolveSkillToolVault_ResolvedVarInPathStillComposes(t *testing.T) {
	t.Setenv("SKILL_TEST_ENVNAME", "staging")
	f := &stubVaultFetcher{secrets: map[string]map[string]any{
		"kv/data/staging/api": kv2(map[string]any{"key": "staging-key"}),
	}}
	entry := map[string]any{"tool_config": map[string]any{"k": "vault://kv/data/${SKILL_TEST_ENVNAME}/api#key"}}
	missing, vaultErrs := resolveSkillToolRefs(entry, f)
	if len(missing) != 0 || len(vaultErrs) != 0 {
		t.Fatalf("missing = %v, vaultErrs = %v, want none", missing, vaultErrs)
	}
	if got := entry["tool_config"].(map[string]any)["k"]; got != "staging-key" {
		t.Errorf("k = %q, want staging-key", got)
	}
}

func TestResolveSkillToolVault_UnsetVarBesideRefStillReportsBoth(t *testing.T) {
	f := &stubVaultFetcher{failOn: map[string]error{"kv/bad": errors.New("connection refused")}}
	entry := map[string]any{
		"headers": map[string]any{"X": "${SKILL_TEST_NO_SUCH} vault://kv/bad#f"},
	}
	missing, vaultErrs := resolveSkillToolRefs(entry, f)
	if len(missing) != 1 {
		t.Fatalf("missing = %v, want the variable reported", missing)
	}
	if len(vaultErrs) != 1 || !strings.Contains(vaultErrs[0].Error(), "connection refused") {
		t.Errorf("vaultErrs = %v, want the independent vault fault reported in the same pass", vaultErrs)
	}
}

func TestResolveSkillToolVault_CredentialLeafComposes(t *testing.T) {
	f := &stubVaultFetcher{secrets: map[string]map[string]any{"kv/api": kv2(map[string]any{"token": "sk-real"})}}
	entry := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"url": "https://api.example/mcp"},
		"headers":           map[string]any{"Authorization": "Bearer vault://kv/api#token"},
	}
	_, vaultErrs := resolveSkillToolRefs(entry, f)
	if len(vaultErrs) != 0 {
		t.Fatalf("vaultErrs = %v, want none", vaultErrs)
	}
	if got := entry["headers"].(map[string]any)["Authorization"]; got != "Bearer sk-real" {
		t.Errorf("Authorization = %q, want the composed credential", got)
	}
}

func TestResolveSkillToolVault_ResultsSurviveLaterEntries(t *testing.T) {
	r := newSkillToolResolver(&stubVaultFetcher{failOn: map[string]error{
		"p/one": errors.New("boom"), "p/two": errors.New("boom"),
	}})

	firstVars, firstErrs := resolveOneSkillTool(r, map[string]any{
		"tool_config": map[string]any{"a": "${SKILL_TEST_ABSENT_1}", "b": "vault://p/one#f"},
	})
	wantVars := strings.Join(firstVars, ",")
	wantURI := firstErrs[0].Ref.URI

	for range 8 {
		resolveOneSkillTool(r, map[string]any{
			"headers": map[string]any{"c": "${SKILL_TEST_ABSENT_2}", "d": "vault://p/two#f"},
		})
	}

	if got := strings.Join(firstVars, ","); got != wantVars {
		t.Errorf("first entry's variables changed to %q, want %q", got, wantVars)
	}
	if len(firstErrs) != 1 || firstErrs[0].Ref.URI != wantURI || firstErrs[0].Err == nil {
		t.Errorf("first entry's vault errors were mutated: %+v", firstErrs)
	}
}
