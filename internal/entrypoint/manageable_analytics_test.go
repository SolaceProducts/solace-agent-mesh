// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestIdentityHasScope_Tiers(t *testing.T) {
	cases := []struct {
		name     string
		identity *auth.Identity
		want     bool
	}{
		{"nil identity (community)", nil, true},
		{"empty scopes (community)", &auth.Identity{}, true},
		{"read only — no content", &auth.Identity{Scopes: []string{rbac.ScopeAnalyticsRead}}, false},
		{"explicit content scope", &auth.Identity{Scopes: []string{rbac.ScopeAnalyticsRead, rbac.ScopeAnalyticsReadContent}}, true},
		{"analytics wildcard", &auth.Identity{Scopes: []string{"analytics:_:*"}}, true},
		{"super-admin wildcard", &auth.Identity{Scopes: []string{"*"}}, true},
		{"unrelated scope", &auth.Identity{Scopes: []string{"tool:basic:read"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := identityHasScope(tc.identity, rbac.ScopeAnalyticsReadContent, false); got != tc.want {
				t.Errorf("identityHasScope = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIdentityHasScope_AuthzEnabled_FailsClosed(t *testing.T) {
	cases := []struct {
		name     string
		identity *auth.Identity
		required string
		want     bool
	}{
		{"nil identity denied (base)", nil, rbac.ScopeAnalyticsRead, false},
		{"empty scopes denied (base)", &auth.Identity{}, rbac.ScopeAnalyticsRead, false},
		{"nil identity denied (content)", nil, rbac.ScopeAnalyticsReadContent, false},
		{"base scope grants base", &auth.Identity{Scopes: []string{rbac.ScopeAnalyticsRead}}, rbac.ScopeAnalyticsRead, true},
		{"base scope no content", &auth.Identity{Scopes: []string{rbac.ScopeAnalyticsRead}}, rbac.ScopeAnalyticsReadContent, false},
		{"content scope grants content", &auth.Identity{Scopes: []string{rbac.ScopeAnalyticsReadContent}}, rbac.ScopeAnalyticsReadContent, true},
		{"super-admin wildcard", &auth.Identity{Scopes: []string{"*"}}, rbac.ScopeAnalyticsReadContent, true},
		{"unrelated scope denied", &auth.Identity{Scopes: []string{"tool:basic:read"}}, rbac.ScopeAnalyticsRead, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := identityHasScope(tc.identity, tc.required, true); got != tc.want {
				t.Errorf("identityHasScope(authzEnabled=true) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnalyticsRequest_ToQuery_Validation(t *testing.T) {
	t.Run("valid normalizes", func(t *testing.T) {
		q, err := analyticsRequest{ExecutionMode: "background", PageNumber: 0, PageSize: 0, GroupBy: "user"}.toQuery()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.PageNumber != 1 {
			t.Errorf("pageNumber = %d, want 1 (default)", q.PageNumber)
		}
		if q.PageSize != defaultAnalyticsPageSize {
			t.Errorf("pageSize = %d, want default %d", q.PageSize, defaultAnalyticsPageSize)
		}
		if q.ExecutionMode != "background" || q.GroupBy != "user" {
			t.Errorf("got executionMode=%q groupBy=%q", q.ExecutionMode, q.GroupBy)
		}
	})

	t.Run("pageSize over limit errors", func(t *testing.T) {
		if _, err := (analyticsRequest{PageSize: maxAnalyticsPageSize + 1}).toQuery(); err == nil {
			t.Error("expected error for pageSize over the limit")
		}
	})

	t.Run("pageSize negative errors", func(t *testing.T) {
		if _, err := (analyticsRequest{PageSize: -1}).toQuery(); err == nil {
			t.Error("expected error for negative pageSize")
		}
	})

	t.Run("pageSize at limit ok", func(t *testing.T) {
		q, err := analyticsRequest{PageSize: maxAnalyticsPageSize}.toQuery()
		if err != nil || q.PageSize != maxAnalyticsPageSize {
			t.Errorf("pageSize=%d err=%v, want %d/nil", q.PageSize, err, maxAnalyticsPageSize)
		}
	})

	t.Run("invalid executionMode errors", func(t *testing.T) {
		if _, err := (analyticsRequest{ExecutionMode: "bogus"}).toQuery(); err == nil {
			t.Error("expected error for invalid executionMode")
		}
	})

	t.Run("invalid groupBy errors", func(t *testing.T) {
		if _, err := (analyticsRequest{GroupBy: "bogus"}).toQuery(); err == nil {
			t.Error("expected error for invalid groupBy")
		}
	})

	t.Run("empty enums ok", func(t *testing.T) {
		if _, err := (analyticsRequest{}).toQuery(); err != nil {
			t.Errorf("empty request should be valid, got %v", err)
		}
	})
}
