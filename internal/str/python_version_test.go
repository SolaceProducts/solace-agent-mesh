// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import "testing"

func TestParsePythonMajorMinor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Python 3.11.2", "3.11"},
		{"Python 3.11", "3.11"},
		{"Python 3.13.0a1", "3.13"},
		{"Python 3.9.18", "3.9"},
		{"Python 2.7.18", "2.7"},
		{"Python 3.12.1rc1", "3.12"},
		{"", ""},
		{"python 3.11", ""},
		{"CPython 3.11", ""},
		{"3.11.2", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := parsePythonMajorMinor(tc.input)
			if got != tc.want {
				t.Errorf("parsePythonMajorMinor(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestProbePythonVersion_NonexistentInterpreter(t *testing.T) {
	got := probePythonVersion("/nonexistent/python_that_does_not_exist")
	if got != "" {
		t.Errorf("expected empty string for missing interpreter, got %q", got)
	}
}
