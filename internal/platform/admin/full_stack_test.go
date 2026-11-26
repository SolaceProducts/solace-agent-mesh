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
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fullStackEnv struct {
	Broker    broker.Broker
	Client    *control.Client
	SkillSvc  *SkillService
	SkillRepo *store.SkillRepo
	ToolSvc   *ToolService
	Namespace string
}

func setupFullStack(t *testing.T) *fullStackEnv {
	t.Helper()

	namespace := "test-ns"
	logger := slog.Default()

	db := testOpenDB(t)

	skillRepo := store.NewSkillRepo(db)
	toolRepo := store.NewToolRepo(db)
	skillSvc := NewSkillService(db, skillRepo, logger)
	toolSvc := NewToolService(toolRepo, logger)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	handler := NewDBResourceEventHandler(skillSvc, toolSvc, nil, logger)
	tracker := platpkg.NewResourceTracker(brk, namespace, handler, logger)
	if err := tracker.Start(context.Background()); err != nil {
		t.Fatalf("start tracker: %v", err)
	}

	deps := &RouteDeps{SkillSvc: skillSvc, ToolSvc: toolSvc}
	mp := &ManageablePlatform{deps: deps, logger: logger}
	stub := &stubPlatformComponent{mp: mp}

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	rt.Register("platform", stub)
	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	t.Cleanup(func() {
		_ = tracker.Stop(context.Background())
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})

	client := control.NewClient(brk, namespace, control.WithTimeout(5*time.Second))

	return &fullStackEnv{
		Broker:    brk,
		Client:    client,
		SkillSvc:  skillSvc,
		SkillRepo: skillRepo,
		ToolSvc:   toolSvc,
		Namespace: namespace,
	}
}

func TestFullStack_ToolInitToControlProtocol(t *testing.T) {
	t.Parallel()
	env := setupFullStack(t)
	ctx := context.Background()

	publishToolInit(t, env.Broker, env.Namespace, remotetool.InitParams{
		ToolName:    "fullstack-tool",
		Description: "End-to-end tool",
		Parameters:  map[string]any{"type": "object"},
	})

	waitForDB(t, 2*time.Second, func() bool {
		raw, err := env.Client.Custom(ctx, "platform", "get", "tools", nil)
		if err != nil {
			return false
		}
		var resp PaginatedResponse[ToolDetailResponse]
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		return resp.Meta.Pagination.Count == 1
	})

	raw, err := env.Client.Custom(ctx, "platform", "get", "tools", nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var resp PaginatedResponse[ToolDetailResponse]
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(resp.Data))
	}
	if resp.Data[0].Name != "fullstack-tool" {
		t.Errorf("name = %q, want fullstack-tool", resp.Data[0].Name)
	}
	if resp.Data[0].Status != "available" {
		t.Errorf("status = %q, want available", resp.Data[0].Status)
	}
}

func TestFullStack_SkillInitToControlProtocol(t *testing.T) {
	t.Parallel()
	env := setupFullStack(t)
	ctx := context.Background()

	desc := "End-to-end skill"
	if err := env.SkillRepo.UpsertByName(ctx, &store.Skill{
		Name: "fullstack-skill", Description: &desc, Origin: "platform",
		DiscoveryStatus: "pending", CreatedBy: "u", UpdatedBy: "u",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	publishSkillInit(t, env.Broker, env.Namespace, remotetool.SkillInitParams{
		SkillName:     "fullstack-skill",
		Description:   "End-to-end skill",
		Tags:          []string{"e2e"},
		HasReferences: true,
		ResourceCount: 5,
	})

	waitForDB(t, 2*time.Second, func() bool {
		raw, err := env.Client.Custom(ctx, "platform", "get", "skills", nil)
		if err != nil {
			return false
		}
		var resp PaginatedResponse[SkillDetailResponse]
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		return len(resp.Data) == 1 && resp.Data[0].HasReferences
	})

	raw, err := env.Client.Custom(ctx, "platform", "get", "skills", nil)
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	var resp PaginatedResponse[SkillDetailResponse]
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(resp.Data))
	}
	sk := resp.Data[0]
	if sk.Name != "fullstack-skill" {
		t.Errorf("name = %q, want fullstack-skill", sk.Name)
	}
	if !sk.HasReferences {
		t.Error("expected hasReferences=true")
	}
	if sk.ResourceCount != 5 {
		t.Errorf("resourceCount = %d, want 5", sk.ResourceCount)
	}
}

func TestFullStack_InitThenRemoveLifecycle(t *testing.T) {
	t.Parallel()
	env := setupFullStack(t)
	ctx := context.Background()

	listTools := func() (PaginatedResponse[ToolDetailResponse], bool) {
		raw, err := env.Client.Custom(ctx, "platform", "get", "tools", nil)
		if err != nil {
			return PaginatedResponse[ToolDetailResponse]{}, false
		}
		var resp PaginatedResponse[ToolDetailResponse]
		if err := json.Unmarshal(raw, &resp); err != nil {
			return PaginatedResponse[ToolDetailResponse]{}, false
		}
		return resp, true
	}

	publishToolInit(t, env.Broker, env.Namespace, remotetool.InitParams{
		ToolName:    "lifecycle-tool",
		Description: "Lifecycle test",
	})
	waitForDB(t, 2*time.Second, func() bool {
		resp, ok := listTools()
		return ok && len(resp.Data) == 1 && resp.Data[0].Status == "available"
	})

	publishToolRemoved(t, env.Broker, env.Namespace, "lifecycle-tool")
	waitForDB(t, 2*time.Second, func() bool {
		resp, ok := listTools()
		return ok && len(resp.Data) == 1 && resp.Data[0].Status == "unavailable"
	})

	resp, ok := listTools()
	if !ok {
		t.Fatal("list tools via control protocol failed")
	}
	if resp.Data[0].Status != "unavailable" {
		t.Errorf("status = %q, want unavailable", resp.Data[0].Status)
	}
}
