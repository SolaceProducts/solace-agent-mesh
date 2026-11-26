// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestA2AAuthTypes_MatchesTheDTOAndTheSecretTable(t *testing.T) {
	declared := A2AAuthTypes()

	for _, tag := range []struct {
		dto   any
		field string
	}{
		{CreateRemoteAgentRequest{}, "AgentAuthType"},
		{CreateRemoteAgentRequest{}, "AgentCardAuthType"},
		{UpdateRemoteAgentRequest{}, "AgentAuthType"},
		{UpdateRemoteAgentRequest{}, "AgentCardAuthType"},
	} {
		dtoName := reflect.TypeOf(tag.dto).Name()
		t.Run("every declared type is what "+dtoName+"."+tag.field+" accepts", func(t *testing.T) {
			field, ok := reflect.TypeOf(tag.dto).FieldByName(tag.field)
			if !ok {
				t.Fatalf("%s has no %s field — this guard is looking at the wrong DTO", dtoName, tag.field)
			}
			var accepted []string
			for _, rule := range strings.Split(field.Tag.Get("validate"), ",") {
				if after, found := strings.CutPrefix(rule, "oneof="); found {
					accepted = strings.Fields(after)
				}
			}
			if len(accepted) == 0 {
				t.Fatalf("%s.%s carries no oneof rule — nothing bounds the type, so this guard cannot check it", dtoName, tag.field)
			}

			wantSorted, gotSorted := slices.Sorted(slices.Values(accepted)), slices.Sorted(slices.Values(declared))
			if !slices.Equal(wantSorted, gotSorted) {
				t.Errorf("A2AAuthTypes() = %v, %s.%s accepts %v — the two have drifted", gotSorted, dtoName, tag.field, wantSorted)
			}
		})
	}

	t.Run("every type but none maps to a secret field", func(t *testing.T) {
		for _, authType := range declared {
			if authType == "none" {
				if pairs := authSecretPairs(authType); len(pairs) != 0 {
					t.Errorf("authSecretPairs(%q) = %v, want none — a type with no auth has no credential to protect", authType, pairs)
				}
				continue
			}
			if pairs := authSecretPairs(authType); len(pairs) == 0 {
				t.Errorf("authSecretPairs(%q) is empty, so every rule reads %q as having no secrets — a stored credential for it would be cleared rather than preserved",
					authType, authType)
			}
		}
	})
}
