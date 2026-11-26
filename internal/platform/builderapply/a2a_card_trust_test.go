// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const cardTrustKeySet = "https://keys.example.com/jwks.json"

type cardTrust struct {
	pin     string
	urls    []string
	require bool
}

func (c cardTrust) equal(o cardTrust) bool {
	return c.pin == o.pin && slices.Equal(c.urls, o.urls) && c.require == o.require
}

func cardTrustComponent(name string, c cardTrust) BuildComponentInput {
	return a2aComponent(name, func(b map[string]any) {
		urls := make([]any, 0, len(c.urls))
		for _, u := range c.urls {
			urls = append(urls, u)
		}
		b["protocolVersion"] = c.pin
		b["trustedCardJwksUrls"] = urls
		b["requireSignedCard"] = c.require
	})
}

func applyCardTrust(t *testing.T, svc *Service, component BuildComponentInput) BuildOperationResult {
	t.Helper()
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	op := opFor(t, resp, kindA2AAgent, component.Name)
	if op.Status != statusSucceeded {
		t.Fatalf("apply status = %q, error %+v", op.Status, op.Error)
	}
	return op
}

func storedCardTrust(t *testing.T, svc *Service, name string) cardTrust {
	t.Helper()
	row, err := store.NewA2AAgentRepo(svc.deps.DB).GetByName(t.Context(), name)
	if err != nil || row == nil {
		t.Fatalf("GetByName(%q) = %v, %v", name, row, err)
	}
	stored := cardTrust{pin: row.ProtocolVersion, require: row.RequireSignedCard}
	if row.TrustedCardJWKSURLs != nil {
		if err := json.Unmarshal([]byte(*row.TrustedCardJWKSURLs), &stored.urls); err != nil {
			t.Fatalf("stored trusted card JWKS URLs %q: %v", *row.TrustedCardJWKSURLs, err)
		}
	}
	return stored
}

func TestApply_A2ACardTrustTurnsOffDeclaratively(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		stored, stated cardTrust
	}{
		{"the pin", cardTrust{pin: "v0_3"}, cardTrust{pin: "auto"}},
		{"the trusted key sets", cardTrust{pin: "auto", urls: []string{cardTrustKeySet}}, cardTrust{pin: "auto"}},
		{"the signature requirement", cardTrust{pin: "auto", urls: []string{cardTrustKeySet}, require: true}, cardTrust{pin: "auto", urls: []string{cardTrustKeySet}}},
		{"all three", cardTrust{pin: "v0_3", urls: []string{cardTrustKeySet}, require: true}, cardTrust{pin: "auto"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			const name = "trust_off_remote"
			applyCardTrust(t, svc, cardTrustComponent(name, tt.stored))

			if op := applyCardTrust(t, svc, cardTrustComponent(name, tt.stated)); op.Action != actionUpdate {
				t.Fatalf("action = %q changed = %v, want an update", op.Action, op.ChangedFields)
			}
			if got := storedCardTrust(t, svc, name); !got.equal(tt.stated) {
				t.Errorf("stored = %+v, want %+v", got, tt.stated)
			}
			if op := applyCardTrust(t, svc, cardTrustComponent(name, tt.stated)); op.Action != actionUnchanged {
				t.Errorf("second apply action = %q changed = %v, want unchanged", op.Action, op.ChangedFields)
			}
		})
	}
}

func TestApply_A2ACardTrustKeptWhenTheArtifactIsSilent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	const name = "trust_kept_remote"
	stored := cardTrust{pin: "v0_3", urls: []string{cardTrustKeySet}, require: true}
	applyCardTrust(t, svc, cardTrustComponent(name, stored))

	silent := a2aComponent(name, func(b map[string]any) { b["description"] = "A remote agent whose description changed." })
	for i, want := range []string{actionUpdate, actionUnchanged} {
		if op := applyCardTrust(t, svc, silent); op.Action != want {
			t.Errorf("silent apply %d action = %q changed = %v, want %q", i+1, op.Action, op.ChangedFields, want)
		}
		if got := storedCardTrust(t, svc, name); !got.equal(stored) {
			t.Errorf("after silent apply %d stored = %+v, want %+v", i+1, got, stored)
		}
	}
}
