// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

const (
	e2eProviderSeedRelPath = "test/e2e/helpers/provider-seed.ts"
	webuiAuthTypeRelPath   = "webui/src/features/models/modelProviderUtils.ts"
)

var (
	e2eAuthSecretKeysBlock = regexp.MustCompile(`(?s)const AUTH_SECRET_KEYS[^=]*=\s*\{(.*?)\n\};`)
	e2eAuthSecretKeyEntry  = regexp.MustCompile(`(?m)^\s+([A-Za-z0-9_]+)\s*:`)

	webuiAuthTypeUnion   = regexp.MustCompile(`export type AuthType\s*=\s*([^;]*);`)
	webuiAuthTypeLiteral = regexp.MustCompile(`"([^"]+)"`)
)

func parseE2EAuthSecretKeys(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the e2e provider seed at %s: %v", path, err)
	}
	block := e2eAuthSecretKeysBlock.FindSubmatch(src)
	if block == nil {
		t.Fatalf("PARSE FAILURE: could not locate the AUTH_SECRET_KEYS block in %s; the declaration was renamed or reshaped, update this parser", path)
	}
	var keys []string
	for _, m := range e2eAuthSecretKeyEntry.FindAllSubmatch(block[1], -1) {
		keys = append(keys, string(m[1]))
	}
	if len(keys) == 0 {
		t.Fatalf("PARSE FAILURE: AUTH_SECRET_KEYS block in %s yielded zero keys; refusing to compare against an empty set", path)
	}
	return keys
}

func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get the package directory to find the repo root: %v", err)
	}
	return filepath.Join(pkgDir, "..", "..")
}

func TestE2EAuthSecretKeysMirrorSupportedModelAuthTypes(t *testing.T) {
	sourcetree.RequireInternal(t)

	path := filepath.Join(repoRootFromTestFile(t), filepath.FromSlash(e2eProviderSeedRelPath))
	got := parseE2EAuthSecretKeys(t, path)

	want := slices.Clone(SupportedModelAuthTypes)
	slices.Sort(want)
	got = slices.Compact(slices.Sorted(slices.Values(got)))
	if !slices.Equal(got, want) {
		t.Fatalf("%s AUTH_SECRET_KEYS keys differ from platform.SupportedModelAuthTypes\n  e2e:  %v\n  go:   %v", e2eProviderSeedRelPath, got, want)
	}
}

func parseWebuiAuthTypes(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the web UI auth type declarations at %s: %v", path, err)
	}
	union := webuiAuthTypeUnion.FindSubmatch(src)
	if union == nil {
		t.Fatalf("PARSE FAILURE: could not locate the AuthType union in %s; the declaration was renamed or reshaped, update this parser", path)
	}
	var types []string
	for _, m := range webuiAuthTypeLiteral.FindAllSubmatch(union[1], -1) {
		types = append(types, string(m[1]))
	}
	if len(types) == 0 {
		t.Fatalf("PARSE FAILURE: AuthType union in %s yielded zero members; refusing to check against an empty set", path)
	}
	return types
}

func TestWebuiAuthTypesAreSupportedModelAuthTypes(t *testing.T) {
	path := filepath.Join(repoRootFromTestFile(t), filepath.FromSlash(webuiAuthTypeRelPath))
	for _, authType := range parseWebuiAuthTypes(t, path) {
		if !slices.Contains(SupportedModelAuthTypes, authType) {
			t.Errorf("%s offers auth type %q, which is missing from platform.SupportedModelAuthTypes; register it there and in the redaction tables", webuiAuthTypeRelPath, authType)
		}
	}
}
