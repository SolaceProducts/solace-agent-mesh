// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestPythonExecutorMirrorsReservedArtifactNames(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "python", "python_executor", "python_executor.py"))
	if err != nil {
		t.Fatalf("read python_executor.py: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")

	assertSameNames(t, "RESERVED_ARTIFACT_SUFFIXES", pythonTuple(t, src, "RESERVED_ARTIFACT_SUFFIXES"), internalArtifactSuffixes)
	assertSameNames(t, "RESERVED_ARTIFACT_NAMES", pythonTuple(t, src, "RESERVED_ARTIFACT_NAMES"), internalArtifactNames)
	assertSameNames(t, "USER_SCOPE_PREFIX", pythonStrings(t, src, `(?m)^USER_SCOPE_PREFIX = (.+)$`, "USER_SCOPE_PREFIX"), []string{userScopePrefix})

	check := pythonFunctionBody(t, src, "_store_name_problem")
	for _, use := range []string{
		"name.endswith(RESERVED_ARTIFACT_SUFFIXES)",
		"name in RESERVED_ARTIFACT_NAMES",
		"name.startswith(USER_SCOPE_PREFIX)",
	} {
		if !strings.Contains(check, use) {
			t.Errorf("_store_name_problem in python_executor.py no longer checks %q", use)
		}
	}
}

func pythonTuple(t *testing.T, src, constant string) []string {
	t.Helper()
	return pythonStrings(t, src, `(?m)^`+constant+` = \(([^)]*)\)$`, constant)
}

func pythonStrings(t *testing.T, src, pattern, constant string) []string {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(src)
	if match == nil {
		t.Fatalf("python_executor.py has no top-level %s assignment", constant)
	}
	var values []string
	for _, literal := range regexp.MustCompile(`"([^"\\]*)"|'([^'\\]*)'`).FindAllStringSubmatch(match[1], -1) {
		values = append(values, literal[1]+literal[2])
	}
	if len(values) == 0 {
		t.Fatalf("python_executor.py assigns no string literals to %s", constant)
	}
	return values
}

func pythonFunctionBody(t *testing.T, src, name string) string {
	t.Helper()
	match := regexp.MustCompile(`(?ms)^def ` + name + `\(.*?(?:\n\S|\z)`).FindString(src)
	if match == "" {
		t.Fatalf("python_executor.py has no top-level function %s", name)
	}
	return match
}

func assertSameNames(t *testing.T, constant string, python, store []string) {
	t.Helper()
	python, store = slices.Sorted(slices.Values(python)), slices.Sorted(slices.Values(store))
	if !slices.Equal(python, store) {
		t.Errorf("python_executor.py %s = %q, the artifact store reserves %q", constant, python, store)
	}
}
