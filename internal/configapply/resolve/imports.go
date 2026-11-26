// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	configloader "github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
)

type Options struct {
	AllowFloating bool

	NoCache bool

	CacheRoot string

	Logf func(format string, args ...any)

	VaultFetcher configloader.VaultFetcher
}

type Resolver struct {
	repoRoot string
	manifest *manifest.Manifest
	opts     Options

	mu      sync.Mutex
	fetched map[string]*fetchedSource

	vault configloader.VaultFetcher
}

type fetchedSource struct {
	Name   string
	Source sources.Source
	Result sources.FetchResult
}

func New(repoRoot string, m *manifest.Manifest, opts Options) *Resolver {
	vault := opts.VaultFetcher
	if vault == nil {
		vault = configloader.EnvVaultFetcher{}
	}
	return &Resolver{
		repoRoot: repoRoot,
		manifest: m,
		opts:     opts,
		fetched:  map[string]*fetchedSource{},
		vault:    vault,
	}
}

func (r *Resolver) Models(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "models", "model")
}

func (r *Resolver) Connectors(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "connectors", "connector")
}

func (r *Resolver) Agents(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "agents", "agent")
}

func (r *Resolver) Workflows(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "workflows", "workflow")
}

func (r *Resolver) Gateways(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "entrypoints", "entrypoint")
}

func (r *Resolver) LinkAgents(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "linkAgents", "linkAgent")
}

func (r *Resolver) Budgets(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "budgets", "budget")
}

func (r *Resolver) Datasets(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "datasets", "dataset")
}

func (r *Resolver) Evaluators(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "evaluators", "evaluator")
}

func (r *Resolver) Experiments(ctx context.Context) ([]ResolvedResource, error) {
	return r.resolveKind(ctx, "experiments", "experiment")
}

func (r *Resolver) Provenance(sourceName string) *SourceProvenance {
	r.mu.Lock()
	defer r.mu.Unlock()
	fs, ok := r.fetched[sourceName]
	if !ok {
		return nil
	}
	return &SourceProvenance{
		Name: fs.Name,
		Ref:  fs.Source.Ref,
		SHA:  fs.Result.SHA,
		Raw:  fs.Source.Raw,
	}
}

func (r *Resolver) fetch(ctx context.Context, name string) (*fetchedSource, error) {
	r.mu.Lock()
	if fs, ok := r.fetched[name]; ok {
		r.mu.Unlock()
		return fs, nil
	}
	r.mu.Unlock()

	src, ok := r.manifest.Sources[name]
	if !ok {
		return nil, fmt.Errorf("source %q is not declared in the manifest's sources: block", name)
	}

	level, msg := sources.ClassifyRef(src, r.opts.AllowFloating)
	switch level {
	case sources.LevelError:
		return nil, fmt.Errorf("source %q: %s", name, msg)
	case sources.LevelWarn:
		if r.opts.Logf != nil && msg != "" {
			r.opts.Logf("warning: %s", msg)
		}
	}

	res, err := sources.Fetch(ctx, src, sources.FetchOptions{
		CacheRoot: r.opts.CacheRoot,
		NoCache:   r.opts.NoCache,
		Logf:      r.opts.Logf,
	})
	if err != nil {
		return nil, fmt.Errorf("source %q: %w", name, err)
	}

	fs := &fetchedSource{Name: name, Source: src, Result: res}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.fetched[name]; ok {
		return existing, nil
	}
	r.fetched[name] = fs
	return fs, nil
}

type claim struct {
	path     string
	source   string
	entryRef string
}

func (r *Resolver) resolveKind(ctx context.Context, resourcesKey, kindName string) ([]ResolvedResource, error) {
	entries, ok := r.manifest.Resources[resourcesKey]
	if !ok || len(entries) == 0 {
		return nil, nil
	}

	localDir := filepath.Join(r.repoRoot, resourcesKey)
	localIndex, localNameless, err := indexKindDirectory(localDir, kindName)
	if err != nil {
		return nil, err
	}

	claims := map[string]claim{}
	var unresolved UnresolvedVarsError

	out := make([]ResolvedResource, 0, len(entries))
	for _, e := range entries {
		fromName := e.From
		if fromName == "" {
			fromName = e.Name
		}

		var (
			rr     ResolvedResource
			origin claim
		)

		if e.Source == "" {
			path, ok := localIndex[fromName]
			if !ok {
				return nil, fmt.Errorf("resources.%s: %q not found under %s%s", resourcesKey, fromName, localDir, namelessHint(localNameless))
			}
			loaded, missing, vaultErrs, err := loadResource(path, kindName, r.manifest, r.vault)
			unresolved.add(path, missing)
			unresolved.addVault(path, vaultErrs)
			if err != nil {
				if len(missing) > 0 || len(vaultErrs) > 0 {
					return nil, &unresolved
				}
				return nil, err
			}
			rr = loaded
			origin = claim{path: path, source: "", entryRef: fromName}
		} else {
			fs, err := r.fetch(ctx, e.Source)
			if err != nil {
				return nil, fmt.Errorf("resources.%s: %q: %w", resourcesKey, e.Name, err)
			}
			srcKindDir := filepath.Join(fs.Result.Dir, resourcesKey)
			srcIndex, srcNameless, err := indexKindDirectory(srcKindDir, kindName)
			if err != nil {
				return nil, fmt.Errorf("resources.%s: source %q: %w", resourcesKey, e.Source, err)
			}
			path, ok := srcIndex[fromName]
			if !ok {
				return nil, fmt.Errorf("resources.%s: %q not found under source %q (%s)%s", resourcesKey, fromName, e.Source, srcKindDir, namelessHint(srcNameless))
			}
			loaded, missing, vaultErrs, err := loadResource(path, kindName, r.manifest, r.vault)
			unresolved.add(path, missing)
			unresolved.addVault(path, vaultErrs)
			if err != nil {
				if len(missing) > 0 || len(vaultErrs) > 0 {
					return nil, &unresolved
				}
				return nil, err
			}
			rr = loaded
			rr.Provenance = &SourceProvenance{
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
			origin = claim{path: path, source: e.Source, entryRef: ref}
		}

		if rr.Name == "" {
			rr.Name = fromName
		} else if rr.Name != fromName {
			return nil, fmt.Errorf("resources.%s[%s]: file %s declares name %q; manifest expected %q",
				resourcesKey, origin.entryRef, origin.path, rr.Name, fromName)
		}

		rr.Name = e.Name

		if existing, dup := claims[rr.Name]; dup {
			return nil, collisionError(resourcesKey, rr.Name, existing, origin)
		}
		claims[rr.Name] = origin

		out = append(out, rr)
	}

	if !unresolved.empty() {
		return nil, &unresolved
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

func collisionError(resourcesKey, name string, existing, current claim) error {
	describe := func(c claim) string {
		if c.source == "" {
			return fmt.Sprintf("local %s", c.path)
		}
		return fmt.Sprintf("import %s (source %q at %s)", c.entryRef, c.source, c.path)
	}
	return fmt.Errorf("resources.%s: name %q is claimed by both %s and %s — pick one or alias the import via {from: …, as: …}",
		resourcesKey, name, describe(existing), describe(current))
}
