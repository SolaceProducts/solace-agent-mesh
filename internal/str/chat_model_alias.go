// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const ToolConfigModelAliasesKey = "_model_aliases"

var errChatModelNotPermitted = errors.New("model alias not permitted for this tool")

func toolChatModelEnabled() bool {
	r := features.NewRegistry()
	if err := features.LoadFlags(r); err != nil {
		return false
	}
	return r.IsEnabled(features.KeyToolChatModel)
}

type toolModelChoice struct {
	alias     string
	requested bool
}

type toolChatModels struct {
	configured string
	own        string
	enabled    bool
	restricted bool
	allowed    []string
}

func toolConfigChatModels(tc map[string]any, enabled bool, strToolModelAlias string) (toolChatModels, error) {
	configured, err := toolConfigModelAlias(tc)
	if err != nil {
		return toolChatModels{}, err
	}
	if !enabled {
		return toolChatModels{configured: configured}, nil
	}
	allowed, restricted, err := toolConfigModelAliases(tc)
	if err != nil {
		return toolChatModels{}, err
	}
	own := cmp.Or(configured, strings.TrimSpace(strToolModelAlias), defaultToolModelAlias)
	return toolChatModels{configured: configured, own: own, enabled: true, restricted: restricted, allowed: allowed}, nil
}

func toolConfigModelAliases(tc map[string]any) (aliases []string, present bool, err error) {
	raw, ok := tc[ToolConfigModelAliasesKey]
	if !ok || raw == nil {
		return nil, false, nil
	}
	var parts []string
	switch v := raw.(type) {
	case string:
		parts = strings.Split(v, ",")
	case []string:
		parts = v
	case []any:
		for _, item := range v {
			s, isString := item.(string)
			if !isString {
				return nil, true, errModelAliasesFormat(item)
			}
			parts = append(parts, s)
		}
	default:
		return nil, true, errModelAliasesFormat(raw)
	}
	for _, part := range parts {
		if alias := strings.TrimSpace(part); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	return aliases, true, nil
}

func errModelAliasesFormat(got any) error {
	return fmt.Errorf("tool_config %q must be a comma-separated string or a list of model alias strings, got %T", ToolConfigModelAliasesKey, got)
}

func (m toolChatModels) pick(model string) (toolModelChoice, error) {
	model = strings.TrimSpace(model)
	if !m.enabled || model == "" {
		return toolModelChoice{alias: m.configured}, nil
	}
	if _, err := uuid.Parse(model); err == nil {
		return toolModelChoice{}, fmt.Errorf("%w: %q is a model configuration id; name a model alias", errChatModelNotPermitted, model)
	}
	if m.restricted && model != m.own && !slices.Contains(m.allowed, model) {
		return toolModelChoice{}, fmt.Errorf("%w: model alias %q is not listed in tool_config %q", errChatModelNotPermitted, model, ToolConfigModelAliasesKey)
	}
	return toolModelChoice{alias: model, requested: true}, nil
}
