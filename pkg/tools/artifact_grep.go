// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const mimeOctetStream = "application/octet-stream"

const binaryMatchLineMaxBytes = 500

type GrepTool struct {
	artifactToolBase
	tool.ReadOnlyTool
}

func (t *GrepTool) Name() string { return "artifact_grep" }

func (t *GrepTool) Description() string {
	return "Returns the numbered lines of an artifact, or of every artifact, that match a regex in Go RE2 syntax (no lookahead, lookbehind or backreferences)."
}

func (t *GrepTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex to match, in Go RE2 syntax: | means or. No lookahead, lookbehind or backreferences.",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Artifact to search; omit for all.",
			},
			"version": map[string]any{
				"type":        "integer",
				"description": "Version; omit for the latest.",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Alias for filename.",
			},
		},
		Required: []string{"pattern"},
	}
}

type grepScan struct {
	results      []string
	skipped      []skippedArtifact
	binary       []binaryArtifact
	totalMatches int

	scanned int

	aborted error
}

type skippedArtifact struct {
	filename string
	reason   string
	missing  bool
}

func unreadableScanCode(skipped []skippedArtifact, named bool) tool.ResultOption {
	if !named {
		return tool.WithErrorCode("")
	}
	for _, s := range skipped {
		if !s.missing {
			return tool.WithErrorCode("")
		}
	}
	return tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound)
}

func abortedScanCode(err error) tool.ResultOption {
	if errors.Is(err, context.DeadlineExceeded) {
		return tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassTimeout))
	}
	return tool.WithErrorCode("")
}

func skippedFilenames(skipped []skippedArtifact) []string {
	names := make([]string, 0, len(skipped))
	for _, s := range skipped {
		names = append(names, s.filename)
	}
	return names
}

func grepArtifacts(
	ctx context.Context,
	facade *tool.Facade,
	files []string,
	version *int,
	re *regexp.Regexp,
	logger *slog.Logger,
) grepScan {
	var scan grepScan
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			scan.aborted = err
			return scan
		}

		content, mimeType, loadErr := facade.LoadArtifact(ctx, f, version)
		if loadErr != nil {
			logger.WarnContext(ctx, "artifact_grep: artifact could not be read, skipping",
				"filename", f, "error", loadErr)
			scan.skipped = append(scan.skipped, skippedArtifact{filename: f, reason: loadErr.Error()})
			continue
		}
		if content == nil {
			reason := "not found"
			if version != nil {
				if versions, listErr := facade.ListArtifactVersions(ctx, f); listErr == nil && len(versions) > 0 {
					reason = fmt.Sprintf("version %d not found; available versions: %v", *version, versions)
				}
			}
			scan.skipped = append(scan.skipped, skippedArtifact{filename: f, reason: reason, missing: true})
			continue
		}

		scan.scanned++
		binaryMIME := detectBinaryMIME(content, mimeType)
		unrecognised := binaryMIME != "" && isUnrecognisedBinary(binaryMIME)
		if binaryMIME != "" && !unrecognised {
			scan.binary = append(scan.binary, binaryArtifact{filename: f, mimeType: binaryMIME})
			continue
		}
		matchesBefore := scan.totalMatches

		for i, line := range strings.Split(string(content), "\n") {
			if !re.MatchString(line) {
				continue
			}
			if unrecognised && len(line) > binaryMatchLineMaxBytes {
				line = line[:binaryMatchLineMaxBytes] + "... [line truncated: binary content]"
			}
			if len(files) > 1 {
				scan.results = append(scan.results, fmt.Sprintf("%s:%d:%s", f, i+1, line))
			} else {
				scan.results = append(scan.results, fmt.Sprintf("%d:%s", i+1, line))
			}
			scan.totalMatches++
		}

		if unrecognised && scan.totalMatches == matchesBefore {
			scan.binary = append(scan.binary, binaryArtifact{filename: f, mimeType: binaryMIME})
		}
	}
	if err := ctx.Err(); err != nil {
		scan.aborted = err
	}
	return scan
}

func isUnrecognisedBinary(mime string) bool {
	return stripMIMEParams(mime) == mimeOctetStream
}

func (t *GrepTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "matches", typ: "string", optional: true,
			description: "Matching lines joined by newlines. Absent when nothing matched."},
		resultField{name: "count", typ: "integer", optional: true,
			description: "Total number of matching lines. Absent when nothing matched."},
		resultField{name: "skipped_files", typ: "array", optional: true,
			description: "Filenames that could not be read at all, so were never searched."},
		resultField{name: "binary_files", typ: "array", optional: true,
			description: "Filenames recognised as binary and deliberately not searched."},
		resultField{name: "message_to_llm", typ: "string", optional: true,
			description: "Version hint, carried only when it is not already in the result message."},
	)
}

func (t *GrepTool) Execute(ctx context.Context, params map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return invalidArgument("pattern is required"), nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return tool.Error(fmt.Sprintf("invalid regex pattern: %s.%s", err, re2SyntaxHint), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}

	filePath, _ := params["filename"].(string)
	if filePath == "" {
		filePath, _ = params["file_path"].(string)
	}

	var version *int
	if v, ok := params["version"].(float64); ok {
		vi := int(v)
		version = &vi
	}

	if name, pinned := parseFilenameVersion(filePath); pinned != nil {
		if version != nil && *version != *pinned {
			return invalidArgument(fmt.Sprintf("filename '%s' pins version %d but version is %d; pass one or the other.", filePath, *pinned, *version)), nil
		}
		filePath, version = name, pinned
	}

	facade := toolCtx.Facade()

	var files []string
	var allVersions []int
	if filePath != "" {
		files = []string{filePath}
		if version == nil {
			if versions, verr := facade.ListArtifactVersions(ctx, filePath); verr == nil && len(versions) > 1 {
				allVersions = versions
			}
		}
	} else {
		allFiles, listErr := facade.ListArtifacts(ctx)
		if listErr != nil {
			return tool.Error("failed to list artifacts: " + listErr.Error()), nil
		}
		for _, f := range allFiles {
			if !artifact.IsInternalArtifact(f) {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			return tool.OK("No artifacts found in session."), nil
		}
	}

	logger := slog.Default().With("logger_name", "artifact_grep")
	scan, alternationNote := grepAlternationRetry(pattern, grepArtifacts(ctx, facade, files, version, re, logger), len(files),
		func(orRe *regexp.Regexp) grepScan { return grepArtifacts(ctx, facade, files, version, orRe, logger) })

	if scan.aborted != nil {
		return tool.Error(fmt.Sprintf("Search stopped before completing: %s. %d of %d artifact(s) were searched.",
			scan.aborted, scan.scanned, len(files)), abortedScanCode(scan.aborted)), nil
	}
	if len(scan.skipped) == len(files) {
		return tool.Error(fmt.Sprintf("None of the %d artifact(s) could be read, so nothing was searched. First failure: %s: %s",
			len(files), scan.skipped[0].filename, scan.skipped[0].reason), unreadableScanCode(scan.skipped, filePath != "" || version != nil)), nil
	}
	results, skippedFiles, binaryFiles := scan.results, scan.skipped, scan.binary
	totalMatches := scan.totalMatches

	binaryNote := describeBinaryArtifacts(binaryFiles)

	if totalMatches == 0 {
		if len(binaryFiles)+len(skippedFiles) == len(files) {
			msg := strings.TrimSpace(withNote(binaryNote, alternationNote))
			if len(skippedFiles) > 0 {
				unreadable := fmt.Sprintf("%d file(s) could not be read at all: %s.",
					len(skippedFiles), strings.Join(skippedFilenames(skippedFiles), ", "))
				msg = strings.TrimSpace(msg + " " + unreadable)
			}
			data := map[string]any{}
			if len(binaryFiles) > 0 {
				data["binary_files"] = binaryFilenames(binaryFiles)
			}
			if len(skippedFiles) > 0 {
				data["skipped_files"] = skippedFilenames(skippedFiles)
			}
			return tool.OK(msg, tool.WithData(data)), nil
		}
		msg := withNote(fmt.Sprintf("No matches found for pattern %q.", pattern), alternationNote)
		if binaryNote != "" {
			msg += " " + binaryNote
		}
		if len(skippedFiles) > 0 {
			msg += fmt.Sprintf(" Note: %d file(s) could not be read and were not searched: %s",
				len(skippedFiles), strings.Join(skippedFilenames(skippedFiles), ", "))
		}
		return tool.OK(msg), nil
	}

	output := strings.Join(results, "\n")
	data := map[string]any{
		"matches": output,
		"count":   totalMatches,
	}
	if len(skippedFiles) > 0 {
		data["skipped_files"] = skippedFilenames(skippedFiles)
	}
	if len(binaryFiles) > 0 {
		data["binary_files"] = binaryFilenames(binaryFiles)
	}
	latestVer := 0
	if len(allVersions) > 0 {
		latestVer = allVersions[len(allVersions)-1]
	}
	if hint := versionHint(allVersions, version, latestVer); hint != "" {
		data["message_to_llm"] = hint
	}
	msg := withNote(fmt.Sprintf("Found %d matching lines.", totalMatches), alternationNote)
	if binaryNote != "" {
		msg += " " + binaryNote
	}
	return tool.OK(msg, tool.WithData(data)), nil
}

func grepAlternationRetry(pattern string, scan grepScan, candidates int, rescan func(*regexp.Regexp) grepScan) (grepScan, string) {
	if scan.aborted != nil || scan.totalMatches > 0 || unsearched(scan) == candidates {
		return scan, ""
	}
	orRe, holds := strutil.GrepAlternation(pattern, false)
	if !holds {
		return scan, ""
	}
	if orRe != nil {
		orScan := rescan(orRe)
		if orScan.aborted != nil {
			return orScan, ""
		}
		if orScan.totalMatches > 0 {
			return orScan, strutil.GrepAlternationRetriedNote
		}
	}
	return scan, strutil.GrepLiteralPipeNote
}

func unsearched(scan grepScan) int {
	n := len(scan.skipped)
	for _, b := range scan.binary {
		if !isUnrecognisedBinary(b.mimeType) {
			n++
		}
	}
	return n
}

func withNote(msg, note string) string {
	if note == "" {
		return msg
	}
	return msg + " " + note
}

type binaryArtifact struct {
	filename string
	mimeType string
}

func binaryFilenames(bins []binaryArtifact) []string {
	names := make([]string, 0, len(bins))
	for _, b := range bins {
		names = append(names, b.filename)
	}
	return names
}

func detectBinaryMIME(content []byte, storedMIME string) string {
	raw := http.DetectContentType(content)
	sniffed := stripMIMEParams(raw)
	if strings.HasPrefix(sniffed, "text/") {
		if utf16IsUnsearchableByGrep(raw) {
			return "text/plain; charset=utf-16"
		}
		return ""
	}
	stored := stripMIMEParams(storedMIME)
	specificStoredBeatsContainer := isGenericContainerMIME(sniffed) && isSpecificMIME(stored)
	if sniffed != mimeOctetStream && !specificStoredBeatsContainer {
		return sniffed
	}
	if stored != "" && !strings.HasPrefix(stored, "text/") {
		return stored
	}
	return sniffed
}

func utf16IsUnsearchableByGrep(raw string) bool {
	return strings.Contains(strings.ToLower(raw), "charset=utf-16")
}

func isGenericContainerMIME(mimeType string) bool {
	return mimeType == "application/zip"
}

func isSpecificMIME(mimeType string) bool {
	switch mimeType {
	case "", mimeOctetStream, "application/zip", "binary/octet-stream":
		return false
	}
	return true
}

func stripMIMEParams(mimeType string) string {
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = mimeType[:idx]
	}
	return strings.ToLower(strings.TrimSpace(mimeType))
}

func describeBinaryArtifacts(bins []binaryArtifact) string {
	if len(bins) == 0 {
		return ""
	}
	named := make([]string, 0, len(bins))
	for _, b := range bins {
		named = append(named, fmt.Sprintf("%s (%s)", b.filename, b.mimeType))
	}
	subject := "is binary and cannot be searched as text"
	if len(named) > 1 {
		subject = "are binary and cannot be searched as text"
	}
	return fmt.Sprintf("%s %s. If a tool available to you is designed to read or convert "+
		"these file types, run it on the file by name. Do not try to parse the file's bytes "+
		"yourself in a code sandbox; if no such tool is available, report that the file "+
		"could not be read.", strings.Join(named, ", "), subject)
}
