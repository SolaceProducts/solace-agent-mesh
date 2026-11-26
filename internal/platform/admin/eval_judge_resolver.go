// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type judgeModelResolver struct {
	svc          *ModelConfigService
	defaultAlias string
	logger       *slog.Logger
}

func newJudgeModelResolver(svc *ModelConfigService, defaultAlias string, logger *slog.Logger) eval.JudgeModelResolver {
	if svc == nil {
		return nil
	}
	return &judgeModelResolver{svc: svc, defaultAlias: defaultAlias, logger: logger}
}

func (r *judgeModelResolver) Resolve(ctx context.Context, modelAliasOrID string) (map[string]any, string, bool, error) {
	raw, snapshot, err := r.svc.GetForSnapshot(ctx, modelAliasOrID)
	if err != nil {
		return nil, "", false, err
	}
	if raw == nil {
		return nil, "", false, nil
	}
	if samapp.IsDecisionModel(raw) {
		return nil, "", false, fmt.Errorf("model %q is a decision model; an evaluator judge needs a chat model", modelAliasOrID)
	}
	if alias, _ := snapshot["alias"].(string); alias == r.defaultAlias {
		return raw, "", false, nil
	}
	id, _ := snapshot["id"].(string)
	if id == "" {
		return raw, "", false, nil
	}
	return raw, id, true, nil
}

type decisionModelResolver struct {
	svc *ModelConfigService
}

func newDecisionModelResolver(svc *ModelConfigService) eval.DecisionModelResolver {
	if svc == nil {
		return nil
	}
	return &decisionModelResolver{svc: svc}
}

func (r *decisionModelResolver) ResolveDecision(ctx context.Context, aliasOrID string) (map[string]any, error) {
	raw, _, err := r.svc.GetForSnapshot(ctx, aliasOrID)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: no configured model is named %q", eval.ErrDecisionModelUnavailable, aliasOrID)
	}
	if !samapp.IsDecisionModel(raw) {
		return nil, fmt.Errorf("%w: model %q is a chat model; a decision evaluator needs a decision model", eval.ErrDecisionModelUnavailable, aliasOrID)
	}
	return raw, nil
}

func newEvaluatorModelTypeLookup(svc *ModelConfigService) eval.ModelTypeLookup {
	if svc == nil {
		return nil
	}
	return func(ctx context.Context, aliasOrID string) (string, bool, error) {
		raw, _, err := svc.GetForSnapshot(ctx, aliasOrID)
		if err != nil || raw == nil {
			return "", false, err
		}
		if samapp.IsDecisionModel(raw) {
			return store.ModelTypeDecision, true, nil
		}
		return store.ModelTypeChat, true, nil
	}
}
