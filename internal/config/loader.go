// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type YAMLLoader struct {
	Lookup func(string) (string, bool)
}

func (l *YAMLLoader) Load(_ context.Context, path string) (Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving path %q: %w", path, err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", absPath, err)
	}

	baseDir := filepath.Dir(absPath)

	text, err := ProcessIncludes(string(data), baseDir)
	if err != nil {
		return nil, fmt.Errorf("processing includes in %q: %w", absPath, err)
	}

	text = ExpandVars(text, l.effectiveLookup())

	return parseYAML([]byte(text))
}

func (l *YAMLLoader) effectiveLookup() func(string) (string, bool) {
	if l.Lookup != nil {
		return l.Lookup
	}
	return os.LookupEnv
}

func (l *YAMLLoader) LoadBytes(_ context.Context, data []byte) (Config, error) {
	text := ExpandVars(string(data), l.effectiveLookup())
	return parseYAML([]byte(text))
}

func parseYAML(data []byte) (Config, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}
	if raw == nil {
		raw = make(map[string]any)
	}
	return &mapConfig{data: raw}, nil
}
