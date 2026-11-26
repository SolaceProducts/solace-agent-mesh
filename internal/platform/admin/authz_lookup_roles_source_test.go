// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type fixedOutcomeResolver struct{ result identityResolution }

func (r fixedOutcomeResolver) Resolve(context.Context, identityLookupRequest) (identityResolution, error) {
	return r.result, nil
}

func stringFieldFromResult(t *testing.T, resp *control.Response, field string) string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	s, _ := res[field].(string)
	return s
}

func TestRolesSourceReachesTheWireForUnmatchedAndRefusedPrincipals(t *testing.T) {
	cases := []struct {
		name            string
		userID          string
		result          identityResolution
		wantResolution  identity.LookupResolution
		wantRolesSource string
		wantReason      string
	}{
		{
			name:            "a no_match principal on the deployment defaults",
			userID:          "stranger@example.org",
			result:          identityResolution{Outcome: identity.LookupNoMatch},
			wantResolution:  identity.LookupNoMatch,
			wantRolesSource: "defaults",
		},
		{
			name:            "a no_match principal with a YAML assignment",
			userID:          "alice@example.org",
			result:          identityResolution{Outcome: identity.LookupNoMatch},
			wantResolution:  identity.LookupNoMatch,
			wantRolesSource: "assigned",
		},
		{
			name:            "a refused principal on the deployment defaults",
			userID:          "stranger@example.org",
			result:          identityResolution{Outcome: identity.LookupRefused, RefusalReason: "inactive_user"},
			wantResolution:  identity.LookupRefused,
			wantRolesSource: "defaults",
			wantReason:      "inactive_user",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := fmt.Sprintf("authz-roles-source-%d", i)
			brk := newTestBroker(t)
			startAuthzHandlerWithResolver(t, brk, newAuthzTestComposer(t), fixedOutcomeResolver{result: tc.result}, ns)

			resp := roundTrip(t, nil, brk, ns, tc.userID, map[string]any{"gateway_id": "webui"})

			if got := identity.LookupResolution(stringFieldFromResult(t, resp, "resolution")); got != tc.wantResolution {
				t.Errorf("resolution = %q, want %q", got, tc.wantResolution)
			}
			if got := stringFieldFromResult(t, resp, "roles_source"); got != tc.wantRolesSource {
				t.Errorf("roles_source = %q, want %q", got, tc.wantRolesSource)
			}
			if got := stringFieldFromResult(t, resp, "refusal_reason"); got != tc.wantReason {
				t.Errorf("refusal_reason = %q, want %q", got, tc.wantReason)
			}
		})
	}
}
