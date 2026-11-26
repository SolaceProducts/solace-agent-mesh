// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

type toolsetEventSink interface {
	HandleToolInit(ctx context.Context, prefixedToolName, description string, meta ToolInitMeta)
	HandleToolRemoved(ctx context.Context, prefixedToolName string)
	HandleToolFailed(ctx context.Context, prefixedToolName, errMsg string)
}

type DBResourceEventHandler struct {
	skillSvc   *SkillService
	toolSvc    *ToolService
	toolsetSvc toolsetEventSink
	logger     *slog.Logger
}

func NewDBResourceEventHandler(skillSvc *SkillService, toolSvc *ToolService, toolsetSvc toolsetEventSink, logger *slog.Logger) *DBResourceEventHandler {
	return &DBResourceEventHandler{
		skillSvc:   skillSvc,
		toolSvc:    toolSvc,
		toolsetSvc: toolsetSvc,
		logger:     logger,
	}
}

func (h *DBResourceEventHandler) OnToolInit(ctx context.Context, params remotetool.InitParams) {
	h.toolSvc.UpsertFromInit(ctx, params)

	if h.toolsetSvc == nil {
		return
	}

	var paramsJSON *string
	if params.Parameters != nil {
		b, err := json.Marshal(params.Parameters)
		if err != nil {
			h.logger.Warn("failed to marshal init params", "error", err, "tool", params.ToolName)
		} else {
			s := string(b)
			paramsJSON = &s
		}
	}

	var configSchemaJSON *string
	if len(params.ConfigSchema) > 0 {
		b, err := json.Marshal(params.ConfigSchema)
		if err != nil {
			h.logger.Warn("failed to marshal config schema", "error", err, "tool", params.ToolName)
		} else {
			s := string(b)
			configSchemaJSON = &s
		}
	}

	var capabilitiesJSON *string
	if params.Capabilities != nil {
		b, err := json.Marshal(params.Capabilities)
		if err != nil {
			h.logger.Warn("failed to marshal capabilities", "error", err, "tool", params.ToolName)
		} else {
			s := string(b)
			capabilitiesJSON = &s
		}
	}

	var authJSON *string
	if params.Auth != nil {
		b, err := json.Marshal(params.Auth)
		if err != nil {
			h.logger.Warn("failed to marshal tool-declared auth", "error", err, "tool", params.ToolName)
		} else {
			s := string(b)
			authJSON = &s
		}
	}

	h.toolsetSvc.HandleToolInit(ctx, params.ToolName, params.Description, ToolInitMeta{
		Params:           paramsJSON,
		ConfigSchema:     configSchemaJSON,
		Capabilities:     capabilitiesJSON,
		Auth:             authJSON,
		PackageToolNames: params.PackageToolNames,
		TimeoutSeconds:   params.TimeoutSeconds,
	})
}

func (h *DBResourceEventHandler) OnToolFailed(ctx context.Context, params remotetool.FailedDiscoveryParams) {
	if h.toolsetSvc != nil {
		h.toolsetSvc.HandleToolFailed(ctx, params.ToolName, params.ErrorMessage)
	}
	if h.skillSvc != nil {
		h.skillSvc.HandleToolFailed(ctx, params.ToolName, params.ErrorMessage)
	}
}

func (h *DBResourceEventHandler) OnToolRemoved(ctx context.Context, toolName string) {
	h.toolSvc.MarkUnavailable(ctx, toolName)

	if h.toolsetSvc != nil {
		h.toolsetSvc.HandleToolRemoved(ctx, toolName)
	}
}

func (h *DBResourceEventHandler) OnSkillInit(ctx context.Context, params remotetool.SkillInitParams) {
	h.skillSvc.UpsertFromInit(ctx, params)
}

func (h *DBResourceEventHandler) OnSkillRemoved(ctx context.Context, skillName string) {
	h.skillSvc.MarkUnavailable(ctx, skillName)
}
