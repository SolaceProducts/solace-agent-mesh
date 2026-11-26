// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"strings"
	"testing"
)

func claimDiffComposer(t *testing.T) *Composer {
	t.Helper()
	db := &DBState{
		Roles: []DBRole{{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}}},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "sam-admins", RoleIDs: []string{"uuid-admin"}},
		},
	}
	yaml := YAMLState{
		ClaimMappings: []DBClaimMapping{
			{OIDCProvider: "okta", ClaimKey: "department", ClaimValue: "finance", RoleID: "sam_admin"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	return c
}

func TestComposer_MappedClaimsChanged(t *testing.T) {
	c := claimDiffComposer(t)

	tests := []struct {
		name   string
		before string
		after  string
		want   bool
	}{
		{
			"an ordinary login moves only volatile claims",
			`{"sub":"alice","groups":["sam-admins"],"iat":1700000000,"exp":1700003600,"nonce":"abc"}`,
			`{"sub":"alice","groups":["sam-admins"],"iat":1700007200,"exp":1700010800,"nonce":"xyz"}`,
			false,
		},
		{
			"a mapped array reordered is the same membership",
			`{"groups":["eng","sam-admins","ops"]}`,
			`{"groups":["ops","eng","sam-admins"]}`,
			false,
		},
		{
			"a mapped claim value moved",
			`{"groups":["sam-admins"]}`,
			`{"groups":["sam-users"]}`,
			true,
		},
		{
			"a mapped claim gained a value",
			`{"groups":["eng"]}`,
			`{"groups":["eng","sam-admins"]}`,
			true,
		},
		{
			"a mapped claim disappeared",
			`{"groups":["sam-admins"],"sub":"alice"}`,
			`{"sub":"alice"}`,
			true,
		},
		{
			"a mapped claim appeared",
			`{"sub":"alice"}`,
			`{"groups":["sam-admins"],"sub":"alice"}`,
			true,
		},
		{
			"a claim mapped under another provider still counts",
			`{"department":"finance"}`,
			`{"department":"legal"}`,
			true,
		},
		{
			"an unmapped claim moving is not a change",
			`{"groups":["sam-admins"],"email":"a@example.com","name":"A"}`,
			`{"groups":["sam-admins"],"email":"b@example.com","name":"B"}`,
			false,
		},
		{
			"a mapped claim of an unmatchable type reads as absent",
			`{"groups":42}`,
			`{"groups":{"nested":"object"}}`,
			false,
		},
		{"empty and empty-object are both an empty claim set", ``, `{}`, false},
		{"empty to populated is a change", ``, `{"groups":["sam-admins"]}`, true},
		{"populated to empty is a change", `{"groups":["sam-admins"]}`, ``, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.MappedClaimsChanged(tt.before, tt.after)
			if err != nil {
				t.Fatalf("MappedClaimsChanged: %v", err)
			}
			if got != tt.want {
				t.Errorf("MappedClaimsChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComposer_MappedClaimsChangedFailsClosed(t *testing.T) {
	built := claimDiffComposer(t)

	t.Run("nil composer", func(t *testing.T) {
		var c *Composer
		if _, err := c.MappedClaimsChanged(`{}`, `{}`); err == nil {
			t.Error("a nil composer must report that it cannot answer")
		}
	})

	t.Run("composer not yet rebuilt", func(t *testing.T) {
		c := NewComposer(YAMLState{}, &staticSource{state: &DBState{}}, newTestLogger())
		if _, err := c.MappedClaimsChanged(`{}`, `{}`); err == nil {
			t.Error("an unbuilt composer must report that it cannot answer")
		}
	})

	t.Run("malformed stored claims", func(t *testing.T) {
		for _, tc := range []struct{ name, before, after string }{
			{"before", `{"groups":`, `{}`},
			{"after", `{}`, `not json at all`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := built.MappedClaimsChanged(tc.before, tc.after)
				if err == nil {
					t.Fatal("malformed claims must report that they cannot be compared")
				}
				if strings.Contains(err.Error(), "groups") || strings.Contains(err.Error(), "not json at all") {
					t.Errorf("error must not carry any claim content, got %q", err)
				}
			})
		}
	})
}
