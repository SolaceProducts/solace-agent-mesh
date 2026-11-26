// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"maps"
	"sync"
)

var (
	toolDefaultsMu sync.RWMutex
	toolDefaults   map[string]map[string]any
)

func SetToolDefaults(defaults map[string]map[string]any) {
	toolDefaultsMu.Lock()
	defer toolDefaultsMu.Unlock()
	toolDefaults = copyDefaults(defaults)
}

func MergeToolDefaults(more map[string]map[string]any) {
	if len(more) == 0 {
		return
	}
	toolDefaultsMu.Lock()
	defer toolDefaultsMu.Unlock()
	if toolDefaults == nil {
		toolDefaults = make(map[string]map[string]any, len(more))
	}
	for name, block := range more {
		existing := toolDefaults[name]
		if existing == nil {
			existing = make(map[string]any, len(block))
			toolDefaults[name] = existing
		}
		for k, v := range block {
			existing[k] = v
		}
	}
}

func copyDefaults(in map[string]map[string]any) map[string]map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(in))
	for name, block := range in {
		if len(block) == 0 {
			continue
		}
		clone := make(map[string]any, len(block))
		for k, v := range block {
			clone[k] = v
		}
		out[name] = clone
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toolDefaultsFor(toolName string) map[string]any {
	toolDefaultsMu.RLock()
	defer toolDefaultsMu.RUnlock()
	src := toolDefaults[toolName]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	maps.Copy(out, src)
	return out
}

func applyToolDefaults(toolName string, toolMap map[string]any) map[string]any {
	defaults := toolDefaultsFor(toolName)
	if defaults == nil {
		return toolMap
	}
	existing, _ := toolMap["tool_config"].(map[string]any)
	merged := defaults
	for k, v := range existing {
		merged[k] = v
	}
	out := make(map[string]any, len(toolMap)+1)
	maps.Copy(out, toolMap)
	out["tool_config"] = merged
	return out
}
