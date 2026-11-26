// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandVarsStrict_SubstitutesSet(t *testing.T) {
	t.Setenv("LP_DIR", "/var/log/app")
	got, err := expandVarsStrict("file: ${LP_DIR}/app.log", lookupEnv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file: /var/log/app/app.log" {
		t.Errorf("got %q", got)
	}
}

func TestExpandVarsStrict_UsesDefault(t *testing.T) {
	got, err := expandVarsStrict("file: ${LP_UNSET, /default/path}/x.log", lookupEnv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file: /default/path/x.log" {
		t.Errorf("got %q", got)
	}
}

func TestExpandVarsStrict_MissingNoDefault(t *testing.T) {
	_, err := expandVarsStrict("file: ${LP_DEFINITELY_NOT_SET}/x.log", lookupEnv)
	if err == nil {
		t.Fatal("expected error for unset var with no default")
	}
	if !strings.Contains(err.Error(), "LP_DEFINITELY_NOT_SET") {
		t.Errorf("error should mention the variable name, got: %v", err)
	}
}

func TestExpandVarsStrict_NestedDefaultRejected(t *testing.T) {
	_, err := expandVarsStrict("file: ${OUTER, /var/log/${INNER}/sam.log}", lookupEnv)
	if err == nil {
		t.Fatal("expected error for nested ${...} in default value")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("error should mention the nested-substitution limitation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "OUTER") {
		t.Errorf("error should name the offending variable, got: %v", err)
	}
}

func TestExpandVarsStrict_SetWinsOverDefault(t *testing.T) {
	t.Setenv("LP_WIN", "real")
	got, err := expandVarsStrict("${LP_WIN, fallback}", lookupEnv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "real" {
		t.Errorf("got %q", got)
	}
}

func TestExpandVarsStrict_NoVariables(t *testing.T) {
	got, err := expandVarsStrict("plain text, no vars", lookupEnv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "plain text, no vars" {
		t.Errorf("got %q", got)
	}
}

func writeExternalFile(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadExternalLogConfig_Valid(t *testing.T) {
	path := writeExternalFile(t, "log.yaml", `
log:
  format: json
  stdout_log_level: INFO
  log_file: /tmp/external.log
  max_size_mb: 50
`)
	cfg, err := LoadExternalLogConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	section := cfg.GetSection("log")
	if section == nil {
		t.Fatal("expected 'log' section in returned config")
	}
	if got := section.GetString("format"); got != "json" {
		t.Errorf("format = %q, want 'json'", got)
	}
	if got := section.GetString("log_file"); got != "/tmp/external.log" {
		t.Errorf("log_file = %q", got)
	}
	if got := section.GetInt("max_size_mb"); got != 50 {
		t.Errorf("max_size_mb = %d", got)
	}
}

func TestLoadExternalLogConfig_RelativePath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := writeExternalFile(t, "rel.yaml", "log:\n  format: text\n")
	rel, err := filepath.Rel(wd, path)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	cfg, err := LoadExternalLogConfig(rel)
	if err != nil {
		t.Fatalf("relative load: %v", err)
	}
	if cfg.GetSection("log") == nil {
		t.Fatal("expected log section")
	}
}

func TestLoadExternalLogConfig_MissingFile(t *testing.T) {
	_, err := LoadExternalLogConfig("/tmp/definitely-not-a-real-file-xyz.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "LOGGING_CONFIG_PATH") && !strings.Contains(err.Error(), "not-a-real-file") {
		t.Errorf("error should identify the problem, got: %v", err)
	}
}

func TestLoadExternalLogConfig_InvalidYAML(t *testing.T) {
	path := writeExternalFile(t, "bad.yaml", "log: [not: a: map:")
	_, err := LoadExternalLogConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadExternalLogConfig_MissingLogKeyErrorHintsPythonMigration(t *testing.T) {
	path := writeExternalFile(t, "nolog.yaml", "something_else:\n  format: json\n")
	_, err := LoadExternalLogConfig(path)
	if err == nil {
		t.Fatal("expected error for missing 'log' key")
	}
	if !strings.Contains(err.Error(), "log") {
		t.Errorf("error should mention missing 'log' key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Python") {
		t.Errorf("error should hint at the Python-schema migration, got: %v", err)
	}
}

func TestLoadExternalLogConfig_EnvSubstitutionApplied(t *testing.T) {
	t.Setenv("EXT_LOG_FILE", "/subst/sam.log")
	path := writeExternalFile(t, "subst.yaml", `
log:
  log_file: ${EXT_LOG_FILE}
  max_size_mb: ${EXT_MAX_MB, 25}
`)
	cfg, err := LoadExternalLogConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	section := cfg.GetSection("log")
	if got := section.GetString("log_file"); got != "/subst/sam.log" {
		t.Errorf("log_file after subst = %q", got)
	}
	if got := section.GetInt("max_size_mb"); got != 25 {
		t.Errorf("max_size_mb (default-resolved) = %d", got)
	}
}

func TestLoadExternalLogConfig_UnsetVarNoDefaultFails(t *testing.T) {
	path := writeExternalFile(t, "unset.yaml", `
log:
  log_file: ${EXT_UNDEFINED_XYZ}
`)
	_, err := LoadExternalLogConfig(path)
	if err == nil {
		t.Fatal("expected error for unset var with no default")
	}
	if !strings.Contains(err.Error(), "EXT_UNDEFINED_XYZ") {
		t.Errorf("error should mention the variable, got: %v", err)
	}
}

func TestLoadExternalLogConfig_LogKeyNull(t *testing.T) {
	path := writeExternalFile(t, "null.yaml", "log:\n")
	_, err := LoadExternalLogConfig(path)
	if err == nil {
		t.Fatal("expected error for null 'log' value")
	}
	if strings.Contains(err.Error(), "missing a top-level") {
		t.Errorf("error should NOT claim the key is missing when it is present with null; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not a YAML mapping") {
		t.Errorf("error should identify the type mismatch, got: %v", err)
	}
}

func TestLoadExternalLogConfig_LogKeyWrongType(t *testing.T) {
	path := writeExternalFile(t, "list.yaml", "log: []\n")
	_, err := LoadExternalLogConfig(path)
	if err == nil {
		t.Fatal("expected error for list-valued 'log' key")
	}
	if strings.Contains(err.Error(), "missing a top-level") {
		t.Errorf("error should NOT claim the key is missing when it is present with a list; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not a YAML mapping") {
		t.Errorf("error should identify the type mismatch, got: %v", err)
	}
}
