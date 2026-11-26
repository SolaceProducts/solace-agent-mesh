// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

var migratedHandlerKeys = map[string]bool{
	"name":                     true,
	"subscriptions":            true,
	"existing_queue_name":      true,
	"payload_format":           true,
	"payload_encoding":         true,
	"event_filter":             true,
	"target_agent_name":        true,
	"target_workflow_name":     true,
	"input_expression":         true,
	"default_user_identity":    true,
	"user_identity_expression": true,
	"run_as":                   true,
	"forward_context":          true,
	"structured_invocation":    true,
	"acknowledgment_policy":    true,
	"artifact_processing":      true,
	"on_success":               true,
	"on_error":                 true,
}

var DynamicTargetKeys = []string{"target_agent_name_expression", "target_workflow_name_expression"}

var handlerKeyAliases = map[string]string{
	"targetAgent":    "target_agent_name",
	"target":         "target_agent_name or target_workflow_name",
	"promptTemplate": `input_expression ("template:" + sacexpr, e.g. "template:Summarize {{json://input.payload}}")`,
	"messageFormat":  "payload_format",
	"successOutput":  "on_success (naming an output_handlers entry)",
	"errorOutput":    "on_error (naming an output_handlers entry)",
}

type DroppedHandlerKey struct {
	Key        string
	Suggestion string
}

func DroppedHandlerKeys(eh map[string]any) []DroppedHandlerKey {
	var dropped []DroppedHandlerKey
	for k := range eh {
		if migratedHandlerKeys[k] || slices.Contains(DynamicTargetKeys, k) {
			continue
		}
		dropped = append(dropped, DroppedHandlerKey{Key: k, Suggestion: suggestHandlerKey(k)})
	}
	slices.SortFunc(dropped, func(a, b DroppedHandlerKey) int { return strings.Compare(a.Key, b.Key) })
	return dropped
}

func suggestHandlerKey(k string) string {
	if alias, ok := handlerKeyAliases[k]; ok {
		return alias
	}
	if snake := camelToSnake(k); migratedHandlerKeys[snake] {
		return snake
	}
	return ""
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (d DroppedHandlerKey) Describe() string {
	if d.Suggestion == "" {
		return fmt.Sprintf("%q is not an event_handlers field", d.Key)
	}
	return fmt.Sprintf("%q is not an event_handlers field; use %s", d.Key, d.Suggestion)
}

func handlerKeyWarnings(eh map[string]any) []string {
	name, _ := eh["name"].(string)
	var warnings []string
	for _, k := range DynamicTargetKeys {
		if v, _ := eh[k].(string); v != "" {
			warnings = append(warnings, fmt.Sprintf(
				"event_mesh: event handler %q: %q dropped — event_rules has no dynamic target routing; set a fixed target_agent_name or target_workflow_name", name, k))
		}
	}
	for _, d := range DroppedHandlerKeys(eh) {
		warnings = append(warnings, fmt.Sprintf("event_mesh: event handler %q: %s — dropped", name, d.Describe()))
	}
	return warnings
}
