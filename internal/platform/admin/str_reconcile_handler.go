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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

const strReconcilePageSize = 200

const (
	reasonToolExists  = "tool.exists"
	reasonSkillExists = "skill.exists"
)

type STRReconcileHandler struct {
	brk             broker.Broker
	toolPackageRepo *store.ToolPackageRepo
	skillRepo       *store.SkillRepo
	namespace       string
	logger          *slog.Logger
}

func NewSTRReconcileHandler(
	brk broker.Broker,
	toolPackageRepo *store.ToolPackageRepo,
	skillRepo *store.SkillRepo,
	namespace string,
	logger *slog.Logger,
) *STRReconcileHandler {
	return &STRReconcileHandler{
		brk:             brk,
		toolPackageRepo: toolPackageRepo,
		skillRepo:       skillRepo,
		namespace:       namespace,
		logger:          withLoggerName(logger, "str-reconcile-handler"),
	}
}

func (h *STRReconcileHandler) Handle(ctx context.Context) {
	if h == nil || h.brk == nil {
		return
	}
	tools := h.publishExistingTools(ctx)
	skills := h.publishExistingSkills(ctx)
	h.logger.Info("STR reconcile handler: published notifications",
		"tools", tools, "skills", skills)
}

func (h *STRReconcileHandler) publishExistingTools(ctx context.Context) int {
	if h.toolPackageRepo == nil {
		return 0
	}
	published := 0
	for page := 1; ; page++ {
		pkgs, total, err := h.toolPackageRepo.ListPaginated(ctx, page, strReconcilePageSize, "", nil)
		if err != nil {
			h.logger.Warn("list tool packages for reconcile failed", "error", err)
			return published
		}
		for _, p := range pkgs {
			if p.Name == "" {
				continue
			}
			if pubErr := h.publish(ctx, "tools", reasonToolExists, p.Name); pubErr != nil {
				h.logger.Warn("publish tool reconcile failed", "pkg", p.Name, "error", pubErr)
				continue
			}
			published++
		}
		if page*strReconcilePageSize >= total {
			break
		}
	}
	return published
}

func (h *STRReconcileHandler) publishBundledSkillsForFreshSTRStartup(ctx context.Context) int {
	published := 0
	for _, name := range builtinskills.Names() {
		if pubErr := h.publish(ctx, "skills", reasonSkillExists, name); pubErr != nil {
			h.logger.Warn("publish builtin skill reconcile failed", "skill", name, "error", pubErr)
			continue
		}
		published++
	}
	for _, sk := range design.Skills() {
		if pubErr := h.publish(ctx, "skills", reasonSkillExists, sk.Name); pubErr != nil {
			h.logger.Warn("publish design skill reconcile failed", "skill", sk.Name, "error", pubErr)
			continue
		}
		published++
	}
	for _, name := range schemaSkillNames() {
		if pubErr := h.publish(ctx, "skills", reasonSkillExists, name); pubErr != nil {
			h.logger.Warn("publish schema skill reconcile failed", "skill", name, "error", pubErr)
			continue
		}
		published++
	}
	return published
}

func (h *STRReconcileHandler) publishExistingSkills(ctx context.Context) int {
	published := h.publishBundledSkillsForFreshSTRStartup(ctx)

	if h.skillRepo == nil {
		return published
	}
	for page := 1; ; page++ {
		skills, total, err := h.skillRepo.ListPaginated(ctx, page, strReconcilePageSize)
		if err != nil {
			h.logger.Warn("list skills for reconcile failed", "error", err)
			return published
		}
		for _, s := range skills {
			if s.Name == "" {
				continue
			}
			if pubErr := h.publish(ctx, "skills", reasonSkillExists, s.Name); pubErr != nil {
				h.logger.Warn("publish skill reconcile failed", "skill", s.Name, "error", pubErr)
				continue
			}
			published++
		}
		if page*strReconcilePageSize >= total {
			break
		}
	}
	return published
}

func (h *STRReconcileHandler) publish(ctx context.Context, category, reason, name string) error {
	payload, err := json.Marshal(syncNotification{
		MessageID: idgen.New(),
		Category:  category,
		Reason:    reason,
		Name:      name,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	topic := a2a.SyncNotifyTopic(h.namespace, category)
	return h.brk.PublishGuaranteed(ctx, topic, &broker.Message{Payload: payload})
}
