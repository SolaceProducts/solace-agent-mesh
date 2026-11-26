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
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func pullEvaluators(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	rows, err := c.ListEvaluators(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping evaluators: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	nameByID := evaluatorNamesByID(rows)

	var entries []fileEntry
	var names []string
	for _, row := range rows {
		if row.IsDefault || (opts.Name != "" && row.Name != opts.Name) {
			continue
		}
		spec, err := evaluatorSpec(row, nameByID)
		if err != nil {
			return nil, nil, err
		}
		name, _ := takeString(spec, "name")
		if name == "" {
			name = row.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("evaluator %q: name is not a safe file name", name)
			continue
		}
		description, _ := takeString(spec, "description")

		doc := resourceDoc{
			Kind:        "evaluator",
			Name:        name,
			Description: description,
			Spec:        dropEmpty(spec),
		}
		raw, err := emitYAML(doc)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("evaluators", safe+".yaml"),
			content: raw,
		})
		names = append(names, name)
	}
	return entries, names, nil
}

func evaluatorNamesByID(rows []wire.EvaluatorResponse) map[string]string {
	nameByID := make(map[string]string, len(rows))
	for _, row := range rows {
		nameByID[row.ID] = row.Name
	}
	return nameByID
}

func evaluatorSpec(row wire.EvaluatorResponse, nameByID map[string]string) (map[string]any, error) {
	spec, err := translate.ReverseEvaluatorV2(row, translate.EvaluatorV2Hooks{})
	if err != nil {
		return nil, err
	}
	if err := nameEscalationTarget(spec, row.Name, nameByID); err != nil {
		return nil, err
	}
	return spec, nil
}

func nameEscalationTarget(spec map[string]any, evaluator string, nameByID map[string]string) error {
	id, ok := spec["escalateToEvaluatorId"].(string)
	if !ok || id == "" {
		return nil
	}
	target, found := nameByID[id]
	if !found {
		return fmt.Errorf("evaluator %q: escalateToEvaluatorId %s names no evaluator", evaluator, id)
	}
	spec["escalateToEvaluatorId"] = target
	return nil
}
