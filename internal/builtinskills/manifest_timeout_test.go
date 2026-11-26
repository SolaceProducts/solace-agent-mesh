// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sdkLLMCallCapSeconds = 240

var llmCallingBundledTools = map[string][]string{
	"sam-test-harness": {"generate_test_plan", "generate_recommendations"},
}

func TestLLMCallingToolsDeclareAdequateTimeout(t *testing.T) {
	t.Parallel()

	type manifest struct {
		Tools map[string]struct {
			TimeoutSeconds int `yaml:"timeout_seconds"`
		} `yaml:"tools"`
	}

	checked := 0
	for skillDir, toolNames := range llmCallingBundledTools {
		manifestPath := path.Join(skillDir, "tools", "manifest.yaml")
		raw, err := fs.ReadFile(FS(), manifestPath)
		if err != nil {
			t.Fatalf("read %s: %v (did the skill move or get renamed?)", manifestPath, err)
		}
		var m manifest
		if err := yaml.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parse %s: %v", manifestPath, err)
		}
		for _, name := range toolNames {
			entry, ok := m.Tools[name]
			if !ok {
				t.Errorf("%s declares no tool %q", manifestPath, name)
				continue
			}
			checked++
			if entry.TimeoutSeconds <= sdkLLMCallCapSeconds {
				t.Errorf("%s: tool %q declares timeout_seconds=%d, which must exceed the %ds "+
					"in-tool LLM call cap so a slow model surfaces as a catchable tool error "+
					"rather than a sandbox kill",
					manifestPath, name, entry.TimeoutSeconds, sdkLLMCallCapSeconds)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no bundled tools were checked; the llmCallingBundledTools map is stale")
	}
}

func TestEveryBundledManifestParses(t *testing.T) {
	t.Parallel()

	found := 0
	err := fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, "tools/manifest.yaml") {
			return nil
		}
		found++
		raw, readErr := fs.ReadFile(FS(), p)
		if readErr != nil {
			t.Errorf("read %s: %v", p, readErr)
			return nil
		}
		var m struct {
			Tools map[string]any `yaml:"tools"`
		}
		if unmarshalErr := yaml.Unmarshal(raw, &m); unmarshalErr != nil {
			t.Errorf("parse %s: %v", p, unmarshalErr)
			return nil
		}
		if len(m.Tools) == 0 {
			t.Errorf("%s has no tools declared", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded skills: %v", err)
	}
	if found == 0 {
		t.Fatal("no bundled tool manifests found in the embedded FS")
	}
}
