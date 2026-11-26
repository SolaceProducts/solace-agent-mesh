// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ToolService struct {
	toolRepo *store.ToolRepo
	logger   *slog.Logger
	now      func() time.Time
}

func NewToolService(toolRepo *store.ToolRepo, logger *slog.Logger) *ToolService {
	return &ToolService{
		toolRepo: toolRepo,
		logger:   logger,
		now:      time.Now,
	}
}

const toolAnnouncedRefresh = time.Hour

func (s *ToolService) GetByID(ctx context.Context, id string) (*ToolDetailResponse, error) {
	tool, err := s.toolRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get tool: %w", err)
	}
	if tool == nil {
		return nil, &NotFoundError{Resource: "tool", ID: id}
	}
	return buildToolResponse(tool), nil
}

func (s *ToolService) GetByName(ctx context.Context, name string) (*ToolDetailResponse, error) {
	tool, err := s.toolRepo.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get tool by name: %w", err)
	}
	if tool == nil {
		return nil, &NotFoundError{Resource: "tool", ID: name}
	}
	return buildToolResponse(tool), nil
}

func (s *ToolService) ListPaginated(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[ToolDetailResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	tools, total, err := s.toolRepo.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	responses := make([]ToolDetailResponse, len(tools))
	for i, t := range tools {
		responses[i] = *buildToolResponse(&t)
	}

	return &PaginatedResponse[ToolDetailResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *ToolService) UpsertFromInit(ctx context.Context, params remotetool.InitParams) {
	var paramsJSON *string
	if params.Parameters != nil {
		if b, err := json.Marshal(params.Parameters); err == nil {
			str := string(b)
			paramsJSON = &str
		}
	}

	var artifactParamsJSON *string
	if params.ArtifactParams != nil {
		if b, err := json.Marshal(params.ArtifactParams); err == nil {
			str := string(b)
			artifactParamsJSON = &str
		}
	}

	var instructions *string
	if params.Instructions != "" {
		instructions = &params.Instructions
	}

	desc := params.Description
	tool := &store.Tool{
		Name:           params.ToolName,
		Description:    &desc,
		Origin:         "filesystem",
		Status:         "available",
		Parameters:     paramsJSON,
		Instructions:   instructions,
		ArtifactParams: artifactParamsJSON,
		CreatedBy:      "system",
		UpdatedBy:      "system",
	}

	existing, err := s.toolRepo.GetByName(ctx, params.ToolName)
	if err != nil {
		s.logger.Warn("load tool before reconcile", "tool", params.ToolName, "error", err)
		return
	}
	if existing != nil && toolContentEqual(existing, tool) {
		s.refreshAnnounced(ctx, existing)
		return
	}

	if err := s.toolRepo.UpsertByName(ctx, tool); err != nil {
		s.logger.Error("upsert tool from init", "tool", params.ToolName, "error", err)
	} else {
		s.logger.Debug("tool reconciled from init", "tool", params.ToolName)
	}
}

func (s *ToolService) refreshAnnounced(ctx context.Context, existing *store.Tool) {
	now := s.now()
	if now.Sub(time.UnixMilli(existing.UpdatedTime)) < toolAnnouncedRefresh {
		return
	}
	if err := s.toolRepo.Touch(ctx, existing.Name, now.UnixMilli()); err != nil {
		s.logger.Warn("refresh announced tool", "tool", existing.Name, "error", err)
	}
}

func toolContentEqual(existing, incoming *store.Tool) bool {
	return strPtrEqual(existing.Description, incoming.Description) &&
		existing.Status == incoming.Status &&
		strPtrEqual(existing.Parameters, incoming.Parameters) &&
		strPtrEqual(existing.Instructions, incoming.Instructions) &&
		strPtrEqual(existing.ArtifactParams, incoming.ArtifactParams)
}

func (s *ToolService) MarkUnavailable(ctx context.Context, toolName string) {
	if err := s.toolRepo.SetStatus(ctx, toolName, "unavailable"); err != nil {
		s.logger.Error("mark tool unavailable", "tool", toolName, "error", err)
	} else {
		s.logger.Debug("tool marked unavailable", "tool", toolName)
	}
}

func buildToolResponse(t *store.Tool) *ToolDetailResponse {
	resp := &ToolDetailResponse{
		ID:          t.ID,
		Name:        t.Name,
		Description: derefStr(t.Description),
		Origin:      t.Origin,
		Status:      t.Status,
		CreatedBy:   t.CreatedBy,
		UpdatedBy:   t.UpdatedBy,
		CreatedTime: Timestamp(t.CreatedTime),
		UpdatedTime: Timestamp(t.UpdatedTime),
	}

	if t.Parameters != nil {
		var params any
		if err := json.Unmarshal([]byte(*t.Parameters), &params); err == nil {
			resp.Parameters = params
		}
	}
	if t.Instructions != nil {
		resp.Instructions = *t.Instructions
	}
	if t.ArtifactParams != nil {
		var ap any
		if err := json.Unmarshal([]byte(*t.ArtifactParams), &ap); err == nil {
			resp.ArtifactParams = ap
		}
	}

	return resp
}
