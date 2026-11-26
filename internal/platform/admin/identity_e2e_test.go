// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

var buildIdentityTestProvider = sync.OnceValues(func() (string, error) {
	pkgDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get the package directory: %w", err)
	}
	srcDir := filepath.Join(pkgDir, "..", "..", "identity", "testdata", "test-provider")
	tmpDir, err := os.MkdirTemp("", "identity-e2e-provider-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	binPath := filepath.Join(tmpDir, "test-provider")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("go build: %w\n%s", buildErr, out)
	}
	return binPath, nil
})

func identityTestProviderBinary(t *testing.T) string {
	t.Helper()
	path, err := buildIdentityTestProvider()
	if err != nil {
		t.Fatalf("build test-provider: %v", err)
	}
	return path
}

type e2eIdentityHarness struct {
	t           *testing.T
	ctx         context.Context
	namespace   string
	db          *store.DB
	broker      broker.Broker
	rt          *runtime.Runtime
	worker      *str.Worker
	cfgRepo     *store.PlatformIdentityConfigRepo
	pkgRepo     *store.ToolPackageRepo
	authzClient *auth.BrokerAuthorizationClient
	cfgSvc      *IdentityConfigService

	profileCache  *auth.ProfileCache
	gatewayServer *httptest.Server
	lookupCount   *atomic.Int64
}

func setupE2EIdentity(t *testing.T) *e2eIdentityHarness {
	t.Helper()
	ctx := t.Context()

	const namespace = "identity-http-e2e"
	logger := slog.Default()

	db := testOpenDB(t)

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(map[string]any{"namespace": namespace}),
		runtime.WithLogger(logger),
	)

	toolBin := identityTestProviderBinary(t)
	manifestPath := writeIdentityE2EManifest(t, "test-provider-e2e", toolBin)
	worker := str.NewWorker(rt.Services(), str.WorkerConfig{
		WorkerID:      "identity-e2e",
		Namespace:     namespace,
		ManifestPaths: []string{manifestPath},
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 4,
		},
	})
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("worker init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("worker start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	cfgRepo := store.NewPlatformIdentityConfigRepo(db)
	pkgRepo := store.NewToolPackageRepo(db)
	cfgSvc := NewIdentityConfigService(cfgRepo, pkgRepo, logger)

	enricher := identity.NewSTREnricher(brk, namespace, cfgRepo, pkgRepo, pkgRepo, nil, logger)
	if err := enricher.Start(ctx); err != nil {
		t.Fatalf("enricher start: %v", err)
	}
	t.Cleanup(func() { _ = enricher.Stop(context.Background()) })
	identitySvc := identity.New(identity.NewRepoConfigSource(cfgRepo), nil, enricher, logger)

	composerYAML := rbac.YAMLState{
		Roles:        rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{"sam_user": {Scopes: []string{"agent_builder:*:read"}}}},
		Users:        rbac.UsersConfig{},
		DefaultRoles: []string{"sam_user"},
	}
	composer := rbac.NewComposer(composerYAML, nil, logger)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}

	lookupHandler := NewAuthzLookupHandler(composer, identitySvc, brk, AuthzLookupConfig{
		Namespace: namespace,
		CacheTTL:  time.Minute,
	}, logger)
	if err := lookupHandler.Start(ctx); err != nil {
		t.Fatalf("lookup handler start: %v", err)
	}
	t.Cleanup(func() { _ = lookupHandler.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	authzClient := auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
		Namespace: namespace,
		GatewayID: "test-gw",
	}, logger)

	profileCache := auth.NewProfileCache(time.Hour)
	var lookupCount atomic.Int64
	lazyLookup := func(ctx context.Context, id *auth.Subject) (auth.CachedProfile, error) {
		lookupCount.Add(1)
		res, lookupErr := authzClient.Lookup(ctx, id.UserID(), map[string]any{
			"gateway_id": "test-gw",
			"idp_claims": map[string]any{"email": id.Email()},
		})
		if lookupErr != nil {
			return auth.CachedProfile{}, lookupErr
		}
		return auth.CachedProfile{
			Profile:          res.Profile,
			EnrichmentStatus: res.EnrichmentStatus,
			EnrichmentReason: res.EnrichmentReason,
		}, nil
	}
	noopProvider := auth.NewNoopProvider("sam_dev_user", "")
	gatewayMux := http.NewServeMux()
	gatewayMux.HandleFunc("GET /echo", echoIdentity)
	gatewayHandler := noopProvider.Middleware()(
		auth.IdentityMiddleware(profileCache, logger, auth.WithLazyEnrichmentLookup(lazyLookup))(gatewayMux),
	)
	gatewayServer := httptest.NewServer(gatewayHandler)
	t.Cleanup(gatewayServer.Close)

	return &e2eIdentityHarness{
		t:             t,
		ctx:           ctx,
		namespace:     namespace,
		db:            db,
		broker:        brk,
		rt:            rt,
		worker:        worker,
		cfgRepo:       cfgRepo,
		pkgRepo:       pkgRepo,
		authzClient:   authzClient,
		cfgSvc:        cfgSvc,
		profileCache:  profileCache,
		gatewayServer: gatewayServer,
		lookupCount:   &lookupCount,
	}
}

func echoIdentity(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil {
		http.Error(w, "no identity in context", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(echoedSubject{
		UserID:           id.UserID(),
		Email:            id.Email(),
		Name:             id.Name(),
		AuthMethod:       id.AuthMethod(),
		Authenticated:    id.Authenticated(),
		Roles:            id.Roles(),
		Scopes:           id.Scopes(),
		Profile:          id.Profile(),
		EnrichmentStatus: id.EnrichmentStatus(),
		EnrichmentReason: id.EnrichmentReason(),
	})
}

type echoedSubject struct {
	UserID           string                    `json:"userId"`
	Email            string                    `json:"email"`
	Name             string                    `json:"name"`
	AuthMethod       string                    `json:"authMethod"`
	Authenticated    bool                      `json:"authenticated"`
	Roles            []string                  `json:"roles"`
	Scopes           []string                  `json:"scopes"`
	Profile          *identity.Profile         `json:"profile"`
	EnrichmentStatus identity.EnrichmentStatus `json:"enrichmentStatus"`
	EnrichmentReason string                    `json:"enrichmentReason"`
}

func writeIdentityE2EManifest(t *testing.T, pkgName, toolBin string) string {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf(
		"version: 1\ncapabilities:\n  profileProvider: true\ntools:\n  %s__enrich_authenticated_user:\n    executable: %s\n",
		pkgName, toolBin,
	)
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func (h *e2eIdentityHarness) seedProviderToolset() string {
	h.t.Helper()
	id := idgen.New()
	now := time.Now().UnixMilli()
	rt := "go"
	if _, err := h.db.ExecContext(h.ctx,
		`INSERT INTO tool_packages (id, name, description, runtime, discovery_status, discovery_errors,
			zip_resource_key, zip_filename, zip_file_size, capabilities,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, NULL, ?, ?, NULL, NULL, NULL, NULL, ?, ?, ?, ?, ?)`,
		id, "test-provider-e2e", &rt, "ready",
		`{"profileProvider": true}`,
		"e2e", "e2e", now, now,
	); err != nil {
		h.t.Fatalf("seed tool_packages: %v", err)
	}
	if _, err := h.db.ExecContext(h.ctx,
		`INSERT INTO tool_package_files (id, package_id, tool_name, tool_description, timeout_seconds, sandbox_profile, parameters, config_schema, auth, created_time)
		 VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL, NULL, ?)`,
		idgen.New(), id, "enrich_authenticated_user", 10, "standard", now,
	); err != nil {
		h.t.Fatalf("seed tool_package_files: %v", err)
	}
	return id
}

func (h *e2eIdentityHarness) seedToolsetConfig(pkgID string, cfg map[string]any) {
	h.t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal tool_config: %v", err)
	}
	cv := string(raw)
	now := time.Now().UnixMilli()
	if _, err := h.db.ExecContext(h.ctx,
		`INSERT INTO toolset_configs (package_id, config_values, updated_by, updated_time)
		 VALUES (?, ?, ?, ?)`,
		pkgID, &cv, "e2e", now,
	); err != nil {
		h.t.Fatalf("seed toolset_configs: %v", err)
	}
}

func (h *e2eIdentityHarness) putIdentityConfig(toolsetID, lookupKey string) ProfileProviderResponse {
	h.t.Helper()
	out, err := h.cfgSvc.Set(h.ctx, UpdateProfileProviderRequest{ToolsetID: toolsetID, LookupKey: lookupKey}, "e2e")
	if err != nil {
		h.t.Fatalf("set identity config: %v", err)
	}
	return *out
}

func (h *e2eIdentityHarness) brokerLookup(userID, lookupEmail string) auth.AuthorizationResolution {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	res, err := h.authzClient.Lookup(ctx, userID, map[string]any{
		"gateway_id": "test-gw",
		"idp_claims": map[string]any{"email": lookupEmail},
	})
	if err != nil {
		h.t.Fatalf("authz lookup: %v", err)
	}
	return res
}

func (h *e2eIdentityHarness) getEcho() *auth.Subject {
	h.t.Helper()
	req, _ := http.NewRequestWithContext(h.ctx, http.MethodGet, h.gatewayServer.URL+"/echo", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("GET /echo: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /echo: status=%d body=%s", resp.StatusCode, string(rb))
	}
	var out echoedSubject
	if err := json.Unmarshal(rb, &out); err != nil {
		h.t.Fatalf("decode echo response: %v (body=%s)", err, string(rb))
	}
	return auth.NewTestSubject(auth.SubjectFields{
		UserID:           out.UserID,
		Email:            out.Email,
		Name:             out.Name,
		AuthMethod:       out.AuthMethod,
		Authenticated:    out.Authenticated,
		Roles:            out.Roles,
		Scopes:           out.Scopes,
		Profile:          out.Profile,
		EnrichmentStatus: out.EnrichmentStatus,
		EnrichmentReason: out.EnrichmentReason,
	})
}

const identityE2EUsersJSON = `[
	{"id": "sam@dev.local", "displayName": "Sam Dev User", "jobTitle": "Engineer", "department": "Test"},
	{"id": "other@dev.local", "displayName": "Other User", "jobTitle": "Other", "department": "Test"}
]`

func TestE2E_Identity_AuthzLookupCarriesProfile(t *testing.T) {
	h := setupE2EIdentity(t)

	pkgID := h.seedProviderToolset()
	h.seedToolsetConfig(pkgID, map[string]any{"users": identityE2EUsersJSON})

	resp := h.putIdentityConfig(pkgID, "email")
	if resp.ToolsetID == nil || *resp.ToolsetID != pkgID {
		t.Fatalf("response toolsetId = %v, want %q", resp.ToolsetID, pkgID)
	}

	res := h.brokerLookup("sam@dev.local", "sam@dev.local")

	if len(res.Scopes) == 0 {
		t.Errorf("response carries no scopes; composer should have returned default role scopes")
	}

	if res.EnrichmentStatus != identity.EnrichmentOK {
		t.Fatalf("EnrichmentStatus = %q, want %q (reason=%q)",
			res.EnrichmentStatus, identity.EnrichmentOK, res.EnrichmentReason)
	}
	if res.Profile == nil {
		t.Fatal("Profile is nil; expected the platform handler to fan out to the identity service and embed the result")
	}
	if res.Profile.ID != "sam@dev.local" {
		t.Errorf("Profile.ID = %q, want %q", res.Profile.ID, "sam@dev.local")
	}
	if res.Profile.DisplayName != "Sam Dev User" {
		t.Errorf("Profile.DisplayName = %q, want %q", res.Profile.DisplayName, "Sam Dev User")
	}
	if res.Profile.Department != "Test" {
		t.Errorf("Profile.Department = %q, want %q", res.Profile.Department, "Test")
	}
}

func TestE2E_Identity_NestedToolsetConfigProjectsToBinary(t *testing.T) {
	h := setupE2EIdentity(t)

	pkgID := h.seedProviderToolset()
	h.seedToolsetConfig(pkgID, map[string]any{
		"enrich_authenticated_user": map[string]any{
			"users": identityE2EUsersJSON,
		},
		"search_users": map[string]any{
			"users": `[{"id":"unrelated@dev.local","displayName":"Unrelated"}]`,
		},
	})

	resp := h.putIdentityConfig(pkgID, "email")
	if resp.ToolsetID == nil || *resp.ToolsetID != pkgID {
		t.Fatalf("response toolsetId = %v, want %q", resp.ToolsetID, pkgID)
	}

	res := h.brokerLookup("sam@dev.local", "sam@dev.local")

	if res.EnrichmentStatus != identity.EnrichmentOK {
		t.Fatalf("EnrichmentStatus = %q, want %q (reason=%q) — projection likely returned the wrapper map instead of the per-tool sub-map; the binary couldn't find its config",
			res.EnrichmentStatus, identity.EnrichmentOK, res.EnrichmentReason)
	}
	if res.Profile == nil {
		t.Fatal("Profile is nil; the binary should have received the projected sub-map and resolved the lookup")
	}
	if res.Profile.ID != "sam@dev.local" {
		t.Errorf("Profile.ID = %q, want %q — the wrong tool's sub-map may have been projected", res.Profile.ID, "sam@dev.local")
	}
}

func TestE2E_Identity_NotConfigured_LookupReturnsScopesOnly(t *testing.T) {
	h := setupE2EIdentity(t)
	res := h.brokerLookup("sam@dev.local", "sam@dev.local")
	if res.EnrichmentStatus != identity.EnrichmentNotConfigured {
		t.Errorf("EnrichmentStatus = %q, want %q",
			res.EnrichmentStatus, identity.EnrichmentNotConfigured)
	}
	if res.Profile != nil {
		t.Errorf("Profile = %+v, want nil when not configured", res.Profile)
	}
	if len(res.Scopes) == 0 {
		t.Errorf("Scopes should still be populated from the composer even without enrichment")
	}
}

func TestE2E_Identity_GatewayHTTP_LazyPopulatesCacheOnFirstRequest(t *testing.T) {
	h := setupE2EIdentity(t)

	pkgID := h.seedProviderToolset()
	h.seedToolsetConfig(pkgID, map[string]any{"users": identityE2EUsersJSON})
	h.putIdentityConfig(pkgID, "email")

	id := h.getEcho()
	if got := h.lookupCount.Load(); got != 1 {
		t.Fatalf("after 1st request lookupCount = %d, want 1 (cache miss should fire one lookup)", got)
	}
	if id.EnrichmentStatus() != identity.EnrichmentOK {
		t.Fatalf("EnrichmentStatus = %q (reason=%q), want %q",
			id.EnrichmentStatus(), id.EnrichmentReason(), identity.EnrichmentOK)
	}
	if id.Profile() == nil {
		t.Fatal("Profile is nil; full chain failed to populate it")
	}
	if id.Profile().ID != "sam@dev.local" {
		t.Errorf("Profile.ID = %q, want sam@dev.local (test-provider fixture mismatch)", id.Profile().ID)
	}
	if id.Profile().DisplayName != "Sam Dev User" {
		t.Errorf("Profile.DisplayName = %q, want Sam Dev User", id.Profile().DisplayName)
	}

	id2 := h.getEcho()
	if got := h.lookupCount.Load(); got != 1 {
		t.Errorf("after 2nd request lookupCount = %d, want 1 (cache hit must skip the broker)", got)
	}
	if id2.Profile() == nil || id2.Profile().ID != id.Profile().ID {
		t.Errorf("2nd Profile = %+v, want same as 1st (%+v)", id2.Profile(), id.Profile())
	}
}

func TestE2E_Identity_GatewayHTTP_CacheInvalidateTriggersReLookup(t *testing.T) {
	h := setupE2EIdentity(t)

	pkgID := h.seedProviderToolset()
	h.seedToolsetConfig(pkgID, map[string]any{"users": identityE2EUsersJSON})
	h.putIdentityConfig(pkgID, "email")

	_ = h.getEcho()
	if got := h.lookupCount.Load(); got != 1 {
		t.Fatalf("after 1st request lookupCount = %d, want 1", got)
	}

	h.profileCache.Invalidate("sam_dev_user")

	id := h.getEcho()
	if got := h.lookupCount.Load(); got != 2 {
		t.Fatalf("after invalidate + 2nd request lookupCount = %d, want 2", got)
	}
	if id.Profile() == nil {
		t.Errorf("Profile should be re-populated after invalidate + request, got nil")
	}
}

func TestE2E_Identity_GatewayHTTP_NotConfigured_LazyLookupReportsStatus(t *testing.T) {
	h := setupE2EIdentity(t)

	id := h.getEcho()
	if got := h.lookupCount.Load(); got != 1 {
		t.Fatalf("after 1st request lookupCount = %d, want 1", got)
	}
	if id.EnrichmentStatus() != identity.EnrichmentNotConfigured {
		t.Errorf("EnrichmentStatus = %q, want %q",
			id.EnrichmentStatus(), identity.EnrichmentNotConfigured)
	}
	if id.Profile() != nil {
		t.Errorf("Profile = %+v, want nil when not configured", id.Profile())
	}

	_ = h.getEcho()
	if got := h.lookupCount.Load(); got != 1 {
		t.Errorf("after 2nd request lookupCount = %d, want 1 (not_configured must also be cached)", got)
	}
}
