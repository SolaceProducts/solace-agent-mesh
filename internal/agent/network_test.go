// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestValidateAllowlistEntry(t *testing.T) {
	good := []string{
		"example.com",
		"*.example.com",
		"api.github.com",
		"localhost",
		"*.s3.amazonaws.com",
	}
	for _, e := range good {
		if err := validateAllowlistEntry(e); err != nil {
			t.Errorf("%q should be valid, got %v", e, err)
		}
	}

	bad := map[string]string{
		"":                   "empty",
		"   ":                "empty",
		" example.com":       "whitespace",
		"example.com ":       "whitespace",
		"http://example.com": "scheme",
		"example.com/path":   "path",
		"example.com:443":    "port",
		"foo.*.example.com":  "wildcard position",
		"*.example.*":        "more than one wildcard",
		"example.*":          "wildcard position",
	}
	for input, wantHint := range bad {
		err := validateAllowlistEntry(input)
		if err == nil {
			t.Errorf("%q should be invalid (%s), got nil", input, wantHint)
			continue
		}
		_ = err
	}
}

func TestMatchAllowlist(t *testing.T) {
	allow := []string{"github.com", "*.github.com", "registry.npmjs.org"}

	cases := []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"api.github.com", true},
		{"a.b.github.com", true},
		{"GitHub.COM", true},
		{"registry.npmjs.org", true},
		{"npmjs.org", false},
		{"evilgithub.com", false},
		{"github.com.evil.com", false},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		got := MatchAllowlist(allow, c.host)
		if got != c.want {
			t.Errorf("MatchAllowlist(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestValidateNetworkPolicies_EmptyAllowlistIsFatal(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "empty"},
		},
	}}
	err := a.validateNetworkPolicies()
	if err == nil {
		t.Fatal("expected error for empty allowlist")
	}
	if !strings.Contains(err.Error(), "empty allowlist") {
		t.Errorf("error should mention 'empty allowlist', got %q", err.Error())
	}
}

func TestValidateNetworkBindings_UnknownPolicyIsFatal(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "github", Allowlist: []string{"*.github.com"}},
		},
		ToolNetworkBindings: map[string]map[string]string{
			"bash": {"outbound": "github"},
			"curl": {"outbound": "missing-policy"},
		},
	}}
	err := a.validateNetworkBindings()
	if err == nil {
		t.Fatal("expected error for unknown policy binding")
	}
	if !strings.Contains(err.Error(), "missing-policy") {
		t.Errorf("error should mention the unknown policy name, got %q", err.Error())
	}
}

func TestValidateNetworkBindings_AllowsValidBindings(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "github", Allowlist: []string{"*.github.com"}},
			{Name: "npm", Allowlist: []string{"registry.npmjs.org"}},
		},
		ToolNetworkBindings: map[string]map[string]string{
			"bash":  {"outbound": "github"},
			"npm":   {"outbound": "npm"},
			"build": {"upstream": "github", "registry": "npm"},
		},
	}}
	if err := a.ValidateNetworkConfig(); err != nil {
		t.Errorf("expected ValidateNetworkConfig to succeed, got %v", err)
	}
}

func TestValidateNetworkPolicies_DuplicateNameIsFatal(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "github", Allowlist: []string{"*.github.com"}},
			{Name: "github", Allowlist: []string{"github.com"}},
		},
	}}
	err := a.validateNetworkPolicies()
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("expected duplicate-name error, got %v", err)
	}
}

func TestValidateNetworkConfig_NoPoliciesNoBindings(t *testing.T) {
	a := &Agent{cfg: AgentConfig{}}
	if err := a.ValidateNetworkConfig(); err != nil {
		t.Errorf("expected no-op success when nothing declared, got %v", err)
	}
}

func TestResolveNetworkBindings_HappyPath(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "github", Allowlist: []string{"*.github.com", "raw.githubusercontent.com"}},
			{Name: "npm", Allowlist: []string{"registry.npmjs.org"}},
		},
		ToolNetworkBindings: map[string]map[string]string{
			"bash": {
				"outbound": "github",
				"packages": "npm",
			},
		},
	}}
	got, err := a.resolveNetworkBindings("bash")
	if err != nil {
		t.Fatalf("resolveNetworkBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 bindings, got %d", len(got))
	}
	byParam := make(map[string]tool.NetworkBinding, len(got))
	for _, b := range got {
		byParam[b.ParamName] = b
	}
	if byParam["outbound"].PolicyName != "github" || len(byParam["outbound"].Allowlist) != 2 {
		t.Errorf("outbound binding wrong: %+v", byParam["outbound"])
	}
	if byParam["packages"].PolicyName != "npm" || byParam["packages"].Allowlist[0] != "registry.npmjs.org" {
		t.Errorf("packages binding wrong: %+v", byParam["packages"])
	}
}

func TestResolveNetworkBindings_NoBindingsForTool(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "github", Allowlist: []string{"*.github.com"}},
		},
		ToolNetworkBindings: map[string]map[string]string{
			"bash": {"outbound": "github"},
		},
	}}
	got, err := a.resolveNetworkBindings("local-only-tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("want nil for unbound tool, got %v", got)
	}
}

func TestResolveVolumeBindings_PropagatesAllowExec(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "code", Provision: "static", VolumeID: "vol-code", AllowExec: true},
			{Name: "docs", Provision: "static", VolumeID: "vol-docs", AllowExec: false},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"tool": {"workspace": "code", "reference": "docs"},
		},
	})
	got, err := a.resolveVolumeBindings("tool", nil)
	if err != nil {
		t.Fatalf("resolveVolumeBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 bindings, got %d", len(got))
	}
	byID := map[string]tool.VolumeBinding{}
	for _, b := range got {
		byID[b.VolumeID] = b
	}
	if !byID["vol-code"].AllowExec {
		t.Errorf("vol-code AllowExec should be true (code workspace)")
	}
	if byID["vol-docs"].AllowExec {
		t.Errorf("vol-docs AllowExec should default to false (data slot)")
	}
}

func TestResolveNetworkBindings_DefensiveCopy(t *testing.T) {
	a := &Agent{cfg: AgentConfig{
		NetworkPolicies: []NetworkPolicy{
			{Name: "p", Allowlist: []string{"a.com", "b.com"}},
		},
		ToolNetworkBindings: map[string]map[string]string{
			"t": {"x": "p"},
		},
	}}
	got, _ := a.resolveNetworkBindings("t")
	if len(got) != 1 || len(got[0].Allowlist) != 2 {
		t.Fatalf("setup wrong: %v", got)
	}
	a.cfg.NetworkPolicies[0].Allowlist[0] = "MUTATED"
	if got[0].Allowlist[0] == "MUTATED" {
		t.Errorf("resolveNetworkBindings did not defensive-copy; in-flight binding was modified")
	}
}
