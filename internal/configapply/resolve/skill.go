// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	configloader "github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
)

type ResolvedSkill struct {
	Name        string
	Description string
	SourceFile  string

	BundleDir string

	ResolvedSkillMD []byte

	Provenance *SourceProvenance
}

func ResolveSkills(rootDir string, m *manifest.Manifest) ([]ResolvedSkill, error) {
	entries, ok := m.Resources["skills"]
	if !ok || len(entries) == 0 {
		return nil, nil
	}
	for _, e := range entries {
		if e.Source != "" {
			return nil, fmt.Errorf("resources.skills: %q references source %q; package-level ResolveSkills does not support imports — construct a Resolver via resolve.New and call its Skills method instead",
				e.Name, e.Source)
		}
	}
	return New(rootDir, m, Options{}).Skills(context.Background())
}

func (r *Resolver) Skills(ctx context.Context) ([]ResolvedSkill, error) {
	entries, ok := r.manifest.Resources["skills"]
	if !ok || len(entries) == 0 {
		return nil, nil
	}

	localSkillsDir := filepath.Join(r.repoRoot, "skills")
	claims := map[string]claim{}

	out := make([]ResolvedSkill, 0, len(entries))
	var unresolved UnresolvedVarsError
	for _, e := range entries {
		fromName := e.From
		if fromName == "" {
			fromName = e.Name
		}

		var (
			skillsDir  string
			origin     claim
			provenance *SourceProvenance
		)

		if e.Source == "" {
			skillsDir = localSkillsDir
			origin = claim{path: filepath.Join(skillsDir, fromName), source: "", entryRef: fromName}

			for _, ext := range []string{".yaml", ".yml"} {
				legacy := filepath.Join(skillsDir, fromName+ext)
				if _, err := os.Stat(legacy); err == nil {
					return nil, fmt.Errorf(
						"legacy skill header at skills/%s%s is no longer supported — "+
							"delete it and put name/description in skills/%s/SKILL.md "+
							"frontmatter (agentskills.io convention)",
						fromName, ext, fromName)
				}
			}
		} else {
			fs, err := r.fetch(ctx, e.Source)
			if err != nil {
				return nil, fmt.Errorf("resources.skills: %q: %w", e.Name, err)
			}
			skillsDir = filepath.Join(fs.Result.Dir, "skills")
			ref := fromName + "@" + e.Source
			if e.Name != fromName {
				ref += " as " + e.Name
			}
			origin = claim{path: filepath.Join(skillsDir, fromName), source: e.Source, entryRef: ref}
			provenance = &SourceProvenance{
				Name:         fs.Name,
				Ref:          fs.Source.Ref,
				SHA:          fs.Result.SHA,
				Raw:          fs.Source.Raw,
				OriginalName: fromName,
			}
		}

		bundleDir := filepath.Join(skillsDir, fromName)
		st, err := os.Stat(bundleDir)
		if err != nil {
			if os.IsNotExist(err) {
				if e.Source == "" {
					return nil, fmt.Errorf("resources.skills: %q has no bundle directory at %s", fromName, bundleDir)
				}
				return nil, fmt.Errorf("resources.skills: %q not found under source %q (%s)", fromName, e.Source, bundleDir)
			}
			return nil, fmt.Errorf("stat %s: %w", bundleDir, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("resources.skills: %q expected directory at %s, found a file", fromName, bundleDir)
		}

		skillMD := filepath.Join(bundleDir, "SKILL.md")
		if _, statErr := os.Stat(skillMD); statErr != nil {
			return nil, fmt.Errorf("resources.skills: %q missing SKILL.md at %s", fromName, skillMD)
		}

		fm, resolvedMD, missing, vaultErrs, err := loadSkillFrontmatter(skillMD, r.manifest, r.vault)
		unresolved.add(skillMD, missing)
		unresolved.addVault(skillMD, vaultErrs)
		if err != nil {
			if len(missing) > 0 || len(vaultErrs) > 0 {
				return nil, &unresolved
			}
			return nil, err
		}
		if fm.Name == "" {
			return nil, fmt.Errorf("%s: SKILL.md frontmatter missing required field 'name'", skillMD)
		}
		if fm.Name != fromName {
			return nil, fmt.Errorf("%s: SKILL.md declares name %q; manifest expected %q", skillMD, fm.Name, fromName)
		}

		finalName := e.Name
		if existing, dup := claims[finalName]; dup {
			return nil, collisionError("skills", finalName, existing, origin)
		}
		claims[finalName] = origin

		out = append(out, ResolvedSkill{
			Name:            finalName,
			Description:     fm.Description,
			SourceFile:      skillMD,
			BundleDir:       bundleDir,
			ResolvedSkillMD: resolvedMD,
			Provenance:      provenance,
		})
	}
	if !unresolved.empty() {
		return nil, &unresolved
	}
	return out, nil
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func loadSkillFrontmatter(path string, m *manifest.Manifest, vault configloader.VaultFetcher) (skillFrontmatter, []byte, []string, []configloader.VaultRefError, error) {
	var fm skillFrontmatter
	raw, err := os.ReadFile(path)
	if err != nil {
		return fm, nil, nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	expanded, missing := configloader.ExpandVarsCollect(string(raw), variableLookup(m))
	expanded, vaultErrs := configloader.ResolveVaultURIs(expanded, vault)
	resolvedBytes := []byte(expanded)

	body, err := extractFrontmatter(expanded, path)
	if err != nil {
		return fm, resolvedBytes, missing, vaultErrs, err
	}
	if err := yaml.Unmarshal([]byte(body), &fm); err != nil {
		return fm, resolvedBytes, missing, vaultErrs, fmt.Errorf("%s: malformed SKILL.md frontmatter: %w", path, err)
	}
	if err := rejectIncludeTags(path, body); err != nil {
		return fm, resolvedBytes, missing, vaultErrs, err
	}
	return fm, resolvedBytes, missing, vaultErrs, nil
}

func extractFrontmatter(content, path string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return "", fmt.Errorf("%s: SKILL.md is empty (frontmatter required)", path)
	}
	if strings.TrimRight(scanner.Text(), "\r") != "---" {
		return "", fmt.Errorf("%s: SKILL.md must start with '---' frontmatter delimiter", path)
	}
	var lines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "---" {
			return strings.Join(lines, "\n"), nil
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("%s: read SKILL.md: %w", path, err)
	}
	return "", fmt.Errorf("%s: SKILL.md frontmatter has no closing '---' delimiter", path)
}
