// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "log/slog"

func translateAgentExtraConfig(blob map[string]any, schema map[string]agentExtraConfigKey, logger *slog.Logger, agentID string) map[string]any {
	if len(blob) == 0 {
		return nil
	}
	out := make(map[string]any, len(blob))
	for jsonKey, val := range blob {
		entry, recognised := schema[jsonKey]
		runtimeKey := jsonKey
		if recognised {
			runtimeKey = runtimeKeyOf(jsonKey, entry)
		} else if logger != nil {
			logger.Debug("agent additional_configurations: unknown key copied verbatim during YAML translation",
				"agentID", agentID,
				"key", jsonKey)
		}
		if recognised && entry.Type == extraTypeObject && entry.Nested != nil {
			child, isMap := val.(map[string]any)
			if isMap {
				out[runtimeKey] = translateAgentExtraConfig(child, entry.Nested, logger, agentID)
				continue
			}
			out[runtimeKey] = val
			continue
		}
		if recognised && entry.Type == extraTypeList && entry.Nested != nil {
			arr, isArr := val.([]any)
			if isArr {
				translated := make([]any, len(arr))
				for i, item := range arr {
					if m, ok := item.(map[string]any); ok {
						translated[i] = translateAgentExtraConfig(m, entry.Nested, logger, agentID)
					} else {
						translated[i] = item
					}
				}
				out[runtimeKey] = translated
				continue
			}
			out[runtimeKey] = val
			continue
		}
		out[runtimeKey] = val
	}
	return out
}

func reverseTranslateAgentExtraConfig(blob map[string]any, schema map[string]agentExtraConfigKey) map[string]any {
	if len(blob) == 0 {
		return blob
	}
	type schemaIndexEntry struct {
		Canonical string
		Entry     agentExtraConfigKey
	}
	runtimeIndex := make(map[string]schemaIndexEntry, len(schema))
	for canonical, entry := range schema {
		runtimeIndex[runtimeKeyOf(canonical, entry)] = schemaIndexEntry{
			Canonical: canonical,
			Entry:     entry,
		}
	}
	out := make(map[string]any, len(blob))
	for k, val := range blob {
		idx, found := runtimeIndex[k]
		if !found {
			out[k] = val
			continue
		}
		canonical := idx.Canonical
		if idx.Entry.Type == extraTypeObject && idx.Entry.Nested != nil {
			child, isMap := val.(map[string]any)
			if isMap {
				out[canonical] = reverseTranslateAgentExtraConfig(child, idx.Entry.Nested)
				continue
			}
		}
		if idx.Entry.Type == extraTypeList && idx.Entry.Nested != nil {
			arr, isArr := val.([]any)
			if isArr {
				translated := make([]any, len(arr))
				for i, item := range arr {
					if m, ok := item.(map[string]any); ok {
						translated[i] = reverseTranslateAgentExtraConfig(m, idx.Entry.Nested)
					} else {
						translated[i] = item
					}
				}
				out[canonical] = translated
				continue
			}
		}
		out[canonical] = val
	}
	return out
}
