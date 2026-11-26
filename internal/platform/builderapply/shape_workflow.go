// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"
	"maps"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func desiredWorkflow(componentName, content string) (desiredComponent, error) {
	app, err := parseBuilderApp(content)
	if err != nil {
		return desiredComponent{}, fmt.Errorf("parse workflow %q: %w", componentName, err)
	}
	if err := schema.WorkflowToolSourceError(app.appConfig); err != nil {
		return desiredComponent{}, fmt.Errorf("workflow %q: %w", componentName, err)
	}
	if err := schema.WorkflowPlatformConnectorEntryError(app.appConfig); err != nil {
		return desiredComponent{}, fmt.Errorf("workflow %q: %w", componentName, err)
	}

	name := cmp.Or(
		stringField(app.appConfig, "name"),
		stringField(app.appConfig, "agent_name"),
		app.name,
		componentName,
	)
	if name == "" {
		return desiredComponent{}, fmt.Errorf("workflow has no name")
	}
	legacy := cmp.Or(stringField(app.appConfig, "name"), app.name, componentName)
	if legacy == name {
		legacy = ""
	}

	body := workflowBody(app.appConfig)
	card := mapField(body, "agent_card")
	description := cmp.Or(
		stringField(card, "description"),
		stringField(body, "description"),
		stringField(app.appConfig, "description"),
	)
	statedDescription, descriptionWarnings := workflowDescriptionStated(card, body, app.appConfig)
	warnings := append(workflowSchemaWarnings(app.appConfig), descriptionWarnings...)

	create := entplatform.CreateWorkflowRequest{
		Name:        name,
		Description: description,
		AppConfig:   app.appConfig,
	}
	authored := authoredKeys("name", "appConfig")
	if statedDescription {
		authored["description"] = struct{}{}
	}
	return desiredComponent{
		planProto:  newWorkflowHashView("", create.Name, create.Description, create.AppConfig),
		create:     create,
		authored:   authored,
		warnings:   warnings,
		legacyName: legacy,
	}, nil
}

func workflowDescriptionStated(card, body, appConfig map[string]any) (bool, []string) {
	var warnings []string
	stated := false
	for _, source := range []struct {
		where string
		in    map[string]any
	}{
		{"agent_card.description", card},
		{"workflow.description", body},
		{"description", appConfig},
	} {
		raw, present := source.in["description"]
		if !present {
			continue
		}
		if _, isText := raw.(string); isText || raw == nil {
			stated = true
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s is %s, not text, and was ignored", source.where, describeEntry(raw)))
	}
	return stated, warnings
}

func workflowSchemaWarnings(appConfig map[string]any) []string {
	body := workflowBody(appConfig)
	if len(body) == 0 {
		return nil
	}
	inherit := func(bodyKey string, outerKeys ...string) {
		if _, present := body[bodyKey]; present {
			return
		}
		for _, k := range outerKeys {
			if v, ok := appConfig[k]; ok {
				body = maps.Clone(body)
				body[bodyKey] = v
				return
			}
		}
	}
	inherit("agent_type", "agent_type")
	inherit("agent_name", "agent_name", "name")

	if _, nested := appConfig["workflow"]; nested {
		if _, present := body["agent_type"]; !present {
			body = maps.Clone(body)
			body["agent_type"] = "workflow"
		}
	}

	out := []string{}
	for _, v := range schema.ValidateAppConfigAs(body, "workflow") {
		out = append(out, "workflow config does not match the workflow schema at "+
			strings.TrimPrefix(v.Path, "apps[0].app_config")+": "+v.Message)
	}
	out = append(out, danglingNodeReferences(body)...)
	if len(out) == 0 {
		return nil
	}
	return out
}

func danglingNodeReferences(body map[string]any) []string {
	nodes := mapSliceField(body, "nodes")
	if len(nodes) == 0 {
		return nil
	}

	defined := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if id := stringField(n, "id"); id != "" {
			defined[id] = struct{}{}
		}
	}

	var out []string
	report := func(from, kind, target string) {
		if target == "" {
			return
		}
		if _, ok := defined[target]; !ok {
			out = append(out, fmt.Sprintf("workflow node %q %s node %q, which this workflow does not define", from, kind, target))
		}
	}

	for _, n := range nodes {
		id := cmp.Or(stringField(n, "id"), "(unnamed)")
		for _, dep := range stringSliceField(n, "depends_on") {
			report(id, "depends on", dep)
		}
		report(id, "runs", stringField(n, "node"))
		report(id, "falls back to", stringField(n, "default"))
		for _, c := range mapSliceField(n, "cases") {
			report(id, "routes to", cmp.Or(stringField(c, "node"), stringField(c, "then")))
		}
	}

	switch handler := cmp.Or(body["on_exit"], body["onExit"]).(type) {
	case string:
		report("on_exit", "runs", handler)
	case map[string]any:
		for _, outcome := range [][]string{{"always"}, {"on_success", "onSuccess"}, {"on_failure", "onFailure"}, {"on_cancel", "onCancel"}} {
			for _, key := range outcome {
				if node := stringField(handler, key); node != "" {
					report("on_exit."+key, "runs", node)
				}
			}
		}
	}
	return out
}

func workflowBody(appConfig map[string]any) map[string]any {
	if nested := mapField(appConfig, "workflow"); nested != nil {
		return nested
	}
	return appConfig
}

func workflowAgentName(appConfig map[string]any) string {
	return cmp.Or(stringField(appConfig, "agent_name"), stringField(workflowBody(appConfig), "agent_name"))
}
