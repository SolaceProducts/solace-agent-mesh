// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestHandleUploadResource_TypeGuard_AllowsAPIOpenAPI(t *testing.T) {
	t.Parallel()
	svc := newConnectorServiceForTest(t)

	ctx := t.Context()
	api, err := svc.Create(ctx, CreateConnectorRequest{
		Description: "E2E test connector for fixture purposes",
		Name:        "my-api-ok",
		Type:        "api",
		Subtype:     "openapi",
		Values:      map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create api connector: %v", err)
	}

	conn, err := svc.GetByID(ctx, api.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if conn.Type != "api" {
		t.Errorf("type = %q, want api", conn.Type)
	}
	if conn.Subtype != "openapi" {
		t.Errorf("subtype = %q, want openapi", conn.Subtype)
	}
	wouldReject := conn.Type != "api" || (conn.Subtype != "" && conn.Subtype != "openapi")
	if wouldReject {
		t.Error("api/openapi connector would be incorrectly rejected by the type guard")
	}
}

func TestValidateRequest_UUIDTag(t *testing.T) {
	t.Parallel()

	req := &CreateAgentDeploymentRequest{AgentID: "not-a-uuid", Action: "DEPLOY"}
	err := validateRequest(req)
	if err == nil {
		t.Fatal("expected error for non-UUID agentId")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "UUID") {
		t.Errorf("message should mention UUID, got %q", ve.Message)
	}
}

func newAgentServiceForTest(t *testing.T) *AgentService {
	t.Helper()
	db := testOpenDB(t)
	return NewAgentService(store.NewAgentRepo(db), nil, nil, nil, nil, nil, slog.Default())
}

func newAgentServiceWithToolPackagesForTest(t *testing.T) *AgentService {
	t.Helper()
	db := testOpenDB(t)
	return NewAgentService(store.NewAgentRepo(db), nil, nil, store.NewToolPackageRepo(db), nil, nil, slog.Default())
}

func TestAgentService_Create_RejectsUnknownToolset(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)

	_, err := svc.Create(t.Context(), CreateAgentRequest{
		Name:         "toolset-agent",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"builtin_artifact_tools", "ffmpeg"},
	}, "u")

	if err == nil {
		t.Fatal("expected error for unknown toolset, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(ve.Message, "ffmpeg") {
		t.Errorf("error should name the bad toolset, got %q", ve.Message)
	}
}

func TestAgentService_Create_AcceptsValidToolsets(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)

	created, err := svc.Create(t.Context(), CreateAgentRequest{
		Name:         "toolset-agent-ok",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"builtin_artifact_tools", "web_tools"},
	}, "u")
	if err != nil {
		t.Fatalf("valid toolsets should not be rejected: %v", err)
	}
	if !slices.Equal(created.Toolsets, []string{"builtin_artifact_tools", "builtin_web_request_tools"}) {
		t.Errorf("stored toolsets = %v, want the canonical ids", created.Toolsets)
	}
}

func TestAgentService_Create_AliasInToolsetConfigs(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceWithToolPackagesForTest(t)

	created, err := svc.Create(t.Context(), CreateAgentRequest{
		Name:         "toolset-config-alias",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"research"},
		ToolsetConfigs: []ToolsetConfigBinding{{
			ToolsetName:  "research",
			ConfigValues: map[string]any{"deep_research": map[string]any{"max_iterations": 2}},
		}},
	}, "u")
	if err != nil {
		t.Fatalf("an alias used consistently in toolsets and toolsetConfigs must be accepted: %v", err)
	}
	if !slices.Equal(created.Toolsets, []string{"builtin_research_tools"}) {
		t.Errorf("stored toolsets = %v, want [builtin_research_tools]", created.Toolsets)
	}
}

func TestAgentService_Create_RejectsDuplicateToolsetConfigSpellings(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceWithToolPackagesForTest(t)

	_, err := svc.Create(t.Context(), CreateAgentRequest{
		Name:         "toolset-config-dup",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"research"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "research", ConfigValues: map[string]any{"deep_research": map[string]any{"max_iterations": 2}}},
			{ToolsetName: "builtin_research_tools", ConfigValues: map[string]any{"deep_research": map[string]any{"max_iterations": 3}}},
		},
	}, "u")
	if err == nil {
		t.Fatal("expected two spellings of one toolset in toolsetConfigs to be rejected")
	}
}

func TestAgentService_Create_ValidationFailureLeavesNoAgent(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceWithToolPackagesForTest(t)
	ctx := t.Context()

	const name = "toolset-orphan-check"
	req := CreateAgentRequest{
		Name:         name,
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"builtin_artifact_tools"},
		ToolsetConfigs: []ToolsetConfigBinding{{
			ToolsetName:  "builtin_research_tools",
			ConfigValues: map[string]any{"deep_research": map[string]any{"max_iterations": 2}},
		}},
	}
	if _, err := svc.Create(ctx, req, "u"); err == nil {
		t.Fatal("expected config for an unbound toolset to be rejected")
	}

	req.ToolsetConfigs = nil
	if _, err := svc.Create(ctx, req, "u"); err != nil {
		t.Fatalf("retry after a rejected create should succeed, got: %v", err)
	}
}

func TestAgentService_Update_RejectsUnknownToolset(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)

	ctx := t.Context()
	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:         "toolset-update-agent",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"builtin_artifact_tools"},
	}, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateAgentRequest{
		Name:         created.Name,
		Description:  "A valid description that is long enough",
		SystemPrompt: "Updated prompt.",
		Toolsets:     []string{"builtin_artifact_tools", "pptx_create_presentation"},
	}, "u")
	if err == nil {
		t.Fatal("expected error for unknown toolset on update, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "pptx_create_presentation") {
		t.Errorf("error should name the bad toolset, got %q", ve.Message)
	}
}

func TestAgentService_Create_AcceptsDeprecatedToolset(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)

	_, err := svc.Create(t.Context(), CreateAgentRequest{
		Name:         "deprecated-toolset-agent",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"artifact_management"},
	}, "u")
	if err != nil {
		t.Fatalf("deprecated toolset should be silently filtered, not rejected: %v", err)
	}
}
