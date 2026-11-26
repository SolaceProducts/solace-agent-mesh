// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedToolPackage(t *testing.T, db *store.DB, name, status, capsJSON string, toolNames ...string) string {
	t.Helper()
	repo := store.NewToolPackageRepo(db)
	desc := name + " desc"
	pkg := &store.ToolPackage{
		Name:            name,
		Description:     &desc,
		DiscoveryStatus: status,
		Capabilities:    capsJSON,
		CreatedBy:       "fixture",
		UpdatedBy:       "fixture",
	}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("seed tool package: %v", err)
	}
	for _, tn := range toolNames {
		desc := tn + " desc"
		if err := repo.UpsertToolFile(t.Context(), &store.ToolPackageFile{
			PackageID:       pkg.ID,
			ToolName:        tn,
			ToolDescription: &desc,
			TimeoutSeconds:  30,
			SandboxProfile:  "default",
		}); err != nil {
			t.Fatalf("seed tool file %s: %v", tn, err)
		}
	}
	return pkg.ID
}

func newIdentityCfgSvc(t *testing.T) (*IdentityConfigService, *store.DB) {
	t.Helper()
	db := testOpenDB(t)
	return NewIdentityConfigService(
		store.NewPlatformIdentityConfigRepo(db),
		store.NewToolPackageRepo(db),
		slog.Default(),
	), db
}

func TestIdentityConfigService_GetUnset(t *testing.T) {
	t.Parallel()
	svc, _ := newIdentityCfgSvc(t)
	resp, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.ToolsetID != nil {
		t.Errorf("toolsetID = %v, want nil", resp.ToolsetID)
	}
	if resp.LookupKey != defaultLookupKey {
		t.Errorf("lookupKey = %q, want %q", resp.LookupKey, defaultLookupKey)
	}
}

func TestIdentityConfigService_SetHappyPath(t *testing.T) {
	t.Parallel()
	svc, db := newIdentityCfgSvc(t)
	id := seedToolPackage(t, db, "provider", "ready", `{"profileProvider": true}`, "enrich_authenticated_user", "search_users")

	resp, err := svc.Set(context.Background(), UpdateProfileProviderRequest{ToolsetID: id, LookupKey: "preferred_username"}, "alice@example.com")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if resp.ToolsetID == nil || *resp.ToolsetID != id {
		t.Errorf("toolsetID = %v, want %q", resp.ToolsetID, id)
	}
	if resp.LookupKey != "preferred_username" {
		t.Errorf("lookupKey = %q, want preferred_username", resp.LookupKey)
	}
	if resp.UpdatedBy != "alice@example.com" {
		t.Errorf("updatedBy = %q", resp.UpdatedBy)
	}
}

func TestIdentityConfigService_SetDefaultsLookupKey(t *testing.T) {
	t.Parallel()
	svc, db := newIdentityCfgSvc(t)
	id := seedToolPackage(t, db, "provider", "ready", `{"profileProvider": true}`, "enrich_authenticated_user")

	resp, err := svc.Set(context.Background(), UpdateProfileProviderRequest{ToolsetID: id}, "alice@example.com")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if resp.LookupKey != defaultLookupKey {
		t.Errorf("lookupKey = %q, want %q (default applied)", resp.LookupKey, defaultLookupKey)
	}
}

func TestIdentityConfigService_SetPreservesLookupKey(t *testing.T) {
	t.Parallel()
	svc, db := newIdentityCfgSvc(t)
	id := seedToolPackage(t, db, "provider", "ready", `{"profileProvider": true}`, "enrich_authenticated_user")

	if _, err := svc.Set(context.Background(), UpdateProfileProviderRequest{ToolsetID: id, LookupKey: "preferred_username"}, "alice@example.com"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	resp, err := svc.Set(context.Background(), UpdateProfileProviderRequest{ToolsetID: id}, "bob@example.com")
	if err != nil {
		t.Fatalf("second Set: %v", err)
	}
	if resp.LookupKey != "preferred_username" {
		t.Errorf("lookupKey = %q, want preferred_username (should be preserved when omitted)", resp.LookupKey)
	}
}

func TestIdentityConfigService_SetRejectsCases(t *testing.T) {
	t.Parallel()
	svc, db := newIdentityCfgSvc(t)
	ready := seedToolPackage(t, db, "ready-no-tool", "ready", `{"profileProvider": true}`)
	pending := seedToolPackage(t, db, "pending-tset", "pending", `{"profileProvider": true}`)
	noCap := seedToolPackage(t, db, "no-cap", "ready", `{}`, "enrich_authenticated_user")
	wrongCap := seedToolPackage(t, db, "wrong-cap", "ready", `{"profileProvider": false}`, "enrich_authenticated_user")

	cases := []struct {
		name      string
		req       UpdateProfileProviderRequest
		wantField string
		wantSub   string
	}{
		{"missing toolsetID", UpdateProfileProviderRequest{}, "toolsetId", "required"},
		{"unknown toolset", UpdateProfileProviderRequest{ToolsetID: idgen.New()}, "toolsetId", "not found"},
		{"not ready", UpdateProfileProviderRequest{ToolsetID: pending}, "toolsetId", "pending"},
		{"missing capability", UpdateProfileProviderRequest{ToolsetID: noCap}, "toolsetId", "profileProvider"},
		{"false capability", UpdateProfileProviderRequest{ToolsetID: wrongCap}, "toolsetId", "profileProvider"},
		{"missing required tool", UpdateProfileProviderRequest{ToolsetID: ready}, "toolsetId", "enrich_authenticated_user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Set(context.Background(), tc.req, "alice@example.com")
			if err == nil {
				t.Fatalf("expected validation error")
			}
			verr, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("err type = %T (%v), want *ValidationError", err, err)
			}
			if verr.Details == nil || len(verr.Details[tc.wantField]) == 0 {
				t.Fatalf("expected details[%s], got %+v", tc.wantField, verr.Details)
			}
			if !detailsContain(verr.Details[tc.wantField], tc.wantSub) {
				t.Errorf("error details = %+v; expected substring %q", verr.Details, tc.wantSub)
			}
		})
	}
}

func TestIdentityConfigService_Clear(t *testing.T) {
	t.Parallel()
	svc, db := newIdentityCfgSvc(t)
	id := seedToolPackage(t, db, "provider", "ready", `{"profileProvider": true}`, "enrich_authenticated_user")

	if _, err := svc.Set(context.Background(), UpdateProfileProviderRequest{ToolsetID: id}, "alice@example.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	resp, err := svc.Clear(context.Background(), "bob@example.com")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if resp.ToolsetID != nil {
		t.Errorf("toolsetID after Clear = %v, want nil", resp.ToolsetID)
	}
	if resp.UpdatedBy != "bob@example.com" {
		t.Errorf("updatedBy = %q, want bob@example.com", resp.UpdatedBy)
	}
}

func detailsContain(details []string, sub string) bool {
	for _, s := range details {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
