// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"slices"
	"strings"
	"testing"
)

func TestParseHILToolConfig_Approvers(t *testing.T) {
	cfg, err := ParseHILToolConfig(map[string]any{
		"require_approval": true,
		"approvers": map[string]any{
			"users":  []any{"alice@example.com", "bob@example.com"},
			"places": []any{map[string]any{"channel_type": "slack", "address": map[string]any{"channel_id": "C0OPS"}}},
		},
	}, "reroute_shipment")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.Approvers.Configured() || !slices.Equal(cfg.Approvers.Users, []string{"alice@example.com", "bob@example.com"}) {
		t.Errorf("users = %v", cfg.Approvers.Users)
	}
	if len(cfg.Approvers.Places) != 1 || cfg.Approvers.Places[0].ChannelType != "slack" || cfg.Approvers.Places[0].Address["channel_id"] != "C0OPS" {
		t.Errorf("places = %+v", cfg.Approvers.Places)
	}
}

func TestParseHILToolConfig_NoApproversKeepsTheTaskUser(t *testing.T) {
	cfg, err := ParseHILToolConfig(map[string]any{"require_approval": true}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approvers.Configured() {
		t.Errorf("approvers configured without the key: %+v", cfg.Approvers)
	}
}

func TestParseHILToolConfig_ApproversRejectsWhatItCannotHonor(t *testing.T) {
	cases := map[string]struct {
		approvers any
		want      string
	}{
		"not a map":          {[]any{"alice@example.com"}, "must be a map"},
		"groups":             {map[string]any{"users": []any{"alice@example.com"}, "groups": []any{"ops"}}, "groups is not supported"},
		"unknown key":        {map[string]any{"users": []any{"alice@example.com"}, "channels": []any{}}, "not a known key"},
		"places only":        {map[string]any{"places": []any{map[string]any{"channel_type": "slack", "address": map[string]any{"channel_id": "C1"}}}}, "at least one user"},
		"empty":              {map[string]any{}, "at least one user"},
		"system principal":   {map[string]any{"users": []any{"system:ping"}}, "system principal"},
		"blank user":         {map[string]any{"users": []any{" "}}, "non-empty user id"},
		"user not a string":  {map[string]any{"users": []any{42}}, "non-empty user id"},
		"place without addr": {map[string]any{"users": []any{"alice@example.com"}, "places": []any{map[string]any{"channel_type": "slack"}}}, "exactly channel_type"},
		"place channel type a gateway cannot match": {map[string]any{"users": []any{"alice@example.com"}, "places": []any{map[string]any{"channel_type": "Slack", "address": map[string]any{"channel_id": "C1"}}}}, "not a valid channel type"},
		"too many users": {map[string]any{"users": func() []any {
			users := make([]any, maxHILApproverUsers+1)
			for i := range users {
				users[i] = "user" + strings.Repeat("x", i) + "@example.com"
			}
			return users
		}()}, "at most"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseHILToolConfig(map[string]any{"require_approval": true, "approvers": tc.approvers}, "t")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestMergeHILToolConfig_ApproversOverrideReplaces(t *testing.T) {
	base, err := ParseHILToolConfig(map[string]any{
		"require_approval": true,
		"approvers":        map[string]any{"users": []any{"alice@example.com"}},
	}, "t")
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MergeHILToolConfig(base, map[string]any{"approvers": map[string]any{"users": []any{"bob@example.com"}}}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(merged.Approvers.Users, []string{"bob@example.com"}) {
		t.Errorf("merged approvers = %v, want only bob", merged.Approvers.Users)
	}
}
