// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type discoveryEnv struct {
	namespace       string
	brk             broker.Broker
	toolPackageRepo *store.ToolPackageRepo
	toolsetSvc      *ToolsetService
	toolSvc         *ToolService
	tracker         *platpkg.ResourceTracker
}

func setupDiscoveryEnv(t *testing.T) *discoveryEnv {
	t.Helper()

	namespace := "disc-test"
	logger := slog.New(slog.DiscardHandler)

	db := testOpenDB(t)
	toolPackageRepo := store.NewToolPackageRepo(db)
	toolRepo := store.NewToolRepo(db)

	resourceStore := objectstore.NewStore(objectstore.NewMemoryClient())
	storage := newToolsetStorage(resourceStore, logger)
	toolsetSvc := NewToolsetService(db, toolPackageRepo, storage, nil, namespace, logger)
	toolSvc := NewToolService(toolRepo, logger)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	handler := NewDBResourceEventHandler(nil, toolSvc, toolsetSvc, logger)
	tracker := platpkg.NewResourceTracker(brk, namespace, handler, logger)
	if err := tracker.Start(t.Context()); err != nil {
		t.Fatalf("start tracker: %v", err)
	}

	t.Cleanup(func() {
		_ = tracker.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})

	return &discoveryEnv{
		namespace:       namespace,
		brk:             brk,
		toolPackageRepo: toolPackageRepo,
		toolsetSvc:      toolsetSvc,
		toolSvc:         toolSvc,
		tracker:         tracker,
	}
}

func (e *discoveryEnv) seedPendingPackage(ctx context.Context, t *testing.T, name string) *ToolsetResponse {
	t.Helper()
	resp, err := e.toolsetSvc.Create(ctx, CreateToolsetRequest{Name: name, Description: "desc"}, "tester")
	if err != nil {
		t.Fatalf("create package %q: %v", name, err)
	}
	if zipErr := e.toolPackageRepo.UpdateZipInfo(ctx, resp.ID, "python", "s3://fake/key", "pkg.zip", 100, "tester"); zipErr != nil {
		t.Fatalf("update zip info: %v", zipErr)
	}
	pkg, err := e.toolPackageRepo.GetByID(ctx, resp.ID)
	if err != nil || pkg == nil {
		t.Fatalf("get package after zip update: %v", err)
	}
	if pkg.DiscoveryStatus != "pending" {
		t.Fatalf("expected discovery_status=pending after zip update, got %q", pkg.DiscoveryStatus)
	}
	return resp
}

func TestDiscoveryFlow_PendingToReady(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	resp := env.seedPendingPackage(ctx, t, "mypkg")

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:    "mypkg__mytool",
		Description: "does things",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	})

	waitForDB(t, 2*time.Second, func() bool {
		pkg, _ := env.toolPackageRepo.GetByID(ctx, resp.ID)
		return pkg != nil && pkg.DiscoveryStatus == "ready"
	})

	pkg, err := env.toolPackageRepo.GetByID(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get package: %v", err)
	}
	if pkg.DiscoveryStatus != "ready" {
		t.Errorf("discovery_status = %q, want %q", pkg.DiscoveryStatus, "ready")
	}

	files, err := env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 tool_package_files row, got %d", len(files))
	}
	if files[0].ToolName != "mytool" {
		t.Errorf("tool_name = %q, want %q", files[0].ToolName, "mytool")
	}
	if files[0].ToolDescription == nil || *files[0].ToolDescription != "does things" {
		t.Errorf("tool_description = %v, want %q", files[0].ToolDescription, "does things")
	}
}

func TestDiscoveryFlow_AuthDeclarationPersisted(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	resp := env.seedPendingPackage(ctx, t, "authedpkg")

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:    "authedpkg__calendar",
		Description: "looks up calendar events",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Auth: &remotetool.AuthDeclaration{
			Type: "oauth2",
			Scheme: &remotetool.AuthDeclarationScheme{
				AuthorizationURL: "https://idp.example.com/authorize",
				TokenURL:         "https://idp.example.com/token",
				Scopes:           []string{"calendar.read"},
			},
		},
	})

	waitForDB(t, 2*time.Second, func() bool {
		files, _ := env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
		return len(files) == 1 && files[0].Auth != nil
	})

	files, err := env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Auth == nil {
		t.Fatal("Auth column nil — init handler did not persist tool-declared auth")
	}
	var decoded remotetool.AuthDeclaration
	if err := json.Unmarshal([]byte(*files[0].Auth), &decoded); err != nil {
		t.Fatalf("unmarshal stored auth: %v", err)
	}
	if decoded.Type != "oauth2" {
		t.Errorf("stored auth.type = %q", decoded.Type)
	}
	if decoded.Scheme == nil || decoded.Scheme.TokenURL != "https://idp.example.com/token" {
		t.Errorf("stored scheme = %+v", decoded.Scheme)
	}
	if len(decoded.Scheme.Scopes) != 1 || decoded.Scheme.Scopes[0] != "calendar.read" {
		t.Errorf("stored scopes = %v", decoded.Scheme.Scopes)
	}
}

func TestDiscoveryFlow_RemovedDeletesToolFile(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	resp := env.seedPendingPackage(ctx, t, "rmpkg")

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:    "rmpkg__rmtool",
		Description: "will be removed",
	})
	waitForDB(t, 2*time.Second, func() bool {
		pkg, _ := env.toolPackageRepo.GetByID(ctx, resp.ID)
		return pkg != nil && pkg.DiscoveryStatus == "ready"
	})

	files, err := env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file row after init, got %d", len(files))
	}

	publishToolRemoved(t, env.brk, env.namespace, "rmpkg__rmtool")

	waitForDB(t, 2*time.Second, func() bool {
		remaining, _ := env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
		return len(remaining) == 0
	})

	files, err = env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get files after removal: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 file rows after removal, got %d", len(files))
	}

	pkg, err := env.toolPackageRepo.GetByID(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get package after removal: %v", err)
	}
	if pkg.DiscoveryStatus != "removed" {
		t.Errorf("discovery_status = %q, want %q", pkg.DiscoveryStatus, "removed")
	}
}

func TestDiscoveryFlow_UnknownPackageIsIgnored(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:    "unknownpkg__sometool",
		Description: "no package for this",
	})

	time.Sleep(100 * time.Millisecond)

	pkgs, total, err := env.toolPackageRepo.ListPaginated(ctx, 1, 100, "", nil)
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	if total != 0 || len(pkgs) != 0 {
		t.Errorf("expected 0 packages, got %d", total)
	}
}

func TestDiscoveryFlow_FailedBroadcastFlipsStatus(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	resp := env.seedPendingPackage(ctx, t, "failpkg")

	const failMsg = "--schema exited with code 1: ModuleNotFoundError"
	publishToolFailed(t, env.brk, env.namespace, "failpkg__mybinary", failMsg)

	waitForDB(t, 2*time.Second, func() bool {
		pkg, _ := env.toolPackageRepo.GetByID(ctx, resp.ID)
		return pkg != nil && pkg.DiscoveryStatus == "failed"
	})

	pkg, err := env.toolPackageRepo.GetByID(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get package: %v", err)
	}
	if pkg.DiscoveryStatus != "failed" {
		t.Errorf("discovery_status = %q, want %q", pkg.DiscoveryStatus, "failed")
	}
	if pkg.DiscoveryErrors == nil {
		t.Fatal("discovery_errors should not be nil")
	}
	var errs []string
	if jsonErr := json.Unmarshal([]byte(*pkg.DiscoveryErrors), &errs); jsonErr != nil {
		t.Fatalf("unmarshal discovery_errors: %v", jsonErr)
	}
	if len(errs) != 1 || errs[0] != failMsg {
		t.Errorf("discovery_errors = %v, want [%q]", errs, failMsg)
	}

	files, err := env.toolPackageRepo.GetFilesForPackage(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 file rows for failed package, got %d", len(files))
	}
}

func TestDiscoveryFlow_CapabilitiesPropagated(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	resp := env.seedPendingPackage(ctx, t, "capspkg")

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:    "capspkg__mytool",
		Description: "carries capabilities",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Capabilities: map[string]any{
			"profileProvider": true,
			"extra":           "preserved",
		},
	})

	waitForDB(t, 2*time.Second, func() bool {
		pkg, _ := env.toolPackageRepo.GetByID(ctx, resp.ID)
		if pkg == nil || pkg.DiscoveryStatus != "ready" {
			return false
		}
		return pkg.Capabilities != "" && pkg.Capabilities != "{}"
	})

	pkg, err := env.toolPackageRepo.GetByID(ctx, resp.ID)
	if err != nil || pkg == nil {
		t.Fatalf("get package: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(pkg.Capabilities), &got); err != nil {
		t.Fatalf("unmarshal stored capabilities %q: %v", pkg.Capabilities, err)
	}
	if got["profileProvider"] != true {
		t.Errorf("profileProvider = %v, want true", got["profileProvider"])
	}
	if got["extra"] != "preserved" {
		t.Errorf("extra = %v, want preserved (pass-through)", got["extra"])
	}
}

func TestDiscoveryFlow_CapabilitiesClearedViaEmptyMap(t *testing.T) {
	t.Parallel()
	env := setupDiscoveryEnv(t)
	ctx := t.Context()

	resp := env.seedPendingPackage(ctx, t, "capsclear")

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:     "capsclear__mytool",
		Description:  "seed with capabilities",
		Parameters:   map[string]any{"type": "object", "properties": map[string]any{}},
		Capabilities: map[string]any{"profileProvider": true},
	})
	waitForDB(t, 2*time.Second, func() bool {
		pkg, _ := env.toolPackageRepo.GetByID(ctx, resp.ID)
		return pkg != nil && pkg.Capabilities != "" && pkg.Capabilities != "{}"
	})

	publishToolInit(t, env.brk, env.namespace, remotetool.InitParams{
		ToolName:     "capsclear__mytool",
		Description:  "clear with explicit empty map",
		Parameters:   map[string]any{"type": "object", "properties": map[string]any{}},
		Capabilities: map[string]any{},
	})
	waitForDB(t, 2*time.Second, func() bool {
		pkg, _ := env.toolPackageRepo.GetByID(ctx, resp.ID)
		return pkg != nil && pkg.Capabilities == "{}"
	})

	pkg, err := env.toolPackageRepo.GetByID(ctx, resp.ID)
	if err != nil || pkg == nil {
		t.Fatalf("get package: %v", err)
	}
	if pkg.Capabilities != "{}" {
		t.Errorf("after clear: capabilities = %q, want %q (explicit-empty-map clear failed)", pkg.Capabilities, "{}")
	}
}
