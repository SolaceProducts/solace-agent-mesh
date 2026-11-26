// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

var integrationTestTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_PLATFORM_DB_URL"),
		"identity_integration",
		func(u string) error {
			db, openErr := store.Open(u, nil)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	integrationTestTemplate = tmpl
	code := m.Run()
	integrationTestTemplate.Close()
	os.Exit(code)
}

func testOpenDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(integrationTestTemplate.Clone(t), nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var buildTestProvider = sync.OnceValues(func() (string, error) {
	pkgDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get the package directory: %w", err)
	}
	srcDir := filepath.Join(pkgDir, "testdata", "test-provider")
	tmpDir, err := os.MkdirTemp("", "identity-test-provider-*")
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

func testProviderBinary(t *testing.T) string {
	t.Helper()
	path, err := buildTestProvider()
	if err != nil {
		t.Fatalf("build test-provider: %v", err)
	}
	return path
}

func writeIntegrationManifest(t *testing.T, pkgName, toolBin string) string {
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

type integrationHarness struct {
	ctx       context.Context
	db        *store.DB
	broker    broker.Broker
	rt        *runtime.Runtime
	worker    *str.Worker
	cfgRepo   *store.PlatformIdentityConfigRepo
	pkgRepo   *store.ToolPackageRepo
	cfgSvc    *entplatform.IdentityConfigService
	identity  *identity.Service
	namespace string
}

func newIntegrationHarness(t *testing.T) *integrationHarness {
	t.Helper()
	ctx := t.Context()

	const namespace = "identity-integration"
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

	toolBin := testProviderBinary(t)
	manifestPath := writeIntegrationManifest(t, "test-provider", toolBin)
	workerCfg := str.WorkerConfig{
		WorkerID:      "identity-integration",
		Namespace:     namespace,
		ManifestPaths: []string{manifestPath},
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 4,
		},
	}
	worker := str.NewWorker(rt.Services(), workerCfg)
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("worker init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("worker start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	cfgRepo := store.NewPlatformIdentityConfigRepo(db)
	pkgRepo := store.NewToolPackageRepo(db)
	cfgSvc := entplatform.NewIdentityConfigService(cfgRepo, pkgRepo, logger)

	enricher := identity.NewSTREnricher(brk, namespace, cfgRepo, pkgRepo, pkgRepo, nil, logger)
	if err := enricher.Start(ctx); err != nil {
		t.Fatalf("enricher start: %v", err)
	}
	t.Cleanup(func() { _ = enricher.Stop(context.Background()) })

	src := identity.NewRepoConfigSource(cfgRepo)
	svc := identity.New(src, nil, enricher, logger)

	return &integrationHarness{
		ctx:       ctx,
		db:        db,
		broker:    brk,
		rt:        rt,
		worker:    worker,
		cfgRepo:   cfgRepo,
		pkgRepo:   pkgRepo,
		cfgSvc:    cfgSvc,
		identity:  svc,
		namespace: namespace,
	}
}

func seedProviderToolset(t *testing.T, db *store.DB) string {
	t.Helper()
	ctx := t.Context()
	id := idgen.New()
	now := time.Now().UnixMilli()
	runtimeStr := "go"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tool_packages (id, name, description, runtime, discovery_status, discovery_errors,
			zip_resource_key, zip_filename, zip_file_size, capabilities,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, NULL, ?, ?, NULL, NULL, NULL, NULL, ?, ?, ?, ?, ?)`,
		id, "test-provider", &runtimeStr, "ready",
		`{"profileProvider": true}`,
		"test", "test", now, now,
	); err != nil {
		t.Fatalf("seed tool_packages: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tool_package_files (id, package_id, tool_name, tool_description, timeout_seconds, sandbox_profile, parameters, config_schema, auth, created_time)
		 VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL, NULL, ?)`,
		idgen.New(), id, "enrich_authenticated_user", 10, "standard", now,
	); err != nil {
		t.Fatalf("seed tool_package_files: %v", err)
	}
	return id
}

func seedToolsetConfig(t *testing.T, db *store.DB, pkgID string, cfg map[string]any) {
	t.Helper()
	ctx := t.Context()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal tool_config: %v", err)
	}
	cv := string(raw)
	now := time.Now().UnixMilli()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO toolset_configs (package_id, config_values, updated_by, updated_time)
		 VALUES (?, ?, ?, ?)`,
		pkgID, &cv, "test", now,
	); err != nil {
		t.Fatalf("seed toolset_configs: %v", err)
	}
}

const usersJSON = `[
	{"id": "alice@dev.local", "displayName": "Alice Anderson", "jobTitle": "Senior Engineer", "department": "Platform"},
	{"id": "bob@dev.local", "displayName": "Bob Baker", "jobTitle": "Product Manager", "department": "Product"}
]`

func TestIntegration_Resolve_HappyPath(t *testing.T) {
	h := newIntegrationHarness(t)
	pkgID := seedProviderToolset(t, h.db)
	seedToolsetConfig(t, h.db, pkgID, map[string]any{"users": usersJSON})

	if _, err := h.cfgSvc.Set(h.ctx, entplatform.UpdateProfileProviderRequest{
		ToolsetID: pkgID,
		LookupKey: "email",
	}, "tester"); err != nil {
		t.Fatalf("identity config Set: %v", err)
	}

	res, err := h.identity.Resolve(h.ctx, map[string]any{
		"sub":   "alice",
		"email": "alice@dev.local",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res == nil {
		t.Fatal("Resolve returned nil resolution")
	}
	if res.EnrichmentStatus != identity.EnrichmentOK {
		t.Fatalf("EnrichmentStatus = %q, want %q (reason=%q)", res.EnrichmentStatus, identity.EnrichmentOK, res.EnrichmentReason)
	}
	if res.Profile == nil {
		t.Fatal("Profile is nil; expected populated")
	}
	if res.Profile.ID != "alice@dev.local" {
		t.Errorf("Profile.ID = %q, want %q", res.Profile.ID, "alice@dev.local")
	}
	if res.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("Profile.DisplayName = %q, want %q", res.Profile.DisplayName, "Alice Anderson")
	}
	if res.Profile.JobTitle != "Senior Engineer" {
		t.Errorf("Profile.JobTitle = %q, want %q", res.Profile.JobTitle, "Senior Engineer")
	}
	if res.Profile.Department != "Platform" {
		t.Errorf("Profile.Department = %q, want %q", res.Profile.Department, "Platform")
	}
}

func TestIntegration_Resolve_NoToolsetConfigured(t *testing.T) {
	h := newIntegrationHarness(t)

	res, err := h.identity.Resolve(h.ctx, map[string]any{
		"email": "alice@dev.local",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EnrichmentStatus != identity.EnrichmentNotConfigured {
		t.Errorf("EnrichmentStatus = %q, want %q", res.EnrichmentStatus, identity.EnrichmentNotConfigured)
	}
	if res.Profile != nil {
		t.Errorf("Profile = %+v, want nil", res.Profile)
	}
}

func TestIntegration_Resolve_ToolError_DegradesToUnavailable(t *testing.T) {
	h := newIntegrationHarness(t)
	pkgID := seedProviderToolset(t, h.db)
	seedToolsetConfig(t, h.db, pkgID, map[string]any{
		"users":       usersJSON,
		"force_error": "directory probe failed for testing",
	})

	if _, err := h.cfgSvc.Set(h.ctx, entplatform.UpdateProfileProviderRequest{
		ToolsetID: pkgID,
		LookupKey: "email",
	}, "tester"); err != nil {
		t.Fatalf("identity config Set: %v", err)
	}

	res, err := h.identity.Resolve(h.ctx, map[string]any{
		"email": "alice@dev.local",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EnrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("EnrichmentStatus = %q, want %q (reason=%q)", res.EnrichmentStatus, identity.EnrichmentUnavailable, res.EnrichmentReason)
	}
	if res.Profile != nil {
		t.Errorf("Profile = %+v, want nil on enrichment failure", res.Profile)
	}
}

func TestIntegration_Resolve_ResolvesPrefixedWireName(t *testing.T) {
	ctx := t.Context()
	const (
		namespace = "identity-integration-prefixed"
		pkgName   = "test_provider"
		bareName  = "enrich_authenticated_user"
	)
	wireName := pkgName + store.PackageToolSeparator + bareName

	db := testOpenDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	pkgID := idgen.New()
	now := time.Now().UnixMilli()
	runtimeStr := "go"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tool_packages (id, name, description, runtime, discovery_status, discovery_errors,
			zip_resource_key, zip_filename, zip_file_size, capabilities,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, NULL, ?, ?, NULL, NULL, NULL, NULL, ?, ?, ?, ?, ?)`,
		pkgID, pkgName, &runtimeStr, "ready",
		`{"profileProvider": true}`, "test", "test", now, now,
	); err != nil {
		t.Fatalf("seed tool_packages: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tool_package_files (id, package_id, tool_name, tool_description, timeout_seconds, sandbox_profile, parameters, config_schema, auth, created_time)
		 VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL, NULL, ?)`,
		idgen.New(), pkgID, bareName, 10, "standard", now,
	); err != nil {
		t.Fatalf("seed tool_package_files: %v", err)
	}
	cv := `{}`
	if _, err := db.ExecContext(ctx,
		`INSERT INTO toolset_configs (package_id, config_values, updated_by, updated_time)
		 VALUES (?, ?, ?, ?)`, pkgID, &cv, "test", now,
	); err != nil {
		t.Fatalf("seed toolset_configs: %v", err)
	}

	q, qErr := brk.CreateQueue("integration-prefixed-stub", broker.QueueOptions{Named: true})
	if qErr != nil {
		t.Fatalf("create queue: %v", qErr)
	}
	invokeTopic := remotetoolInvokeTopic(t, namespace, wireName)
	if subErr := brk.Subscribe(q, invokeTopic); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}
	stubDone := make(chan struct{})
	go func() {
		defer close(stubDone)
		stubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, recvErr := brk.Receive(stubCtx, q)
		if recvErr != nil || msg == nil {
			return
		}
		msg.Ack()
		respondToInvokeWithProfile(t, brk, msg, map[string]any{
			"id":          "alice@dev.local",
			"displayName": "Alice Anderson",
			"workEmail":   "alice@dev.local",
			"jobTitle":    "Senior Engineer",
		})
	}()

	cfgRepo := store.NewPlatformIdentityConfigRepo(db)
	pkgRepo := store.NewToolPackageRepo(db)
	enricher := identity.NewSTREnricher(brk, namespace, cfgRepo, pkgRepo, pkgRepo, nil, slog.Default())
	if startErr := enricher.Start(ctx); startErr != nil {
		t.Fatalf("enricher start: %v", startErr)
	}
	t.Cleanup(func() { _ = enricher.Stop(context.Background()) })

	pkgIDLocal := pkgID
	if setErr := cfgRepo.Set(ctx, &pkgIDLocal, "email", "test"); setErr != nil {
		t.Fatalf("identity config Set: %v", setErr)
	}

	svc := identity.New(identity.NewRepoConfigSource(cfgRepo), nil, enricher, slog.Default())
	res, resolveErr := svc.Resolve(ctx, map[string]any{"email": "alice@dev.local"})
	if resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	if res.EnrichmentStatus != identity.EnrichmentOK {
		t.Fatalf("EnrichmentStatus = %q, want %q (reason=%q)", res.EnrichmentStatus, identity.EnrichmentOK, res.EnrichmentReason)
	}
	if res.Profile == nil {
		t.Fatal("expected profile, got nil")
	}
	if res.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("DisplayName = %q, want Alice Anderson", res.Profile.DisplayName)
	}

	select {
	case <-stubDone:
	case <-time.After(5 * time.Second):
		t.Fatal("STR stub never received the invoke — enricher likely routed to the wrong topic")
	}
}

func remotetoolInvokeTopic(t *testing.T, namespace, toolName string) string {
	t.Helper()
	return fmt.Sprintf("%s/a2a/v1/sam_remote_tool/invoke/%s", namespace, toolName)
}

func respondToInvokeWithProfile(t *testing.T, brk broker.Broker, msg *broker.Message, profile map[string]any) {
	t.Helper()
	var rpc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(msg.Payload, &rpc)
	corrID := msg.CorrelationID
	if corrID == "" {
		corrID = rpc.ID
	}
	replyTo := msg.UserProperties["replyTo"]
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      corrID,
		"result": map[string]any{
			"tool_result": map[string]any{
				"_schema":         "ToolResult",
				"_schema_version": "1.0",
				"status":          "success",
				"data":            map[string]any{"profile": profile},
			},
		},
	}
	payload, _ := json.Marshal(resp)
	_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
}

func TestIntegration_Resolve_LookupValueMissing(t *testing.T) {
	h := newIntegrationHarness(t)
	pkgID := seedProviderToolset(t, h.db)
	seedToolsetConfig(t, h.db, pkgID, map[string]any{"users": usersJSON})

	if _, err := h.cfgSvc.Set(h.ctx, entplatform.UpdateProfileProviderRequest{
		ToolsetID: pkgID,
		LookupKey: "email",
	}, "tester"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	res, err := h.identity.Resolve(h.ctx, map[string]any{"sub": "no-email-user"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EnrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("EnrichmentStatus = %q, want %q", res.EnrichmentStatus, identity.EnrichmentUnavailable)
	}
	if res.EnrichmentReason == "" {
		t.Error("EnrichmentReason should explain the missing claim")
	}
}
