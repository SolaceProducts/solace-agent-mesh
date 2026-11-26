// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func pullDatasets(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	rows, err := c.ListDatasets(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping datasets: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, row := range rows {
		if opts.Name != "" && row.Name != opts.Name {
			continue
		}
		spec, err := translate.ReverseDatasetV2(row, translate.DatasetV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		name, _ := takeString(spec, "name")
		if name == "" {
			name = row.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("dataset %q: name is not a safe file name", name)
			continue
		}
		description, _ := takeString(spec, "description")

		csvName := safe + ".examples.csv"
		csvBytes, attachments, csvErr := buildDatasetExamplesCSV(ctx, c, row.ID)
		if csvErr != nil {
			return nil, nil, fmt.Errorf("dataset %q: build examples csv: %w", name, csvErr)
		}
		if attachments > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"dataset %q has %d example attachments, which sam config pull does not export; re-applying this tree will not restore them.",
				name, attachments))
		}
		spec["examples_file"] = csvName

		doc := resourceDoc{
			Kind:        "dataset",
			Name:        name,
			Description: description,
			Spec:        dropEmpty(spec),
		}
		raw, err := emitYAML(doc)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("datasets", safe+".yaml"),
			content: raw,
		})
		entries = append(entries, fileEntry{
			relPath: filepath.Join("datasets", csvName),
			content: csvBytes,
		})
		names = append(names, name)
	}
	return entries, names, nil
}

func buildDatasetExamplesCSV(ctx context.Context, c *reconcile.Client, datasetID string) ([]byte, int, error) {
	rows, err := c.ListAllExamples(ctx, datasetID)
	if err != nil {
		return nil, 0, err
	}
	attachments := 0
	specs := make([]translate.ExampleSpec, 0, len(rows))
	for _, r := range rows {
		if r.InputArtifact != nil {
			attachments++
		}
		if r.ExpectedArtifact != nil {
			attachments++
		}
		input, inputErr := evalplatform.CanonicalInputData(json.RawMessage(r.InputData))
		if inputErr != nil {
			return nil, 0, fmt.Errorf("example %d inputData: %w", r.SequenceNumber, inputErr)
		}
		specs = append(specs, translate.ExampleSpec{
			SequenceNumber:   r.SequenceNumber,
			Prompt:           r.Prompt,
			ExpectedResponse: r.ExpectedResponse,
			InputData:        input,
			ExpectedPath:     wire.EvalExpectedPath(r.ExpectedPath),
			ExpectedFacts:    wire.EvalExpectedFacts(r.ExpectedFacts),
		})
	}
	slices.SortFunc(specs, func(a, b translate.ExampleSpec) int { return a.SequenceNumber - b.SequenceNumber })
	body, err := translate.EmitExamplesCSV(specs)
	if err != nil {
		return nil, 0, err
	}
	return body, attachments, nil
}
