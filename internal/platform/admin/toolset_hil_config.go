// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const reservedHILConfigKey = "hil"

func validateToolsetHIL(val any, toolNames map[string]struct{}) map[string][]string {
	details := map[string][]string{}
	if val == nil {
		return details
	}
	m, ok := val.(map[string]any)
	if !ok {
		details[reservedHILConfigKey] = append(details[reservedHILConfigKey],
			fmt.Sprintf("expected object keyed by tool name, got %T", val))
		return details
	}
	for toolName, entryRaw := range m {
		path := reservedHILConfigKey + "." + toolName
		if len(toolNames) > 0 {
			if _, known := toolNames[toolName]; !known {
				details[path] = append(details[path], "unknown tool name for this toolset")
				continue
			}
		}
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			details[path] = append(details[path], fmt.Sprintf("expected object, got %T", entryRaw))
			continue
		}
		if _, hasTools := entry["tools"]; hasTools {
			details[path+".tools"] = append(details[path+".tools"],
				"`tools:` is only valid in an MCP-connector hil block; a toolset hil block is keyed by tool name directly")
			continue
		}
		if err := validateHILEntry(entry, path); err != nil {
			if ve, ok := errors.AsType[*ValidationError](err); ok {
				details[path] = append(details[path], ve.Message)
			} else {
				details[path] = append(details[path], err.Error())
			}
		}
	}
	return details
}

func hilForTool(agentHIL, tsHIL map[string]any, toolName string) map[string]any {
	if agentHIL != nil {
		if h, ok := agentHIL[toolName].(map[string]any); ok {
			return h
		}
	}
	if tsHIL != nil {
		if h, ok := tsHIL[toolName].(map[string]any); ok {
			return h
		}
	}
	return nil
}

func toolNamesFromFiles(files []store.ToolPackageFile) map[string]struct{} {
	names := make(map[string]struct{}, len(files))
	for _, f := range files {
		names[f.ToolName] = struct{}{}
	}
	return names
}
