// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"regexp"
	"strings"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func validateAgainstRegistry(prefix string, fields []platpkg.FieldDef, values map[string]any) error {
	for _, f := range fields {
		if !conditionalDisplayMet(f.ConditionalDisplay, values) {
			continue
		}
		if err := validateOneField(prefix, f, values); err != nil {
			return err
		}
	}
	return nil
}

func conditionalDisplayMet(cond map[string]any, siblings map[string]any) bool {
	if len(cond) == 0 {
		return true
	}
	for siblingKey, expected := range cond {
		actual, ok := siblings[siblingKey]
		if !ok {
			return false
		}
		switch want := expected.(type) {
		case string:
			s, _ := actual.(string)
			if s != want {
				return false
			}
		case []string:
			s, _ := actual.(string)
			matched := false
			for _, w := range want {
				if s == w {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		case []any:
			s, _ := actual.(string)
			matched := false
			for _, w := range want {
				if ws, ok := w.(string); ok && ws == s {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		default:
		}
	}
	return true
}

func validateOneField(prefix string, f platpkg.FieldDef, values map[string]any) error {
	path := joinPath(prefix, f.Name)
	raw, present := values[f.Name]
	if !present || isEmptyValue(raw) {
		if f.Required {
			return &ValidationError{Message: fmt.Sprintf("%s is required", path)}
		}
		return nil
	}

	if f.AcceptStringShorthand {
		if s, ok := raw.(string); ok {
			raw = map[string]any{f.StringShorthandField: s}
		}
	}

	switch f.Type {
	case "string", "textarea", "agent_select", "workflow_select", "system_user_select", "password":
		return validateStringValue(path, f, raw)
	case "number":
		return validateNumberValue(path, f, raw)
	case "boolean":
		return validateBoolValue(path, f, raw)
	case "select", "radio":
		return validateEnumValue(path, f, raw)
	case "array<object>", "event_rules":
		return validateArrayObjectValue(path, f, raw)
	case "object":
		return validateObjectValue(path, f, raw)
	}
	return nil
}

func isEmptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func validateStringValue(path string, f platpkg.FieldDef, raw any) error {
	s, ok := raw.(string)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("%s: must be a string", path)}
	}
	if isPlaceholder(s) {
		return nil
	}
	if f.Pattern != "" {
		re, err := regexp.Compile(f.Pattern)
		if err == nil && !re.MatchString(s) {
			msg := fmt.Sprintf("%s: must match pattern %s (got %q)", path, f.Pattern, s)
			if errMsg, ok := f.ExtraValidation["errorMsg"].(string); ok && errMsg != "" {
				msg = fmt.Sprintf("%s: %s", path, errMsg)
			}
			return &ValidationError{Message: msg}
		}
	}
	if f.MinLen > 0 && len(s) < f.MinLen {
		return &ValidationError{
			Message: fmt.Sprintf("%s: must be at least %d characters", path, f.MinLen),
		}
	}
	if f.MaxLen > 0 && len(s) > f.MaxLen {
		return &ValidationError{
			Message: fmt.Sprintf("%s: must be at most %d characters", path, f.MaxLen),
		}
	}
	return nil
}

func validateNumberValue(path string, f platpkg.FieldDef, raw any) error {
	n, ok := toFloat64(raw)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("%s: must be a number", path)}
	}
	if f.MinValue != nil && n < float64(*f.MinValue) {
		return &ValidationError{
			Message: fmt.Sprintf("%s: must be >= %d", path, *f.MinValue),
		}
	}
	if f.MaxValue != nil && n > float64(*f.MaxValue) {
		return &ValidationError{
			Message: fmt.Sprintf("%s: must be <= %d", path, *f.MaxValue),
		}
	}
	return nil
}

func validateBoolValue(path string, _ platpkg.FieldDef, raw any) error {
	switch raw.(type) {
	case bool:
		return nil
	case string:
		return nil
	}
	return &ValidationError{Message: fmt.Sprintf("%s: must be a boolean", path)}
}

func validateEnumValue(path string, f platpkg.FieldDef, raw any) error {
	s, ok := raw.(string)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("%s: must be a string", path)}
	}
	if len(f.OneOf) == 0 {
		return nil
	}
	for _, opt := range f.OneOf {
		if opt.Value == s {
			return nil
		}
	}
	allowed := make([]string, 0, len(f.OneOf))
	for _, opt := range f.OneOf {
		allowed = append(allowed, opt.Value)
	}
	return &ValidationError{
		Message: fmt.Sprintf("%s: must be one of: %s (got %q)", path, strings.Join(allowed, ", "), s),
	}
}

func validateArrayObjectValue(path string, f platpkg.FieldDef, raw any) error {
	list, ok := raw.([]any)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("%s: must be an array", path)}
	}
	if f.MinItems > 0 && len(list) < f.MinItems {
		return &ValidationError{
			Message: fmt.Sprintf("%s: must have at least %d entries", path, f.MinItems),
		}
	}
	if len(f.Inner) == 0 {
		return nil
	}
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("%s[%d]: must be an object", path, i)}
		}
		if err := validateAgainstRegistry(fmt.Sprintf("%s[%d]", path, i), f.Inner, obj); err != nil {
			return err
		}
	}
	return nil
}

func validateObjectValue(path string, f platpkg.FieldDef, raw any) error {
	obj, ok := raw.(map[string]any)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("%s: must be an object", path)}
	}
	if len(f.Inner) == 0 {
		return nil
	}
	return validateAgainstRegistry(path, f.Inner, obj)
}
