// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
)

type importEntry struct {
	From   string
	Source string
	Raw    string
}

func sourceURLs(m *manifest.Manifest) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m.Sources))
	for name, src := range m.Sources {
		out[name] = src.Raw
	}
	return out
}

func buildImportLookups(m *manifest.Manifest) map[string]map[string]importEntry {
	out := map[string]map[string]importEntry{}
	if m == nil {
		return out
	}
	for plural, entries := range m.Resources {
		var kindMap map[string]importEntry
		for _, e := range entries {
			if e.Source == "" {
				continue
			}
			src, ok := m.Sources[e.Source]
			if !ok {
				continue
			}
			if kindMap == nil {
				kindMap = map[string]importEntry{}
			}
			kindMap[e.Name] = importEntry{
				From:   e.From,
				Source: e.Source,
				Raw:    src.Raw,
			}
		}
		if len(kindMap) > 0 {
			out[plural] = kindMap
		}
	}
	return out
}

func applyProvenance(names []string, lookups map[string]importEntry) (refs []ResourceRef, importedResources map[string]struct{}, usedSources map[string]struct{}) {
	refs = make([]ResourceRef, 0, len(names))
	importedResources = map[string]struct{}{}
	usedSources = map[string]struct{}{}
	for _, n := range names {
		if e, ok := lookups[n]; ok {
			refs = append(refs, ResourceRef{Name: n, From: e.From, Source: e.Source})
			importedResources[n] = struct{}{}
			usedSources[e.Source] = struct{}{}
			continue
		}
		refs = append(refs, ResourceRef{Name: n})
	}
	return refs, importedResources, usedSources
}

func kindDirFor(kind string) string {
	switch kind {
	case "rbacRole":
		return rbacRolesDir
	case "rbacGrant":
		return rbacGrantsDir
	case "rbacClaimMapping":
		return rbacClaimMappingsDir
	case resolve.ServiceAccountKind:
		return rbacServiceAccountsDir
	case resolve.LegacyServiceAccountKind:
		return rbacSystemUsersDir
	}
	return pluralKind(kind)
}

func dropImportFiles(entries []fileEntry, kindDir string, imported map[string]struct{}) []fileEntry {
	if len(imported) == 0 || kindDir == "" {
		return entries
	}
	importedFiles := make(map[string]struct{}, len(imported))
	for name := range imported {
		importedFiles[resourceFileName(name)] = struct{}{}
	}
	out := make([]fileEntry, 0, len(entries))
	prefix := kindDir + string(filepath.Separator)
	for _, e := range entries {
		rel := filepath.ToSlash(e.relPath)
		nrm := strings.ReplaceAll(prefix, string(filepath.Separator), "/")
		if !strings.HasPrefix(rel, nrm) {
			out = append(out, e)
			continue
		}
		rest := strings.TrimPrefix(rel, nrm)
		var name string
		if head, _, ok := strings.Cut(rest, "/"); ok {
			name = head
		} else {
			name = strings.TrimSuffix(rest, ".yaml")
			name = strings.TrimSuffix(name, ".yml")
		}
		if _, drop := importedFiles[name]; drop {
			continue
		}
		out = append(out, e)
	}
	return out
}
