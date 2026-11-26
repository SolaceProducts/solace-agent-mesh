// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
)

const skillMDFilename = "SKILL.md"

func pullSkills(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	skills, err := c.ListSkills(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skipping skills: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, s := range skills {
		if opts.Name != "" && s.Name != opts.Name {
			continue
		}
		safe := safeFileName(s.Name)
		if safe == "" {
			res.drop("skill %q: name is not a safe file name", s.Name)
			continue
		}

		hasLocalCopy := localSkillDirExists(opts.OutputDir, safe)
		stub := fileEntry{
			relPath: filepath.Join("skills", safe, skillMDFilename),
			content: []byte(synthesizeSkillMD(s.Name, valueOrEmpty(s.Description))),
		}

		zipBytes, downloadErr := c.DownloadSkill(ctx, s.ID)
		if downloadErr != nil {
			names = append(names, s.Name)
			switch {
			case isRedactionRefusal(downloadErr) && hasLocalCopy:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"skill %q: platform refused to export SKILL.md (frontmatter it cannot redact safely, e.g. YAML anchors); left the local copy unchanged", s.Name))
				res.preservedDirs = append(res.preservedDirs, filepath.Join("skills", safe))
			case isRedactionRefusal(downloadErr):
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"skill %q: platform refused to export SKILL.md (frontmatter it cannot redact safely, e.g. YAML anchors); %s", s.Name, noLocalCopyAdvice))
			case hasLocalCopy:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"skill %q: bundle download failed (%s); left the local copy unchanged", s.Name, describeDownloadError(downloadErr)))
				res.preservedDirs = append(res.preservedDirs, filepath.Join("skills", safe))
			case isNoBundle(downloadErr):
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("skill %q: no uploaded bundle (%s); wrote synthesized SKILL.md only", s.Name, downloadErr))
				entries = append(entries, stub)
			default:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"skill %q: bundle download failed (%s); %s", s.Name, describeDownloadError(downloadErr), noLocalCopyAdvice))
			}
			continue
		}
		bundleEntries, unpackErr := unpackSkillZIP(safe, zipBytes)
		if unpackErr != nil {
			names = append(names, s.Name)
			if hasLocalCopy {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"skill %q: bundle unpack failed (%s); left the local copy unchanged", s.Name, unpackErr))
				res.preservedDirs = append(res.preservedDirs, filepath.Join("skills", safe))
				continue
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"skill %q: bundle unpack failed (%s); %s", s.Name, unpackErr, noLocalCopyAdvice))
			continue
		}

		bundleEntries = ensureSkillMDFrontmatter(bundleEntries, safe, s.Name, valueOrEmpty(s.Description))
		bundleEntries = placeholderSkillSecrets(bundleEntries, safe, s.Name, res)
		entries = append(entries, bundleEntries...)
		names = append(names, s.Name)
	}
	return entries, names, nil
}

const noLocalCopyAdvice = "no local copy exists, so plan/apply will fail until you add skills/<name>/ or remove it from the manifest (removing it lets --prune delete the skill)"

func isNoBundle(err error) bool {
	httpErr, ok := errors.AsType[*reconcile.HTTPError](err)
	return ok && httpErr.StatusCode == http.StatusConflict &&
		strings.HasPrefix(serverMessage(httpErr.Body), entplatform.SkillNoBundleMessage)
}

func localSkillDirExists(outputDir, safeName string) bool {
	if outputDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(outputDir, "skills", safeName))
	return err == nil && info.IsDir()
}

func isRedactionRefusal(err error) bool {
	httpErr, ok := errors.AsType[*reconcile.HTTPError](err)
	return ok && httpErr.StatusCode == http.StatusUnprocessableEntity &&
		serverMessage(httpErr.Body) == skillsecret.RedactionRefusedMessage
}

func describeDownloadError(err error) string {
	if httpErr, ok := errors.AsType[*reconcile.HTTPError](err); ok {
		if msg := serverMessage(httpErr.Body); msg != "" {
			return fmt.Sprintf("status %d: %s", httpErr.StatusCode, msg)
		}
	}
	return err.Error()
}

func serverMessage(body string) string {
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &payload) != nil {
		return ""
	}
	return payload.Message
}

func placeholderSkillSecrets(entries []fileEntry, safeName, name string, res *Result) []fileEntry {
	root := filepath.Join("skills", safeName)
	used := map[string]int{}
	for i, e := range entries {
		if !strings.EqualFold(filepath.Base(e.relPath), skillMDFilename) {
			continue
		}
		out, subs, err := skillsecret.PlaceholdersShared(e.content, name, used)
		inSkill, _ := filepath.Rel(root, e.relPath)
		inSkill = filepath.ToSlash(inSkill)
		if err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skill %q: could not replace redacted values in %s with placeholders (%s); kept as downloaded", name, inSkill, err))
			continue
		}
		entries[i].content = out
		for _, sub := range subs {
			fieldPath := sub.FieldPath
			if inSkill != skillMDFilename {
				fieldPath = inSkill + ":" + fieldPath
			}
			res.SecretPlaceholders = append(res.SecretPlaceholders, SecretWarning{
				Kind:         "skill",
				ResourceName: name,
				FieldPath:    fieldPath,
				Placeholder:  sub.Placeholder,
			})
		}
	}
	return entries
}

func ensureSkillMDFrontmatter(entries []fileEntry, safeName, name, description string) []fileEntry {
	root := filepath.Join("skills", safeName)
	for i, e := range entries {
		if filepath.Dir(e.relPath) != root || !strings.EqualFold(filepath.Base(e.relPath), skillMDFilename) {
			continue
		}
		entries[i].relPath = filepath.Join(root, skillMDFilename)
		if hasYAMLFrontmatter(e.content) {
			return entries
		}
		entries[i].content = []byte(skillMDFrontmatter(name, description) + string(e.content))
		return entries
	}
	entries = append(entries, fileEntry{
		relPath: filepath.Join(root, skillMDFilename),
		content: []byte(synthesizeSkillMD(name, description)),
	})
	return entries
}

func hasYAMLFrontmatter(content []byte) bool {
	trimmed := bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return false
	}
	rest := trimmed[3:]
	if len(rest) == 0 {
		return false
	}
	return rest[0] == '\n' || rest[0] == '\r'
}

func skillMDFrontmatter(name, description string) string {
	var b strings.Builder
	b.WriteString("---\nname: ")
	b.WriteString(name)
	b.WriteString("\n")
	if description != "" {
		b.WriteString("description: ")
		b.WriteString(yamlScalar(description))
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	return b.String()
}

func synthesizeSkillMD(name, description string) string {
	return skillMDFrontmatter(name, description) + "# " + name + "\n"
}

func yamlScalar(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, "\n\r\t\"'`") || strings.Contains(value, ": ") {
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		escaped = strings.ReplaceAll(escaped, "\n", `\n`)
		escaped = strings.ReplaceAll(escaped, "\r", `\r`)
		escaped = strings.ReplaceAll(escaped, "\t", `\t`)
		return `"` + escaped + `"`
	}
	switch value[0] {
	case '{', '[', '&', '*', '!', '|', '>', '%', '@', '#', '-', '?', ':':
		return `"` + value + `"`
	}
	return value
}

func unpackSkillZIP(safeName string, zipBytes []byte) ([]fileEntry, error) {
	if len(zipBytes) == 0 {
		return nil, fmt.Errorf("empty zip")
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("read zip: %w", err)
	}

	root := filepath.Join("skills", safeName)
	var out []fileEntry
	seenByFoldedPath := map[string]string{}
	for _, f := range zr.File {
		name := f.Name
		if name == "" {
			continue
		}
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("unsafe path in zip: %s", name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		rel := name
		if idx := strings.IndexByte(rel, '/'); idx >= 0 {
			top := rel[:idx]
			if top == safeName {
				rel = rel[idx+1:]
			}
		}
		if rel == "" {
			continue
		}
		if prev, dup := seenByFoldedPath[strings.ToLower(rel)]; dup {
			return nil, fmt.Errorf("%s and %s map to the same file", prev, rel)
		}
		seenByFoldedPath[strings.ToLower(rel)] = rel
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		body, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", name, readErr)
		}
		out = append(out, fileEntry{
			relPath: filepath.Join(root, filepath.FromSlash(rel)),
			content: body,
		})
	}
	return out, nil
}
