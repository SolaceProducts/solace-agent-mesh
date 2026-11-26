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
	"unicode"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const minGatewayDescription = 10

var eventMeshValueKeys = []string{
	"output_handlers", "event_mesh_broker_config", "default_user_identity",
}

const ackPolicyKey = "acknowledgment_policy"

var gatewayConsumedKeys = []string{ackPolicyKey}

var gatewayIdentityKeys = []string{"gateway_id", "agent_name", "agent_card", "description", "event_handlers"}

func droppedGatewayKeyWarnings(appConfig map[string]any) []string {
	accounted := make(map[string]struct{}, len(eventMeshValueKeys)+len(gatewayIdentityKeys)+len(gatewayConsumedKeys))
	for _, keys := range [][]string{eventMeshValueKeys, gatewayIdentityKeys, gatewayConsumedKeys} {
		for _, k := range keys {
			accounted[k] = struct{}{}
		}
	}

	var out []string
	for _, key := range slices.Sorted(maps.Keys(appConfig)) {
		if _, ok := accounted[key]; !ok {
			out = append(out, fmt.Sprintf("entrypoint config key %s has no platform equivalent and was ignored", key))
		}
	}
	return out
}

func desiredGateway(componentName, content string) (desiredComponent, error) {
	app, err := parseBuilderApp(content)
	if err != nil {
		return desiredComponent{}, fmt.Errorf("parse entrypoint %q: %w", componentName, err)
	}

	if _, ok := app.appConfig["event_handlers"].([]any); !ok {
		return desiredComponent{}, fmt.Errorf("entrypoint %q has no event_handlers; only event mesh entrypoints are authored by the Builder", componentName)
	}

	explicit := firstNonBlank(stringField(app.appConfig, "gateway_id"), stringField(app.appConfig, "agent_name"))
	rawName := firstNonBlank(explicit, componentName, app.name)
	name := cmp.Or(toPascalCase(rawName), rawName)
	if name == "" {
		return desiredComponent{}, fmt.Errorf("entrypoint has no name")
	}
	var nameWarnings []string
	if manifest := strings.TrimSpace(componentName); manifest != "" && normalizedResourceName(name) != normalizedResourceName(cmp.Or(toPascalCase(manifest), manifest)) {
		nameWarnings = append(nameWarnings, fmt.Sprintf(
			"entrypoint %q is stored as %q because gateway_id or agent_name is set; the build plan still calls it %q",
			manifest, name, manifest))
	}

	card := mapField(app.appConfig, "agent_card")
	description, descriptionAuthored := "", false
	var unreadableDescription []string
	for _, source := range []struct {
		block  string
		fields map[string]any
	}{{"agent_card", card}, {"app_config", app.appConfig}} {
		raw, present := source.fields["description"]
		if !present {
			continue
		}
		text, isString := raw.(string)
		switch {
		case !isString:
			unreadableDescription = append(unreadableDescription, fmt.Sprintf(
				"%s description is a %T, not text, and was ignored", source.block, raw))
		case !descriptionAuthored || description == "":
			description, descriptionAuthored = strings.TrimSpace(text), true
		}
	}

	descriptionWarning := unreadableDescription
	if descriptionAuthored && len(description) < minGatewayDescription {
		descriptionWarning = append(descriptionWarning, fmt.Sprintf(
			"entrypoint description %q is shorter than the platform minimum of %d characters and was replaced",
			description, minGatewayDescription))
	}
	if len(description) < minGatewayDescription {
		description = "Event mesh entrypoint " + name
	}

	ackWarnings := pushAckPolicyDefault(app.appConfig)

	values := map[string]any{"event_handlers": app.appConfig["event_handlers"]}
	for _, key := range eventMeshValueKeys {
		if v, ok := app.appConfig[key]; ok {
			values[key] = v
		}
	}
	warnings := slices.Concat(nameWarnings, ackWarnings, droppedGatewayKeyWarnings(app.appConfig))

	if err := entplatform.RejectUnsupportedEventMeshAuthoring(values); err != nil {
		return desiredComponent{}, fmt.Errorf("entrypoint %q: %w", componentName, err)
	}
	normalized, normalizeWarnings := entplatform.NormalizeEventMeshAuthoredValues(values)
	warnings = append(warnings, normalizeWarnings...)

	create := entplatform.CreateGatewayRequest{
		Name:        name,
		Description: description,
		Type:        "event_mesh",
		Values:      normalized,
		Deploy:      false,
	}
	authored := authoredKeys("name", "type", "values")
	if descriptionAuthored {
		authored["description"] = struct{}{}
	}

	return desiredComponent{
		planProto: create,
		create:    create,
		warnings:  append(warnings, descriptionWarning...),
		authored:  authored,
	}, nil
}

func pushAckPolicyDefault(appConfig map[string]any) []string {
	raw, present := appConfig[ackPolicyKey]
	if !present || raw == nil {
		return nil
	}
	policy, isMap := raw.(map[string]any)
	if !isMap {
		return []string{fmt.Sprintf("entrypoint %s must be a mapping such as {mode: on_completion}; it was ignored, so handlers without their own policy acknowledge on receipt", ackPolicyKey)}
	}
	if !slices.ContainsFunc([]string{"mode", "timeout_seconds", "on_failure"}, func(key string) bool { _, ok := policy[key]; return ok }) {
		return []string{fmt.Sprintf("entrypoint %s has none of mode, timeout_seconds or on_failure and was ignored", ackPolicyKey)}
	}
	handlers, _ := appConfig["event_handlers"].([]any)
	for _, h := range handlers {
		handler, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if _, hasOwn := handler[ackPolicyKey].(map[string]any); !hasOwn {
			handler[ackPolicyKey] = maps.Clone(policy)
		}
	}
	return nil
}

func firstNonBlank(candidates ...string) string {
	for _, c := range candidates {
		if t := strings.TrimSpace(c); t != "" {
			return t
		}
	}
	return ""
}

func toPascalCase(s string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range s {
		if r == '_' || r == '-' || r == ' ' {
			upperNext = true
			continue
		}
		if upperNext {
			b.WriteRune(unicode.ToUpper(r))
			upperNext = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
