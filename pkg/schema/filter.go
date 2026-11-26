// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"gopkg.in/yaml.v3"
)

func FilterSystemFields(yamlBytes []byte) ([]byte, error) {
	var schema map[string]any
	if err := yaml.Unmarshal(yamlBytes, &schema); err != nil {
		return nil, err
	}

	filterObject(schema)

	if _, hasProps := schema["properties"]; hasProps {
		schema["additionalProperties"] = false
	}

	return yaml.Marshal(schema)
}

func filterObject(obj map[string]any) {
	if propsRaw, ok := obj["properties"]; ok {
		if props, ok := propsRaw.(map[string]any); ok {
			var systemNames []string

			for name, valRaw := range props {
				valMap, ok := valRaw.(map[string]any)
				if !ok {
					continue
				}

				if isSystem(valMap) {
					systemNames = append(systemNames, name)
				} else {
					filterObject(valMap)
				}
			}

			for _, name := range systemNames {
				delete(props, name)
			}

			if len(systemNames) > 0 {
				pruneRequired(obj, systemNames)
			}
		}
	}

	if itemsRaw, ok := obj["items"]; ok {
		if items, ok := itemsRaw.(map[string]any); ok {
			filterObject(items)
		}
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if listRaw, ok := obj[key]; ok {
			if list, ok := listRaw.([]any); ok {
				for _, itemRaw := range list {
					if item, ok := itemRaw.(map[string]any); ok {
						filterObject(item)
					}
				}
			}
		}
	}

	if defsRaw, ok := obj["$defs"]; ok {
		if defs, ok := defsRaw.(map[string]any); ok {
			for _, defRaw := range defs {
				if def, ok := defRaw.(map[string]any); ok {
					filterObject(def)
				}
			}
		}
	}
}

func isSystem(propDef map[string]any) bool {
	v, ok := propDef["x-sam-system"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func pruneRequired(obj map[string]any, names []string) {
	reqRaw, ok := obj["required"]
	if !ok {
		return
	}
	reqList, ok := reqRaw.([]any)
	if !ok {
		return
	}

	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}

	var filtered []any
	for _, item := range reqList {
		s, ok := item.(string)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		if _, remove := nameSet[s]; !remove {
			filtered = append(filtered, item)
		}
	}

	if len(filtered) == 0 {
		delete(obj, "required")
	} else {
		obj["required"] = filtered
	}
}
