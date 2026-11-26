// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"strings"
	"testing"
)

func TestParseNetworkPolicies_HappyPath(t *testing.T) {
	raw := []any{
		map[string]any{
			"name":        "github-only",
			"description": "Read-only mirror access",
			"allowlist":   []any{"*.github.com", "raw.githubusercontent.com"},
		},
	}
	policies, err := parseNetworkPolicies(raw)
	if err != nil {
		t.Fatalf("parseNetworkPolicies: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("want 1 policy, got %d", len(policies))
	}
	p := policies[0]
	if p.Name != "github-only" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Description != "Read-only mirror access" {
		t.Errorf("Description = %q", p.Description)
	}
	if len(p.Allowlist) != 2 || p.Allowlist[0] != "*.github.com" || p.Allowlist[1] != "raw.githubusercontent.com" {
		t.Errorf("Allowlist = %v", p.Allowlist)
	}
}

func TestParseNetworkPolicies_MissingNameIsFatal(t *testing.T) {
	raw := []any{
		map[string]any{"allowlist": []any{"*.example.com"}},
	}
	_, err := parseNetworkPolicies(raw)
	if err == nil || !strings.Contains(err.Error(), "missing required 'name'") {
		t.Fatalf("expected missing-name error, got %v", err)
	}
}

func TestParseNetworkPolicies_DuplicateNameIsFatal(t *testing.T) {
	raw := []any{
		map[string]any{"name": "x", "allowlist": []any{"a.com"}},
		map[string]any{"name": "x", "allowlist": []any{"b.com"}},
	}
	_, err := parseNetworkPolicies(raw)
	if err == nil || !strings.Contains(err.Error(), "duplicate network policy") {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}

func TestParseNetworkPolicies_EmptyAllowlistAcceptedAtParseTime(t *testing.T) {
	raw := []any{
		map[string]any{"name": "x"},
	}
	policies, err := parseNetworkPolicies(raw)
	if err != nil {
		t.Fatalf("parseNetworkPolicies should accept empty allowlist at parse time, got %v", err)
	}
	if len(policies) != 1 || policies[0].Name != "x" || len(policies[0].Allowlist) != 0 {
		t.Fatalf("unexpected parse result: %+v", policies)
	}
}

func TestParseVolumeSlots_AllowExecPropagates(t *testing.T) {
	raw := []any{
		map[string]any{
			"name":       "project",
			"provision":  "prompt_user",
			"allow_exec": true,
		},
		map[string]any{
			"name":      "docs",
			"provision": "prompt_user",
		},
	}
	slots, err := parseVolumeSlots(raw)
	if err != nil {
		t.Fatalf("parseVolumeSlots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("want 2 slots, got %d", len(slots))
	}
	if !slots[0].AllowExec {
		t.Errorf("project slot should have AllowExec=true")
	}
	if slots[1].AllowExec {
		t.Errorf("docs slot should default to AllowExec=false")
	}
}

func TestExtractNetworkBindings_PerToolMapping(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolNames: []string{"bash"},
			toolMap: map[string]any{
				"network_bindings": map[string]any{
					"outbound": "github-only",
				},
			},
		},
		{
			toolNames: []string{"npm-install"},
			toolMap: map[string]any{
				"network_bindings": map[string]any{
					"registry": "npm-registry",
				},
			},
		},
		{
			toolNames: []string{"local-only-tool"},
			toolMap:   map[string]any{},
		},
	}
	got := extractNetworkBindings(entries)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d (%v)", len(got), got)
	}
	if got["bash"]["outbound"] != "github-only" {
		t.Errorf("bash outbound = %q", got["bash"]["outbound"])
	}
	if got["npm-install"]["registry"] != "npm-registry" {
		t.Errorf("npm-install registry = %q", got["npm-install"]["registry"])
	}
	if _, present := got["local-only-tool"]; present {
		t.Errorf("local-only-tool should not be in result")
	}
}
