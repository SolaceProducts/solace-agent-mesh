// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func reconcileBundledAgentSkills(ctx context.Context, store ResourceStore, namespace string, logger *slog.Logger) error {
	if store == nil {
		return errors.New("reconcileBundledAgentSkills: store is nil")
	}

	subst, err := loadSchemaSubstitutions()
	if err != nil {
		return fmt.Errorf("load schema substitutions: %w", err)
	}

	var firstErr error

	for _, sk := range design.Skills() {
		if err := reconcileOneDesignSkill(ctx, store, namespace, sk, logger); err != nil {
			logger.Error("reconcile design skill", "skill", sk.Name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	for _, name := range schemaSkillNames() {
		if err := reconcileOneSchemaSkill(ctx, store, namespace, name, subst, logger); err != nil {
			logger.Error("reconcile schema skill", "skill", name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

func schemaSkillNames() []string {
	entries, err := fs.ReadDir(settings.SchemaSkillsFS(), ".")
	if err != nil {
		panic(fmt.Errorf("read SchemaSkillsFS: %w", err))
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

func reconcileOneDesignSkill(ctx context.Context, store ResourceStore, namespace string, sk design.Skill, logger *slog.Logger) error {
	zipBytes, hashHex, err := buildDesignSkillZip(sk)
	if err != nil {
		return fmt.Errorf("build zip: %w", err)
	}
	return uploadSkillZip(ctx, store, namespace, sk.Name, zipBytes, hashHex, "design", logger)
}

func reconcileOneSchemaSkill(ctx context.Context, store ResourceStore, namespace, name string, subst map[string]string, logger *slog.Logger) error {
	zipBytes, hashHex, err := buildSchemaSkillZip(name, subst)
	if err != nil {
		return fmt.Errorf("build zip: %w", err)
	}
	return uploadSkillZip(ctx, store, namespace, name, zipBytes, hashHex, "schema", logger)
}

func loadSchemaSubstitutions() (map[string]string, error) {
	out := make(map[string]string)
	for placeholder, schemaName := range schema.SkillSchemaInline() {
		data, err := schema.SchemaContentFiltered(schemaName)
		if err != nil {
			return nil, fmt.Errorf("schema %q: %w", schemaName, err)
		}
		out[placeholder] = string(data)
	}
	out["{{TOOL_GROUPS_TABLE}}"] = schema.GenerateToolGroupsTable()
	return out, nil
}

func applySchemaSubstitutions(content []byte, subst map[string]string) []byte {
	if len(subst) == 0 {
		return content
	}
	s := string(content)
	for placeholder, replacement := range subst {
		s = strings.ReplaceAll(s, placeholder, replacement)
	}
	return []byte(s)
}

func buildDesignSkillZip(sk design.Skill) ([]byte, string, error) {
	mainMD, err := sk.Router()
	if err != nil {
		return nil, "", fmt.Errorf("read router %q: %w", sk.Name, err)
	}
	body, err := sk.Body(design.Builder)
	if err != nil {
		return nil, "", fmt.Errorf("read guide %q: %w", sk.Name, err)
	}

	return zipFromEntries([]zipEntry{
		{name: "SKILL.md", data: mainMD},
		{name: "references/", isDir: true},
		{name: "references/" + sk.BodyFile, data: body},
	})
}

func buildSchemaSkillZip(name string, subst map[string]string) ([]byte, string, error) {
	root := name
	sfs := settings.SchemaSkillsFS()
	if _, err := fs.Stat(sfs, root); err != nil {
		return nil, "", fmt.Errorf("schema skill %q not found: %w", name, err)
	}

	var entries []zipEntry
	walkErr := fs.WalkDir(sfs, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		if d.IsDir() {
			entries = append(entries, zipEntry{name: rel + "/", isDir: true})
			return nil
		}
		data, readErr := fs.ReadFile(sfs, p)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", p, readErr)
		}
		if path.Base(rel) == "SKILL.md" {
			data = applySchemaSubstitutions(data, subst)
		}
		entries = append(entries, zipEntry{name: rel, data: data})
		return nil
	})
	if walkErr != nil {
		return nil, "", walkErr
	}
	return zipFromEntries(entries)
}

type zipEntry struct {
	name  string
	isDir bool
	data  []byte
}

func zipFromEntries(entries []zipEntry) ([]byte, string, error) {
	slices.SortFunc(entries, func(a, b zipEntry) int {
		return strings.Compare(a.name, b.name)
	})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: bundledZipModTime}
		switch {
		case e.isDir:
			hdr.Method = zip.Store
			hdr.SetMode(0o755 | fs.ModeDir)
		default:
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, "", fmt.Errorf("zip create %q: %w", e.name, err)
		}
		if !e.isDir {
			if _, err := w.Write(e.data); err != nil {
				return nil, "", fmt.Errorf("zip write %q: %w", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("zip close: %w", err)
	}
	zipBytes := buf.Bytes()
	sum := sha256.Sum256(zipBytes)
	return zipBytes, hex.EncodeToString(sum[:]), nil
}

var bundledZipModTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
