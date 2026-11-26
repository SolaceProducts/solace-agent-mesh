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
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func pullGateways(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	gws, err := c.ListGateways(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping gateways: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, g := range gws {
		if opts.Name != "" && g.Name != opts.Name {
			continue
		}
		spec, err := translate.ReverseEntrypointV2(g, translate.EntrypointV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		if !slugIsOperatorChosen(g) {
			delete(spec, "slug")
		}
		if vals, ok := spec["values"].(map[string]any); ok {
			entplatform.StripDerivedGatewayValues(g.EntrypointType, vals)
		}
		name, _ := takeString(spec, "name")
		if name == "" {
			name = g.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("entrypoint %q: name is not a safe file name", name)
			continue
		}
		description, _ := takeString(spec, "description")

		spec, warnings := substituteSecrets("entrypoint", name, spec)
		res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)

		doc := resourceDoc{
			Kind:        "entrypoint",
			Name:        name,
			Description: description,
			Spec:        dropEmpty(spec),
		}
		raw, err := emitYAML(doc)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("entrypoints", safe+".yaml"),
			content: raw,
		})
		names = append(names, name)
	}
	return entries, names, nil
}

func slugIsOperatorChosen(g wire.EntrypointResponse) bool {
	return g.Slug != nil && *g.Slug != g.ID
}
