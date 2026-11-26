// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const generalModelAlias = "general"

type generalModel struct {
	client   llm.Client
	memoKey  string
	spendKey string
	configID string
}

func resolveGeneralModel(ctx context.Context, core *entrypoint.GatewayCore) generalModel {
	m := resolveGeneralSource(ctx, core)
	if m.client != nil {
		m.client = core.MeteredClient(m.client, "")
	}
	return m
}

func resolveGeneralSource(ctx context.Context, core *entrypoint.GatewayCore) generalModel {
	svc := core.Services()
	if svc == nil {
		return generalModel{}
	}
	if reg := svc.Bindings(); reg != nil {
		if m, ok := boundGeneralModel(ctx, core, reg); ok {
			return m
		}
	}
	return defaultGeneralModel(svc.LLM(""))
}

func boundGeneralModel(ctx context.Context, core *entrypoint.GatewayCore, reg runtime.ModelBindings) (generalModel, bool) {
	client, err := reg.ResolveOrFetch(ctx, generalModelAlias)
	if err != nil {
		warnUnresolvedBinding(ctx, core, "general model bindings resolve failed; falling back to default LLM", err)
		return generalModel{}, false
	}
	if client == nil {
		return generalModel{}, false
	}
	raw, err := reg.ResolveRawOrFetch(ctx, generalModelAlias)
	if err != nil {
		warnUnresolvedBinding(ctx, core, "general model binding config resolve failed; recording its spend unpriced", err)
		return generalModel{client: client}, true
	}
	model, _ := raw["model"].(string)
	return generalModel{client: client, memoKey: model, spendKey: generalModelAlias, configID: samapp.ModelConfigID(raw)}, true
}

func warnUnresolvedBinding(ctx context.Context, core *entrypoint.GatewayCore, msg string, err error) {
	if errors.Is(err, bindings.ErrAliasUnbound) || ctx.Err() != nil {
		return
	}
	core.Logger().Warn(msg, "alias", generalModelAlias, "error", err)
}

func defaultGeneralModel(c llm.Client) generalModel {
	if c == nil {
		return generalModel{}
	}
	m := generalModel{client: c, memoKey: defaultClientKey(c)}
	if reconfigurable, ok := c.(*llm.ReconfigurableClient); ok {
		if id := reconfigurable.ModelConfigID(); id != "" {
			m.spendKey, m.configID = generalModelAlias, id
		}
	}
	return m
}

func gatewayHasLLM(core *entrypoint.GatewayCore) bool {
	svc := core.Services()
	if svc == nil {
		return false
	}
	return svc.Bindings() != nil || svc.LLM("") != nil
}

func defaultClientKey(c llm.Client) string {
	reconfigurable, ok := c.(*llm.ReconfigurableClient)
	if !ok {
		return ""
	}
	key := "default-llm#" + strconv.FormatUint(reconfigurable.Generation(), 10)
	if model := reconfigurable.Model(); model != "" {
		key += "/" + model
	}
	return key
}

func temperaturePolicyFor(model string, policy *llm.TemperaturePolicy, fallbackKey string) (*llm.TemperaturePolicy, string) {
	if model == "" {
		return nil, fallbackKey
	}
	return policy, model
}

func toolChoicePolicyFor(model string, policy *llm.ToolChoicePolicy) *llm.ToolChoicePolicy {
	if model == "" {
		return nil
	}
	return policy
}

func logAssistantLLMFailure(logger *slog.Logger, msg string, err error, args ...any) {
	args = append(args, "error", err)
	if entrypoint.IsBudgetRefusal(err) || entrypoint.IsBudgetUnconfirmed(err) {
		logger.Debug(msg, args...)
		return
	}
	logger.Warn(msg, args...)
}
