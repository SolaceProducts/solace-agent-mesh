// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func initedCore(t *testing.T, gatewayID string, withDB bool) *entrypoint.GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	cfg := map[string]any{
		"namespace":          "default",
		"gateway_id":         gatewayID,
		"session_secret_key": "test-secret",
		"adapter_config": map[string]any{
			"transport":              "sse",
			"enable_auth":            true,
			"external_auth_provider": "fake",
			"issuer":                 "https://mcp.example.com",
		},
	}
	if withDB {
		cfg["session_service"] = map[string]any{
			"type":         "sql",
			"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "gw.db"),
		}
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, cfg)
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core
}

func TestOAuthRecordsUseTheGatewayDatabase(t *testing.T) {
	a := &Adapter{}
	repo, box, err := a.oauthRecords(initedCore(t, "mcp-records-db", true))
	if err != nil || repo == nil || box == nil {
		t.Fatalf("with a database: repo=%v box=%v err=%v", repo, box, err)
	}
	repo, box, err = a.oauthRecords(initedCore(t, "mcp-records-mem", false))
	if err != nil || repo != nil || box != nil {
		t.Fatalf("without a database: repo=%v box=%v err=%v", repo, box, err)
	}
}

func TestInitStoresLoginStateInTheGatewayDatabase(t *testing.T) {
	const gw = "mcp-init-records-gw"
	auth.RegisterValidatorFactory(gw+":fake", func(map[string]any) (auth.TokenValidator, error) {
		return auth.TokenValidatorFunc(func(_ context.Context, _ string) (*auth.Subject, error) {
			return auth.NewTestSubject(auth.SubjectFields{}), nil
		}), nil
	})
	core := initedCore(t, gw, true)
	a := &Adapter{}
	a.SetAuthDeps(&MCPAuthDeps{IDPClient: fakeMcpauthIDPClient{}, SAMMinter: fakeMcpauthMinter{}, AuthzResolver: fakeMcpauthResolver{}})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	q := url.Values{
		"redirect_uri":          {"http://localhost:9000/cb"},
		"code_challenge":        {mcpauth.ComputeS256Challenge("v")},
		"code_challenge_method": {"S256"},
	}
	rr := httptest.NewRecorder()
	a.mcpauthHandler.HandleAuthorize(rr, httptest.NewRequest(http.MethodGet, mcpauth.PathAuthorize+"?"+q.Encode(), nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("authorize = %d %s", rr.Code, rr.Body.String())
	}
	var n int
	if err := core.Store().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mcp_oauth_state WHERE gateway_id = ?`, gw).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("login states in the gateway database = %d, want 1", n)
	}
}
