// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func TestForScheduledTaskOwner_ARefusalNamesWhyTheOwnerWasRefusedAndNotWho(t *testing.T) {
	const owner = "dana.fictional@example.com"
	cases := []struct {
		name    string
		ent     Entitlement
		wantErr error
		want    OwnerRefusal
	}{
		{
			name:    "a deactivated account",
			ent:     Entitlement{Scopes: []string{"agent:*:invoke"}, Resolution: identity.LookupRefused, RefusalReason: refusalInactiveUser},
			wantErr: ErrOwnerDeactivated,
			want:    OwnerRefusal{Resolution: identity.LookupRefused, RefusalReason: refusalInactiveUser},
		},
		{
			name:    "a refusal for another reason",
			ent:     Entitlement{Scopes: []string{"agent:*:invoke"}, Resolution: identity.LookupRefused, RefusalReason: "sender_changed"},
			wantErr: ErrOwnerUnresolved,
			want:    OwnerRefusal{Resolution: identity.LookupRefused, RefusalReason: "sender_changed"},
		},
		{
			name:    "an ambiguous email",
			ent:     Entitlement{Scopes: []string{"agent:*:invoke"}, Resolution: identity.LookupAmbiguous, RolesSource: identity.RolesSourceDefaults},
			wantErr: ErrOwnerUnresolved,
			want:    OwnerRefusal{Resolution: identity.LookupAmbiguous, RolesSource: identity.RolesSourceDefaults},
		},
		{
			name:    "no account on the default roles",
			ent:     Entitlement{Scopes: []string{"agent:*:invoke"}, Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults},
			wantErr: ErrOwnerUnresolved,
			want:    OwnerRefusal{Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults},
		},
		{
			name:    "no account from a platform that does not report where the roles came from",
			ent:     Entitlement{Scopes: []string{"agent:*:invoke"}, Resolution: identity.LookupNoMatch},
			wantErr: ErrOwnerUnresolved,
			want:    OwnerRefusal{Resolution: identity.LookupNoMatch},
		},
		{
			name:    "no account and no scopes is refused as an unresolved owner, not as no scopes",
			ent:     Entitlement{Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults},
			wantErr: ErrOwnerUnresolved,
			want:    OwnerRefusal{Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, err := ForScheduledTaskOwner(t.Context(), owner, &fakeLookup{ent: c.ent})

			if id != nil {
				t.Fatalf("ForScheduledTaskOwner built %v, want no caller", id)
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			var refusal *OwnerRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("err = %v, want an *OwnerRefusal that carries what the platform answered", err)
			}
			if refusal.Resolution != c.want.Resolution || refusal.RefusalReason != c.want.RefusalReason || refusal.RolesSource != c.want.RolesSource {
				t.Errorf("refusal = {resolution %q, reason %q, roles source %q}, want {%q, %q, %q}",
					refusal.Resolution, refusal.RefusalReason, refusal.RolesSource, c.want.Resolution, c.want.RefusalReason, c.want.RolesSource)
			}
			if strings.Contains(err.Error(), owner) {
				t.Errorf("err = %q carries the owner's id, want only what the platform answered", err)
			}
		})
	}
}

func TestForScheduledTaskOwner_ARefusalForNoScopesDoesNotCarryTheOwnersId(t *testing.T) {
	const owner = "dana.fictional@example.com"

	_, err := ForScheduledTaskOwner(t.Context(), owner, &fakeLookup{ent: Entitlement{Resolution: identity.LookupMatched}})

	if !errors.Is(err, ErrNoEffectiveScopes) {
		t.Fatalf("err = %v, want ErrNoEffectiveScopes", err)
	}
	if strings.Contains(err.Error(), owner) {
		t.Errorf("err = %q carries the owner's id", err)
	}
}
