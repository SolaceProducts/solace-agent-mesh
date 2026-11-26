// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestA2AAgentRepo_PinAndCardTrustRoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := t.Context()

	urls := `["https://keys.example.com/jwks.json"]`
	a := remoteFixture("pinned-agent")
	a.ProtocolVersion = "v0_3"
	a.TrustedCardJWKSURLs = &urls
	a.RequireSignedCard = true
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	expect := func(read string, got *A2AAgent, pin string, urls *string, require bool) {
		t.Helper()
		if got == nil {
			t.Fatalf("%s: not found", read)
		}
		if got.ProtocolVersion != pin || (got.TrustedCardJWKSURLs == nil) != (urls == nil) ||
			(urls != nil && *got.TrustedCardJWKSURLs != *urls) || got.RequireSignedCard != require {
			t.Errorf("%s = %q %v %v, want %q %v %v", read, got.ProtocolVersion, got.TrustedCardJWKSURLs, got.RequireSignedCard, pin, urls, require)
		}
	}

	byID, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect("GetByID", byID, "v0_3", &urls, true)
	byName, err := repo.GetByName(ctx, "pinned-agent")
	if err != nil {
		t.Fatal(err)
	}
	expect("GetByName", byName, "v0_3", &urls, true)
	listed, _, err := repo.ListPaginated(ctx, 1, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListPaginated = %v, %v", listed, err)
	}
	expect("ListPaginated", listed[0], "v0_3", &urls, true)

	byID.ProtocolVersion, byID.TrustedCardJWKSURLs, byID.RequireSignedCard = "auto", nil, false
	if updateErr := repo.Update(ctx, byID); updateErr != nil {
		t.Fatalf("update: %v", updateErr)
	}
	updated, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect("after Update", updated, "auto", nil, false)

	updated.ProtocolVersion, updated.TrustedCardJWKSURLs, updated.RequireSignedCard = "v1_0", &urls, true
	if updateErr := repo.Update(ctx, updated); updateErr != nil {
		t.Fatalf("update back to card trust: %v", updateErr)
	}
	trusted, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect("after Update back to card trust", trusted, "v1_0", &urls, true)
}

func TestA2AAgentRepo_RowWrittenBeforeCardTrustReadsTheDefaults(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := t.Context()
	const id = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"
	if _, err := db.ExecContext(ctx, `INSERT INTO a2a_agents (id, agent_url, name, agent_card_location, request_timeout_seconds, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'https://example.com/agent', 'legacy-agent', 'well_known', 120, 'u', 'u', 1, 1)`, id); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := repo.GetByID(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetByID = %v, %v", got, err)
	}
	if got.ProtocolVersion != "auto" || got.TrustedCardJWKSURLs != nil || got.RequireSignedCard {
		t.Errorf("legacy row = %q %v %v, want auto, NULL and false", got.ProtocolVersion, got.TrustedCardJWKSURLs, got.RequireSignedCard)
	}
}
