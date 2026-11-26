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

func pullModels(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	models, err := c.ListModels(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping models: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, m := range models {
		if opts.Name != "" && m.Alias != opts.Name {
			continue
		}
		if reconcile.IsPlaceholderModel(m) || reconcile.IsDelegatedModel(m) {
			continue
		}
		spec, err := translate.ReverseModelV2(m, translate.ModelV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		if p, ok := spec["pricing"].(map[string]any); ok {
			spec["pricing"] = dropEmpty(p)
		}
		delete(spec, "authType")
		alias, _ := takeString(spec, "alias")
		if alias == "" {
			alias = m.Alias
		}
		safe := resourceFileName(alias)
		if safe == "" {
			res.drop("model %q: name is not a safe file name", alias)
			continue
		}
		description, _ := takeString(spec, "description")

		spec, warnings := substituteSecrets("model", alias, spec)
		res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)

		doc := resourceDoc{
			Kind:        "model",
			Name:        alias,
			Description: description,
			Spec:        dropEmpty(spec),
		}
		raw, err := emitYAML(doc)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("models", safe+".yaml"),
			content: raw,
		})
		names = append(names, alias)
	}
	return entries, names, nil
}
