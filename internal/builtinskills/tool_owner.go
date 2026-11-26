// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"io/fs"
	"maps"
	"path"
	"slices"
	"sync"

	"gopkg.in/yaml.v3"
)

const toolManifestPath = "tools/manifest.yaml"

type toolManifest struct {
	Tools map[string]yaml.Node `yaml:"tools"`
}

var internalSkills = map[string]bool{"sam-build-apply": true, "sam-test-harness": true}

var toolOwners = sync.OnceValue(func() map[string]string {
	owners := map[string]string{}
	for _, skill := range Names() {
		if internalSkills[skill] {
			continue
		}
		data, err := fs.ReadFile(FS(), path.Join(skill, toolManifestPath))
		if err != nil {
			continue
		}
		var manifest toolManifest
		if yaml.Unmarshal(data, &manifest) != nil {
			continue
		}
		for tool := range manifest.Tools {
			if _, claimed := owners[tool]; !claimed {
				owners[tool] = skill
			}
		}
	}
	return owners
})

func ToolOwningSkills() []string {
	seen := map[string]bool{}
	for _, skill := range toolOwners() {
		seen[skill] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

func SkillProvidingTool(tool string) (string, bool) {
	skill, ok := toolOwners()[tool]
	return skill, ok
}

func BundledToolNames(skill string) []string {
	data, err := fs.ReadFile(FS(), path.Join(skill, toolManifestPath))
	if err != nil {
		return nil
	}
	var manifest toolManifest
	if yaml.Unmarshal(data, &manifest) != nil || len(manifest.Tools) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(manifest.Tools))
}
