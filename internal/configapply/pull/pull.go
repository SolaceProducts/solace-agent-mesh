// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/registry"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
)

type Options struct {
	OutputDir string

	Force bool

	Merge bool

	OnlyKind string

	Name string

	ManifestName string

	AuthResolver target.OAuthTokenResolver

	IncludeYAMLPreview bool

	ReferenceManifest *manifest.Manifest
}

type Result struct {
	OutputDir    string
	ManifestPath string

	Counts map[string]int

	Skipped map[string]int

	SecretPlaceholders []SecretWarning

	Warnings []string

	preservedDirs []string

	lossy []string
}

func (r *Result) drop(format string, args ...any) {
	r.lossy = append(r.lossy, fmt.Sprintf(format, args...))
}

func (r *Result) skip(preview bool, msg string) {
	if preview {
		r.Warnings = append(r.Warnings, msg)
		return
	}
	r.drop("%s", msg)
}

type SecretWarning struct {
	Kind         string
	ResourceName string
	FieldPath    string
	Placeholder  string
}

var inScopeKinds = []string{"model", "connector", "toolset", "skill", "agent", "entrypoint", "linkAgent", "workflow", "dataset", "evaluator", "experiment", "rbacRole", "rbacGrant", "rbacClaimMapping", "serviceAccount", "budget"}

var hiddenKinds []string

var pulledKinds = slices.Concat(inScopeKinds, hiddenKinds, []string{resolve.LegacyServiceAccountKind})

func InScopeKinds() []string { return slices.Clone(inScopeKinds) }

var outOfScopeKinds = []string{}

func Run(ctx context.Context, t target.Target, opts Options) (*Result, error) {
	if opts.OutputDir == "" {
		return nil, errors.New("pull: OutputDir is required")
	}
	if opts.Force && opts.Merge {
		return nil, errors.New("pull: --force and --merge are mutually exclusive")
	}
	if opts.OnlyKind != "" && !validKind(opts.OnlyKind) {
		return nil, fmt.Errorf("pull: unknown --only kind=%s (valid: %s)", opts.OnlyKind, strings.Join(inScopeKinds, "|"))
	}

	resolved, err := t.Resolve(ctx, opts.AuthResolver)
	if err != nil {
		return nil, err
	}
	c := reconcile.NewClient(resolved)

	abs, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("pull: resolve output dir: %w", err)
	}
	opts.OutputDir = abs

	if !opts.Force && !opts.Merge {
		empty, dirErr := dirIsEmpty(abs)
		if dirErr != nil {
			return nil, dirErr
		}
		if !empty {
			return nil, fmt.Errorf("pull: output directory %s is not empty (use --force to overwrite or --merge to update)", abs)
		}
	}

	bundle, res, err := fetchAndTranslate(ctx, c, opts)
	if err != nil {
		return res, err
	}

	if opts.Force {
		if err := wipeContents(abs, res.preservedDirs); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("pull: mkdir %s: %w", abs, err)
	}

	for _, f := range bundle {
		full := filepath.Join(abs, f.relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("pull: mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, f.content, 0o644); err != nil {
			return nil, fmt.Errorf("pull: write %s: %w", full, err)
		}
	}

	res.OutputDir = abs
	res.ManifestPath = filepath.Join(abs, res.ManifestPath)
	return res, nil
}

type fileEntry struct {
	relPath string
	content []byte
}

func claimFiles(entries []fileEntry, written map[string]struct{}, res *Result) []fileEntry {
	out := entries[:0]
	for _, e := range entries {
		foldedPath := strings.ToLower(e.relPath)
		rel := filepath.ToSlash(e.relPath)
		if _, dup := written[foldedPath]; dup {
			preview := strings.Contains(rel, "/"+rbacYAMLPreviewSubdir+"/")
			res.skip(preview, rel+": two resources map to this file")
			continue
		}
		if kept, ok := preservedDirHolding(foldedPath, res.preservedDirs); ok {
			res.drop("%s: maps into %s, the local copy kept unchanged", rel, filepath.ToSlash(kept))
			continue
		}
		written[foldedPath] = struct{}{}
		out = append(out, e)
	}
	return out
}

func preservedDirHolding(foldedPath string, preservedDirs []string) (string, bool) {
	for _, dir := range preservedDirs {
		if strings.HasPrefix(foldedPath, strings.ToLower(dir)+string(filepath.Separator)) {
			return dir, true
		}
	}
	return "", false
}

type kindFetcher func(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error)

func fetchAndTranslate(ctx context.Context, c *reconcile.Client, opts Options) ([]fileEntry, *Result, error) {
	res := &Result{
		Counts:  map[string]int{},
		Skipped: map[string]int{},
	}

	manifestEntries := map[string][]ResourceRef{}
	usedSources := map[string]string{}
	importLookups := buildImportLookups(opts.ReferenceManifest)
	referenceSourceURLs := sourceURLs(opts.ReferenceManifest)

	var bundle []fileEntry

	dispatch := []struct {
		kind string
		fn   kindFetcher
	}{
		{"model", pullModels},
		{"connector", pullConnectors},
		{"toolset", pullToolsets},
		{"workflow", pullWorkflows},
		{"entrypoint", pullGateways},
		{linkAgentKind, pullLinkAgents},
		{"skill", pullSkills},
		{"agent", pullAgents},
		{"dataset", pullDatasets},
		{"evaluator", pullEvaluators},
		{"experiment", pullExperiments},
		{"budget", pullBudgets},
		{"rbacRole", pullRBACRoles},
		{"rbacGrant", pullRBACGrants},
		{"rbacClaimMapping", pullRBACClaimMappings},
		{resolve.ServiceAccountKind, pullServiceAccounts},
		{resolve.LegacyServiceAccountKind, pullLegacySystemUsers},
	}

	written := map[string]struct{}{}
	for _, d := range dispatch {
		if opts.OnlyKind != "" && !onlyKindSelects(opts.OnlyKind, d.kind) {
			continue
		}
		entries, names, err := d.fn(ctx, c, opts, res)
		if err != nil {
			return nil, res, fmt.Errorf("pull %s: %w", d.kind, err)
		}
		plural := pluralKind(d.kind)
		refs, importedResources, srcs := applyProvenance(names, importLookups[plural])
		for srcName := range srcs {
			if raw, ok := referenceSourceURLs[srcName]; ok {
				usedSources[srcName] = raw
			}
		}
		entries = claimFiles(entries, written, res)
		bundle = append(bundle, dropImportFiles(entries, kindDirFor(d.kind), importedResources)...)
		res.Counts[d.kind] = len(names)
		if len(refs) > 0 {
			manifestEntries[plural] = append(manifestEntries[plural], refs...)
		}
	}

	if opts.OnlyKind == "" {
		for _, k := range outOfScopeKinds {
			count, err := countSkippedKind(ctx, c, k)
			if err != nil {
				continue
			}
			if count > 0 {
				res.Skipped[k] = count
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("skipping %d %s%s: pull for this kind is not implemented in v1", count, k, plural(count)))
			}
		}
	}

	for k := range manifestEntries {
		slices.SortStableFunc(manifestEntries[k], func(a, b ResourceRef) int { return cmp.Compare(a.Name, b.Name) })
	}

	platform, err := pullPlatformBlock(ctx, c, opts, res)
	if err != nil {
		return nil, res, err
	}
	if len(res.lossy) > 0 {
		return nil, res, fmt.Errorf("pull cannot reproduce %d resource%s, so nothing was written:\n  - %s\n"+
			"fix or rename these on the platform, or pull the rest with --only or --name",
			len(res.lossy), plural(len(res.lossy)), strings.Join(res.lossy, "\n  - "))
	}

	manifestBytes, err := buildManifestYAML(opts, manifestEntries, usedSources, platform, c)
	if err != nil {
		return nil, res, err
	}
	manifestRel := manifestRelPath(opts.ManifestName)
	res.ManifestPath = manifestRel
	bundle = append(bundle, fileEntry{
		relPath: manifestRel,
		content: manifestBytes,
	})

	slices.SortStableFunc(bundle, func(a, b fileEntry) int { return cmp.Compare(a.relPath, b.relPath) })
	slices.SortStableFunc(res.SecretPlaceholders, func(a, b SecretWarning) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ResourceName, b.ResourceName); c != 0 {
			return c
		}
		return cmp.Compare(a.FieldPath, b.FieldPath)
	})

	return bundle, res, nil
}

func buildRegistry(ctx context.Context, c *reconcile.Client) (*registry.Registry, error) {
	reg := registry.New(reconcile.NewLister(c))
	reg.SetBuiltinToolsets(reconcile.BuiltinAgentToolsets())
	reg.SetBuiltinSkills(builtinskills.Names())
	if err := reg.Load(ctx); err != nil {
		return nil, err
	}
	return reg, nil
}

func validKind(s string) bool {
	return slices.Contains(pulledKinds, s)
}

func onlyKindSelects(only, kind string) bool {
	if only == kind {
		return true
	}
	spellings := []string{resolve.ServiceAccountKind, resolve.LegacyServiceAccountKind}
	return slices.Contains(spellings, only) && slices.Contains(spellings, kind)
}

func pluralKind(singular string) string {
	switch singular {
	case "model":
		return "models"
	case "connector":
		return "connectors"
	case "toolset":
		return "toolsets"
	case "skill":
		return "skills"
	case "agent":
		return "agents"
	case "entrypoint":
		return "entrypoints"
	case "workflow":
		return "workflows"
	case "dataset":
		return "datasets"
	case "evaluator":
		return "evaluators"
	case "experiment":
		return "experiments"
	case "rbacRole":
		return "rbacRoles"
	case "rbacGrant":
		return "rbacGrants"
	case "rbacClaimMapping":
		return "rbacClaimMappings"
	}
	return singular + "s"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dirIsEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("pull: stat %s: %w", path, err)
	}
	return len(entries) == 0, nil
}

func wipeContents(dir string, keep []string) error {
	return wipeBelow(dir, "", keep)
}

func wipeBelow(root, rel string, keep []string) error {
	entries, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("pull: read %s: %w", filepath.Join(root, rel), err)
	}
	for _, e := range entries {
		entryRel := filepath.Join(rel, e.Name())
		switch {
		case slices.Contains(keep, entryRel):
			continue
		case e.IsDir() && hasKeptDescendant(entryRel, keep):
			if err := wipeBelow(root, entryRel, keep); err != nil {
				return err
			}
		default:
			if err := os.RemoveAll(filepath.Join(root, entryRel)); err != nil {
				return fmt.Errorf("pull: remove %s: %w", entryRel, err)
			}
		}
	}
	return nil
}

func hasKeptDescendant(dirRel string, keep []string) bool {
	return slices.ContainsFunc(keep, func(k string) bool {
		return strings.HasPrefix(k, dirRel+string(filepath.Separator))
	})
}

func manifestRelPath(name string) string {
	if name == "" {
		return filepath.Join("manifests", "pulled-"+timestampSlug()+".yaml")
	}
	cleaned := filepath.Clean(name)
	if strings.HasPrefix(cleaned, "..") {
		cleaned = filepath.Base(cleaned)
	}
	if filepath.Dir(cleaned) != "." {
		return cleaned
	}
	if filepath.Ext(cleaned) == "" {
		cleaned += ".yaml"
	}
	return filepath.Join("manifests", cleaned)
}

func timestampSlug() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func safeFileName(name string) string {
	if name == "" {
		return ""
	}
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return ""
	}
	return name
}

func resourceFileName(name string) string {
	return strings.TrimLeft(strings.NewReplacer("/", "_", `\`, "_").Replace(name), ".")
}
