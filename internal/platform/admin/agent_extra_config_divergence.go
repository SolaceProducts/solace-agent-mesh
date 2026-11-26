// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
)

func DivergentAgentExtraConfigKeys(blob map[string]any) []string {
	return divergentExtraConfigKeys(blob, agentExtraConfigSchema)
}

func divergentExtraConfigKeys(blob map[string]any, schema map[string]agentExtraConfigKey) []string {
	var out []string
	for _, canonical := range slices.Sorted(maps.Keys(schema)) {
		entry := schema[canonical]
		if extraConfigKeyDiverges(blob, canonical, entry, schema) {
			out = append(out, runtimeKeyOf(canonical, entry))
		}
	}
	return out
}

func extraConfigKeyDiverges(blob map[string]any, canonical string, entry agentExtraConfigKey, schema map[string]agentExtraConfigKey) bool {
	value, held := blob[canonical]
	if !held {
		return false
	}
	runtime := runtimeKeyOf(canonical, entry)
	if copied, heldRuntime := blob[runtime]; runtime != canonical && heldRuntime {
		translated := translateAgentExtraConfig(map[string]any{canonical: value}, schema, nil, "")[runtime]
		if !sameJSON(translated, copied) {
			return true
		}
	}
	return holdsDivergentPair(value, entry)
}

func holdsDivergentPair(value any, entry agentExtraConfigKey) bool {
	if entry.Nested == nil {
		return false
	}
	switch held := value.(type) {
	case map[string]any:
		return entry.Type == extraTypeObject && len(divergentExtraConfigKeys(held, entry.Nested)) > 0
	case []any:
		if entry.Type != extraTypeList {
			return false
		}
		return slices.ContainsFunc(held, func(item any) bool {
			m, isMap := item.(map[string]any)
			return isMap && len(divergentExtraConfigKeys(m, entry.Nested)) > 0
		})
	}
	return false
}

func sameJSON(a, b any) bool {
	var left, right any
	for _, pair := range []struct {
		in  any
		out *any
	}{{a, &left}, {b, &right}} {
		encoded, err := json.Marshal(pair.in)
		if err != nil || json.Unmarshal(encoded, pair.out) != nil {
			return false
		}
	}
	return reflect.DeepEqual(left, right)
}
