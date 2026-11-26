// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"fmt"
)

var workflowDroppedKeys = map[string]struct{}{
	"name":         {},
	"display_name": {},
	"namespace":    {},
}

func MigrateWorkflow(doc map[string]any) (CleanSpec, []string, error) {
	ac, err := extractAppConfig(doc)
	if err != nil {
		return CleanSpec{}, nil, err
	}

	var warnings []string
	out := CleanSpec{Kind: "workflow", Spec: map[string]any{}}

	displayName, _ := ac["display_name"].(string)
	wfName, _ := ac["name"].(string)
	switch {
	case displayName != "" && displayName != wfName:
		out.Name = displayName
	case wfName != "":
		out.Name = wfName
	case displayName != "":
		out.Name = displayName
	default:
		return CleanSpec{}, nil, fmt.Errorf("workflow has no app_config.name")
	}

	if wf, ok := ac["workflow"].(map[string]any); ok {
		if d, ok := wf["description"].(string); ok && d != "" {
			out.Description = d
		}
	}
	if out.Description == "" {
		out.Description = fmt.Sprintf("Workflow: %s", out.Name)
		warnings = append(warnings, "no workflow.description in legacy YAML; synthesised one — please replace")
	}

	for k, v := range dropCruft(ac) {
		if _, drop := workflowDroppedKeys[k]; drop {
			continue
		}
		out.Spec[k] = v
	}

	return out, warnings, nil
}
