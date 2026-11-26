// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestManifestMerge_RewiringAComponentRetiresTheStampAndItsVersions(t *testing.T) {
	fragments := map[string]string{
		"connectors": "components:\n  - name: Comp0\n    connectors: [Ghost]\n",
		"depends_on": "components:\n  - name: Comp0\n    depends_on: [Ghost]\n",
		"skills":     "components:\n  - name: Comp0\n    skills: [Ghost]\n",
		"tools":      "components:\n  - name: Comp0\n    tools: [ghost_tool]\n",
		"nodes":      "components:\n  - name: Comp1\n    nodes:\n      - id: step1\n        type: agent\n        agent_name: Ghost\n",
		"filename":   "components:\n  - name: Comp0\n    filename: Other.yaml\n",
		"origin":     "components:\n  - name: Comp0\n    origin: platform\n",
		"a new one":  "components:\n  - name: Extra\n    type: agent\n    filename: Extra.yaml\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range fragments {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, earnedStampWithVersions+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				latest := store.latest(t)
				assertNoStampKey(t, latest)
				assertStoredVersions(t, latest, nil)
				if !strings.Contains(result.Message, stampRetiredNote) {
					t.Errorf("message = %q, want it to say the stamp is no longer true", result.Message)
				}
			})
		}
	}
}

func TestManifestMerge_AChangeThatLeavesTheWiringAloneKeepsTheStamp(t *testing.T) {
	fragments := map[string]string{
		"a status":                      "components:\n  - name: Comp0\n    status: validated\n",
		"a description":                 "components:\n  - name: Comp0\n    description: New words.\n",
		"the same connectors":           "components:\n  - name: Comp0\n    connectors: [conn_0]\n",
		"the same nodes, rewritten":     "components:\n  - name: Comp1\n    status: validated\n    nodes:\n      - id: step1\n        type: agent\n        agent_name: Comp1\n      - id: step2\n        type: agent\n        agent_name: Comp1\n        depends_on: [step1]\n",
		"a manifest field off the list": "display_name: Renamed\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range fragments {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, earnedStampWithVersions+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				latest := store.latest(t)
				if got := manifestRoot(t, latest)[fullValidationPassedKey]; got != true {
					t.Errorf("%s = %v, want it kept: nothing the checks read changed\n%s", fullValidationPassedKey, got, latest)
				}
				assertStoredVersions(t, latest, map[string]int{"Comp0.yaml": 3})
				if strings.Contains(result.Message, stampRetiredNote) {
					t.Errorf("message = %q, want no retirement note", result.Message)
				}
			})
		}
	}
}

func TestManifestMerge_AStampWrittenOverNoEarnedStampIsDroppedWhateverVersionsAreStored(t *testing.T) {
	stored := map[string]string{
		"no stamp, versions left by a rewrite": "full_validation_versions:\n  Comp0.yaml: 3\n",
		"a false stamp":                        "full_validation_passed: false\nfull_validation_versions:\n  Comp0.yaml: 3\n",
		"no stamp, no versions":                "",
	}
	for _, merger := range manifestMergers() {
		for name, prefix := range stored {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, prefix+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, "full_validation_passed: true\n")

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				latest := store.latest(t)
				if got, present := manifestRoot(t, latest)[fullValidationPassedKey]; got == true {
					t.Errorf("%s = %v (present %v), want no true an agent wrote\n%s", fullValidationPassedKey, got, present, latest)
				}
				assertStoredVersions(t, latest, nil)
				if !strings.Contains(result.Message, fullValidationPassedKey+" was not written") {
					t.Errorf("message = %q, want it to say the stamp was not written", result.Message)
				}
			})
		}
	}
}
