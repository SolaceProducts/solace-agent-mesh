// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestResourceTracker_ToolInitCreatesDBRow(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishToolInit(t, env.Broker, env.Namespace, remotetool.InitParams{
		ToolName:    "my_tool",
		Description: "A test tool",
		Parameters:  map[string]any{"type": "object"},
	})

	waitForDB(t, 2*time.Second, func() bool {
		tool, _ := env.ToolRepo.GetByName(ctx, "my_tool")
		return tool != nil
	})

	tool, err := env.ToolRepo.GetByName(ctx, "my_tool")
	if err != nil {
		t.Fatalf("get tool: %v", err)
	}
	if tool.Status != "available" {
		t.Errorf("expected status=available, got %q", tool.Status)
	}
	if tool.Origin != "filesystem" {
		t.Errorf("expected origin=filesystem, got %q", tool.Origin)
	}
	if tool.Description == nil || *tool.Description != "A test tool" {
		t.Errorf("expected description='A test tool', got %v", tool.Description)
	}
}

func TestResourceTracker_ToolRemovedSetsUnavailable(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishToolInit(t, env.Broker, env.Namespace, remotetool.InitParams{
		ToolName:    "removable_tool",
		Description: "Will be removed",
	})
	waitForDB(t, 2*time.Second, func() bool {
		tool, _ := env.ToolRepo.GetByName(ctx, "removable_tool")
		return tool != nil
	})

	publishToolRemoved(t, env.Broker, env.Namespace, "removable_tool")
	waitForDB(t, 2*time.Second, func() bool {
		tool, _ := env.ToolRepo.GetByName(ctx, "removable_tool")
		return tool != nil && tool.Status == "unavailable"
	})

	tool, _ := env.ToolRepo.GetByName(ctx, "removable_tool")
	if tool.Status != "unavailable" {
		t.Errorf("expected status=unavailable, got %q", tool.Status)
	}
}

func TestResourceTracker_SkillInitIgnoredWhenNoRow(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishSkillInit(t, env.Broker, env.Namespace, remotetool.SkillInitParams{
		SkillName:   "filesystem_skill",
		Description: "A skill that lives on disk in STR",
	})

	time.Sleep(100 * time.Millisecond)

	skill, err := env.SkillRepo.GetByName(ctx, "filesystem_skill")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if skill != nil {
		t.Fatalf("filesystem skill should not be persisted, got %+v", skill)
	}
}

func TestResourceTracker_SkillRemovedSetsUnavailable(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	desc := "Will be removed"
	if err := env.SkillRepo.UpsertByName(ctx, &store.Skill{
		Name:            "removable_skill",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	publishSkillRemoved(t, env.Broker, env.Namespace, "removable_skill")
	waitForDB(t, 2*time.Second, func() bool {
		skill, _ := env.SkillRepo.GetByName(ctx, "removable_skill")
		return skill != nil && skill.DiscoveryStatus == "failed"
	})

	skill, _ := env.SkillRepo.GetByName(ctx, "removable_skill")
	if skill.DiscoveryStatus != "failed" {
		t.Errorf("expected discoveryStatus=failed, got %q", skill.DiscoveryStatus)
	}
}

func TestResourceTracker_ToolUpsertUpdatesExisting(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishToolInit(t, env.Broker, env.Namespace, remotetool.InitParams{
		ToolName:    "upsert_tool",
		Description: "version 1",
	})
	waitForDB(t, 2*time.Second, func() bool {
		tool, _ := env.ToolRepo.GetByName(ctx, "upsert_tool")
		return tool != nil
	})

	publishToolInit(t, env.Broker, env.Namespace, remotetool.InitParams{
		ToolName:    "upsert_tool",
		Description: "version 2",
	})
	waitForDB(t, 2*time.Second, func() bool {
		tool, _ := env.ToolRepo.GetByName(ctx, "upsert_tool")
		return tool != nil && tool.Description != nil && *tool.Description == "version 2"
	})

	tools, total, err := env.ToolRepo.ListPaginated(ctx, 1, 100)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 tool, got %d", total)
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool in results, got %d", len(tools))
	}
}

func TestResourceTracker_SkillUpsertUpdatesExisting(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	desc := "version 0"
	if err := env.SkillRepo.UpsertByName(ctx, &store.Skill{
		Name:            "upsert_skill",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	publishSkillInit(t, env.Broker, env.Namespace, remotetool.SkillInitParams{
		SkillName:     "upsert_skill",
		Description:   "version 2",
		Tags:          []string{"updated"},
		HasReferences: true,
		ResourceCount: 5,
	})
	waitForDB(t, 2*time.Second, func() bool {
		skill, _ := env.SkillRepo.GetByName(ctx, "upsert_skill")
		return skill != nil && skill.Description != nil && *skill.Description == "version 2"
	})

	skills, total, err := env.SkillRepo.ListPaginated(ctx, 1, 100)
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 skill, got %d", total)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill in results, got %d", len(skills))
	}
	if !skills[0].HasReferences {
		t.Error("expected hasReferences=true after upsert")
	}
	if skills[0].ResourceCount != 5 {
		t.Errorf("expected resourceCount=5, got %d", skills[0].ResourceCount)
	}
}

func TestResourceTracker_SkillRemovedNeverInitd(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishSkillRemoved(t, env.Broker, env.Namespace, "phantom_skill")

	time.Sleep(50 * time.Millisecond)
	skills, total, _ := env.SkillRepo.ListPaginated(ctx, 1, 100)
	if total != 0 || len(skills) != 0 {
		t.Errorf("expected no skills, got %d", total)
	}
}

func TestResourceTracker_ToolRemovedNeverInitd(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishToolRemoved(t, env.Broker, env.Namespace, "phantom_tool")

	time.Sleep(50 * time.Millisecond)
	tools, total, _ := env.ToolRepo.ListPaginated(ctx, 1, 100)
	if total != 0 || len(tools) != 0 {
		t.Errorf("expected no tools, got %d", total)
	}
}

func TestResourceTracker_IgnoresUnknownMethod(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	publishJSONRPC(t, env.Broker,
		remotetool.InitTopic(env.Namespace, "ghost"),
		"unknown/method",
		map[string]string{"foo": "bar"},
	)

	time.Sleep(50 * time.Millisecond)
	tools, total, _ := env.ToolRepo.ListPaginated(ctx, 1, 100)
	if total != 0 || len(tools) != 0 {
		t.Errorf("expected no tools, got %d", total)
	}
}

func TestResourceTracker_MalformedJSON(t *testing.T) {
	t.Parallel()
	env := setupTestPlatformWithBroker(t)
	ctx := context.Background()

	msg := &broker.Message{Payload: []byte("not valid json {")}
	if err := env.Broker.Publish(ctx, remotetool.InitTopic(env.Namespace, "bad"), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	tools, total, _ := env.ToolRepo.ListPaginated(ctx, 1, 100)
	if total != 0 || len(tools) != 0 {
		t.Errorf("expected no tools, got %d", total)
	}
}
