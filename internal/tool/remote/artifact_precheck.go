// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	maxSuggestedArtifacts = 5
	minContainsMatchStem  = 3
)

const saveArtifactFirstHint = "Pass only the name of a file saved in this session, not a path or an invented name. " +
	"To create the file, emit a «««save_artifact: ... »»» block in your reply first, then call this tool again."

func (t *Tool) refuseMissingArtifacts(ctx context.Context, args map[string]any, toolCtx *tool.Context) *tool.Result {
	if toolCtx.ArtifactService == nil {
		return nil
	}
	refs := t.distinctArtifactRefs(args)
	if len(refs) == 0 {
		return nil
	}
	facade := toolCtx.Facade()
	versionsByName := make(map[string][]int, len(refs))
	for _, ref := range refs {
		if ref.badVersion {
			return tool.Error(fmt.Sprintf("Invalid version in '%s'. Use a positive version number such as 'name:2', or omit it (or use ':-1') for the latest.", ref.raw),
				tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound))
		}
		versions, seen := versionsByName[ref.filename]
		if !seen {
			var err error
			versions, err = facade.ListArtifactVersions(ctx, ref.filename)
			if err != nil {
				t.logger.Debug("artifact precheck skipped", "tool", t.cfg.ToolName, "errorType", fmt.Sprintf("%T", err))
				continue
			}
			versionsByName[ref.filename] = versions
		}
		if len(versions) == 0 {
			return missingArtifactResult(ref.filename, t.savedArtifactNames(ctx, facade))
		}
		if ref.version >= 0 && !slices.Contains(versions, ref.version) {
			return tool.Error(fmt.Sprintf("Version %d of artifact '%s' does not exist. Available versions: %v.",
				ref.version, ref.filename, versions), tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound))
		}
	}
	return nil
}

func (t *Tool) savedArtifactNames(ctx context.Context, facade *tool.Facade) []string {
	known, err := facade.ListArtifacts(ctx)
	if err != nil {
		t.logger.Debug("artifact suggestions skipped", "tool", t.cfg.ToolName, "errorType", fmt.Sprintf("%T", err))
		return nil
	}
	return known
}

type artifactRefTarget struct {
	raw        string
	filename   string
	version    int
	badVersion bool
}

func (t *Tool) distinctArtifactRefs(args map[string]any) []artifactRefTarget {
	var refs []artifactRefTarget
	for _, entry := range t.artifactRefEntries(args) {
		filename, version := parseArtifactSpec(entry.raw)
		ref := artifactRefTarget{raw: entry.raw, filename: filename, version: version, badVersion: hasInvalidVersionSuffix(entry.raw)}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

func hasInvalidVersionSuffix(spec string) bool {
	if strings.HasSuffix(spec, latestVersionSuffix) {
		return false
	}
	idx := strings.LastIndex(spec, ":")
	if idx < 0 {
		return false
	}
	v, err := strconv.Atoi(spec[idx+1:])
	return err == nil && v < 0
}

func missingArtifactResult(filename string, known []string) *tool.Result {
	message := fmt.Sprintf("Artifact '%s' is not saved in this session.", filename)
	if near := closestArtifactNames(filename, known); len(near) > 0 {
		message += " Closest saved files: " + strings.Join(near, ", ") + "."
	} else if len(known) == 0 {
		message += " No files are saved in this session yet."
	}
	return tool.Error(message+" "+saveArtifactFirstHint, tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound))
}

func closestArtifactNames(want string, known []string) []string {
	wantLower := strings.ToLower(want)
	wantExt := path.Ext(wantLower)
	type ranked struct {
		name string
		tier int
	}
	var candidates []ranked
	for _, name := range slices.Compact(slices.Sorted(slices.Values(known))) {
		lower := strings.ToLower(name)
		switch {
		case stemsOverlap(wantLower, lower):
			candidates = append(candidates, ranked{name, 0})
		case wantExt != "" && path.Ext(lower) == wantExt:
			candidates = append(candidates, ranked{name, 1})
		}
	}
	slices.SortStableFunc(candidates, func(a, b ranked) int { return a.tier - b.tier })
	names := make([]string, 0, maxSuggestedArtifacts)
	for _, c := range candidates[:min(len(candidates), maxSuggestedArtifacts)] {
		names = append(names, c.name)
	}
	return names
}

func stemsOverlap(a, b string) bool {
	stemA := strings.TrimSuffix(a, path.Ext(a))
	stemB := strings.TrimSuffix(b, path.Ext(b))
	if min(len(stemA), len(stemB)) < minContainsMatchStem {
		return stemA == stemB
	}
	return strings.Contains(stemA, stemB) || strings.Contains(stemB, stemA)
}
