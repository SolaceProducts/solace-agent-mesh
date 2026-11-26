// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"slices"
	"testing"
)

func mockLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestExpandVars(t *testing.T) {
	env := map[string]string{
		"HOST":       "localhost",
		"PORT":       "8080",
		"EMPTY_VAR":  "",
		"WITH_SPACE": "hello world",
		"BOOL_TRUE":  "true",
		"BOOL_FALSE": "false",
		"SPECIAL":    "a=b&c=d",
		"HYPHEN-VAR": "hyphen-val",
		"DOT.VAR":    "dot-val",
	}
	lookup := mockLookup(env)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple var",
			input: "${HOST}",
			want:  "localhost",
		},
		{
			name:  "var in context",
			input: "http://${HOST}:${PORT}/api",
			want:  "http://localhost:8080/api",
		},
		{
			name:  "no vars",
			input: "plain text without variables",
			want:  "plain text without variables",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},

		{
			name:  "default with space after comma",
			input: "${MISSING, fallback}",
			want:  "fallback",
		},
		{
			name:  "default without space (Python compat: comma absorbed into var name)",
			input: "${MISSING,fallback}",
			want:  "",
		},
		{
			name:  "default with spaces around comma",
			input: "${MISSING , fallback}",
			want:  "fallback",
		},
		{
			name:  "default with multiple words",
			input: "${MISSING, hello world}",
			want:  "hello world",
		},
		{
			name:  "default not used when var exists",
			input: "${HOST, other}",
			want:  "localhost",
		},
		{
			name:  "missing var no default gives empty",
			input: "${MISSING}",
			want:  "",
		},
		{
			name:  "default with special chars",
			input: "${MISSING, %(levelname)s|%(message)s}",
			want:  "%(levelname)s|%(message)s",
		},

		{
			name:  "empty var returns empty not default",
			input: "${EMPTY_VAR, fallback}",
			want:  "",
		},
		{
			name:  "empty var without default",
			input: "${EMPTY_VAR}",
			want:  "",
		},

		{
			name:  "underscore in var name",
			input: "${WITH_SPACE}",
			want:  "hello world",
		},
		{
			name:  "hyphen in var name",
			input: "${HYPHEN-VAR}",
			want:  "hyphen-val",
		},
		{
			name:  "dot in var name",
			input: "${DOT.VAR}",
			want:  "dot-val",
		},

		{
			name:  "bool true as string",
			input: "${BOOL_TRUE}",
			want:  "true",
		},
		{
			name:  "bool default true",
			input: "${MISSING, true}",
			want:  "true",
		},
		{
			name:  "bool default false",
			input: "${MISSING, false}",
			want:  "false",
		},

		{
			name:  "unclosed brace",
			input: "${HOST",
			want:  "${HOST",
		},
		{
			name:  "no opening brace",
			input: "$HOST}",
			want:  "$HOST}",
		},
		{
			name:  "space in var name",
			input: "${MY VAR}",
			want:  "${MY VAR}",
		},
		{
			name:  "colon in var name",
			input: "${PATH:0}",
			want:  "${PATH:0}",
		},
		{
			name:  "double dollar",
			input: "$${HOST}",
			want:  "$localhost",
		},
		{
			name:  "empty braces",
			input: "${}",
			want:  "${}",
		},

		{
			name:  "multiple vars",
			input: "${HOST}:${PORT}",
			want:  "localhost:8080",
		},
		{
			name:  "mixed existing and missing",
			input: "${HOST}:${MISSING, 9090}",
			want:  "localhost:9090",
		},

		{
			name:  "yaml key value",
			input: "host: ${HOST}",
			want:  "host: localhost",
		},
		{
			name:  "yaml with default",
			input: "log_level: ${LOG_LEVEL, INFO}",
			want:  "log_level: INFO",
		},
		{
			name:  "yaml multiline",
			input: "host: ${HOST}\nport: ${PORT}\nmode: ${MODE, production}",
			want:  "host: localhost\nport: 8080\nmode: production",
		},

		{
			name:  "value with special chars",
			input: "${SPECIAL}",
			want:  "a=b&c=d",
		},
		{
			name:  "adjacent to other text",
			input: "prefix${HOST}suffix",
			want:  "prefixlocalhostsuffix",
		},

		{
			name:  "outer set — inner in default never evaluated",
			input: "${HOST, ${PORT}/fallback}",
			want:  "localhost",
		},
		{
			name:  "outer missing — inner in default is expanded",
			input: "${MISSING, sqlite:///${HOST}/db}",
			want:  "sqlite:///localhost/db",
		},
		{
			name:  "outer missing — inner also missing gives empty inner",
			input: "${MISSING, prefix/${ALSO_MISSING}/suffix}",
			want:  "prefix//suffix",
		},
		{
			name:  "real-world sqlite default with SAM_DATA_DIR",
			input: "${MISSING_DB_URL, sqlite:///${HOST}/sam.db}",
			want:  "sqlite:///localhost/sam.db",
		},
		{
			name:  "inner default has its own default (depth-1 only — inner default not re-expanded)",
			input: "${MISSING, prefix-${MISSING2}-suffix}",
			want:  "prefix--suffix",
		},

		{
			name:  ":- unset uses default",
			input: "${MISSING:-fallback}",
			want:  "fallback",
		},
		{
			name:  ":- set non-empty returns value",
			input: "${HOST:-fallback}",
			want:  "localhost",
		},
		{
			name:  ":- set empty uses default (differs from comma form)",
			input: "${EMPTY_VAR:-fallback}",
			want:  "fallback",
		},
		{
			name:  ":- comma form on same input returns empty",
			input: "${EMPTY_VAR, fallback}",
			want:  "",
		},
		{
			name:  ":- default with multiple words",
			input: "${MISSING:-hello world}",
			want:  "hello world",
		},
		{
			name:  ":- nested var in default expanded when unset",
			input: "${MISSING:-sqlite:///${HOST}/db}",
			want:  "sqlite:///localhost/db",
		},
		{
			name:  ":- nested var in default expanded when empty",
			input: "${EMPTY_VAR:-sqlite:///${HOST}/db}",
			want:  "sqlite:///localhost/db",
		},
		{
			name:  ":- no whitespace tolerated around operator",
			input: "${MISSING :- fallback}",
			want:  "${MISSING :- fallback}",
		},

		{
			name:  ":+ unset returns empty",
			input: "${MISSING:+alt}",
			want:  "",
		},
		{
			name:  ":+ set empty returns empty",
			input: "${EMPTY_VAR:+alt}",
			want:  "",
		},
		{
			name:  ":+ set non-empty returns alt",
			input: "${HOST:+alt}",
			want:  "alt",
		},
		{
			name:  ":+ alt is expanded when var set non-empty",
			input: "${HOST:+prefix-${PORT}-suffix}",
			want:  "prefix-8080-suffix",
		},
		{
			name:  ":+ alt not expanded when var unset",
			input: "${MISSING:+prefix-${PORT}-suffix}",
			want:  "",
		},

		{
			name:  "mixed comma :- :+ operators",
			input: "${HOST}|${MISSING, c}|${EMPTY_VAR:-d}|${HOST:+e}",
			want:  "localhost|c|d|e",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandVars(tt.input, lookup)
			if got != tt.want {
				t.Errorf("ExpandVars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandVarsEmptyEnv(t *testing.T) {
	lookup := mockLookup(map[string]string{})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"all defaults", "${A, x}${B, y}", "xy"},
		{"all empty", "${A}${B}", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandVars(tt.input, lookup)
			if got != tt.want {
				t.Errorf("ExpandVars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandVarsCollect(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		input       string
		wantText    string
		wantMissing []string
	}{
		{
			name:        "no missing",
			env:         map[string]string{"HOST": "x"},
			input:       "${HOST}",
			wantText:    "x",
			wantMissing: nil,
		},
		{
			name:        "one missing bare",
			env:         map[string]string{},
			input:       "host=${HOST}",
			wantText:    "host=",
			wantMissing: []string{"HOST"},
		},
		{
			name:        "many missing sorted unique",
			env:         map[string]string{"B": "b"},
			input:       "${C}-${A}-${B}-${A}-${D}",
			wantText:    "--b--",
			wantMissing: []string{"A", "C", "D"},
		},
		{
			name:        "comma default opts out of missing",
			env:         map[string]string{},
			input:       "${A, fallback}-${B}",
			wantText:    "fallback-",
			wantMissing: []string{"B"},
		},
		{
			name:        "colon-dash default opts out",
			env:         map[string]string{"B": ""},
			input:       "${A:-x}-${B:-y}-${C}",
			wantText:    "x-y-",
			wantMissing: []string{"C"},
		},
		{
			name:        "colon-plus does not collect",
			env:         map[string]string{"A": "1"},
			input:       "${A:+yes}-${B:+yes}-${C}",
			wantText:    "yes--",
			wantMissing: []string{"C"},
		},
		{
			name:        "empty-but-set value is not missing",
			env:         map[string]string{"EMPTY": ""},
			input:       "${EMPTY}-${OTHER}",
			wantText:    "-",
			wantMissing: []string{"OTHER"},
		},
		{
			name:        "no substitutions",
			env:         map[string]string{},
			input:       "plain text",
			wantText:    "plain text",
			wantMissing: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, missing := ExpandVarsCollect(tt.input, mockLookup(tt.env))
			if got != tt.wantText {
				t.Errorf("text = %q, want %q", got, tt.wantText)
			}
			if !slices.Equal(missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
			}
		})
	}
}
