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

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func netSvcNoopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func createGateway(t *testing.T, repo *store.GatewayRepo, gwType string, dnsID *string) string {
	t.Helper()
	desc := "test gateway fixture"
	vals := `{}`
	gw := &store.Gateway{
		Name:        "gw-" + gwType,
		Description: &desc,
		Type:        gwType,
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
		DNSID:       dnsID,
	}
	if err := repo.Create(context.Background(), gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return gw.ID
}

func TestGetNetworking_TeamsSharedIngress(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	id := createGateway(t, repo, "teams", nil)

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for teams gateway")
	}
	wantURL := "/gw/" + id
	if resp.URL != wantURL {
		t.Errorf("URL = %q, want %q (UI prepends window.location.origin)", resp.URL, wantURL)
	}
	wantWebhook := wantURL + "/api/messages"
	if resp.WebhookURL != wantWebhook {
		t.Errorf("WebhookURL = %q, want %q", resp.WebhookURL, wantWebhook)
	}
	if len(resp.APIPaths) != 2 || resp.APIPaths[0].Path != "/api/messages" {
		t.Errorf("APIPaths = %+v, want /api/messages first", resp.APIPaths)
	}
}

func TestGetNetworking_TeamsCloud(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "sam.example.com", "mesh1", netSvcNoopLogger())

	dnsID := "abc12345"
	id := createGateway(t, repo, "teams", &dnsID)

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	wantDNS := "gw-abc12345-mesh1.sam.example.com"
	if resp.DNSName != wantDNS {
		t.Errorf("DNSName = %q, want %q", resp.DNSName, wantDNS)
	}
	wantURL := "https://" + wantDNS
	if resp.URL != wantURL {
		t.Errorf("URL = %q, want %q", resp.URL, wantURL)
	}
	wantWebhook := wantURL + "/api/messages"
	if resp.WebhookURL != wantWebhook {
		t.Errorf("WebhookURL = %q, want %q", resp.WebhookURL, wantWebhook)
	}
}

func TestGetNetworking_MCPRequiresIngress(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	id := createGateway(t, repo, "mcp", nil)

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for mcp gateway (mcp now requires ingress)")
	}
	wantWebhook := "/gw/" + id
	if resp.WebhookURL != wantWebhook {
		t.Errorf("WebhookURL = %q, want %q", resp.WebhookURL, wantWebhook)
	}
	if len(resp.APIPaths) != 1 || resp.APIPaths[0].Path != "" {
		t.Errorf("APIPaths = %+v, want a single root-mount entry", resp.APIPaths)
	}
}

func TestGetNetworking_WhatsAppRequiresIngress(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	id := createGateway(t, repo, "whatsapp", nil)

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for whatsapp gateway (inbound Meta webhook)")
	}
	wantWebhook := "/gw/" + id + "/webhook"
	if resp.WebhookURL != wantWebhook {
		t.Errorf("WebhookURL = %q, want %q", resp.WebhookURL, wantWebhook)
	}
	if len(resp.APIPaths) != 1 || resp.APIPaths[0].Path != "/webhook" {
		t.Errorf("APIPaths = %+v, want /webhook", resp.APIPaths)
	}
}

func TestGetNetworking_NoIngressType(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	id := createGateway(t, repo, "event_mesh", nil)

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response for event_mesh (no ingress), got %+v", resp)
	}
}

func TestGetNetworking_SlackNoIngress(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	id := createGateway(t, repo, "slack", nil)

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp != nil {
		t.Errorf("slack must not expose ingress (Socket Mode), got %+v", resp)
	}
}

func TestGetNetworking_TeamsPublicURLSurfaced(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	id := createGateway(t, repo, "teams", nil)

	gw, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	publicURL := "https://teams.example.com/api/messages"
	gw.PublicURL = &publicURL
	if updateErr := repo.Update(context.Background(), gw); updateErr != nil {
		t.Fatalf("update: %v", updateErr)
	}

	resp, err := svc.GetNetworking(context.Background(), id)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response for teams gateway")
	}
	if resp.PublicURL != publicURL {
		t.Errorf("PublicURL = %q, want %q", resp.PublicURL, publicURL)
	}
}

func TestGetNetworking_WebhookSharedIngress(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	desc := "webhook gateway"
	vals := `{"routePath":"/orders"}`
	gw := &store.Gateway{
		Name:        "gw-webhook",
		Description: &desc,
		Type:        "webhook",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(context.Background(), gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	resp, err := svc.GetNetworking(context.Background(), gw.ID)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for webhook gateway")
	}
	wantWebhook := "/gw/" + gw.ID + "/orders"
	if resp.WebhookURL != wantWebhook {
		t.Errorf("WebhookURL = %q, want %q", resp.WebhookURL, wantWebhook)
	}
	if len(resp.APIPaths) != 1 || resp.APIPaths[0].Path != "/orders" {
		t.Errorf("APIPaths = %+v, want single entry with path /orders", resp.APIPaths)
	}
}

func TestGetNetworking_WebhookNoValues(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	desc := "webhook gateway"
	emptyVals := "{}"
	gw := &store.Gateway{
		Name:        "gw-wh-novals",
		Description: &desc,
		Type:        "webhook",
		Values:      &emptyVals,
		CreatedBy:   "u1",
		UpdatedBy:   "u1",
	}
	if err := repo.Create(context.Background(), gw); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := svc.GetNetworking(context.Background(), gw.ID)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for webhook gateway")
	}
	want := "/gw/" + gw.ID + "/"
	if resp.WebhookURL != want {
		t.Errorf("WebhookURL = %q, want %q", resp.WebhookURL, want)
	}
}

func TestGetNetworking_WebhookMalformedValues(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayNetworkingService(repo, "", "", netSvcNoopLogger())

	desc := "webhook gateway"
	vals := `not valid json{`
	gw := &store.Gateway{
		Name:        "gw-wh-badvals",
		Description: &desc,
		Type:        "webhook",
		Values:      &vals,
		CreatedBy:   "u1",
		UpdatedBy:   "u1",
	}
	if err := repo.Create(context.Background(), gw); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := svc.GetNetworking(context.Background(), gw.ID)
	if err != nil {
		t.Fatalf("GetNetworking: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for webhook gateway")
	}
	want := "/gw/" + gw.ID + "/"
	if resp.WebhookURL != want {
		t.Errorf("WebhookURL = %q, want %q (should fall back to / on parse error)", resp.WebhookURL, want)
	}
}

func TestBuildWebhookURL(t *testing.T) {
	if got := buildWebhookURL("", []GatewayAPIPath{{Path: "/x"}}); got != "" {
		t.Errorf("empty base URL should produce empty webhook, got %q", got)
	}
	if got := buildWebhookURL("http://host", nil); got != "" {
		t.Errorf("nil paths should produce empty webhook, got %q", got)
	}
	if got := buildWebhookURL("http://host", []GatewayAPIPath{{Path: "/api/messages"}}); got != "http://host/api/messages" {
		t.Errorf("buildWebhookURL = %q, want http://host/api/messages", got)
	}
}
