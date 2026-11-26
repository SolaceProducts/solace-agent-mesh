// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func pullConnectors(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	conns, err := c.ListConnectors(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping connectors: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, conn := range conns {
		if opts.Name != "" && conn.Name != opts.Name {
			continue
		}
		spec, err := translate.ReverseConnectorV2(conn, translate.ConnectorV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		name, _ := takeString(spec, "name")
		if name == "" {
			name = conn.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("connector %q: name is not a safe file name", name)
			continue
		}

		if specEntry := pullConnectorSpecFile(ctx, c, conn, spec, safe, res); specEntry != nil {
			entries = append(entries, *specEntry)
		}

		description, _ := takeString(spec, "description")
		spec, warnings := substituteSecrets("connector", name, spec)
		res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)

		raw, err := emitYAML(resourceDoc{
			Kind:        "connector",
			Name:        name,
			Description: description,
			Spec:        dropEmpty(spec),
		})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("connectors", safe+".yaml"),
			content: raw,
		})
		names = append(names, name)
	}
	return entries, names, nil
}

func pullConnectorSpecFile(ctx context.Context, c *reconcile.Client, conn wire.ConnectorResponse, spec map[string]any, safe string, res *Result) *fileEntry {
	if conn.ConnectorType != "api" || conn.ConnectorSubtype != "openapi" || !conn.HasResource {
		return nil
	}
	values, ok := spec["values"].(map[string]any)
	if !ok {
		return nil
	}
	if u, _ := values["specification_url"].(string); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return nil
	}

	data, filename, err := c.DownloadConnectorResource(ctx, conn.ID)
	if err != nil {
		res.drop("connector %q: spec download failed (%s)", conn.Name, err)
		return nil
	}

	sidecar := safe + ".openapi" + specFileExt(filename)
	values["specification_file"] = sidecar
	delete(values, "specification_url")
	delete(values, "file_s3_key")
	return &fileEntry{
		relPath: filepath.Join("connectors", sidecar),
		content: data,
	}
}

func specFileExt(filename string) string {
	switch ext := strings.ToLower(filepath.Ext(filename)); ext {
	case ".yaml", ".yml", ".json":
		return ext
	}
	return ".yaml"
}
