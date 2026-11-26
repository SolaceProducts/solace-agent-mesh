// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type builderApp struct {
	name      string
	appConfig map[string]any
	envelope  map[string]any
}

func parseBuilderApp(content string) (builderApp, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return builderApp{}, err
	}
	apps, ok := doc["apps"].([]any)
	if !ok || len(apps) == 0 {
		return builderApp{}, fmt.Errorf("no non-empty top-level apps: list")
	}
	first, ok := apps[0].(map[string]any)
	if !ok {
		return builderApp{}, fmt.Errorf("apps[0] is not a mapping")
	}
	appConfig, ok := first["app_config"].(map[string]any)
	if !ok {
		return builderApp{}, fmt.Errorf("apps[0].app_config is missing or not a mapping")
	}
	return builderApp{name: stringField(first, "name"), appConfig: appConfig, envelope: first}, nil
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func mapField(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func mapSliceField(m map[string]any, key string) []map[string]any {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if entry, ok := e.(map[string]any); ok {
			out = append(out, entry)
		}
	}
	return out
}

func stringSliceField(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
