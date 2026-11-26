// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type entryKeyOutcome int

const (
	entryKeyRead entryKeyOutcome = iota + 1
	entryKeyRefused
	entryKeyUnstored
)

var builtinEntryKeyOutcomes = map[string]entryKeyOutcome{
	"tool_type": entryKeyRead, "tool_name": entryKeyRead, "group_name": entryKeyRead,
	"required_scopes": entryKeyRefused, "auth": entryKeyRefused,
	"tool_config": entryKeyRead, "hil": entryKeyRead,
	"timeout_seconds": entryKeyUnstored, "sandbox_profile": entryKeyUnstored, "parameters": entryKeyUnstored,
	"tool_description": entryKeyUnstored, "volume_params": entryKeyUnstored, "volume_bindings": entryKeyUnstored,
	"effect": entryKeyRefused,
}

var refusedEntryKeyConsequences = map[string]string{
	"required_scopes": "change who may run the tool",
	"auth":            "change who may run the tool",
	"effect":          "drop the operator's effect override and leave the tool on its own classification",
}

func isBuiltinEntry(entry map[string]any) bool {
	toolType := stringField(entry, "tool_type")
	return toolType == "builtin" || toolType == "builtin-group"
}

func builtinEntryName(entry map[string]any) string {
	return cmp.Or(stringField(entry, "tool_name"), stringField(entry, "group_name"))
}

func sortedEntryKeys(entry map[string]any) []string {
	return slices.Sorted(maps.Keys(entry))
}

func rejectUnsavedToolGates(componentName string, appConfig map[string]any) error {
	raw, _ := appConfig["tools"].([]any)
	var messages []string
	for i, e := range raw {
		entry, isMapping := e.(map[string]any)
		if !isMapping || !isBuiltinEntry(entry) {
			continue
		}
		for _, key := range sortedEntryKeys(entry) {
			if builtinEntryKeyOutcomes[key] != entryKeyRefused {
				continue
			}
			messages = append(messages, fmt.Sprintf(
				"tools[%d] (%s): %s is not saved for a built-in tool, and dropping it would %s; remove it from the entry",
				i, builtinEntryName(entry), key, refusedEntryKeyConsequences[key]))
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return &entplatform.ValidationError{
		Message: fmt.Sprintf("agent %q: %s", componentName, strings.Join(messages, "; ")),
		Details: map[string][]string{"agent " + componentName: messages},
	}
}

func unstoredEntryKeyWarnings(i int, entry map[string]any) []string {
	var warnings []string
	name := builtinEntryName(entry)
	for _, key := range sortedEntryKeys(entry) {
		switch builtinEntryKeyOutcomes[key] {
		case entryKeyRead, entryKeyRefused:
		case entryKeyUnstored:
			warnings = append(warnings, fmt.Sprintf("tools[%d] (%s): %s has no storage for a built-in tool and was not saved", i, name, key))
		default:
			warnings = append(warnings, fmt.Sprintf("tools[%d] (%s): %s was not recognised and was not saved", i, name, key))
		}
	}
	return warnings
}
