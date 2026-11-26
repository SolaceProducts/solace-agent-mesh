// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func stringifyTemplateValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", v)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(val); err == nil {
		return strings.TrimRight(buf.String(), "\n")
	}
	return fmt.Sprintf("%v", val)
}

var templatePattern = regexp.MustCompile(`^\{\{\s*([^{}]+?)\s*\}\}$`)

var embeddedTemplatePattern = regexp.MustCompile(`\{\{(.+?)\}\}`)

var templateAliases = []struct {
	from string
	to   string
}{
	{"{{item}}", "{{_map_item}}"},
	{"{{item.", "{{_map_item."},
	{"workflow.parameters.", "workflow.input."},
}

func applyArgoAliases(s string) string {
	for _, a := range templateAliases {
		s = strings.ReplaceAll(s, a.from, a.to)
	}
	return s
}

func isOperatorMap(v map[string]any) bool {
	if len(v) != 1 {
		return false
	}
	_, coalesce := v["coalesce"]
	_, concat := v["concat"]
	return coalesce || concat
}

type absentValue struct{}

var absent any = absentValue{}

func isAbsent(v any) bool {
	_, ok := v.(absentValue)
	return ok
}

func nilIfAbsent(v any) any {
	if isAbsent(v) {
		return nil
	}
	return v
}

type resolveMode struct {
	leaveUnresolved bool
	dropAbsentKeys  bool
}

var nodeInputMode = resolveMode{dropAbsentKeys: true}

var outputMappingMode = resolveMode{}

var outputMappingLenientMode = resolveMode{leaveUnresolved: true}

func ResolveValue(value any, nodeOutputs map[string]map[string]any) (any, error) {
	return resolveValue(value, nodeOutputs, nil)
}

type schemaDerived map[string]bool

func resolveValue(value any, nodeOutputs map[string]map[string]any, named schemaDerived) (any, error) {
	resolved, err := resolveValueWith(value, nodeOutputs, named, nodeInputMode)
	return nilIfAbsent(resolved), err
}

func resolveOutputMapping(value any, nodeOutputs map[string]map[string]any, named schemaDerived) (any, error) {
	resolved, err := resolveValueWith(value, nodeOutputs, named, outputMappingMode)
	return nilIfAbsent(resolved), err
}

func resolveOutputMappingLeavingUnresolved(value any, nodeOutputs map[string]map[string]any, named schemaDerived) any {
	resolved, _ := resolveValueWith(value, nodeOutputs, named, outputMappingLenientMode)
	return nilIfAbsent(resolved)
}

func resolveValueWith(value any, nodeOutputs map[string]map[string]any, named schemaDerived, mode resolveMode) (any, error) {
	switch v := value.(type) {
	case string:
		if templatePattern.MatchString(applyArgoAliases(v)) {
			resolved, err := resolveTemplate(v, nodeOutputs, named)
			if err != nil && mode.leaveUnresolved {
				return v, nil
			}
			return resolved, err
		}
		if strings.Contains(v, "{{") {
			resolved, err := resolveStringTemplates(v, nodeOutputs, named)
			if err != nil && !mode.leaveUnresolved {
				return nil, err
			}
			return resolved, nil
		}
		return v, nil

	case map[string]any:
		if isOperatorMap(v) {
			if args, ok := v["coalesce"]; ok {
				return resolveCoalesce(args, nodeOutputs, named, mode)
			}
			return resolveConcat(v["concat"], nodeOutputs, named, mode)
		}
		result := make(map[string]any, len(v))
		for k, val := range v {
			resolved, err := resolveValueWith(val, nodeOutputs, named, mode)
			if err != nil {
				return nil, err
			}
			if isAbsent(resolved) {
				if mode.dropAbsentKeys {
					continue
				}
				resolved = nil
			}
			result[k] = resolved
		}
		return result, nil

	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			resolved, err := resolveValueWith(item, nodeOutputs, named, mode)
			if err != nil {
				return nil, err
			}
			result[i] = nilIfAbsent(resolved)
		}
		return result, nil

	default:
		return value, nil
	}
}

func resolveTemplate(template string, nodeOutputs map[string]map[string]any, named schemaDerived) (any, error) {
	template = applyArgoAliases(template)

	match := templatePattern.FindStringSubmatch(template)
	if match == nil {
		return template, nil
	}

	path := match[1]
	return resolvePath(path, nodeOutputs, named)
}

func resolvePath(path string, nodeOutputs map[string]map[string]any, named schemaDerived) (any, error) {
	parts := strings.Split(path, ".")

	if len(parts) == 0 {
		return nil, fmt.Errorf("empty template path")
	}

	if parts[0] == "workflow" && len(parts) >= 2 {
		switch parts[1] {
		case "input", "parameters":
			nodeData, ok := nodeOutputs["workflow_input"]
			if !ok {
				return absent, nil
			}
			data, ok := nodeData["output"]
			if !ok {
				return absent, nil
			}
			remaining := parts[2:]
			return navigatePath(data, remaining, true, 0)
		default:
			nodeData, ok := nodeOutputs["workflow"]
			if !ok {
				return nil, nil
			}
			remaining := parts[1:]
			var data any = nodeData
			val, err := navigatePath(data, remaining, true, 0)
			return nilIfAbsent(val), err
		}
	}

	nodeID := parts[0]
	isSpecialVar := nodeID == "_map_item" || nodeID == "_map_index" || nodeID == "_loop_iteration"

	nodeData, ok := nodeOutputs[nodeID]
	if !ok {
		if isSpecialVar {
			return nil, nil
		}
		return nil, nil
	}

	if isSpecialVar {
		data, ok := nodeData["output"]
		if !ok {
			return nil, nil
		}
		remaining := parts[1:]
		val, err := navigatePath(data, remaining, true, 0)
		return nilIfAbsent(val), err
	}

	var data any = nodeData
	remaining := parts[1:]
	nameableDepth := 0
	if named[nodeID] {
		nameableDepth = 1
	}
	return navigatePath(data, remaining, false, nameableDepth)
}

func availableFieldsHint(m map[string]any) string {
	if len(m) == 0 {
		return "that value has no fields"
	}
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	const maxListed = 20
	if len(keys) > maxListed {
		return fmt.Sprintf("available fields: %s (+%d more)", strings.Join(keys[:maxListed], ", "), len(keys)-maxListed)
	}
	return "available fields: " + strings.Join(keys, ", ")
}

func fieldsHint(m map[string]any, depth, maxNamedDepth int) string {
	if depth <= maxNamedDepth {
		return availableFieldsHint(m)
	}
	if len(m) == 0 {
		return "that value has no fields"
	}
	return fmt.Sprintf("that value has %d fields, which cannot be named here because "+
		"they are whatever the tool returned rather than a declared schema — "+
		"check the tool's own result shape", len(m))
}

func navigatePath(data any, parts []string, safeNavigation bool, maxNamedDepth int) (any, error) {
	current := data
	for depth, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			if safeNavigation {
				return nil, nil
			}
			return nil, fmt.Errorf("cannot navigate path %q: value is not a map (type %T)", part, current)
		}
		val, exists := m[part]
		if !exists {
			if safeNavigation {
				return absent, nil
			}
			return nil, fmt.Errorf("field %q not found in node output; %s", part, fieldsHint(m, depth, maxNamedDepth))
		}
		current = val
	}
	return current, nil
}

func ResolveStringWithTemplates(s string, nodeOutputs map[string]map[string]any) string {
	resolved, _ := resolveStringTemplates(s, nodeOutputs, nil)
	return resolved
}

func resolveStringTemplates(s string, nodeOutputs map[string]map[string]any, named schemaDerived) (string, error) {
	resolved, res := resolveStringTemplatesReporting(s, nodeOutputs, named)
	return resolved, res.firstErr
}

type templateResolution struct {
	failed         int
	unresolvedText []string
	firstErr       error
	inputs         int
	firstInput     string
	nothing        int
	firstNothing   string
	decideText     string
}

func resolveStringTemplatesReporting(s string, nodeOutputs map[string]map[string]any, named schemaDerived) (string, templateResolution) {
	s = applyArgoAliases(s)

	var res templateResolution
	var resolved, decide strings.Builder
	last := 0
	for _, loc := range embeddedTemplatePattern.FindAllStringSubmatchIndex(s, -1) {
		start, end := loc[0], loc[1]
		match := s[start:end]
		resolved.WriteString(s[last:start])
		decide.WriteString(s[last:start])
		last = end

		if len(loc) < 4 || loc[2] < 0 {
			resolved.WriteString(match)
			decide.WriteString(match)
			continue
		}
		path := strings.TrimSpace(s[loc[2]:loc[3]])
		val, err := resolvePath(path, nodeOutputs, named)
		switch {
		case err != nil:
			res.failed++
			res.unresolvedText = append(res.unresolvedText, match)
			if res.firstErr == nil {
				res.firstErr = fmt.Errorf("%s: %w", match, err)
			}
			resolved.WriteString(match)
			decide.WriteString(match)
		case val == nil || isAbsent(val):
			res.unresolvedText = append(res.unresolvedText, match)
			resolved.WriteString(match)
			if !isWorkflowInputPath(path) {
				res.nothing++
				if res.firstNothing == "" {
					res.firstNothing = match
				}
				decide.WriteString(match)
				continue
			}
			res.inputs++
			if res.firstInput == "" {
				res.firstInput = match
			}
			if insideStringLiteral(s, start) {
				decide.WriteString(match)
			} else {
				decide.WriteString(`"` + match + `"`)
			}
		default:
			v := stringifyTemplateValue(val)
			resolved.WriteString(v)
			decide.WriteString(v)
		}
	}
	resolved.WriteString(s[last:])
	decide.WriteString(s[last:])
	res.decideText = decide.String()
	return resolved.String(), res
}

func isWorkflowInputPath(path string) bool {
	parts := strings.SplitN(path, ".", 3)
	return len(parts) >= 2 && parts[0] == "workflow" && (parts[1] == "input" || parts[1] == "parameters")
}

func insideStringLiteral(s string, pos int) bool {
	var quote byte
	for i := 0; i < pos && i < len(s); i++ {
		if strings.HasPrefix(s[i:], "{{") {
			if end := strings.Index(s[i:], "}}"); end >= 0 && i+end < pos {
				i += end + 1
				continue
			}
		}
		c := s[i]
		switch {
		case quote == 0:
			switch {
			case c == '\'' || c == '"' || c == '`':
				quote = c
			case strings.HasPrefix(s[i:], "//"):
				if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
					i += nl
				} else {
					return false
				}
			case strings.HasPrefix(s[i:], "/*"):
				if end := strings.Index(s[i+2:], "*/"); end >= 0 {
					i += end + 3
				} else {
					return false
				}
			}
		case c == '\\' && quote != '`':
			i++
		case c == quote:
			quote = 0
		}
	}
	return quote != 0
}

func resolveCoalesce(args any, nodeOutputs map[string]map[string]any, named schemaDerived, mode resolveMode) (any, error) {
	list, ok := args.([]any)
	if !ok {
		return nil, fmt.Errorf("'coalesce' operator requires a list of values")
	}

	absentOnly := len(list) > 0
	for _, arg := range list {
		resolved, err := resolveValueWith(arg, nodeOutputs, named, mode)
		if err != nil {
			return nil, err
		}
		if isAbsent(resolved) {
			continue
		}
		absentOnly = false
		if resolved != nil {
			return resolved, nil
		}
	}
	if absentOnly {
		return absent, nil
	}
	return nil, nil
}

func resolveConcat(args any, nodeOutputs map[string]map[string]any, named schemaDerived, mode resolveMode) (any, error) {
	list, ok := args.([]any)
	if !ok {
		return nil, fmt.Errorf("'concat' operator requires a list of values")
	}

	var parts []string
	for _, arg := range list {
		resolved, err := resolveValueWith(arg, nodeOutputs, named, mode)
		if err != nil {
			return nil, err
		}
		if resolved != nil && !isAbsent(resolved) {
			parts = append(parts, stringifyTemplateValue(resolved))
		}
	}
	return strings.Join(parts, ""), nil
}
