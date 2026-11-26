// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	configloader "github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
)

const parseErrorFormat = "parse %s: %w"

const serviceAccountSpellingHint = "a service account's manifest key, directory and kind use one spelling together, in every manifest that lists it: " +
	"resources.serviceAccounts, rbac/service-accounts/ and kind: serviceAccount, or the older resources.systemUsers, rbac/system-users/ and kind: systemUser"

var (
	errResourceNotFound    = errors.New("not found")
	errKindDoesNotMatchDir = errors.New("does not match directory")
)

type SourceProvenance struct {
	Name         string
	Ref          string
	SHA          string
	Raw          string
	OriginalName string
}

func (p *SourceProvenance) String() string {
	if p == nil {
		return ""
	}
	if p.SHA == "" && p.Ref == "" {
		return fmt.Sprintf("from %s", p.Name)
	}
	short := p.SHA
	if len(short) > 7 {
		short = short[:7]
	}
	if p.SHA == "" {
		return fmt.Sprintf("from %s@%s", p.Name, p.Ref)
	}
	return fmt.Sprintf("from %s@%s [%s]", p.Name, p.Ref, short)
}

type ResolvedResource struct {
	Kind        string
	Name        string
	SourceFile  string
	Spec        map[string]any
	Description string

	Provenance *SourceProvenance
}

func ResolveModels(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("models", "model")
}

func ResolveConnectors(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("connectors", "connector")
}

func ResolveAgents(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("agents", "agent")
}

func ResolveGateways(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("entrypoints", "entrypoint")
}

func ResolveLinkAgents(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("linkAgents", "linkAgent")
}

func ResolveWorkflows(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("workflows", "workflow")
}

func ResolveDatasets(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("datasets", "dataset")
}

func ResolveEvaluators(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("evaluators", "evaluator")
}

func ResolveExperiments(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnly("experiments", "experiment")
}

func ResolveRBACRoles(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnlyAtDir("rbacRoles", "rbacRole", filepath.Join("rbac", "roles"))
}

func ResolveRBACClaimMappings(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnlyAtDir("rbacClaimMappings", "rbacClaimMapping", filepath.Join("rbac", "claim-mappings"))
}

func ResolveRBACGrants(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	return New(rootDir, m, Options{}).resolveKindLocalOnlyAtDir("rbacGrants", "rbacGrant", filepath.Join("rbac", "grants"))
}

const (
	ServiceAccountKind       = "serviceAccount"
	LegacyServiceAccountKind = "systemUser"
)

func ResolveServiceAccounts(rootDir string, m *manifest.Manifest) ([]ResolvedResource, error) {
	r := New(rootDir, m, Options{})
	current, err := r.resolveKindLocalOnlyAtDir("serviceAccounts", ServiceAccountKind, filepath.Join("rbac", "service-accounts"))
	if err != nil {
		return nil, withServiceAccountSpellingHint(err)
	}
	legacy, err := r.resolveKindLocalOnlyAtDir("systemUsers", LegacyServiceAccountKind, filepath.Join("rbac", "system-users"))
	if err != nil {
		return nil, withServiceAccountSpellingHint(err)
	}
	for _, l := range legacy {
		for _, c := range current {
			if l.Name == c.Name {
				return nil, fmt.Errorf("%q is declared under both resources.serviceAccounts (%s) and resources.systemUsers (%s); keep one", l.Name, c.SourceFile, l.SourceFile)
			}
		}
	}
	return append(current, legacy...), nil
}

func withServiceAccountSpellingHint(err error) error {
	if errors.Is(err, errResourceNotFound) || errors.Is(err, errKindDoesNotMatchDir) {
		return fmt.Errorf("%w; %s", err, serviceAccountSpellingHint)
	}
	return err
}

func (r *Resolver) resolveKindLocalOnly(resourcesKey, kindName string) ([]ResolvedResource, error) {
	return r.resolveKindLocalOnlyAtDir(resourcesKey, kindName, resourcesKey)
}

func (r *Resolver) resolveKindLocalOnlyAtDir(resourcesKey, kindName, relDir string) ([]ResolvedResource, error) {
	entries, ok := r.manifest.Resources[resourcesKey]
	if !ok || len(entries) == 0 {
		return nil, nil
	}
	for _, e := range entries {
		if e.Source != "" {
			return nil, fmt.Errorf("resources.%s: %q references source %q; package-level Resolve* helpers do not support imports — construct a Resolver via resolve.New and call its method instead",
				resourcesKey, e.Name, e.Source)
		}
	}

	dir := filepath.Join(r.repoRoot, relDir)
	index, nameless, err := indexKindDirectory(dir, kindName)
	if err != nil {
		return nil, err
	}

	out := make([]ResolvedResource, 0, len(entries))
	var unresolved UnresolvedVarsError
	for _, e := range entries {
		fromName := e.From
		if fromName == "" {
			fromName = e.Name
		}
		path, ok := index[fromName]
		if !ok {
			return nil, fmt.Errorf("resources.%s: %q %w under %s%s", resourcesKey, fromName, errResourceNotFound, dir, namelessHint(nameless))
		}
		rr, missing, vaultErrs, err := loadResource(path, kindName, r.manifest, r.vault)
		unresolved.add(path, missing)
		unresolved.addVault(path, vaultErrs)
		if err != nil {
			if len(missing) > 0 || len(vaultErrs) > 0 {
				return nil, &unresolved
			}
			return nil, err
		}
		if rr.Name == "" {
			rr.Name = e.Name
		} else if rr.Name != e.Name {
			return nil, fmt.Errorf("resources.%s[%s]: file %s declares name %q; manifest expected %q", resourcesKey, e.Name, path, rr.Name, e.Name)
		}
		out = append(out, rr)
	}
	if !unresolved.empty() {
		return nil, &unresolved
	}
	return out, nil
}

func indexKindDirectory(dir, expectedKind string) (idx map[string]string, nameless []string, err error) {
	idx = map[string]string{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return idx, nil, nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", dir, err)
	}

	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		head, err := peekHeader(path)
		if err != nil {
			return err
		}
		if head.Kind == "" {
			return nil
		}
		if head.Kind != expectedKind {
			return fmt.Errorf("%s: kind %q %w %q", path, head.Kind, errKindDoesNotMatchDir, expectedKind)
		}
		if head.Name == "" {
			nameless = append(nameless, path)
			return nil
		}
		if existing, dup := idx[head.Name]; dup {
			return fmt.Errorf("duplicate resource name %q in %s and %s", head.Name, existing, path)
		}
		idx[head.Name] = path
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	return idx, nameless, nil
}

func namelessHint(nameless []string) string {
	if len(nameless) == 0 {
		return ""
	}
	return fmt.Sprintf("; these files have no top-level name (add `name: <resource-name>` above spec): %s", strings.Join(nameless, ", "))
}

type header struct {
	Kind string `yaml:"kind"`
	Name string `yaml:"name"`
}

func peekHeader(path string) (header, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return header{}, fmt.Errorf("read %s: %w", path, err)
	}
	var h header
	if err := yaml.Unmarshal(data, &h); err != nil {
		return header{}, fmt.Errorf(parseErrorFormat, path, err)
	}
	return h, nil
}

func loadResource(path, expectedKind string, m *manifest.Manifest, vault configloader.VaultFetcher) (ResolvedResource, []string, []configloader.VaultRefError, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ResolvedResource{}, nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	expanded, missing := configloader.ExpandVarsCollect(string(raw), variableLookup(m))
	expanded, vaultErrs := configloader.ResolveVaultURIs(expanded, vault)

	var doc struct {
		Kind        string         `yaml:"kind"`
		Name        string         `yaml:"name"`
		Description string         `yaml:"description"`
		Spec        map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		return ResolvedResource{}, missing, vaultErrs, fmt.Errorf(parseErrorFormat, path, err)
	}
	if err := rejectUnknownTopLevelKeys(path, expanded); err != nil {
		return ResolvedResource{}, missing, vaultErrs, err
	}
	if err := rejectIncludeTags(path, expanded); err != nil {
		return ResolvedResource{}, missing, vaultErrs, err
	}
	if doc.Kind != expectedKind {
		return ResolvedResource{}, missing, vaultErrs, fmt.Errorf("%s: kind %q does not match expected %q", path, doc.Kind, expectedKind)
	}
	if doc.Spec == nil {
		doc.Spec = map[string]any{}
	}
	return ResolvedResource{
		Kind:        doc.Kind,
		Name:        doc.Name,
		SourceFile:  path,
		Spec:        doc.Spec,
		Description: doc.Description,
	}, missing, vaultErrs, nil
}

const includeTag = "!include"

func rejectIncludeTags(path, content string) error {
	var root yaml.Node
	_ = yaml.Unmarshal([]byte(content), &root)
	if node := findIncludeTag(&root); node != nil {
		return fmt.Errorf("%s: line %d: %s %s is not expanded in a resource file (only the manifest expands includes); put the content inline or use ${VAR}",
			path, node.Line, includeTag, node.Value)
	}
	return nil
}

func findIncludeTag(node *yaml.Node) *yaml.Node {
	if node.Tag == includeTag {
		return node
	}
	for _, child := range node.Content {
		if found := findIncludeTag(child); found != nil {
			return found
		}
	}
	return nil
}

var resourceTopLevelKeys = []string{"kind", "name", "description", "spec"}

func rejectUnknownTopLevelKeys(path, content string) error {
	var top map[string]any
	if err := yaml.Unmarshal([]byte(content), &top); err != nil {
		return fmt.Errorf(parseErrorFormat, path, err)
	}
	var unknown []string
	for key := range top {
		if !slices.Contains(resourceTopLevelKeys, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	msgs := make([]string, len(unknown))
	for i, key := range unknown {
		msgs[i] = fmt.Sprintf("unknown top-level key %q; did you mean spec.%s?", key, key)
	}
	return fmt.Errorf("%s: %s (top-level keys are %s)", path, strings.Join(msgs, "; "), strings.Join(resourceTopLevelKeys, ", "))
}

func variableLookup(m *manifest.Manifest) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if m != nil {
			if v, ok := m.Variables[name]; ok {
				return v, true
			}
		}
		return os.LookupEnv(name)
	}
}

func SortedNames(rs []ResolvedResource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	sort.Strings(out)
	return out
}
