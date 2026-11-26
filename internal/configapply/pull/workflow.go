// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

func pullWorkflows(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	wfs, err := c.ListWorkflows(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping workflows: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, w := range wfs {
		if opts.Name != "" && w.Name != opts.Name {
			continue
		}
		header, err := translate.ReverseWorkflowV2(w, translate.WorkflowV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		name, _ := takeString(header, "name")
		if name == "" {
			name = w.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("workflow %q: name is not a safe file name", name)
			continue
		}
		description, _ := takeString(header, "description")
		spec := make(map[string]any, len(w.AppConfig))
		maps.Copy(spec, w.AppConfig)
		spec, warnings := substituteSecrets("workflow", name, spec)
		res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)
		raw, err := emitYAML(resourceDoc{
			Kind:        "workflow",
			Name:        name,
			Description: description,
			Spec:        reconcile.WorkflowSpecFor(dropEmpty(spec)),
		})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("workflows", safe+".yaml"),
			content: raw,
		})
		names = append(names, name)
	}
	return entries, names, nil
}
