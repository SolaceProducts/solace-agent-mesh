// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
)

func newGatewayServiceForSlugTest(t *testing.T) *GatewayService {
	t.Helper()
	db := testOpenDB(t)
	return NewGatewayService(
		store.NewGatewayRepo(db),
		store.NewGatewayDeploymentRepo(db),
		nil,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func slackValues() map[string]any {
	return map[string]any{
		"bot_token": "xoxb-test",
		"app_token": "xapp-test",
	}
}

func TestGatewayService_Create_ExplicitSlug(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForSlugTest(t)

	resp, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "Explicit Slug Gateway",
		Slug:        "my-mcp",
		Description: "Test gateway with an explicit slug",
		Type:        "slack",
		Values:      slackValues(),
	}, "test-user")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.Slug != "my-mcp" {
		t.Fatalf("response slug = %q, want %q", resp.Slug, "my-mcp")
	}

	got, err := svc.GetByID(t.Context(), resp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Slug != "my-mcp" {
		t.Fatalf("GetByID slug = %q, want %q", got.Slug, "my-mcp")
	}
}

func TestGatewayService_Create_OmittedSlugStaysUnset(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForSlugTest(t)

	resp, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "My MCP Gateway",
		Description: "Test gateway without an explicit slug",
		Type:        "slack",
		Values:      slackValues(),
	}, "test-user")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.Slug != "" {
		t.Fatalf("expected omitted slug to stay unset, got %q", resp.Slug)
	}

	got, err := svc.GetByID(t.Context(), resp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Slug != "" {
		t.Fatalf("read-back slug = %q, want empty (unset)", got.Slug)
	}
}

func TestGatewayService_Create_SlugCollisionReturns409(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForSlugTest(t)

	if _, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "First Gateway",
		Slug:        "shared-slug",
		Description: "First gateway, holds the slug",
		Type:        "slack",
		Values:      slackValues(),
	}, "test-user"); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "Second Gateway",
		Slug:        "shared-slug",
		Description: "Second gateway collides on slug",
		Type:        "slack",
		Values:      slackValues(),
	}, "test-user")
	if err == nil {
		t.Fatal("expected slug-conflict error, got nil")
	}
	conflict, ok := errors.AsType[*ConflictError](err)
	if !ok {
		t.Fatalf("expected *ConflictError, got %T: %v", err, err)
	}
	if !strings.Contains(conflict.Message, "shared-slug") {
		t.Fatalf("conflict message %q should mention slug %q", conflict.Message, "shared-slug")
	}
	if !strings.Contains(conflict.Message, "slug") {
		t.Fatalf("conflict message %q should mention 'slug' (not 'name')", conflict.Message)
	}
}

func TestGatewayService_Create_NameCollisionStillReturns409_WithDifferentSlug(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForSlugTest(t)

	if _, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "Duplicate Name",
		Slug:        "slug-one",
		Description: "Holds the name",
		Type:        "slack",
		Values:      slackValues(),
	}, "test-user"); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "Duplicate Name",
		Slug:        "slug-two",
		Description: "Collides on name only",
		Type:        "slack",
		Values:      slackValues(),
	}, "test-user")
	if err == nil {
		t.Fatal("expected name-conflict error, got nil")
	}
	conflict, ok := errors.AsType[*ConflictError](err)
	if !ok {
		t.Fatalf("expected *ConflictError, got %T: %v", err, err)
	}
	if !strings.Contains(conflict.Message, "Duplicate Name") {
		t.Fatalf("conflict message %q should mention the name", conflict.Message)
	}
	if strings.Contains(conflict.Message, "slug") {
		t.Fatalf("name-conflict message %q must not mention 'slug'", conflict.Message)
	}
}
