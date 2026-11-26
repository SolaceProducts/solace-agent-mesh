// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	configloader "github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
)

type ResolvedToolset struct {
	Name        string
	Description string
	SourceFile  string
	ToolsetDir  string

	SourceDir string
	ZipPath   string

	Config map[string]any

	Provenance *SourceProvenance
}

func ResolveToolsets(rootDir string, m *manifest.Manifest) ([]ResolvedToolset, error) {
	entries, ok := m.Resources["toolsets"]
	if !ok || len(entries) == 0 {
		return nil, nil
	}
	for _, e := range entries {
		if e.Source != "" {
			return nil, fmt.Errorf("resources.toolsets: %q references source %q; package-level ResolveToolsets does not support imports — construct a Resolver via resolve.New and call its Toolsets method instead",
				e.Name, e.Source)
		}
	}
	return New(rootDir, m, Options{}).Toolsets(context.Background())
}

func (r *Resolver) Toolsets(ctx context.Context) ([]ResolvedToolset, error) {
	entries, ok := r.manifest.Resources["toolsets"]
	if !ok || len(entries) == 0 {
		return nil, nil
	}

	claims := map[string]claim{}
	out := make([]ResolvedToolset, 0, len(entries))
	var unresolved UnresolvedVarsError

	for _, e := range entries {
		fromName := e.From
		if fromName == "" {
			fromName = e.Name
		}

		var (
			rt          ResolvedToolset
			origin      claim
			rootForKind string
		)

		if e.Source == "" {
			rootForKind = r.repoRoot
			metadataPath := filepath.Join(rootForKind, "toolsets", fromName+".yaml")
			if _, err := os.Stat(metadataPath); err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("resources.toolsets: %q has no metadata at %s", fromName, metadataPath)
				}
				return nil, fmt.Errorf("stat %s: %w", metadataPath, err)
			}
			loaded, missing, vaultErrs, err := loadToolsetManifest(metadataPath, fromName, r.manifest, r.vault)
			unresolved.add(metadataPath, missing)
			unresolved.addVault(metadataPath, vaultErrs)
			if err != nil {
				if len(missing) > 0 || len(vaultErrs) > 0 {
					return nil, &unresolved
				}
				return nil, err
			}
			rt = loaded
			origin = claim{path: metadataPath, source: "", entryRef: fromName}
		} else {
			fs, err := r.fetch(ctx, e.Source)
			if err != nil {
				return nil, fmt.Errorf("resources.toolsets: %q: %w", e.Name, err)
			}
			rootForKind = fs.Result.Dir
			metadataPath := filepath.Join(rootForKind, "toolsets", fromName+".yaml")
			if _, statErr := os.Stat(metadataPath); statErr != nil {
				if os.IsNotExist(statErr) {
					return nil, fmt.Errorf("resources.toolsets: %q not found under source %q (%s)", fromName, e.Source, metadataPath)
				}
				return nil, fmt.Errorf("stat %s: %w", metadataPath, statErr)
			}
			loaded, missing, vaultErrs, err := loadToolsetManifest(metadataPath, fromName, r.manifest, r.vault)
			unresolved.add(metadataPath, missing)
			unresolved.addVault(metadataPath, vaultErrs)
			if err != nil {
				if len(missing) > 0 || len(vaultErrs) > 0 {
					return nil, &unresolved
				}
				return nil, err
			}
			rt = loaded
			rt.Provenance = &SourceProvenance{
				Name:         fs.Name,
				Ref:          fs.Source.Ref,
				SHA:          fs.Result.SHA,
				Raw:          fs.Source.Raw,
				OriginalName: fromName,
			}
			ref := fromName + "@" + e.Source
			if e.Name != fromName {
				ref += " as " + e.Name
			}
			origin = claim{path: metadataPath, source: e.Source, entryRef: ref}
		}

		rt.Name = e.Name
		rt.ToolsetDir = filepath.Join(rootForKind, "toolsets", fromName)
		if err := resolveBundleSource(&rt, fromName, rt.ToolsetDir); err != nil {
			return nil, err
		}

		if existing, dup := claims[rt.Name]; dup {
			return nil, collisionError("toolsets", rt.Name, existing, origin)
		}
		claims[rt.Name] = origin

		out = append(out, rt)
	}
	if !unresolved.empty() {
		return nil, &unresolved
	}
	return out, nil
}

func resolveBundleSource(rt *ResolvedToolset, bundleName, toolsetDir string) error {
	srcDir := filepath.Join(toolsetDir, "src")
	zipPath := filepath.Join(toolsetDir, bundleName+".zip")

	srcStat, srcErr := os.Stat(srcDir)
	hasSrc := srcErr == nil && srcStat.IsDir()

	_, zipErr := os.Stat(zipPath)
	hasZip := zipErr == nil

	switch {
	case hasSrc && hasZip:
		return fmt.Errorf("toolset %q: both %s/ and %s exist — delete one (src/ is for the author build flow, %s.zip is for the pull/mirror flow)",
			rt.Name, srcDir, zipPath, bundleName)
	case hasSrc:
		rt.SourceDir = srcDir
		return nil
	case hasZip:
		rt.ZipPath = zipPath
		return nil
	default:
		return fmt.Errorf("toolset %q: no bundle source — expected either %s/ (author flow) or %s (mirror flow)",
			rt.Name, srcDir, zipPath)
	}
}

type toolsetSpec struct {
	Config map[string]any `yaml:"config"`
}

func loadToolsetManifest(path, expectedName string, m *manifest.Manifest, vault configloader.VaultFetcher) (ResolvedToolset, []string, []configloader.VaultRefError, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ResolvedToolset{}, nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	expanded, missing := configloader.ExpandVarsCollect(string(raw), variableLookup(m))
	expanded, vaultErrs := configloader.ResolveVaultURIs(expanded, vault)

	var doc struct {
		Kind        string      `yaml:"kind"`
		Name        string      `yaml:"name"`
		Description string      `yaml:"description"`
		Spec        toolsetSpec `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		return ResolvedToolset{}, missing, vaultErrs, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := rejectUnknownTopLevelKeys(path, expanded); err != nil {
		return ResolvedToolset{}, missing, vaultErrs, err
	}
	if err := rejectIncludeTags(path, expanded); err != nil {
		return ResolvedToolset{}, missing, vaultErrs, err
	}
	if doc.Kind != "toolset" {
		return ResolvedToolset{}, missing, vaultErrs, fmt.Errorf("%s: kind %q does not match expected %q", path, doc.Kind, "toolset")
	}
	if doc.Name == "" {
		doc.Name = expectedName
	} else if doc.Name != expectedName {
		return ResolvedToolset{}, missing, vaultErrs, fmt.Errorf("%s: declares name %q; manifest expected %q", path, doc.Name, expectedName)
	}

	return ResolvedToolset{
		Name:        doc.Name,
		Description: doc.Description,
		SourceFile:  path,
		Config:      doc.Spec.Config,
	}, missing, vaultErrs, nil
}
