// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
)

const workflowAppConfigWrapperKey = "appConfig"

var workflowHeaderKeys = []string{"name", "description"}

func WorkflowSpecFor(definition map[string]any) map[string]any {
	if flatWorkflowSpecHolds(definition) {
		return definition
	}
	return map[string]any{workflowAppConfigWrapperKey: definition}
}

func flatWorkflowSpecHolds(definition map[string]any) bool {
	if _, taken := definition[workflowAppConfigWrapperKey]; taken {
		return false
	}
	return !slices.ContainsFunc(workflowHeaderKeys, func(key string) bool {
		_, set := definition[key]
		return set
	})
}

func workflowAppConfigFromInput(in map[string]any) map[string]any {
	body := make(map[string]any, len(in))
	for k, v := range in {
		if slices.Contains(workflowHeaderKeys, k) {
			continue
		}
		body[k] = v
	}
	return body
}

func flatWorkflowSpecAdvisories(r resolve.ResolvedResource) []string {
	if _, wrapped := r.Spec[workflowAppConfigWrapperKey]; wrapped {
		return nil
	}
	var advisories []string
	for _, key := range workflowHeaderKeys {
		if _, set := r.Spec[key]; !set {
			continue
		}
		advisories = append(advisories, fmt.Sprintf(
			"workflow %q (%s): spec.%s is not applied: without spec.appConfig, %s is the resource's own field, "+
				"so the definition is stored without it. Put the definition under spec.appConfig to keep it",
			r.Name, r.SourceFile, key, key))
	}
	return advisories
}
