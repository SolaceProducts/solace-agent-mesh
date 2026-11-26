// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateHILEntry_ApproversAreCheckedAsTheAgentRuntimeChecksThem(t *testing.T) {
	cases := map[string]struct {
		approvers any
		wantErr   string
	}{
		"users and a place": {map[string]any{
			"users":  []any{"alice@example.com"},
			"places": []any{map[string]any{"channel_type": "slack", "address": map[string]any{"channel_id": "C1"}}},
		}, ""},
		"users only":       {map[string]any{"users": []any{"alice@example.com", "bob@example.com"}}, ""},
		"places only":      {map[string]any{"places": []any{map[string]any{"channel_type": "slack", "address": map[string]any{"channel_id": "C1"}}}}, "at least one user"},
		"groups":           {map[string]any{"users": []any{"alice@example.com"}, "groups": []any{"ops"}}, "groups is not supported"},
		"system principal": {map[string]any{"users": []any{"system:ping"}}, "system principal"},
		"not a map":        {[]any{"alice@example.com"}, "must be a map"},
	}
	for name, tc := range cases {
		for _, path := range []string{"hil", "hil.tools.deleteIssue"} {
			t.Run(name+" at "+path, func(t *testing.T) {
				err := validateHILEntry(map[string]any{"require_approval": true, "approvers": tc.approvers}, path)

				switch {
				case tc.wantErr == "" && err != nil:
					t.Errorf("validateHILEntry rejected a set the agent accepts: %v", err)
				case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
					t.Errorf("validateHILEntry error = %v, want one containing %q", err, tc.wantErr)
				}
				if _, isValidation := errors.AsType[*ValidationError](err); tc.wantErr != "" && !isValidation {
					t.Errorf("validateHILEntry error = %T, want a *ValidationError so the API answers 4xx", err)
				}
			})
		}
	}
}
