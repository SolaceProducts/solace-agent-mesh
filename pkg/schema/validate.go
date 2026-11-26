// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

var errPrinter = message.NewPrinter(language.English)

type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Valid         bool              `json:"valid"`
	ComponentType string            `json:"component_type"`
	Errors        []ValidationError `json:"errors,omitempty"`
	Warnings      []ValidationError `json:"warnings,omitempty"`
}

func ValidateConfig(yamlBytes []byte) (*ValidationResult, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return &ValidationResult{
			Valid:         false,
			ComponentType: "unknown",
			Errors: []ValidationError{{
				Path:    "",
				Message: fmt.Sprintf("invalid YAML: %v", err),
			}},
		}, nil
	}

	result := &ValidationResult{Valid: true}
	apps, appConfigs, errs := extractAppConfigs(raw)
	if len(errs) > 0 {
		return &ValidationResult{
			Valid:         false,
			ComponentType: "unknown",
			Errors:        errs,
		}, nil
	}

	for i, appConfig := range appConfigs {
		compType := DetectComponentType(appConfig)
		if result.ComponentType == "" {
			result.ComponentType = compType
		}

		app := apps[i]
		if _, ok := app["name"]; !ok {
			result.Errors = append(result.Errors, ValidationError{
				Path:    fmt.Sprintf("apps[%d].name", i),
				Message: "required field missing",
			})
		}

		schemaErrors := validateAgainstSchema(appConfig, compType, i)
		result.Errors = append(result.Errors, schemaErrors...)

		if compType == "agent" || compType == "workflow" {
			toolErrors, toolWarnings := ValidateToolReferences(appConfig)
			if compType == "workflow" {
				toolErrors = append(toolErrors, workflowToolSourceValidationErrors(appConfig)...)
				toolErrors = append(toolErrors, workflowPlatformConnectorEntryValidationErrors(appConfig)...)
			}
			prefix := fmt.Sprintf("apps[%d].app_config", i)
			for _, e := range toolErrors {
				e.Path = prefix + "/" + e.Path
				result.Errors = append(result.Errors, e)
			}
			for _, w := range toolWarnings {
				w.Path = prefix + "/" + w.Path
				result.Warnings = append(result.Warnings, w)
			}
		}
	}

	result.Valid = len(result.Errors) == 0
	return result, nil
}

func ParseAppsConfig(yamlBytes []byte) ([]map[string]any, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	apps, _, errs := extractAppConfigs(raw)
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid config: %s", errs[0].Message)
	}
	return apps, nil
}

func extractAppConfigs(raw map[string]any) ([]map[string]any, []map[string]any, []ValidationError) {
	appsRaw, ok := raw["apps"]
	if !ok {
		return nil, nil, []ValidationError{{
			Path:    "apps",
			Message: "required field missing: top-level 'apps' array",
		}}
	}

	appsList, ok := appsRaw.([]any)
	if !ok {
		return nil, nil, []ValidationError{{
			Path:    "apps",
			Message: "must be an array",
		}}
	}

	if len(appsList) == 0 {
		return nil, nil, []ValidationError{{
			Path:    "apps",
			Message: "must contain at least one app",
		}}
	}

	var apps []map[string]any
	var configs []map[string]any
	var errs []ValidationError

	for i, item := range appsList {
		appMap, ok := item.(map[string]any)
		if !ok {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("apps[%d]", i),
				Message: "must be a map with 'name' and 'app_config'",
			})
			continue
		}
		apps = append(apps, appMap)

		acRaw, ok := appMap["app_config"]
		if !ok {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("apps[%d].app_config", i),
				Message: "required field missing",
			})
			continue
		}

		ac, ok := acRaw.(map[string]any)
		if !ok {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("apps[%d].app_config", i),
				Message: "must be a map",
			})
			continue
		}
		configs = append(configs, ac)
	}

	return apps, configs, errs
}

func ValidateAppConfigAs(appConfig map[string]any, componentType string) []ValidationError {
	return validateAgainstSchema(appConfig, componentType, 0)
}

func validateAgainstSchema(appConfig map[string]any, compType string, appIndex int) []ValidationError {
	schemaYAML, err := SchemaContentFiltered(compType)
	if err != nil {
		return []ValidationError{{
			Path:    fmt.Sprintf("apps[%d].app_config", appIndex),
			Message: fmt.Sprintf("no schema for component type %q", compType),
		}}
	}

	var schemaMap any
	if err = yaml.Unmarshal(schemaYAML, &schemaMap); err != nil {
		return []ValidationError{{
			Path:    fmt.Sprintf("apps[%d].app_config", appIndex),
			Message: fmt.Sprintf("schema parse error: %v", err),
		}}
	}

	schemaJSON, err := json.Marshal(normalizeForJSON(schemaMap))
	if err != nil {
		return nil
	}

	var schemaObj any
	if err = json.Unmarshal(schemaJSON, &schemaObj); err != nil {
		return nil
	}

	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("schema.json", schemaObj); err != nil {
		return nil
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return nil
	}

	if compType == "agent" {
		appConfig = withoutNullEntries(appConfig)
	}

	configJSON, err := json.Marshal(normalizeForJSON(appConfig))
	if err != nil {
		return nil
	}
	var configObj any
	if err := json.Unmarshal(configJSON, &configObj); err != nil {
		return nil
	}

	validationErr := schema.Validate(configObj)
	if validationErr == nil {
		return nil
	}

	prefix := fmt.Sprintf("apps[%d].app_config", appIndex)
	return convertSchemaErrors(validationErr, prefix)
}

func withoutNullEntries(v map[string]any) map[string]any {
	out := make(map[string]any, len(v))
	for key, val := range v {
		switch {
		case val == nil:
		case key == "hil":
			out[key] = val
		default:
			out[key] = withoutNullEntriesIn(val)
		}
	}
	return out
}

func withoutNullEntriesIn(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return withoutNullEntries(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = withoutNullEntriesIn(item)
		}
		return out
	}
	return v
}

func convertSchemaErrors(err error, prefix string) []ValidationError {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []ValidationError{{
			Path:    prefix,
			Message: err.Error(),
		}}
	}

	var result []ValidationError
	collectErrors(ve, prefix, &result)
	return result
}

func collectErrors(ve *jsonschema.ValidationError, prefix string, result *[]ValidationError) {
	if len(ve.Causes) == 0 {
		path := prefix
		if len(ve.InstanceLocation) > 0 {
			path = prefix + "/" + strings.Join(ve.InstanceLocation, "/")
		}
		*result = append(*result, ValidationError{
			Path:    path,
			Message: ve.ErrorKind.LocalizedString(errPrinter),
		})
		return
	}
	for _, cause := range ve.Causes {
		collectErrors(cause, prefix, result)
	}
}

func ValidateConnectorConfig(jsonBytes []byte) (*ValidationResult, error) {
	return validateJSONAgainstNamedSchema(jsonBytes, "connector", "connector")
}

func validateJSONAgainstNamedSchema(jsonBytes []byte, schemaName, componentType string) (*ValidationResult, error) {
	var configObj any
	if err := json.Unmarshal(jsonBytes, &configObj); err != nil {
		return &ValidationResult{Valid: false, ComponentType: componentType, Errors: []ValidationError{{Path: "", Message: fmt.Sprintf("invalid JSON: %v", err)}}}, nil
	}
	schemaContent, err := SchemaContent(schemaName)
	if err != nil {
		return nil, fmt.Errorf("loading %s schema: %w", schemaName, err)
	}
	var schemaObj any
	if unmarshalErr := json.Unmarshal(schemaContent, &schemaObj); unmarshalErr != nil {
		return nil, fmt.Errorf("parsing %s schema: %w", schemaName, unmarshalErr)
	}
	if containsPlaceholder(configObj) {
		stripSchemaPatterns(schemaObj)
	}
	compiler := jsonschema.NewCompiler()
	resource := schemaName + ".json"
	if addErr := compiler.AddResource(resource, schemaObj); addErr != nil {
		return nil, fmt.Errorf("adding %s schema resource: %w", schemaName, addErr)
	}
	sch, err := compiler.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("compiling %s schema: %w", schemaName, err)
	}
	if validationErr := sch.Validate(configObj); validationErr != nil {
		return &ValidationResult{Valid: false, ComponentType: componentType, Errors: convertSchemaErrors(validationErr, "")}, nil
	}
	return &ValidationResult{Valid: true, ComponentType: componentType}, nil
}

func ValidateA2AAgentConfig(jsonBytes []byte) (*ValidationResult, error) {
	return validateJSONAgainstNamedSchema(jsonBytes, "a2a-agent", "a2a_agent")
}

func containsPlaceholder(v any) bool {
	switch val := v.(type) {
	case map[string]any:
		for _, child := range val {
			if containsPlaceholder(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(val, containsPlaceholder)
	case string:
		return strings.HasPrefix(val, "<<__SAM_") && strings.HasSuffix(val, "__>>")
	}
	return false
}

func stripSchemaPatterns(v any) {
	switch val := v.(type) {
	case map[string]any:
		delete(val, "pattern")
		for _, child := range val {
			stripSchemaPatterns(child)
		}
	case []any:
		for _, child := range val {
			stripSchemaPatterns(child)
		}
	}
}

func ValidateSkillConfig(yamlBytes []byte) (*ValidationResult, error) {
	var configObj any
	if err := yaml.Unmarshal(yamlBytes, &configObj); err != nil {
		return &ValidationResult{
			Valid:         false,
			ComponentType: "skill",
			Errors: []ValidationError{{
				Path:    "",
				Message: fmt.Sprintf("invalid YAML: %v", err),
			}},
		}, nil
	}

	configObj = normalizeYAML(configObj)

	schemaContent, err := SchemaContent("skill")
	if err != nil {
		return nil, fmt.Errorf("loading skill schema: %w", err)
	}

	var schemaObj any
	errUnmarshal := yaml.Unmarshal(schemaContent, &schemaObj)
	if errUnmarshal != nil {
		return nil, fmt.Errorf("parsing skill schema: %w", errUnmarshal)
	}
	schemaObj = normalizeYAML(schemaObj)

	compiler := jsonschema.NewCompiler()
	errAdd := compiler.AddResource("skill.json", schemaObj)
	if errAdd != nil {
		return nil, fmt.Errorf("adding skill schema resource: %w", errAdd)
	}
	sch, err := compiler.Compile("skill.json")
	if err != nil {
		return nil, fmt.Errorf("compiling skill schema: %w", err)
	}

	result := &ValidationResult{Valid: true, ComponentType: "skill"}

	validationErr := sch.Validate(configObj)
	if validationErr != nil {
		errs := convertSchemaErrors(validationErr, "")
		result.Valid = false
		result.Errors = errs
	}

	result.Errors = append(result.Errors, validateSkillReferences(configObj)...)
	if len(result.Errors) > 0 {
		result.Valid = false
	}

	return result, nil
}

func validateSkillReferences(configObj any) []ValidationError {
	root, ok := configObj.(map[string]any)
	if !ok {
		return nil
	}
	refsRaw, ok := root["references"]
	if !ok {
		return nil
	}
	refs, ok := refsRaw.([]any)
	if !ok {
		return nil
	}

	var errs []ValidationError
	for i, item := range refs {
		ref, ok := item.(map[string]any)
		if !ok {
			continue
		}
		_, hasContent := ref["content"]
		_, hasArtifact := ref["artifact"]

		if hasContent && hasArtifact {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("references[%d]", i),
				Message: "reference must have either 'content' or 'artifact', not both",
			})
		} else if !hasContent && !hasArtifact {
			errs = append(errs, ValidationError{
				Path:    fmt.Sprintf("references[%d]", i),
				Message: "reference must have either 'content' or 'artifact'",
			})
		}
	}
	return errs
}

func normalizeYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[fmt.Sprintf("%v", k)] = normalizeYAML(v)
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[k] = normalizeYAML(v)
		}
		return m
	case []any:
		for i, v := range val {
			val[i] = normalizeYAML(v)
		}
		return val
	default:
		return v
	}
}

var hilFieldNames = []string{"require_approval", "approval_message", "timeout", "show_args", "require_approval_when", "approvers"}

func ValidateToolReferences(appConfig map[string]any) ([]ValidationError, []ValidationError) {
	toolsRaw, ok := appConfig["tools"]
	if !ok {
		return nil, nil
	}
	toolsList, ok := toolsRaw.([]any)
	if !ok {
		return nil, nil
	}

	var errs, warns []ValidationError

	for i, toolRaw := range toolsList {
		toolMap, ok := toolRaw.(map[string]any)
		if !ok {
			continue
		}

		toolType, _ := toolMap["tool_type"].(string)

		if hil, isBlock := toolMap["hil"].(map[string]any); isBlock {
			for _, field := range hilFieldNames {
				if value, present := hil[field]; present && value == nil {
					errs = append(errs, ValidationError{
						Path:    fmt.Sprintf("tools[%d]/hil/%s", i, field),
						Message: "must not be null: leave the field out, or remove the whole hil block with `hil: null`",
					})
				}
			}
		}

		switch toolType {
		case "builtin-group":
			groupName, _ := toolMap["group_name"].(string)
			if groupName == "" {
				continue
			}
			if !IsValidBuiltinGroup(groupName) {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("tools[%d]/group_name", i),
					Message: fmt.Sprintf("unknown builtin group %q", groupName),
				})
			}

		case "builtin":
			toolName, _ := toolMap["tool_name"].(string)
			if toolName == "" {
				continue
			}
			if !ValidBuiltinToolNames[toolName] {
				warns = append(warns, ValidationError{
					Path: fmt.Sprintf("tools[%d]/tool_name", i),
					Message: fmt.Sprintf("tool %q is not a recognized in-process built-in — check the exact "+
						"snake_case spelling against the schema's tool_name list (do not change casing), or use a "+
						"builtin-group; if intentional it resolves only when an STR worker provides it", toolName),
				})
			}

		}
	}

	return errs, warns
}

func normalizeForJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[k] = normalizeForJSON(v)
		}
		return result
	case map[any]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[fmt.Sprint(k)] = normalizeForJSON(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = normalizeForJSON(v)
		}
		return result
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return v
	}
}
