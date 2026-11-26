// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"errors"
	"fmt"
	"strings"
)

var engineConnectorTypes = map[string]bool{"email": true}

type restatedConnectorEntry struct {
	path   string
	name   string
	nodeID string
}

func restatesBoundConnector(raw any, bound map[string]string) (name, nodeID string, ok bool) {
	entry, isMap := raw.(map[string]any)
	if !isMap {
		return "", "", false
	}
	name, _ = entry["name"].(string)
	nodeID, isBound := bound[name]
	if !isBound {
		return "", "", false
	}
	connType, _ := entry["type"].(string)
	cfg, _ := entry["config"].(map[string]any)
	if engineConnectorTypes[connType] && len(cfg) > 0 {
		return "", "", false
	}
	return name, nodeID, true
}

type connectorLevel struct {
	m      map[string]any
	prefix string
}

func connectorLevels(appConfig map[string]any) (levels []connectorLevel, body map[string]any) {
	if nested, ok := appConfig["workflow"].(map[string]any); ok {
		return []connectorLevel{{appConfig, ""}, {nested, "workflow/"}}, nested
	}
	return []connectorLevel{{appConfig, ""}}, appConfig
}

func restatedConnectorEntries(appConfig map[string]any) []restatedConnectorEntry {
	levels, body := connectorLevels(appConfig)
	bound := nodeConnectorBindings(body)
	if len(bound) == 0 {
		return nil
	}
	var found []restatedConnectorEntry
	for _, lv := range levels {
		list, _ := lv.m["connectors"].([]any)
		for i, raw := range list {
			if name, nodeID, ok := restatesBoundConnector(raw, bound); ok {
				found = append(found, restatedConnectorEntry{
					path: fmt.Sprintf("%sconnectors[%d]", lv.prefix, i), name: name, nodeID: nodeID,
				})
			}
		}
	}
	return found
}

func DropWorkflowPlatformConnectorEntries(appConfig map[string]any) []string {
	levels, body := connectorLevels(appConfig)
	bound := nodeConnectorBindings(body)
	if len(bound) == 0 {
		return nil
	}
	var dropped []string
	for _, lv := range levels {
		list, ok := lv.m["connectors"].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(list))
		for i, raw := range list {
			if _, _, restated := restatesBoundConnector(raw, bound); restated {
				dropped = append(dropped, fmt.Sprintf("%sconnectors[%d]", lv.prefix, i))
				continue
			}
			kept = append(kept, raw)
		}
		switch {
		case len(kept) == len(list):
		case len(kept) == 0:
			delete(lv.m, "connectors")
		default:
			lv.m["connectors"] = kept
		}
	}
	return dropped
}

func WorkflowPlatformConnectorEntryError(appConfig map[string]any) error {
	entries := restatedConnectorEntries(appConfig)
	if len(entries) == 0 {
		return nil
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.path + ": " + restatedConnectorMessage(e)
	}
	return errors.New(strings.Join(parts, "; "))
}

func workflowPlatformConnectorEntryValidationErrors(appConfig map[string]any) []ValidationError {
	entries := restatedConnectorEntries(appConfig)
	errs := make([]ValidationError, len(entries))
	for i, e := range entries {
		errs[i] = ValidationError{Path: e.path, Message: restatedConnectorMessage(e)}
	}
	return errs
}

func restatedConnectorMessage(e restatedConnectorEntry) string {
	return fmt.Sprintf("%q restates the platform connector bound by tool node %q's `connector:`; "+
		"remove it from the workflow-level `connectors:` list, which only takes inline engine connectors "+
		"the engine can build — the node's `connector:` alone wires the connector's tools at deploy",
		e.name, e.nodeID)
}

func nodeConnectorBindings(body map[string]any) map[string]string {
	rawNodes, _ := body["nodes"].([]any)
	bound := map[string]string{}
	for _, raw := range rawNodes {
		node, _ := raw.(map[string]any)
		connector, _ := node["connector"].(string)
		if node["type"] != "tool" || connector == "" {
			continue
		}
		if _, seen := bound[connector]; !seen {
			id, _ := node["id"].(string)
			bound[connector] = id
		}
	}
	return bound
}
