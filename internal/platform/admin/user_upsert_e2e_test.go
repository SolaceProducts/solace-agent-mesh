// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestUserUpsert_EndToEnd_ClientToHandlerToStore(t *testing.T) {
	const ns = "users-e2e"
	db := store.TestDB(t)
	repo := store.NewUsersRepo(db)
	brk := newTestBroker(t)
	startUserUpsertHandler(t, brk, repo, ns)

	client := auth.NewBrokerUserUpsertClient(brk, auth.BrokerUserUpsertClientConfig{
		Namespace:   ns,
		GatewayID:   "webui",
		Timeout:     2 * time.Second,
		MaxAttempts: 2,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := client.Upsert(context.Background(), auth.UserRecord{
		Issuer:       "https://idp.example.com",
		Sub:          "sub-e2e",
		OIDCProvider: "azure",
		DisplayName:  "E2E User",
		UserName:     "e2e",
		Email:        "e2e@example.com",
		Groups:       []string{"engineering"},
		Source:       `{"sub":"sub-e2e"}`,
	}); err != nil {
		t.Fatalf("client.Upsert over broker: %v", err)
	}

	got, groups, err := repo.GetByIssuerSub(context.Background(), "https://idp.example.com", "sub-e2e")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("no row persisted via the real client -> broker -> handler -> store path")
	}
	if got.OIDCProvider != "azure" || got.Email == nil || *got.Email != "e2e@example.com" {
		t.Errorf("unexpected persisted record: %+v", got)
	}
	if got.DisplayName == nil || *got.DisplayName != "E2E User" || got.UserName == nil || *got.UserName != "e2e" {
		t.Errorf("display_name/user_name did not round-trip: displayName=%v userName=%v", got.DisplayName, got.UserName)
	}
	if got.LastLoginAt == nil || *got.LastLoginAt == 0 {
		t.Errorf("last_login_at = %v, want non-zero (set at upsert)", got.LastLoginAt)
	}
	if len(groups) != 1 || groups[0] != "engineering" {
		t.Errorf("groups = %v, want [engineering]", groups)
	}
}
