// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import "testing"

func mapProbe(versions map[string]string) func(string) string {
	return func(interp string) string {
		return versions[interp]
	}
}

func fixedScan(names ...string) func() []string {
	return func() []string { return names }
}

func TestSelectPythonInterpreter_AmbientAtFloorWins(t *testing.T) {
	probe := mapProbe(map[string]string{
		"python3":    "3.11",
		"python3.13": "3.13",
	})

	path, version := selectPythonInterpreter("", "python3", fixedScan("python3.13"), "3.10", probe)

	if path != "python3" || version != "3.11" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (python3, 3.11): ambient python3 at/above the floor must win over a higher scan candidate", path, version)
	}
}

func TestSelectPythonInterpreter_BelowFloorPicksScanCandidate(t *testing.T) {
	probe := mapProbe(map[string]string{
		"python3":                      "3.9",
		"/opt/homebrew/bin/python3.11": "3.11",
	})

	path, version := selectPythonInterpreter("", "python3", fixedScan("/opt/homebrew/bin/python3.11"), "3.10", probe)

	if path != "/opt/homebrew/bin/python3.11" || version != "3.11" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (/opt/homebrew/bin/python3.11, 3.11): a below-floor ambient must be replaced by a scan candidate at/above the floor", path, version)
	}
}

func TestSelectPythonInterpreter_PicksFirstAtOrAboveFloorAndStops(t *testing.T) {
	probed := map[string]bool{}
	probe := func(interp string) string {
		probed[interp] = true
		return map[string]string{"python3": "3.9", "python3.13": "3.13", "python3.12": "3.12"}[interp]
	}

	path, version := selectPythonInterpreter("", "python3", fixedScan("python3.13", "python3.12"), "3.10", probe)

	if path != "python3.13" || version != "3.13" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (python3.13, 3.13): the first at-or-above-floor candidate wins", path, version)
	}
	if probed["python3.12"] {
		t.Error("selector kept probing after the first at-or-above-floor hit; that risks a probe storm on a degraded host with hanging interpreters")
	}
}

func TestSelectPythonInterpreter_OverrideAtFloorWinsOverEverything(t *testing.T) {
	probe := mapProbe(map[string]string{
		"/opt/py310/bin/python3": "3.10",
		"python3":                "3.11",
		"python3.13":             "3.13",
	})

	path, version := selectPythonInterpreter("/opt/py310/bin/python3", "python3", fixedScan("python3.13"), "3.10", probe)

	if path != "/opt/py310/bin/python3" || version != "3.10" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (/opt/py310/bin/python3, 3.10): an at-or-above-floor override must win over ambient and scan candidates", path, version)
	}
}

func TestSelectPythonInterpreter_BelowFloorOverrideIgnored(t *testing.T) {
	probe := mapProbe(map[string]string{
		"/opt/old/python3": "3.9",
		"python3":          "3.9",
		"python3.12":       "3.12",
	})

	path, version := selectPythonInterpreter("/opt/old/python3", "python3", fixedScan("python3.12"), "3.10", probe)

	if path != "python3.12" || version != "3.12" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (python3.12, 3.12): a below-floor override must be ignored, not used as a last resort", path, version)
	}
}

func TestSelectPythonInterpreter_NothingAtFloorFallsBackToAmbient(t *testing.T) {
	probe := mapProbe(map[string]string{
		"python3":    "3.9",
		"python3.12": "3.9",
	})

	path, version := selectPythonInterpreter("", "python3", fixedScan("python3.12"), "3.10", probe)

	if path != "python3" || version != "3.9" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (python3, 3.9): with nothing at/above the floor, fall back to bare python3 so the preflight guard still surfaces the clear error", path, version)
	}
}

func TestSelectPythonInterpreter_NoInterpreterAnywhere(t *testing.T) {
	probe := mapProbe(map[string]string{})

	path, version := selectPythonInterpreter("", "python3", fixedScan(), "3.10", probe)

	if path != "python3" || version != "" {
		t.Fatalf("selectPythonInterpreter() = (%q, %q); want (python3, \"\"): with no interpreter anywhere, fall back to bare python3 and an empty version", path, version)
	}
}

func TestSelectPythonInterpreter_ScanNotBuiltWhenAmbientOrOverrideSatisfiesFloor(t *testing.T) {
	probe := mapProbe(map[string]string{
		"python3":           "3.11",
		"/opt/py311/python": "3.11",
	})
	explode := func() []string {
		t.Fatal("scan thunk must not be evaluated when an earlier tier already satisfies the floor (on Windows it would exec `py --list-paths`)")
		return nil
	}

	selectPythonInterpreter("", "python3", explode, "3.10", probe)
	selectPythonInterpreter("/opt/py311/python", "python3", explode, "3.10", probe)
}

func TestResolvePython_HonorsSAMSTRPythonOverride(t *testing.T) {
	t.Setenv(strPythonOverrideEnv, "/opt/custom/python3")
	plan := func() (string, func() []string) {
		return "python3", func() []string {
			t.Error("scan must not run when the SAM_STR_PYTHON override already satisfies the floor")
			return nil
		}
	}
	probe := mapProbe(map[string]string{"/opt/custom/python3": "3.12", "python3": "3.9"})

	path, version := resolvePython(plan, probe)

	if path != "/opt/custom/python3" || version != "3.12" {
		t.Fatalf("resolvePython() = (%q, %q); want (/opt/custom/python3, 3.12): SAM_STR_PYTHON must flow through as the override", path, version)
	}
}

func TestSTRAdvertisedVersionMatchesRerouteInterpreter(t *testing.T) {
	path, version := resolveSTRPython()

	if strPythonVersion() != version {
		t.Errorf("strPythonVersion() = %q but the resolver reports %q; the advertised version must be the resolved interpreter's version", strPythonVersion(), version)
	}
	if containerPython() != path {
		t.Errorf("containerPython() = %q but the resolver reports %q; the console-script reroute must use the resolved interpreter", containerPython(), path)
	}
	if got := probePythonVersion(path); got != version {
		t.Errorf("resolved interpreter %q actually reports %q, but the STR advertises %q; a deploy would build for the wrong Python", path, got, version)
	}
}
