// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

func pullExperiments(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	rows, err := c.ListExperiments(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping experiments: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	hooks, err := buildExperimentHooks(ctx, c)
	if err != nil {
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, row := range rows {
		if opts.Name != "" && row.Name != opts.Name {
			continue
		}
		spec, err := translate.ReverseExperimentV2(row, translate.ExperimentV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		if rerr := reverseExperimentXRefs(spec, hooks); rerr != nil {
			return nil, nil, rerr
		}
		dropEmptyModelKeys(spec)
		name, _ := takeString(spec, "name")
		if name == "" {
			name = row.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("experiment %q: name is not a safe file name", name)
			continue
		}
		description, _ := takeString(spec, "description")
		raw, err := emitYAML(resourceDoc{
			Kind:        "experiment",
			Name:        name,
			Description: description,
			Spec:        dropEmpty(spec),
		})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("experiments", safe+".yaml"),
			content: raw,
		})
		names = append(names, name)
	}
	return entries, names, nil
}

func reverseExperimentXRefs(spec map[string]any, hooks translate.ExperimentHooks) error {
	if hooks.LookupDatasetName != nil {
		if id, ok := spec["datasetId"].(string); ok && id != "" {
			name, err := hooks.LookupDatasetName(id)
			if err != nil {
				return fmt.Errorf("datasetId reverse: %w", err)
			}
			spec["datasetId"] = name
		}
	}
	if hooks.LookupEvaluatorName == nil {
		return nil
	}
	if raw, ok := spec["evaluatorIds"]; ok {
		if ids, ok := asStringSlice(raw); ok && len(ids) > 0 {
			rewritten, err := lookupNames(ids, hooks.LookupEvaluatorName, "evaluator")
			if err != nil {
				return err
			}
			spec["evaluatorIds"] = rewritten
		}
	}
	if id, ok := spec["primaryEvaluatorId"].(string); ok && id != "" {
		name, err := hooks.LookupEvaluatorName(id)
		if err != nil {
			return fmt.Errorf("primaryEvaluatorId reverse: %w", err)
		}
		spec["primaryEvaluatorId"] = name
	}
	return nil
}

func buildExperimentHooks(ctx context.Context, c *reconcile.Client) (translate.ExperimentHooks, error) {
	datasets, err := c.ListDatasets(ctx)
	if err != nil {
		return translate.ExperimentHooks{}, fmt.Errorf("list datasets for experiment hooks: %w", err)
	}
	evaluators, err := c.ListEvaluators(ctx)
	if err != nil {
		return translate.ExperimentHooks{}, fmt.Errorf("list evaluators for experiment hooks: %w", err)
	}
	dsByID := make(map[string]string, len(datasets))
	for _, d := range datasets {
		dsByID[d.ID] = d.Name
	}
	evByID := make(map[string]string, len(evaluators))
	for _, e := range evaluators {
		evByID[e.ID] = e.Name
	}
	return translate.ExperimentHooks{
		LookupDatasetName: func(id string) (string, error) {
			if name, ok := dsByID[id]; ok {
				return name, nil
			}
			return "", fmt.Errorf("dataset ID %q not found on platform", id)
		},
		LookupEvaluatorName: func(id string) (string, error) {
			if name, ok := evByID[id]; ok {
				return name, nil
			}
			return "", fmt.Errorf("evaluator ID %q not found on platform", id)
		},
	}, nil
}

func dropEmptyModelKeys(spec map[string]any) {
	models, _ := spec["models"].([]any)
	for _, m := range models {
		entry, ok := m.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range entry {
			if value == "" {
				delete(entry, key)
			}
		}
	}
}
