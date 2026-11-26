// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"fmt"
	"strings"
)

const (
	AgentModelKey         = "model"
	agentModelProviderKey = "model_provider"
	appConfigKey          = "app_config"

	platformDefaultModelRef = "${LLM_SERVICE_GENERAL_MODEL_NAME}"

	modelBindingAdvice = `an agent's model is bound by app_config.model_provider: ["<alias>"], ` +
		`where <alias> is a model name from platform_list_components with component_type "model" (the alias on the Models page)`
)

func AgentModelKeyErrors(app, appConfig map[string]any, appIndex int) []ValidationError {
	var errs []ValidationError
	appPath := fmt.Sprintf("apps[%d]", appIndex)
	configPath := appPath + "." + appConfigKey + "/"

	if statesModelChoice(app[AgentModelKey]) {
		errs = append(errs, ValidationError{
			Path: appPath + "." + AgentModelKey,
			Message: "model is written beside app_config, where nothing reads it, so the agent would keep its current model; " +
				modelBindingAdvice + ". Remove this key.",
		})
	}
	if _, stated := app[agentModelProviderKey]; stated {
		errs = append(errs, ValidationError{
			Path:    appPath + "." + agentModelProviderKey,
			Message: "model_provider is written beside app_config, where nothing reads it, so the agent would keep its current model; move it inside app_config.",
		})
	}
	if statesModelChoice(appConfig[AgentModelKey]) {
		errs = append(errs, ValidationError{
			Path: configPath + AgentModelKey,
			Message: "model is not saved, so the agent would keep its current model; " +
				modelBindingAdvice + ". Replace this key with model_provider or remove it.",
		})
	}
	if problem := modelProviderShapeProblem(appConfig[agentModelProviderKey]); problem != "" {
		errs = append(errs, ValidationError{
			Path: configPath + agentModelProviderKey,
			Message: "model_provider " + problem + `, so the agent would keep its current model; give one model name, ["<alias>"], ` +
				"or [] to return to the platform default.",
		})
	}
	return errs
}

func modelProviderShapeProblem(v any) string {
	switch p := v.(type) {
	case nil:
		return ""
	case string:
		if strings.TrimSpace(p) == "" {
			return "is an empty name"
		}
	case []any:
		if len(p) > 1 {
			return "lists more than one model and the platform binds one"
		}
		if len(p) == 1 && !isModelName(p[0]) {
			return "holds an entry that is not a model name"
		}
	default:
		return "is neither a model name nor a list holding one"
	}
	return ""
}

func isModelName(v any) bool {
	name, ok := v.(string)
	return ok && strings.TrimSpace(name) != ""
}

func statesModelChoice(v any) bool {
	switch m := v.(type) {
	case nil:
		return false
	case string:
		name := strings.TrimSpace(m)
		return name != "" && name != platformDefaultModelRef
	case map[string]any:
		if len(m) == 0 {
			return false
		}
		if len(m) > 1 {
			return true
		}
		inner, ok := m[AgentModelKey]
		return !ok || statesModelChoice(inner)
	default:
		return true
	}
}
