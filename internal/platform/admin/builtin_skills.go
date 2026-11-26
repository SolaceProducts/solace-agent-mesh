// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

const builtinSkillNamePrefix = "sam-"

const builtinSkillHashMetadata = "sam-builtin-hash"

var builtinSkillResponses = []SkillDetailResponse{
	buildBuiltinSkillResponse(
		"sam-knowledge",
		"About Solace Agent Mesh",
		"Gives your agent a base understanding of Solace Agent Mesh — what it is, how it works, and common questions — useful for onboarding or support scenarios.",
		[]string{"onboarding", "knowledge"},
	),
	buildBuiltinSkillResponse(
		"sam-docs",
		"Solace Agent Mesh Documentation",
		"Gives your agent access to the official Solace Agent Mesh documentation, so it can answer questions about how the platform works. Includes: architecture, components, configuration, development, deployment, and enterprise features.",
		[]string{"documentation", "reference"},
	),
	buildBuiltinSkillResponse(
		"sam-python-executor",
		"Python Code Execution",
		"Lets your agent write and run Python code in a safe sandbox to perform calculations, analyze data, generate charts, or create files.",
		[]string{"python", "code-execution", "data-analysis", "sandbox"},
	),
	buildBuiltinSkillResponse(
		"sam-excel",
		"Excel Workbooks",
		"Allows your agent to read, create, and edit Excel files — including filling in data, applying styles, adding charts, and updating formulas.",
		[]string{"excel", "xlsx", "spreadsheet", "documents"},
	),
	buildBuiltinSkillResponse(
		"sam-docx",
		"Word Documents",
		"Allows your agent to create Word documents — headings, formatted paragraphs, bullet and numbered lists, tables, images, page breaks, and a table of contents.",
		[]string{"word", "docx", "document", "documents"},
	),
	buildBuiltinSkillResponse(
		"sam-pptx",
		"PowerPoint Decks",
		"Helps your agent build PowerPoint decks that read well: planning the story, gathering figures efficiently, choosing layouts, native charts and tables, and a visual check. The Builder attaches it to the agents it creates with Document Tools.",
		[]string{"powerpoint", "pptx", "presentation", "documents"},
	),
	buildBuiltinSkillResponse(
		"sam-templates",
		"Reusable Report Templates",
		"Author and use reusable report templates. Authoring: parameterize a finished report's session-specific bits as @@KEY@@ substitutions and data_inputs, verify it renders, and package it as a downloadable .samt artifact (verify_template_candidate, package_template). Use: inspect a template's contract and render it from session data (read_template, instantiate_template).",
		[]string{"template", "report", "authoring"},
	),
	buildBuiltinSkillResponse(
		"sam-scheduling",
		"Task Scheduling",
		"Lets your agent schedule a workflow or agent to run later — once at a set time, or repeatedly on a cron or interval — for reminders, delayed follow-ups, escalations, and recurring work.",
		[]string{"scheduling", "timers", "cron", "automation"},
	),
}

var builtinSkillFeatureFlags = map[string]string{
	"sam-python-executor": pythonCodeExecutionFeatureKey,
}

func visibleBuiltinSkills(reg *features.Registry) []SkillDetailResponse {
	out := make([]SkillDetailResponse, 0, len(builtinSkillResponses))
	for _, s := range builtinSkillResponses {
		if flag := builtinSkillFeatureFlags[s.ID]; flag != "" && (reg == nil || !reg.IsEnabled(flag)) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func buildBuiltinSkillResponse(name, label, description string, tags []string) SkillDetailResponse {
	return SkillDetailResponse{
		ID:              name,
		Name:            name,
		Label:           label,
		Description:     description,
		Tags:            tags,
		Origin:          "builtin",
		DiscoveryStatus: "ready",
		BundledTools:    []any{},
		Toolsets:        []string{},
		Agents:          []SkillAgentRef{},
	}
}

func keepSkillRef(ref string, hasM2M bool) bool {
	return !hasM2M || builtinskills.HasName(ref)
}

func paginateBuiltinSkills(responses []SkillDetailResponse, pageNumber, pageSize int) *PaginatedResponse[SkillDetailResponse] {
	total := len(responses)
	start := min((pageNumber-1)*pageSize, total)
	end := min(start+pageSize, total)
	page := make([]SkillDetailResponse, end-start)
	copy(page, responses[start:end])
	return &PaginatedResponse[SkillDetailResponse]{
		Data: page,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}
}

func reconcileBuiltinSkills(ctx context.Context, store ResourceStore, namespace string, logger *slog.Logger) error {
	if store == nil {
		return errors.New("reconcileBuiltinSkills: store is nil")
	}
	names := builtinskills.Names()
	if len(names) == 0 {
		logger.Debug("no built-in skills bundled — nothing to reconcile")
		return nil
	}

	var firstErr error
	for _, name := range names {
		if err := reconcileOneBuiltinSkill(ctx, store, namespace, name, logger); err != nil {
			logger.Error("reconcile builtin skill", "skill", name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

type runtimeSkillLister interface {
	List(ctx context.Context, prefix string) ([]string, error)
}

func bundledSkillNames() map[string]struct{} {
	keep := make(map[string]struct{})
	for _, n := range builtinskills.Names() {
		keep[n] = struct{}{}
	}
	for _, sk := range design.Skills() {
		keep[sk.Name] = struct{}{}
	}
	for _, n := range schemaSkillNames() {
		keep[n] = struct{}{}
	}
	return keep
}

func pruneOrphanedBundledSkills(ctx context.Context, store ResourceStore, namespace string, logger *slog.Logger) {
	lister, ok := store.(runtimeSkillLister)
	if !ok {
		logger.Debug("runtime store does not support listing — skipping bundled skill orphan prune")
		return
	}

	keep := bundledSkillNames()

	prefix := skillRuntimePrefix(namespace)
	keys, err := lister.List(ctx, prefix)
	if err != nil {
		logger.Warn("list runtime skills for orphan prune failed", "prefix", prefix, "error", err)
		return
	}

	for _, key := range keys {
		name, ok := skillNameFromRuntimeKey(prefix, key)
		if !ok {
			continue
		}
		if !strings.HasPrefix(name, builtinSkillNamePrefix) {
			continue
		}
		if _, kept := keep[name]; kept {
			continue
		}
		if delErr := store.Delete(ctx, key); delErr != nil {
			logger.Warn("delete orphaned bundled skill ZIP failed", "skill", name, "key", key, "error", delErr)
			continue
		}
		logger.Info("pruned orphaned bundled skill", "skill", name, "key", key)
	}
}

func reconcileOneBuiltinSkill(ctx context.Context, store ResourceStore, namespace, name string, logger *slog.Logger) error {
	zipBytes, hashHex, err := builtinskills.HashZip(name)
	if err != nil {
		return fmt.Errorf("build zip: %w", err)
	}
	return uploadSkillZip(ctx, store, namespace, name, zipBytes, hashHex, "builtin", logger)
}

func uploadSkillZip(ctx context.Context, store ResourceStore, namespace, name string, zipBytes []byte, hashHex, kind string, logger *slog.Logger) error {
	key := skillRuntimeKey(namespace, name)

	if md, headErr := store.Head(ctx, key); headErr == nil {
		if md[builtinSkillHashMetadata] == hashHex {
			logger.Debug("skill already current — skipping upload", "skill", name, "kind", kind, "key", key)
			return nil
		}
	} else if !objectstore.IsNotFound(headErr) {
		logger.Warn("skill head failed — will re-upload", "skill", name, "kind", kind, "key", key, "error", headErr)
	}

	metadata := map[string]string{builtinSkillHashMetadata: hashHex}
	if putErr := store.PutWithMetadata(ctx, key, bytes.NewReader(zipBytes), metadata); putErr != nil {
		return fmt.Errorf("upload to %q: %w", key, putErr)
	}
	logger.Debug("reconciled skill", "name", name, "kind", kind, "key", key, "size_bytes", len(zipBytes), "hash", hashHex)
	return nil
}
