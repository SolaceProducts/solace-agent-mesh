// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse_Minimal(t *testing.T) {
	data := []byte(`
kind: manifest
name: dev
target:
  url: http://localhost:8080
resources:
  models: [planning]
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Name != "dev" {
		t.Errorf("Name = %q; want dev", m.Name)
	}
	if m.Target.URL != "http://localhost:8080" {
		t.Errorf("URL = %q", m.Target.URL)
	}
	got := m.Resources["models"]
	if len(got) != 1 || got[0].Name != "planning" || got[0].Source != "" {
		t.Errorf("models entries = %+v", got)
	}
}

func TestParse_FullManifest(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
description: "Production deployment."
defaults:
  namespace: acme-prod
variables:
  AUTH_PROVIDER: oidc-prod
  PORT: 5432
target:
  url: https://platform.example.com
  auth:
    type: bearer_token
    envVar: SAM_PLATFORM_TOKEN
sources:
  ai-team-agents: git+https://github.com/example-org/ai-team-agents@v1.4.2
resources:
  models:
    - planning
    - general
    - research_agent@ai-team-agents
    - from: research_agent@ai-team-agents
      as: team_research_agent
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Variables["AUTH_PROVIDER"] != "oidc-prod" {
		t.Errorf("AUTH_PROVIDER = %q", m.Variables["AUTH_PROVIDER"])
	}
	if m.Variables["PORT"] != "5432" {
		t.Errorf("PORT = %q; want 5432", m.Variables["PORT"])
	}
	if m.Defaults.Namespace != "acme-prod" {
		t.Errorf("namespace = %q", m.Defaults.Namespace)
	}
	if m.Target.Auth.Type != "bearer_token" || m.Target.Auth.EnvVar != "SAM_PLATFORM_TOKEN" {
		t.Errorf("Auth = %+v", m.Target.Auth)
	}
	src, ok := m.Sources["ai-team-agents"]
	if !ok {
		t.Fatalf("Sources missing %q", "ai-team-agents")
	}
	if src.Ref != "v1.4.2" {
		t.Errorf("ai-team-agents ref = %q; want v1.4.2", src.Ref)
	}
	models := m.Resources["models"]
	if len(models) != 4 {
		t.Fatalf("models = %d entries; want 4", len(models))
	}
	if models[2].Source != "ai-team-agents" {
		t.Errorf("third entry source = %q", models[2].Source)
	}
	if models[3].Name != "team_research_agent" {
		t.Errorf("aliased name = %q", models[3].Name)
	}
}

func TestParse_UndeclaredSource(t *testing.T) {
	data := []byte(`
kind: manifest
name: dev
target: { url: http://x }
resources:
  models:
    - foo@missing
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error referencing undeclared source")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %v; want it to mention undeclared source name", err)
	}
}

func TestParse_SourcesParsed(t *testing.T) {
	data := []byte(`
kind: manifest
name: dev
target: { url: http://x }
sources:
  local: file://../local-dir
  pinned: git+https://github.com/org/repo@v1.0.0
  subdir: git+https://github.com/org/repo@v1.0.0#subdirectory=tools/
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := m.Sources["local"].Scheme; got != "file" {
		t.Errorf("local.Scheme = %q", got)
	}
	if got := m.Sources["pinned"].Ref; got != "v1.0.0" {
		t.Errorf("pinned.Ref = %q", got)
	}
	if got := m.Sources["subdir"].Subdirectory; got != "tools/" {
		t.Errorf("subdir.Subdirectory = %q", got)
	}
}

func TestParse_BadSourceURL(t *testing.T) {
	data := []byte(`
kind: manifest
name: dev
target: { url: http://x }
sources:
  bogus: not-a-url
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for unsupported scheme in source URL")
	}
}

func TestParse_UnknownKey_Warns(t *testing.T) {
	data := []byte(`
kind: manifest
name: dev
target: { url: http://x }
mystery: 42
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "mystery") {
		t.Errorf("warnings = %v; want one warning mentioning 'mystery'", m.Warnings)
	}
}

func TestParse_RejectsWrongKind(t *testing.T) {
	data := []byte(`
kind: agent
name: x
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for kind != manifest")
	}
}

func TestParse_RequiresName(t *testing.T) {
	data := []byte(`
kind: manifest
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParse_BadResourceEntry(t *testing.T) {
	data := []byte(`
kind: manifest
name: x
resources:
  models:
    - { from: a@b }
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for {from} without {as}")
	}
}

func TestParse_PlatformProfileProvider(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  profileProvider:
    toolset: bamboohr_profile_provider
    claim: email
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Platform == nil || m.Platform.ProfileProvider == nil {
		t.Fatalf("Platform.ProfileProvider nil; manifest = %+v", m)
	}
	if got := m.Platform.ProfileProvider.Toolset; got != "bamboohr_profile_provider" {
		t.Errorf("toolset = %q", got)
	}
	if got := m.Platform.ProfileProvider.Claim; got != "email" {
		t.Errorf("claim = %q", got)
	}
}

func TestParse_PlatformProfileProvider_ClaimOptional(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  profileProvider:
    toolset: bamboohr_profile_provider
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Platform.ProfileProvider.Claim != "" {
		t.Errorf("claim should default empty; got %q", m.Platform.ProfileProvider.Claim)
	}
}

func TestParse_PlatformProfileProvider_RequiresToolset(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  profileProvider:
    claim: email
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for missing toolset")
	}
	if !strings.Contains(err.Error(), "toolset") {
		t.Errorf("error = %v; want it to mention 'toolset'", err)
	}
}

func TestParse_PlatformProfileProvider_RejectsUnknownField(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  profileProvider:
    toolset: bamboohr_profile_provider
    lookupKey: email
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for unknown field lookupKey (wire name leaked into manifest)")
	}
	if !strings.Contains(err.Error(), "lookupKey") {
		t.Errorf("error = %v; want it to mention the unknown field", err)
	}
}

func TestParse_PlatformDefaultRoles(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  defaultRoles:
    roles: [sam_user, limited]
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Platform == nil || m.Platform.DefaultRoles == nil {
		t.Fatalf("Platform.DefaultRoles nil; manifest = %+v", m)
	}
	if got := m.Platform.DefaultRoles.Roles; len(got) != 2 || got[0] != "sam_user" || got[1] != "limited" {
		t.Errorf("roles = %v, want [sam_user limited]", got)
	}
}

func TestParse_PlatformDefaultRoles_EmptyListClears(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  defaultRoles:
    roles: []
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Platform == nil || m.Platform.DefaultRoles == nil {
		t.Fatalf("Platform.DefaultRoles nil; an empty roles list is a valid clear instruction")
	}
	if len(m.Platform.DefaultRoles.Roles) != 0 {
		t.Errorf("roles = %v, want empty", m.Platform.DefaultRoles.Roles)
	}
}

func TestParse_PlatformDefaultRoles_RejectsUnknownField(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  defaultRoles:
    roleIds: [x]
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for unknown field roleIds under platform.defaultRoles")
	}
	if !strings.Contains(err.Error(), "roleIds") {
		t.Errorf("error = %v; want it to mention the unknown field", err)
	}
}

func TestParse_PlatformWebuiSettings_EveryFieldParses(t *testing.T) {
	specType := reflect.TypeFor[WebuiSettingsSpec]()
	for i := range specType.NumField() {
		field := specType.Field(i)
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		literal := `"probe"`
		switch field.Type.Elem().Kind() {
		case reflect.Bool:
			literal = "true"
		case reflect.Int, reflect.Int32, reflect.Int64:
			literal = "42"
		}
		t.Run(key, func(t *testing.T) {
			data := fmt.Appendf(nil, `
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    %s: %s
`, key, literal)
			m, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if m.Platform == nil || m.Platform.WebuiSettings == nil {
				t.Fatalf("platform.webuiSettings absent after parsing %q", key)
			}
			got := reflect.ValueOf(*m.Platform.WebuiSettings).Field(i)
			if got.IsNil() {
				t.Errorf("field %s (yaml %q) stayed nil — the parser does not cover it", field.Name, key)
			}
		})
	}
}

func TestParse_PlatformWebuiSettings(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    appName: "Acme Assistant"
    welcomeMessage: "How can I assist you today?"
    disclaimerText: ""
    logoUrl: "https://cdn.acme.com/logo.svg"
    smallLogoUrl: "https://cdn.acme.com/logo-sm.svg"
    collectFeedback: true
    systemPurpose: "You are a support assistant for Acme."
    responseFormat: "Answer in Markdown. Cite sources."
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ws := m.Platform.WebuiSettings
	if ws == nil {
		t.Fatalf("Platform.WebuiSettings nil; manifest = %+v", m)
	}
	if ws.AppName == nil || *ws.AppName != "Acme Assistant" {
		t.Errorf("appName = %v, want Acme Assistant", ws.AppName)
	}
	if ws.DisclaimerText == nil || *ws.DisclaimerText != "" {
		t.Errorf("disclaimerText = %v, want explicit empty string, not nil", ws.DisclaimerText)
	}
	if ws.CollectFeedback == nil || *ws.CollectFeedback != true {
		t.Errorf("collectFeedback = %v, want true", ws.CollectFeedback)
	}
	if ws.SystemPurpose == nil || *ws.SystemPurpose != "You are a support assistant for Acme." {
		t.Errorf("systemPurpose = %v", ws.SystemPurpose)
	}
}

func TestParse_PlatformWebuiSettings_OmittedFieldIsNil(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    appName: "Acme Assistant"
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ws := m.Platform.WebuiSettings
	if ws == nil || ws.AppName == nil || *ws.AppName != "Acme Assistant" {
		t.Fatalf("appName not parsed correctly; ws = %+v", ws)
	}
	if ws.WelcomeMessage != nil || ws.DisclaimerText != nil || ws.LogoURL != nil ||
		ws.SmallLogoURL != nil || ws.SystemPurpose != nil || ws.ResponseFormat != nil || ws.CollectFeedback != nil ||
		ws.PublishFeedback != nil {
		t.Errorf("every omitted field must be nil, got %+v", ws)
	}
}

func TestParse_PlatformWebuiSettings_EmptyBlockParsesToAllNil(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings: {}
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ws := m.Platform.WebuiSettings
	if ws == nil {
		t.Fatal("Platform.WebuiSettings nil; an empty block ({}) must parse to a non-nil, all-nil-fields spec")
	}
	if ws.HasOverrides() {
		t.Errorf("expected every field nil for an empty block, got %+v", ws)
	}
}

func TestParse_PlatformWebuiSettings_ExplicitFieldNullMatchesOmission(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    appName: null
    welcomeMessage: "Hi"
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ws := m.Platform.WebuiSettings
	if ws == nil || ws.AppName != nil {
		t.Errorf("appName = %v, want nil for an explicit null value", ws.AppName)
	}
	if ws == nil || ws.WelcomeMessage == nil || *ws.WelcomeMessage != "Hi" {
		t.Errorf("welcomeMessage not parsed; ws = %+v", ws)
	}
}

func TestParse_PlatformWebuiSettings_NullBlockIsRejected(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings: null
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for a null platform.webuiSettings block")
	}
	if !strings.Contains(err.Error(), "drop the key") || !strings.Contains(err.Error(), "{}") {
		t.Errorf("error = %v; want it to name both alternatives (drop the key, or use {})", err)
	}
}

func TestParse_PlatformWebuiSettings_RejectsUnknownField(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    notASetting: "typo"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for unknown field notASetting under platform.webuiSettings")
	}
	if !strings.Contains(err.Error(), "notASetting") {
		t.Errorf("error = %v; want it to mention the unknown field", err)
	}
}

func TestParse_PlatformWebuiSettings_RejectsWrongFieldType(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  webuiSettings:
    collectFeedback: "yes"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for a non-bool collectFeedback value")
	}
	if !strings.Contains(err.Error(), "collectFeedback") {
		t.Errorf("error = %v; want it to mention the offending field", err)
	}
}

func TestParse_PlatformBlock_RejectsUnknownTopField(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
platform:
  mystery: yes
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for unknown platform.* field")
	}
}

func TestParse_NoPlatformBlock(t *testing.T) {
	data := []byte(`
kind: manifest
name: prod
target: { url: http://x }
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Platform != nil {
		t.Errorf("Platform should be nil when block absent; got %+v", m.Platform)
	}
}

func TestParse_RejectsListVar(t *testing.T) {
	data := []byte(`
kind: manifest
name: x
variables:
  PORTS: [1, 2]
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for non-scalar variable")
	}
}

func TestParse_VariableEnvExpansion(t *testing.T) {
	data := []byte(`
kind: manifest
name: x
variables:
  MODEL: ${TEST_SAM_MODEL, gpt-4o}
  REGION: ${TEST_SAM_REGION, us-east-1}
`)
	t.Run("uses env var when set", func(t *testing.T) {
		t.Setenv("TEST_SAM_MODEL", "claude-sonnet")
		m, err := Parse(data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got := m.Variables["MODEL"]; got != "claude-sonnet" {
			t.Errorf("MODEL = %q; want claude-sonnet", got)
		}
	})

	t.Run("uses default when env var unset", func(t *testing.T) {
		m, err := Parse(data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got := m.Variables["MODEL"]; got != "gpt-4o" {
			t.Errorf("MODEL = %q; want gpt-4o", got)
		}
		if got := m.Variables["REGION"]; got != "us-east-1" {
			t.Errorf("REGION = %q; want us-east-1", got)
		}
	})
}

func writeFile(t *testing.T, dir, path, content string) string {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func TestLoad_IncludeSharedBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shared/webui-settings.yaml", `appName: Shared App
collectFeedback: true
disclaimerText: >
  Multi-line text
  stays intact.
`)
	path := writeFile(t, dir, "manifests/prod.yaml", `kind: manifest
name: prod
target:
  url: http://localhost:8080
platform:
  defaultRoles:
    roles: [base-role]
  webuiSettings:
    !include ../shared/webui-settings.yaml
resources:
  models: [planning]
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Platform == nil || m.Platform.WebuiSettings == nil {
		t.Fatal("platform.webuiSettings not parsed")
	}
	ws := m.Platform.WebuiSettings
	if ws.AppName == nil || *ws.AppName != "Shared App" {
		t.Errorf("AppName = %v; want Shared App", ws.AppName)
	}
	if ws.CollectFeedback == nil || !*ws.CollectFeedback {
		t.Errorf("CollectFeedback = %v; want true", ws.CollectFeedback)
	}
	if ws.DisclaimerText == nil || *ws.DisclaimerText != "Multi-line text stays intact.\n" {
		t.Errorf("DisclaimerText = %v", ws.DisclaimerText)
	}
	if m.Platform.DefaultRoles == nil || !reflect.DeepEqual(m.Platform.DefaultRoles.Roles, []string{"base-role"}) {
		t.Errorf("DefaultRoles = %+v", m.Platform.DefaultRoles)
	}
	if got := m.Resources["models"]; len(got) != 1 || got[0].Name != "planning" {
		t.Errorf("models = %+v", got)
	}
}

func TestLoad_NoIncludes(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "manifests/dev.yaml", `kind: manifest
name: dev
target:
  url: http://localhost:8080
resources:
  models: [planning]
`)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Name != "dev" {
		t.Errorf("Name = %q; want dev", m.Name)
	}
	if m.Platform != nil {
		t.Errorf("Platform = %+v; want nil", m.Platform)
	}
}

func TestLoad_IncludeMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "manifests/dev.yaml", `kind: manifest
name: dev
target:
  url: http://localhost:8080
platform:
  webuiSettings:
    !include ../shared/nope.yaml
resources:
  models: [planning]
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing include target")
	}
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Errorf("error should name the missing file; got %v", err)
	}
}
