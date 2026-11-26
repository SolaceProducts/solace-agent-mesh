// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
)

var workflowLabelKeys = []string{"display_name", "displayName"}

type workflowLabelSection struct {
	prefix string
	body   map[string]any
}

func workflowDisplayNameAdvisories(r resolve.ResolvedResource, appConfig map[string]any) []string {
	sections := []workflowLabelSection{{body: appConfig}}
	if nested, ok := appConfig["workflow"].(map[string]any); ok {
		sections = append(sections, workflowLabelSection{prefix: "workflow.", body: nested})
	}
	var advisories []string
	for _, s := range sections {
		for _, key := range workflowLabelKeys {
			label, ok := s.body[key].(string)
			if !ok || label == "" || label == r.Name {
				continue
			}
			advisories = append(advisories, fmt.Sprintf(
				"workflow %q (%s): %s%s %q is ignored: the platform labels the workflow's card %q, the only name "+
					"entrypoint rules and `sam task send` resolve it by. Remove it, and point any reference written "+
					"against %q (an agent's interAgentCommunication allow or deny list, an event-mesh rule, a scheduled task) at %q",
				r.Name, r.SourceFile, s.prefix, key, label, r.Name, label, r.Name))
		}
	}
	return advisories
}
