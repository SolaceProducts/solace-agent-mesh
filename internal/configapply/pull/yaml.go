// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

type resourceDoc struct {
	Kind        string         `yaml:"kind"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Spec        map[string]any `yaml:"spec,omitempty"`
}

func emitYAML(d resourceDoc) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("marshal resource: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func take(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		delete(m, key)
		return v, true
	}
	return nil, false
}

func takeString(m map[string]any, key string) (string, bool) {
	v, ok := take(m, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		m[key] = v
		return "", false
	}
	return s, true
}

func dropEmpty(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case nil:
			continue
		case string:
			if x == "" {
				continue
			}
			out[k] = x
		case []any:
			if len(x) == 0 {
				continue
			}
			out[k] = x
		case []string:
			if len(x) == 0 {
				continue
			}
			out[k] = x
		case map[string]any:
			if len(x) == 0 {
				continue
			}
			out[k] = x
		default:
			out[k] = v
		}
	}
	return out
}
