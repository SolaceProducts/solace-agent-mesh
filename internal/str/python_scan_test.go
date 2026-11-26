// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"slices"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
)

func TestStrPythonFloorMatchesBuildFloor(t *testing.T) {
	if strPythonFloor != build.MinToolPythonVersion {
		t.Fatalf("strPythonFloor = %q but build.MinToolPythonVersion = %q; the interpreter the STR resolves must not sit below the floor the build guard enforces", strPythonFloor, build.MinToolPythonVersion)
	}
}

func TestPythonScanList_CoversVersionedNamesAndSearchDirs(t *testing.T) {
	candidates := pythonScanList([]string{"/opt/homebrew/bin"})

	floorMinor := 10
	for minor := floorMinor; minor <= maxProbePythonMinor; minor++ {
		name := "python3." + strconv.Itoa(minor)
		if !slices.Contains(candidates, name) {
			t.Errorf("scan list missing bare PATH candidate %q", name)
		}
		if !slices.Contains(candidates, "/opt/homebrew/bin/"+name) {
			t.Errorf("scan list missing search-dir candidate %q", "/opt/homebrew/bin/"+name)
		}
	}

	if !slices.Contains(candidates, "/opt/homebrew/bin/python3") {
		t.Errorf("scan list must include the bare python3 in each search dir to catch a newer symlinked interpreter")
	}
}

func TestPythonScanList_NoSearchDirsStillProbesPath(t *testing.T) {
	candidates := pythonScanList(nil)

	if !slices.Contains(candidates, "python3.10") {
		t.Errorf("with no search dirs, the scan list must still probe versioned names on PATH; got %v", candidates)
	}
}
