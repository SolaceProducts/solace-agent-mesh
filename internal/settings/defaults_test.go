// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
	"gopkg.in/yaml.v3"
)

func TestBuilderMaxTokensFallsBackOnEmptyEnv(t *testing.T) {
	raw := string(BuilderConfigYAML()["builder_agent.yaml"])

	only := func(val string, set bool) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if k == "LLM_SERVICE_MAX_TOKENS" {
				return val, set
			}
			return "", false
		}
	}

	cases := []struct {
		name   string
		lookup func(string) (string, bool)
		want   string
	}{
		{"unset", only("", false), "max_tokens: 32000"},
		{"set_but_empty_falls_back_to_floor", only("", true), "max_tokens: 32000"},
		{"set", only("64000", true), "max_tokens: 64000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expanded := config.ExpandVars(raw, tc.lookup)
			var got string
			for line := range strings.SplitSeq(expanded, "\n") {
				if strings.Contains(line, "max_tokens:") {
					got = strings.TrimSpace(line)
					break
				}
			}
			if got != tc.want {
				t.Errorf("LLM_SERVICE_MAX_TOKENS %s: got %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestBuilderCapsSelfDelegationDepth(t *testing.T) {
	raw := string(BuilderConfigYAML()["builder_agent.yaml"])

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("builder_agent.yaml is not valid YAML: %v", err)
	}
	apps, ok := doc["apps"].([]any)
	if !ok || len(apps) != 1 {
		t.Fatalf("builder_agent.yaml apps = %v, want one app", doc["apps"])
	}
	app, _ := apps[0].(map[string]any)
	appCfg, _ := app["app_config"].(map[string]any)

	if got := appCfg["max_call_depth"]; got != 1 {
		t.Errorf("Builder max_call_depth = %v, want 1 — the fork-recursion cap must stay pinned "+
			"(default 3 re-opens the self-fork explosion)", got)
	}

	if !strings.Contains(raw, "ARE YOU A SUB-TASK?") {
		t.Error("Builder instruction missing the leaf-worker prompt block — forks lose the " +
			"'do not delegate / skip discovery+manifest' guidance that reinforces the depth cap")
	}
}

func TestDefaultConfigYAML(t *testing.T) {
	configs := DefaultConfigYAML()
	if len(configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(configs))
	}

	for name, content := range configs {
		if len(content) == 0 {
			t.Errorf("config %q is empty", name)
		}
	}

	if _, ok := configs["default_agent.yaml"]; ok {
		t.Error("default_agent.yaml should no longer be bundled")
	}
	if _, ok := configs["default_gateway.yaml"]; !ok {
		t.Error("missing default_gateway.yaml")
	}
	if _, ok := configs["default_platform.yaml"]; !ok {
		t.Error("missing default_platform.yaml")
	}
}

func TestDefaultGatewayOIDCSurface(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal(defaultGatewayYAML, &doc); err != nil {
		t.Fatalf("default gateway config is not valid YAML: %v", err)
	}

	providers, ok := doc["providers"].(map[string]any)
	if !ok || len(providers) == 0 {
		t.Fatalf("default gateway config missing top-level providers catalog; got %T", doc["providers"])
	}

	raw := string(defaultGatewayYAML)
	for _, env := range []string{"OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "FRONTEND_USE_AUTHORIZATION", "AUTHORIZATION_TYPE"} {
		if !strings.Contains(raw, env) {
			t.Errorf("default gateway config missing env-var hook %q", env)
		}
	}
}

func TestDefaultPlatformRBACSurface(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal(defaultPlatformYAML, &doc); err != nil {
		t.Fatalf("default platform config is not valid YAML: %v", err)
	}

	if _, ok := doc["trust_manager"]; !ok {
		t.Error("default platform config missing trust_manager block")
	}

	apps, ok := doc["apps"].([]any)
	if !ok || len(apps) != 1 {
		t.Fatalf("default platform config apps = %v, want one app", doc["apps"])
	}
	app, _ := apps[0].(map[string]any)
	appCfg, _ := app["app_config"].(map[string]any)
	if appCfg["component_type"] != "platform" {
		t.Errorf("default platform app component_type = %v, want platform (else it won't classify as the platform service)", appCfg["component_type"])
	}
	if _, ok := appCfg["authorization_service"].(map[string]any); !ok {
		t.Error("default platform config missing authorization_service block")
	}

	raw := string(defaultPlatformYAML)
	for _, env := range []string{"AUTHORIZATION_TYPE", "ROLE_DEFINITIONS_PATH", "USER_ROLES_PATH", "TRUST_MANAGER_ENABLED", "SEED_BUILTIN_AGENTS"} {
		if !strings.Contains(raw, env) {
			t.Errorf("default platform config missing env-var hook %q", env)
		}
	}
}

func TestAWEClusterBaseConfigYAML(t *testing.T) {
	data := AWEClusterBaseConfigYAML()
	if len(data) == 0 {
		t.Fatal("AWE base config is empty")
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("AWE base config is not valid YAML: %v", err)
	}

	if _, ok := doc["broker"]; !ok {
		t.Error("AWE base config missing top-level broker block")
	}
	if _, ok := doc["trust_manager"]; !ok {
		t.Error("AWE base config missing trust_manager block")
	}
	apps, ok := doc["apps"]
	if !ok {
		t.Fatal("AWE base config missing apps key")
	}
	if list, ok := apps.([]any); !ok || len(list) != 0 {
		t.Errorf("AWE base config apps = %v, want empty list", apps)
	}
}

func TestWriteDefaultConfigs(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "configs")

	if err := WriteDefaultConfigs(configDir); err != nil {
		t.Fatal(err)
	}

	for name := range DefaultConfigYAML() {
		path := filepath.Join(configDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("config %q not found: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("config %q is empty", name)
		}
	}
}

func TestWriteSkills(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteSkills(skillsDir); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("no skill files written")
	}
}

func TestWriteSkills_EventMeshSkillsCreated(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteSkills(skillsDir); err != nil {
		t.Fatal(err)
	}

	designSkill := filepath.Join(skillsDir, "sam-event-mesh-design", "SKILL.md")
	if info, err := os.Stat(designSkill); err != nil {
		t.Errorf("event mesh design skill not found: %v", err)
	} else if info.Size() == 0 {
		t.Error("event mesh design SKILL.md is empty")
	}

	schemaSkill := filepath.Join(skillsDir, "sam-event-mesh-schema", "SKILL.md")
	if info, err := os.Stat(schemaSkill); err != nil {
		t.Errorf("event mesh schema skill not found: %v", err)
	} else if info.Size() == 0 {
		t.Error("event mesh schema SKILL.md is empty")
	}

	designRef := filepath.Join(skillsDir, "sam-event-mesh-design", "references", "event-mesh-design-guide.md")
	if info, err := os.Stat(designRef); err != nil {
		t.Errorf("event mesh design reference not found: %v", err)
	} else if info.Size() == 0 {
		t.Error("event mesh design reference is empty")
	}
}

func TestWriteSkills_PlaceholdersExpanded(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteSkills(skillsDir); err != nil {
		t.Fatal(err)
	}

	knownUnresolved := map[string]bool{
		"sam-connector-schema/SKILL.md": true,
	}

	err := filepath.Walk(skillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) != "SKILL.md" {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)

		rel, _ := filepath.Rel(skillsDir, path)
		if knownUnresolved[filepath.ToSlash(rel)] {
			return nil
		}

		if strings.Contains(content, "{{SCHEMA:") {
			t.Errorf("%s: contains unexpanded {{SCHEMA:...}} placeholder", rel)
		}

		if strings.Contains(content, "{{TOOL_GROUPS_TABLE}}") {
			t.Errorf("%s: contains unexpanded {{TOOL_GROUPS_TABLE}} placeholder", rel)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteSkills_ToolGroupsTableExpanded(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteSkills(skillsDir); err != nil {
		t.Fatal(err)
	}

	agentSkill := filepath.Join(skillsDir, "sam-agent-schema", "SKILL.md")
	data, err := os.ReadFile(agentSkill)
	if err != nil {
		t.Fatalf("read sam-agent-schema SKILL.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "| Group | Tools Included | Use When |") {
		t.Error("sam-agent-schema SKILL.md missing tool groups table header")
	}

	expectedGroups := []string{
		"artifact_management",
		"general_agent_tools",
		"image_tools",
		"web_tools",
		"data_analysis",
		"builder_tools",
		"hil_tools",
		"builtin_artifact_tools",
		"builtin_time_tools",
	}
	for _, group := range expectedGroups {
		if !strings.Contains(content, "`"+group+"`") {
			t.Errorf("sam-agent-schema SKILL.md missing group %q in tool groups table", group)
		}
	}
}

func TestWriteSkills_DesignSkillsFromSharedPackage(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := WriteSkills(skillsDir); err != nil {
		t.Fatal(err)
	}
	for _, sk := range design.Skills() {
		skillMD := filepath.Join(skillsDir, sk.Name, "SKILL.md")
		got, err := os.ReadFile(skillMD)
		if err != nil {
			t.Errorf("read %s: %v", skillMD, err)
			continue
		}
		want, err := sk.Router()
		if err != nil {
			t.Fatalf("%s.Router(): %v", sk.Name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s drift: WriteSkills wrote %d bytes, shared package has %d",
				skillMD, len(got), len(want))
		}
		if !strings.HasPrefix(string(got), "---\nname: "+sk.Name+"\n") {
			t.Errorf("%s missing/incorrect frontmatter header", skillMD)
		}
		refPath := filepath.Join(skillsDir, sk.Name, "references", sk.BodyFile)
		refGot, err := os.ReadFile(refPath)
		if err != nil {
			t.Errorf("read %s: %v", refPath, err)
			continue
		}
		refWant, err := sk.Body(design.Builder)
		if err != nil {
			t.Fatalf("%s.Body(Builder): %v", sk.Name, err)
		}
		if !bytes.Equal(refGot, refWant) {
			t.Errorf("%s drift vs shared package", refPath)
		}
	}
}

func TestWriteDesignSkills_UnwritableBaseDirErrors(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeDesignSkills(filepath.Join(blocker, "skills")); err == nil {
		t.Fatal("writeDesignSkills() succeeded under a path whose parent is a regular file")
	}
}

func TestWriteSkills_SchemasFiltered(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteSkills(skillsDir); err != nil {
		t.Fatal(err)
	}

	agentSchema := filepath.Join(skillsDir, "sam-agent-schema", "references", "agent-config-schema.yaml")
	data, err := os.ReadFile(agentSchema)
	if err != nil {
		t.Fatalf("read agent schema: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "x-sam-system") {
		t.Error("written agent schema should not contain x-sam-system annotations")
	}
	if strings.Contains(content, "session_service:") {
		t.Error("written agent schema should not contain session_service (system field)")
	}
	if strings.Contains(content, "artifact_service:") {
		t.Error("written agent schema should not contain artifact_service (system field)")
	}

	gwSchema := filepath.Join(skillsDir, "sam-gateway-schema", "references", "gateway-config-schema.yaml")
	gwData, err := os.ReadFile(gwSchema)
	if err != nil {
		t.Fatalf("read gateway schema: %v", err)
	}
	gwContent := string(gwData)

	if strings.Contains(gwContent, "fastapi_port:") {
		t.Error("written gateway schema should not contain fastapi_port (system field)")
	}
	if !strings.Contains(gwContent, "event_handlers:") {
		t.Error("written gateway schema should contain event_handlers")
	}
}

func fakeSkillFS(exeBytes []byte) fstest.MapFS {
	return fstest.MapFS{
		"myskill/SKILL.md":            {Data: []byte("# fake skill")},
		"myskill/tools/manifest.yaml": {Data: []byte("tools: []")},
		"myskill/tools/foo/foo":       {Data: exeBytes},
	}
}

func TestWriteSkillTreeFromFS_BundledExecGetsExecBit(t *testing.T) {
	dir := t.TempDir()
	if err := writeSkillTreeFromFS(fakeSkillFS([]byte("fake-binary")), dir); err != nil {
		t.Fatal(err)
	}

	exePath := filepath.Join(dir, "myskill", "tools", "foo", "foo")
	info, err := os.Stat(exePath)
	if err != nil {
		t.Fatalf("stat exe: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("bundled tool binary at %s has mode %v, want exec bit set", exePath, info.Mode())
	}

	skillPath := filepath.Join(dir, "myskill", "SKILL.md")
	skillInfo, err := os.Stat(skillPath)
	if err != nil {
		t.Fatalf("stat SKILL.md: %v", err)
	}
	if skillInfo.Mode().Perm()&0o100 != 0 {
		t.Errorf("non-binary SKILL.md unexpectedly has exec bit: %v", skillInfo.Mode())
	}
}

func TestWriteSkillTreeFromFS_PreservesExistingExec(t *testing.T) {
	dir := t.TempDir()

	preExisting := filepath.Join(dir, "myskill", "tools", "foo", "foo")
	if err := os.MkdirAll(filepath.Dir(preExisting), 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "PRE-PLACED CROSS-ARCH ELF — must not be overwritten"
	if err := os.WriteFile(preExisting, []byte(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := writeSkillTreeFromFS(fakeSkillFS([]byte("host-arch-binary")), dir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(preExisting)
	if err != nil {
		t.Fatalf("read pre-placed binary: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("pre-placed tool binary was overwritten.\n"+
			"  got bytes (%d): %q...\n"+
			"  want sentinel (%d): %q",
			len(got), string(got[:min(60, len(got))]),
			len(sentinel), sentinel)
	}

	for _, rel := range []string{"myskill/SKILL.md", "myskill/tools/manifest.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s to be written, got error: %v", rel, err)
		}
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	_ = os.Unsetenv(key)
}

func platformAuthzBlock(t *testing.T) map[string]any {
	t.Helper()
	loaded, err := (&config.YAMLLoader{}).LoadBytes(context.Background(), defaultPlatformYAML)
	if err != nil {
		t.Fatalf("load default_platform.yaml: %v", err)
	}
	apps, ok := loaded.GetRaw("apps").([]any)
	if !ok || len(apps) != 1 {
		t.Fatalf("apps = %v, want one app", loaded.GetRaw("apps"))
	}
	app, _ := apps[0].(map[string]any)
	appCfg, _ := app["app_config"].(map[string]any)
	authz, ok := appCfg["authorization_service"].(map[string]any)
	if !ok {
		t.Fatalf("authorization_service missing or wrong type: %T", appCfg["authorization_service"])
	}
	return authz
}

func TestDefaultPlatformAuthzEnvResolution(t *testing.T) {
	t.Run("unset defaults", func(t *testing.T) {
		unsetEnv(t, "AUTHORIZATION_TYPE")
		unsetEnv(t, "DEFAULT_ROLES")
		authz := platformAuthzBlock(t)
		if got := authz["type"]; got != "none" {
			t.Errorf("type = %v, want none", got)
		}
		if got, _ := authz["default_roles"].(string); strings.TrimSpace(got) != "" {
			t.Errorf("default_roles = %q, want empty", got)
		}
	})

	t.Run("default_rbac with roles", func(t *testing.T) {
		t.Setenv("AUTHORIZATION_TYPE", "default_rbac")
		t.Setenv("DEFAULT_ROLES", "sam_user,viewer")
		authz := platformAuthzBlock(t)
		if got := authz["type"]; got != "default_rbac" {
			t.Errorf("type = %v, want default_rbac", got)
		}
		if got, _ := authz["default_roles"].(string); got != "sam_user,viewer" {
			t.Errorf("default_roles = %v (%T), want \"sam_user,viewer\" scalar", authz["default_roles"], authz["default_roles"])
		}
	})
}
