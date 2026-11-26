// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newGatewayServiceForPublicURLTest(t *testing.T, gwType string) (*GatewayService, string) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewGatewayRepo(db)
	depRepo := store.NewGatewayDeploymentRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewGatewayService(repo, depRepo, nil, nil, nil, logger)

	values := map[string]any{}
	switch gwType {
	case "slack":
		values = map[string]any{"bot_token": "xoxb-test", "app_token": "xapp-test"}
	case "teams":
		values = map[string]any{
			"microsoft_app_id":       "12345678-1234-1234-1234-123456789abc",
			"microsoft_app_password": "secret",
		}
	}
	req := CreateGatewayRequest{
		Name:        "pub-url-" + gwType,
		Description: "Gateway for public URL testing",
		Type:        gwType,
		Values:      values,
	}
	resp, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return svc, resp.ID
}

func TestUpdateGateway_PublicURL_Set(t *testing.T) {
	svc, id := newGatewayServiceForPublicURLTest(t, "teams")

	req := UpdateGatewayRequest{PublicURL: new("https://teams.example.com/api/messages")}
	resp, err := svc.Update(context.Background(), id, req, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if resp.PublicURL != "https://teams.example.com/api/messages" {
		t.Errorf("PublicURL = %q, want %q", resp.PublicURL, "https://teams.example.com/api/messages")
	}

	got, err := svc.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PublicURL != "https://teams.example.com/api/messages" {
		t.Errorf("persisted PublicURL = %q", got.PublicURL)
	}
}

func TestUpdateGateway_PublicURL_Clear_EmptyString(t *testing.T) {
	svc, id := newGatewayServiceForPublicURLTest(t, "teams")

	if _, err := svc.Update(context.Background(), id, UpdateGatewayRequest{PublicURL: new("https://teams.example.com")}, "user1"); err != nil {
		t.Fatalf("set: %v", err)
	}

	resp, err := svc.Update(context.Background(), id, UpdateGatewayRequest{PublicURL: new("")}, "user1")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if resp.PublicURL != "" {
		t.Errorf("PublicURL after clear = %q, want empty", resp.PublicURL)
	}

	got, err := svc.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PublicURL != "" {
		t.Errorf("persisted PublicURL after clear = %q, want empty", got.PublicURL)
	}
}

func TestUpdateGateway_PublicURL_Preserve_OmittedField(t *testing.T) {
	svc, id := newGatewayServiceForPublicURLTest(t, "teams")

	if _, err := svc.Update(context.Background(), id, UpdateGatewayRequest{PublicURL: new("https://a.example.com")}, "user1"); err != nil {
		t.Fatalf("set: %v", err)
	}

	resp, err := svc.Update(context.Background(), id, UpdateGatewayRequest{Name: "new-name"}, "user2")
	if err != nil {
		t.Fatalf("update name: %v", err)
	}
	if resp.Name != "new-name" {
		t.Errorf("Name = %q, want new-name", resp.Name)
	}
	if resp.PublicURL != "https://a.example.com" {
		t.Errorf("PublicURL preserved = %q, want https://a.example.com", resp.PublicURL)
	}
}

func TestCreateGateway_PublicURL_HttpsOnly(t *testing.T) {
	svc, _ := newGatewayServiceForPublicURLTest(t, "slack")

	for _, c := range []string{"http://teams.example.com", "ftp://example.com", "not-a-url", "https://"} {
		t.Run(c, func(t *testing.T) {
			_, err := svc.Create(t.Context(), CreateGatewayRequest{
				Name: "pub-url-create", Description: "Gateway for public URL testing", Type: "slack",
				Values:    map[string]any{"bot_token": "xoxb-test", "app_token": "xapp-test"},
				PublicURL: c,
			}, "user1")
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v (%T), want *ValidationError", err, err)
			}
		})
	}

	resp, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name: "pub-url-create", Description: "Gateway for public URL testing", Type: "slack",
		Values:    map[string]any{"bot_token": "xoxb-test", "app_token": "xapp-test"},
		PublicURL: "https://hooks.example.com/slack",
	}, "user1")
	if err != nil {
		t.Fatalf("create with https publicUrl: %v", err)
	}
	if resp.PublicURL != "https://hooks.example.com/slack" {
		t.Errorf("PublicURL = %q, want https://hooks.example.com/slack", resp.PublicURL)
	}
}

func TestUpdateGateway_PublicURL_HttpsOnly(t *testing.T) {
	svc, id := newGatewayServiceForPublicURLTest(t, "teams")

	cases := []string{
		"http://teams.example.com",
		"ftp://example.com",
		"not-a-url",
		"javascript:alert('x')",
		"file:///etc/passwd",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, err := svc.Update(context.Background(), id, UpdateGatewayRequest{PublicURL: &c}, "user1")
			if err == nil {
				t.Fatalf("expected ValidationError for %q, got nil", c)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %T, want *ValidationError", err)
			}
		})
	}
}
