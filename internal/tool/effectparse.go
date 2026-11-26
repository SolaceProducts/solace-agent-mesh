// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"fmt"
	"log/slog"
)

const EffectConfigKey = "effect"

func ParseEffectOverrides(log *slog.Logger, entries []HILEntry) (map[string]EffectClass, error) {
	if log == nil {
		log = discardLogger
	}
	overrides := make(map[string]EffectClass)
	for _, entry := range entries {
		raw, present := entry.ToolMap[EffectConfigKey]
		if !present {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("tool %q: %s must be a string, got %T", entryConfigName(entry), EffectConfigKey, raw)
		}
		effect, err := ParseEffect(s)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", entryConfigName(entry), err)
		}
		if effect != EffectSideEffecting {
			return nil, fmt.Errorf("tool %q: %s may only be %q; a tool cannot be marked %q in agent config", entryConfigName(entry), EffectConfigKey, EffectSideEffecting, effect)
		}
		if entry.Degraded || len(entry.ToolNames) == 0 {
			log.Warn("effect is set on an entry that did not register its full tool surface this boot, so the tools it did not register carry no operator override",
				"configName", entryConfigName(entry), "registeredTools", entry.ToolNames)
		}
		for _, name := range entry.ToolNames {
			overrides[name] = EffectClass{Effect: EffectSideEffecting, Source: EffectSourceOperator}
		}
	}
	return overrides, nil
}
