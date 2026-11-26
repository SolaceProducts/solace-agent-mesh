// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"maps"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func manifestRoot(t *testing.T, manifestYAML string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal([]byte(manifestYAML), &root); err != nil {
		t.Fatalf("manifest does not parse: %v\n%s", err, manifestYAML)
	}
	return root
}

func assertNoStampKey(t *testing.T, manifestYAML string) {
	t.Helper()
	if value, present := manifestRoot(t, manifestYAML)[fullValidationPassedKey]; present {
		t.Errorf("%s = %v, want the key absent: only validate_build_manifest sets it\n%s", fullValidationPassedKey, value, manifestYAML)
	}
}

func TestManifestMerge_ANullStampIsRemovedNotWritten(t *testing.T) {
	cases := map[string]struct {
		stored   string
		fragment string
	}{
		"a null stamp over no stamp":      {"", "full_validation_passed: null\n"},
		"a null stamp over a true stamp":  {"full_validation_passed: true\n", "full_validation_passed: null\n"},
		"a null stamp beside a status":    {"", "full_validation_passed: null\ncomponents:\n  - name: Comp1\n    status: validated\n"},
		"an empty stamp value":            {"", "full_validation_passed:\n"},
		"a tilde stamp value":             {"", "full_validation_passed: ~\n"},
		"a null stamp over a false stamp": {"full_validation_passed: false\n", "full_validation_passed: null\n"},
	}
	for _, merger := range manifestMergers() {
		for name, tc := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, tc.stored+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, tc.fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				assertNoStampKey(t, store.latest(t))
			})
		}
	}
}

func TestManifestMerge_RetiringAnEarnedStampIsReported(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, "full_validation_passed: true\n"+fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "full_validation_passed: null\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			assertNoStampKey(t, store.latest(t))
			if !strings.Contains(result.Message, stampRetiredNote) {
				t.Errorf("message = %q, want it to say the stamp is no longer true", result.Message)
			}
		})
	}
}

const stampRetiredNote = fullValidationPassedKey + " is no longer true"

func TestManifestMerge_OnlyAnEarnedStampBeingRetiredIsReported(t *testing.T) {
	cases := map[string]struct {
		stored   string
		fragment string
	}{
		"no stamp stored, a null written":     {"", "full_validation_passed: null\n"},
		"no stamp stored, an unrelated merge": {"", "display_name: X\n"},
		"a null stored, an unrelated merge":   {"full_validation_passed: null\n", "display_name: X\n"},
		"a false stored, a null written":      {"full_validation_passed: false\n", "full_validation_passed: null\n"},
		"a true stored, an unrelated merge":   {"full_validation_passed: true\n", "display_name: X\n"},
	}
	for _, merger := range manifestMergers() {
		for name, tc := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, tc.stored+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, tc.fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				if strings.Contains(result.Message, stampRetiredNote) {
					t.Errorf("message = %q, want no note: no earned stamp was retired", result.Message)
				}
			})
		}
	}
}

func TestManifestMerge_AnEarnedStampTurnedFalseIsReported(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, "full_validation_passed: true\n"+fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "full_validation_passed: false\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			if !strings.Contains(result.Message, stampRetiredNote) {
				t.Errorf("message = %q, want it to say the stamp is no longer true", result.Message)
			}
		})
	}
}

func TestManifestMerge_AStoredNullStampIsRepairedByTheNextMerge(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, "full_validation_passed: null\n"+fixtureManifest(numberedSpecs(3)))
			before := decodeManifestComponents(t, store.latest(t))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Comp1\n    status: validated\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			latest := store.latest(t)
			assertNoStampKey(t, latest)
			assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, latest), map[string]string{"Comp1": "validated"})
		})
	}
}

func TestManifestMerge_AMergeThatLeavesTheStampAloneKeepsIt(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, "full_validation_passed: true\n"+fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "display_name: Renamed\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			if got := manifestRoot(t, store.latest(t))[fullValidationPassedKey]; got != true {
				t.Errorf("%s = %v, want the existing stamp kept: only a null is removed", fullValidationPassedKey, got)
			}
		})
	}
}

func TestManifestMerge_NullUIOwnedComponentFieldsAreNotWritten(t *testing.T) {
	cases := map[string]string{
		"a null id":                        "id: null",
		"a null deployed_version":          "deployed_version: null",
		"a null id and deployed_version":   "id: null\n    deployed_version: null",
		"an empty deployed_version":        "deployed_version:",
		"a null id in a new-looking entry": "id: ~",
	}
	for _, merger := range manifestMergers() {
		for name, field := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))
				before := decodeManifestComponents(t, store.latest(t))

				result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Comp1\n    status: validated\n    "+field+"\n")

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, store.latest(t)), map[string]string{"Comp1": "validated"})
			})
		}
	}
}

const earnedStampWithVersions = "full_validation_passed: true\nfull_validation_versions:\n  Comp0.yaml: 3\n"

func assertStoredVersions(t *testing.T, manifestYAML string, want map[string]int) {
	t.Helper()
	if got := storedVersions(t, manifestYAML); !maps.Equal(got, want) {
		t.Errorf("%s = %v, want %v\n%s", fullValidationVersionsKey, got, want, manifestYAML)
	}
}

func TestManifestMerge_RetiringTheStampDropsTheRecordedVersions(t *testing.T) {
	fragments := map[string]string{
		"a null stamp":       "full_validation_passed: null\n",
		"a false stamp":      "full_validation_passed: false\n",
		"a null versions":    "full_validation_versions: null\nfull_validation_passed: null\n",
		"a stamp and a null": "full_validation_passed: null\nfull_validation_versions: {Comp0.yaml: 3}\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range fragments {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, earnedStampWithVersions+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				assertStoredVersions(t, store.latest(t), nil)
			})
		}
	}
}

func TestManifestMerge_AnAgentCannotForgeTheRecordedVersions(t *testing.T) {
	fragments := map[string]string{
		"overwrite the map":   "full_validation_versions: {Comp0.yaml: 99}\n",
		"add a filename":      "full_validation_versions: {Comp1.yaml: 1}\n",
		"delete the map":      "full_validation_versions: null\n",
		"overwrite and stamp": "full_validation_passed: true\nfull_validation_versions: {Comp0.yaml: 99}\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range fragments {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, earnedStampWithVersions+fixtureManifest(numberedSpecs(3)))

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				assertStoredVersions(t, store.latest(t), map[string]int{"Comp0.yaml": 3})
			})
		}
	}
}

func TestManifestMerge_VersionsCannotBeWrittenWithoutAnEarnedStamp(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "full_validation_passed: true\nfull_validation_versions: {Comp0.yaml: 1}\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			assertStoredVersions(t, store.latest(t), nil)
		})
	}
}

func TestManifestMerge_AnUnrelatedMergeKeepsTheRecordedVersions(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, earnedStampWithVersions+fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Comp1\n    status: validated\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			assertStoredVersions(t, store.latest(t), map[string]int{"Comp0.yaml": 3})
		})
	}
}
