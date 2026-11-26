// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type systemUserRowsStub struct {
	ids map[string]string
	err error
}

func (s systemUserRowsStub) SystemUserID(_ context.Context, issuer, sub string) (string, error) {
	if issuer != rbac.SystemUserIssuer {
		return "", nil
	}
	return s.ids[sub], s.err
}

type recoveringSystemUserRows struct {
	failures atomic.Int32
	ids      map[string]string
}

func (r *recoveringSystemUserRows) SystemUserID(_ context.Context, _, sub string) (string, error) {
	if r.failures.Add(-1) >= 0 {
		return "", errors.New("db down")
	}
	return r.ids[sub], nil
}

func startSystemUserLookup(t *testing.T, ns string, rows systemUserRows) *auth.BrokerAuthorizationClient {
	t.Helper()
	return startSystemUserLookupWithConfig(t, AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute}, rows)
}

func startSystemUserLookupWithConfig(t *testing.T, cfg AuthzLookupConfig, rows systemUserRows) *auth.BrokerAuthorizationClient {
	t.Helper()
	brk := newTestBroker(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"nightly_reporter": {Scopes: []string{"agent:reporter:invoke"}},
		}},
		Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"system:nightly": {Roles: []string{"nightly_reporter"}},
		}},
	}, nil, logger)
	if err := composer.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := NewAuthzLookupHandler(composer, nil, brk, cfg, logger)
	h.SetSystemUserRows(rows)
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	return auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
		Namespace: cfg.Namespace, GatewayID: "webui", Timeout: time.Second,
	}, logger)
}

type systemUserRowReport struct {
	ID    string
	State auth.SystemUserRow
}

func TestAuthzLookup_ReportsTheSystemUsersRowState(t *testing.T) {
	const rowID = "0193a1c2-7d4e-7f00-8a00-0000000000cc"
	cases := []struct {
		name      string
		rows      systemUserRows
		principal string
		want      systemUserRowReport
	}{
		{"the row exists", systemUserRowsStub{ids: map[string]string{"system:nightly": rowID}}, "system:nightly", systemUserRowReport{rowID, auth.SystemUserRowFound}},
		{"there is no row", systemUserRowsStub{}, "system:nightly", systemUserRowReport{"", auth.SystemUserRowMissing}},
		{"the row lookup fails", systemUserRowsStub{err: errors.New("db down")}, "system:nightly", systemUserRowReport{"", auth.SystemUserRowUnavailable}},
		{"no row source is wired", nil, "system:nightly", systemUserRowReport{}},
		{"a person is not a service account", systemUserRowsStub{ids: map[string]string{"system:nightly": rowID}}, "alice@example.com", systemUserRowReport{}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := startSystemUserLookup(t, fmt.Sprintf("authz-system-row-%d", i), c.rows)

			res, err := client.Lookup(t.Context(), c.principal, map[string]any{"gateway_id": "webui"})
			if err != nil {
				t.Fatalf("Lookup(%q): %v", c.principal, err)
			}

			got := systemUserRowReport{res.SystemUserID, res.SystemUserRow}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("Lookup(%q) service account row mismatch (-want +got):\n%s", c.principal, diff)
			}
		})
	}
}

func TestAuthzLookup_StillCarriesTheSystemUsersScopes(t *testing.T) {
	client := startSystemUserLookup(t, "authz-system-scopes", systemUserRowsStub{err: errors.New("db down")})

	res, err := client.Lookup(t.Context(), "system:nightly", map[string]any{"gateway_id": "webui"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	if diff := cmp.Diff([]string{"agent:reporter:invoke"}, res.Scopes); diff != "" {
		t.Errorf("Lookup scopes mismatch (-want +got):\n%s", diff)
	}
}

func TestAuthzLookup_AFailedRowLookupIsCachedOnlyForTheDegradedTTL(t *testing.T) {
	const rowID = "0193a1c2-7d4e-7f00-8a00-0000000000cc"
	rows := &recoveringSystemUserRows{ids: map[string]string{"system:nightly": rowID}}
	rows.failures.Store(1)
	client := startSystemUserLookupWithConfig(t, AuthzLookupConfig{
		Namespace: "authz-system-degraded-ttl", CacheTTL: time.Minute, DegradedResolveTTL: 10 * time.Millisecond,
	}, rows)
	lookup := func() auth.SystemUserRow {
		res, err := client.Lookup(t.Context(), "system:nightly", map[string]any{"gateway_id": "webui"})
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		return res.SystemUserRow
	}

	if got := lookup(); got != auth.SystemUserRowUnavailable {
		t.Fatalf("first Lookup row state = %q, want %q", got, auth.SystemUserRowUnavailable)
	}

	deadline := time.Now().Add(5 * time.Second)
	got := lookup()
	for got != auth.SystemUserRowFound && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		got = lookup()
	}
	if got != auth.SystemUserRowFound {
		t.Errorf("Lookup row state after the degraded TTL = %q, want %q: the failed lookup stayed cached", got, auth.SystemUserRowFound)
	}
}

func TestAuthzLookup_AComposerFailureIsAnUnavailableLookupToTheGatewayClient(t *testing.T) {
	brk := newTestBroker(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composerNeverBuilt := rbac.NewComposer(rbac.YAMLState{}, nil, logger)
	h := NewAuthzLookupHandler(composerNeverBuilt, nil, brk, AuthzLookupConfig{Namespace: "authz-composer-failure", CacheTTL: time.Minute}, logger)
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	client := auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
		Namespace: "authz-composer-failure", GatewayID: "webui", Timeout: time.Second,
	}, logger)

	_, err := client.Lookup(t.Context(), "system:nightly", map[string]any{"gateway_id": "webui"})

	if !errors.Is(err, auth.ErrIdentityLookupUnavailable) {
		t.Errorf("Lookup after a composer failure error = %v, want the gateway client to class the platform's reply as an unavailable lookup, which a scheduled run waits for", err)
	}
}
