// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

const (
	linkAgentKind = "linkAgent"
	linkAgentsDir = "linkAgents"
)

func pullLinkAgents(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	rows, err := c.ListLinkAgents(ctx)
	if err != nil {
		if linkAgentsNotServed(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping linkAgents: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	entrypointNames, err := entrypointNamesByID(ctx, c)
	if err != nil {
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, row := range rows {
		if opts.Name != "" && row.Name != opts.Name {
			continue
		}
		safe := resourceFileName(row.Name)
		if safe == "" {
			res.drop("linkAgent %q: name is not a safe file name", row.Name)
			continue
		}
		entrypointName, known := entrypointNames[row.EntrypointID]
		if !known {
			res.drop("linkAgent %q: its entrypoint %s is not among the platform's entrypoints", row.Name, row.EntrypointID)
			continue
		}
		spec, err := linkAgentSpec(row, entrypointName)
		if err != nil {
			return nil, nil, err
		}
		description, _ := takeString(spec, "description")
		delete(spec, "name")

		spec, warnings := substituteSecrets(linkAgentKind, row.Name, spec)
		res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)

		raw, err := emitYAML(resourceDoc{Kind: linkAgentKind, Name: row.Name, Description: description, Spec: dropEmpty(spec)})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{relPath: filepath.Join(linkAgentsDir, safe+".yaml"), content: raw})
		names = append(names, row.Name)
	}
	return entries, names, nil
}

func linkAgentsNotServed(err error) bool {
	if isFeatureDisabled(err) {
		return true
	}
	httpErr, ok := errors.AsType[*reconcile.HTTPError](err)
	return ok && httpErr.StatusCode == http.StatusNotFound
}

func entrypointNamesByID(ctx context.Context, c *reconcile.Client) (map[string]string, error) {
	entrypoints, err := c.ListGateways(ctx)
	if err != nil {
		return nil, fmt.Errorf("list entrypoints to name each link agent's entrypoint: %w", err)
	}
	names := make(map[string]string, len(entrypoints))
	for _, e := range entrypoints {
		names[e.ID] = e.Name
	}
	return names, nil
}

func linkAgentSpec(row wire.LinkAgentResponse, entrypointName string) (map[string]any, error) {
	spec, err := translate.ReverseLinkAgentV2(row, translate.LinkAgentV2Hooks{})
	if err != nil {
		return nil, err
	}
	spec["entrypoint"] = entrypointName
	delete(spec, "enabled")
	if !row.Enabled {
		spec["enabled"] = false
	}
	if wake, ok := spec["wake"].(map[string]any); ok && row.Wake != nil && row.Wake.OnStart {
		delete(wake, "onStart")
	}
	return spec, nil
}
