// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

//go:embed files/default_gateway.yaml
var defaultGatewayYAML []byte

//go:embed files/default_platform.yaml
var defaultPlatformYAML []byte

//go:embed files/builder_agent.yaml
var builderAgentYAML []byte

//go:embed files/awe_base.yaml
var aweBaseYAML []byte

//go:embed files/skills
var skillsFS embed.FS

func SchemaSkillsFS() fs.FS {
	sub, err := fs.Sub(skillsFS, "files/skills")
	if err != nil {
		panic(fmt.Errorf("settings: fs.Sub: %w", err))
	}
	return sub
}

func DefaultConfigYAML() map[string][]byte {
	return map[string][]byte{
		"default_gateway.yaml":  defaultGatewayYAML,
		"default_platform.yaml": defaultPlatformYAML,
	}
}

func AWEClusterBaseConfigYAML() []byte {
	return aweBaseYAML
}

func BuilderConfigYAML() map[string][]byte {
	return map[string][]byte{
		"builder_agent.yaml": builderAgentYAML,
	}
}

func AdditionalConfigYAML() map[string][]byte {
	result := make(map[string][]byte)
	maps.Copy(result, BuilderConfigYAML())
	return result
}

func writeDesignSkills(baseDir string) error {
	for _, sk := range design.Skills() {
		skillMD, err := sk.Router()
		if err != nil {
			return fmt.Errorf("read design router %q: %w", sk.Name, err)
		}
		body, err := sk.Body(design.Builder)
		if err != nil {
			return fmt.Errorf("read design guide %q: %w", sk.Name, err)
		}
		skillDir := filepath.Join(baseDir, sk.Name)
		refsDir := filepath.Join(skillDir, "references")
		if err := os.MkdirAll(refsDir, 0o755); err != nil {
			return fmt.Errorf("create design skill dir %s: %w", refsDir, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), skillMD, 0o644); err != nil {
			return fmt.Errorf("write %s/SKILL.md: %w", sk.Name, err)
		}
		if err := os.WriteFile(filepath.Join(refsDir, sk.BodyFile), body, 0o644); err != nil {
			return fmt.Errorf("write %s/references/%s: %w", sk.Name, sk.BodyFile, err)
		}
	}
	return nil
}

func writeBuiltinSkills(baseDir string) error {
	return writeSkillTreeFromFS(builtinskills.FS(), baseDir)
}

func writeSkillTreeFromFS(bfs fs.FS, baseDir string) error {
	return fs.WalkDir(bfs, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		dest := filepath.Join(baseDir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, readErr := fs.ReadFile(bfs, p)
		if readErr != nil {
			return fmt.Errorf("read builtin skill %q: %w", p, readErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return mkErr
		}
		mode := os.FileMode(0o644)
		isExe := false
		if idx := strings.Index(p, "/"); idx >= 0 && builtinskills.IsBundledExecutable(p[idx+1:]) {
			mode = 0o755
			isExe = true
		}
		if isExe {
			if _, err := os.Stat(dest); err == nil {
				return nil
			}
		}
		return os.WriteFile(dest, data, mode)
	})
}

func WriteDefaultConfigs(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}

	for name, content := range DefaultConfigYAML() {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func WriteSkills(baseDir string) error {
	inlineMap := schema.SkillSchemaInline()
	schemaContent := make(map[string]string, len(inlineMap))
	for placeholder, schemaName := range inlineMap {
		data, err := schema.SchemaContentFiltered(schemaName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skill schema %q not available for inline substitution: %v\n", schemaName, err)
			continue
		}
		schemaContent[placeholder] = string(data)
	}

	if err := fs.WalkDir(skillsFS, "files/skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel("files/skills", path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		destPath := filepath.Join(baseDir, rel)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := skillsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}

		if filepath.Base(path) == "SKILL.md" {
			content := string(data)
			for placeholder, schemaYAML := range schemaContent {
				content = strings.ReplaceAll(content, placeholder, schemaYAML)
			}
			content = strings.ReplaceAll(content, "{{TOOL_GROUPS_TABLE}}", schema.GenerateToolGroupsTable())
			data = []byte(content)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if idx := strings.Index(filepath.ToSlash(rel), "/"); idx >= 0 &&
			builtinskills.IsBundledExecutable(filepath.ToSlash(rel)[idx+1:]) {
			mode = 0o755
		}
		return os.WriteFile(destPath, data, mode)
	}); err != nil {
		return err
	}

	if err := writeBuiltinSkills(baseDir); err != nil {
		return err
	}

	if err := writeDesignSkills(baseDir); err != nil {
		return err
	}

	schemas := schema.SchemaFilesFiltered()
	mapping := schema.SkillSchemaMapping()
	for filename, refPath := range mapping {
		data, ok := schemas[filename]
		if !ok {
			continue
		}
		destPath := filepath.Join(baseDir, refPath)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("create schema dir for %s: %w", refPath, err)
		}
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("write schema %s: %w", refPath, err)
		}
	}

	return nil
}
