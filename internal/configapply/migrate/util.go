// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

var cruftKeys = map[string]struct{}{
	"broker":                {},
	"session_service":       {},
	"artifact_service":      {},
	"data_tools_config":     {},
	"app_module":            {},
	"app_exec":              {},
	"app_base_path":         {},
	"trust_manager":         {},
	"observability":         {},
	"log":                   {},
	"agent_card_publishing": {},
	"agent_discovery":       {},
}

func extractAppConfig(doc map[string]any) (map[string]any, error) {
	apps, ok := doc["apps"]
	if !ok {
		return nil, fmt.Errorf("no top-level apps: key (legacy SAC files always have one)")
	}
	appsList, ok := apps.([]any)
	if !ok || len(appsList) == 0 {
		return nil, fmt.Errorf("apps: is not a non-empty list")
	}
	first, ok := appsList[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("apps[0] is not a mapping")
	}
	rawAC, ok := first["app_config"]
	if !ok {
		return nil, fmt.Errorf("apps[0].app_config missing")
	}
	ac, ok := rawAC.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("apps[0].app_config is not a mapping")
	}
	return ac, nil
}

func firstAppName(doc map[string]any) string {
	apps, ok := doc["apps"]
	if !ok {
		return ""
	}
	appsList, ok := apps.([]any)
	if !ok || len(appsList) == 0 {
		return ""
	}
	first, ok := appsList[0].(map[string]any)
	if !ok {
		return ""
	}
	if name, ok := first["name"].(string); ok {
		return name
	}
	return ""
}

func dropCruft(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, ok := cruftKeys[k]; ok {
			continue
		}
		out[k] = v
	}
	return out
}

func asStringList(v any) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return s, true
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			str, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	case string:
		return []string{s}, true
	}
	return nil, false
}

func renderCleanSpec(doc CleanSpec) ([]byte, error) {
	var headerBuf bytes.Buffer
	orderedHeader := []map[string]any{}
	orderedHeader = append(orderedHeader, map[string]any{"kind": doc.Kind})
	orderedHeader = append(orderedHeader, map[string]any{"name": doc.Name})
	if doc.Description != "" {
		orderedHeader = append(orderedHeader, map[string]any{"description": doc.Description})
	}
	for _, kv := range orderedHeader {
		body, err := yaml.Marshal(kv)
		if err != nil {
			return nil, fmt.Errorf("encode header: %w", err)
		}
		headerBuf.Write(body)
	}

	if len(doc.Spec) == 0 {
		return headerBuf.Bytes(), nil
	}
	specBody, err := yaml.Marshal(map[string]any{"spec": doc.Spec})
	if err != nil {
		return nil, fmt.Errorf("encode spec: %w", err)
	}
	headerBuf.Write(specBody)
	return headerBuf.Bytes(), nil
}

type CleanSpec struct {
	Kind        string
	Name        string
	Description string
	Spec        map[string]any
}
