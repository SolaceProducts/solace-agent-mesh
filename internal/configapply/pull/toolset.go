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
)

var _ kindFetcher = pullToolsets

const toolsetDiscoveryReady = "ready"

func pullToolsets(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	ts, err := c.ListToolsets(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping toolsets: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, t := range ts {
		if opts.Name != "" && t.Name != opts.Name {
			continue
		}
		safe := safeFileName(t.Name)
		if safe == "" {
			res.drop("toolset %q: name is not a safe file name", t.Name)
			continue
		}

		spec := map[string]any{}
		if status := valueOrEmpty(t.DiscoveryStatus); status != toolsetDiscoveryReady {
			res.Warnings = append(res.Warnings, fmt.Sprintf("toolset %q: discovery is %s, so its config was not pulled", t.Name, status))
		} else if cfg, err := c.GetToolsetConfig(ctx, t.ID); err != nil {
			res.drop("toolset %q: config fetch failed (%s)", t.Name, err)
		} else if cfg != nil && len(cfg.Values) > 0 {
			rewritten, warnings := substituteSecrets("toolset", t.Name, map[string]any{"config": cfg.Values})
			res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)
			if cfgMap, ok := rewritten["config"].(map[string]any); ok && len(cfgMap) > 0 {
				spec["config"] = cfgMap
			}
		}

		raw, err := emitYAML(resourceDoc{
			Kind:        "toolset",
			Name:        t.Name,
			Description: valueOrEmpty(t.Description),
			Spec:        dropEmpty(spec),
		})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("toolsets", safe+".yaml"),
			content: raw,
		})

		if t.Bundle == nil || t.Bundle.Filename == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("toolset %q: no bundle uploaded; wrote metadata only", t.Name))
			names = append(names, t.Name)
			continue
		}

		zipBytes, dlErr := c.DownloadToolset(ctx, t.ID)
		if dlErr != nil {
			res.drop("toolset %q: bundle download failed (%s)", t.Name, dlErr)
		} else if len(zipBytes) == 0 {
			res.drop("toolset %q: bundle is empty on the platform", t.Name)
		} else {
			entries = append(entries, fileEntry{
				relPath: filepath.Join("toolsets", safe, safe+".zip"),
				content: zipBytes,
			})
		}

		names = append(names, t.Name)
	}
	return entries, names, nil
}

func valueOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
