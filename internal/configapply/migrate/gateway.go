// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"fmt"
	"maps"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/migrate/eventmesh"
)

const eventMeshRegistryType = "event_mesh"

var gatewayAdapterTypes = map[string]string{
	"sam_slack_gateway_adapter.adapter.SlackAdapter":          "slack",
	"sam_teams_gateway_adapter.adapter.TeamsAdapter":          "teams",
	"sam_webui_gateway_adapter.adapter.WebUIAdapter":          "webui",
	"sam_rest_gateway_adapter.adapter.RESTAdapter":            "rest",
	"sam_event_mesh_gateway_adapter.adapter.EventMeshAdapter": eventMeshRegistryType,
	"sam_mcp_gateway_adapter.adapter.MCPAdapter":              "mcp",
}

var gatewayDroppedKeys = map[string]struct{}{
	"namespace":             {},
	"default_agent_name":    {},
	"system_purpose":        {},
	"result_format":         {},
	"authorization_service": {},
	"identity_service":      {},
	"trust_manager":         {},
}

func MigrateGateway(doc map[string]any) (CleanSpec, []string, error) {
	ac, err := extractAppConfig(doc)
	if err != nil {
		return CleanSpec{}, nil, err
	}

	var warnings []string
	out := CleanSpec{Kind: "entrypoint", Spec: map[string]any{}}

	rawName := firstAppName(doc)
	out.Name = strings.TrimSuffix(rawName, "__app")
	if out.Name == "" {
		return CleanSpec{}, nil, fmt.Errorf("gateway has no apps[0].name to derive a resource name")
	}

	out.Description = fmt.Sprintf("Gateway: %s", out.Name)
	warnings = append(warnings, "no gateway description in legacy YAML; synthesised one — please replace")

	gType := "generic"
	switch {
	case mapAppModule(doc) != "":
		gType = mapAppModule(doc)
	case ac["gateway_adapter"] != nil:
		adapter, _ := ac["gateway_adapter"].(string)
		if mapped, ok := gatewayAdapterTypes[adapter]; ok {
			gType = mapped
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"unknown gateway_adapter %q; using type=generic — verify against the platform's gateway-type registry",
				adapter,
			))
		}
	default:
		warnings = append(warnings, "no gateway_adapter or recognised app_module found; using type=generic")
	}
	out.Spec["type"] = gType

	values := map[string]any{}
	if adapterCfg, ok := ac["adapter_config"].(map[string]any); ok {
		maps.Copy(values, adapterCfg)
	}
	for k, v := range dropCruft(ac) {
		if _, drop := gatewayDroppedKeys[k]; drop {
			continue
		}
		if k == "gateway_adapter" || k == "adapter_config" {
			continue
		}
		values[k] = v
	}

	switch gType {
	case eventMeshRegistryType:
		var emWarnings []string
		values, emWarnings = eventmesh.MigrateValues(values)
		warnings = append(warnings, emWarnings...)
	default:
		for k := range values {
			if _, known := wellKnownValuesKeys[k]; known {
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"non-standard gateway field %q preserved under spec.values — verify it's meaningful to type=%q",
				k, gType,
			))
		}
	}

	if len(values) > 0 {
		out.Spec["values"] = values
	}

	return out, warnings, nil
}

var wellKnownValuesKeys = map[string]struct{}{}

var gatewayAppModuleTypes = map[string]string{
	"sam_event_mesh_gateway.app": eventMeshRegistryType,
}

func mapAppModule(doc map[string]any) string {
	apps, _ := doc["apps"].([]any)
	if len(apps) == 0 {
		return ""
	}
	app, _ := apps[0].(map[string]any)
	mod, _ := app["app_module"].(string)
	return gatewayAppModuleTypes[mod]
}
