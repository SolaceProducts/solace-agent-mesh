// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

const webUIModelUtilsRelPath = "webui/src/features/models/modelProviderUtils.ts"

var (
	webUIRuntimePrefixesBlock = regexp.MustCompile(`(?s)const RUNTIME_MODEL_PREFIXES[^=]*=\s*new Set\(\[(.*?)\]\)`)
	webUIQuotedWord           = regexp.MustCompile(`"([^"]+)"`)
)

func webUIRuntimeModelPrefixes(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(sourcetree.Root(t), filepath.FromSlash(webUIModelUtilsRelPath))
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block := webUIRuntimePrefixesBlock.FindSubmatch(source)
	if block == nil {
		t.Fatalf("RUNTIME_MODEL_PREFIXES not found in %s", webUIModelUtilsRelPath)
	}
	var prefixes []string
	for _, word := range webUIQuotedWord.FindAllSubmatch(block[1], -1) {
		prefixes = append(prefixes, string(word[1]))
	}
	if len(prefixes) == 0 {
		t.Fatalf("no prefixes parsed from RUNTIME_MODEL_PREFIXES in %s", webUIModelUtilsRelPath)
	}
	return prefixes
}

func TestRuntimeModelPrefixesMatchTheWebUI(t *testing.T) {
	t.Parallel()
	webUI := webUIRuntimeModelPrefixes(t)
	slices.Sort(webUI)
	runtime := slices.Sorted(maps.Keys(knownProviders))
	if !slices.Equal(webUI, runtime) {
		t.Errorf("RUNTIME_MODEL_PREFIXES in %s = %v, knownProviders = %v; keep them in sync", webUIModelUtilsRelPath, webUI, runtime)
	}
}
