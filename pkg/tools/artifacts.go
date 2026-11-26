// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const createArtifactHint = " This tool only modifies an artifact that already exists; " +
	"it cannot create one. To create it, emit a " + agent.BlockPrefixSaveArtifact +
	" ... " + agent.BlockDelimiterClose + " block in your reply."

const re2SyntaxHint = " Patterns use Go RE2 syntax: no lookahead, no lookbehind and no backreferences such as \\1; use (?i) for case-insensitive and | for alternatives."

type artifactToolBase struct{ authoredSchemas }

func (artifactToolBase) Instructions() string { return "" }

func (artifactToolBase) IsLongRunning() bool { return false }

func (artifactToolBase) RawStringArgs() []string { return nil }

func (artifactToolBase) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (artifactToolBase) ContextParam() string { return "" }

type ListArtifactsTool struct {
	artifactToolBase
	tool.ReadOnlyTool
}

func (t *ListArtifactsTool) Name() string { return "list_artifacts" }

func (t *ListArtifactsTool) Description() string {
	return "Lists the session's artifacts with their versions and latest metadata."
}

func (t *ListArtifactsTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{},
	}
}

func (t *ListArtifactsTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "artifacts", typ: "array",
			description: "One entry per artifact, each carrying filename, versions and a metadata_summary. An empty array when the session holds none. The metadata_summary keys are open-ended — custom artifact metadata is copied through verbatim."},
	)
}

func (t *ListArtifactsTool) Execute(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	allFiles, err := toolCtx.Facade().ListArtifacts(ctx)
	if err != nil {
		return tool.Error("Failed to list artifacts: " + err.Error()), nil
	}

	files := allFiles[:0]
	for _, f := range allFiles {
		if !artifact.IsInternalArtifact(f) {
			files = append(files, f)
		}
	}

	if len(files) == 0 {
		return tool.OK("Found 0 artifacts.", tool.WithData(map[string]any{"artifacts": []any{}})), nil
	}

	svc := toolCtx.ArtifactService
	summaries := make([]map[string]any, 0, len(files))
	for _, filename := range files {
		entry := map[string]any{"filename": filename}
		loc := svc.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename)

		if versions, vErr := svc.Inner.ListVersions(ctx, loc); vErr == nil {
			entry["versions"] = versions
		}

		metaSummary := map[string]any{}
		companion := artifact.LoadCompanionMetadata(ctx, svc,
			toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename, nil)

		if companion != nil {
			if desc, ok := companion["description"].(string); ok && desc != "" {
				metaSummary["description"] = desc
			}
			if source, ok := companion["source"]; ok && source != nil {
				metaSummary["source"] = source
			}
			if mt, ok := companion["mime_type"].(string); ok && mt != "" {
				metaSummary["type"] = mt
			}
			if sb, ok := companion["size_bytes"].(float64); ok && sb > 0 {
				metaSummary["size"] = int64(sb)
			}
			schemaType, _ := companion["mime_type"].(string)
			if schema, ok := companion["schema"].(map[string]any); ok {
				if st, ok := schema["type"].(string); ok && st != "" {
					schemaType = st
				}
				if inferred, ok := schema["inferred"].(bool); ok {
					metaSummary["schema_inferred"] = inferred
				}
			}
			if schemaType != "" {
				metaSummary["schema_type"] = schemaType
			}
		} else {
			if meta, metaErr := svc.Inner.GetVersionMetadata(ctx, loc, nil); metaErr == nil && meta != nil {
				if meta.MIMEType != "" {
					metaSummary["type"] = meta.MIMEType
					metaSummary["schema_type"] = meta.MIMEType
				}
				if meta.SizeBytes > 0 {
					metaSummary["size"] = meta.SizeBytes
				}
				if meta.Description != "" {
					metaSummary["description"] = meta.Description
				}
				for k, v := range meta.Custom {
					metaSummary[k] = v
				}
			}
		}
		if len(metaSummary) == 0 {
			metaSummary["info"] = "No metadata file found"
		}
		entry["metadata_summary"] = metaSummary

		summaries = append(summaries, entry)
	}

	return tool.OK(
		fmt.Sprintf("Found %d artifacts.", len(files)),
		tool.WithData(map[string]any{"artifacts": summaries}),
	), nil
}

type LoadArtifactTool struct {
	artifactToolBase
	tool.ReadOnlyTool
}

func (t *LoadArtifactTool) Name() string { return "load_artifact" }

func (t *LoadArtifactTool) Description() string {
	return "Loads an artifact's content: text for text files, a summary for binary ones. Latest version unless version is set."
}

func (t *LoadArtifactTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"filename": map[string]any{
				"type":        "string",
				"description": "Artifact filename.",
			},
			"version": map[string]any{
				"type":        "integer",
				"description": "Version to load (0-indexed). Omit for the latest.",
			},
			"load_metadata_only": map[string]any{
				"type":        "boolean",
				"description": "Return only metadata (MIME type, size, schema).",
			},
			"max_content_length": map[string]any{
				"type":        "integer",
				"description": "Maximum bytes to return (100-100,000). Omitted, content over the agent's tool-result limit is cut with a note on reading the rest.",
			},
			"include_line_numbers": map[string]any{
				"type":        "boolean",
				"description": "Prefix each line with its 1-based number.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "First line to read (1-based).",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum lines to return.",
			},
		},
		Required: []string{"filename"},
	}
}

func (t *LoadArtifactTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "status", typ: "string",
			description: "Always \"success\" on this path."},
		resultField{name: "filename", typ: "string",
			description: "Name of the artifact that was loaded."},
		resultField{name: "version", typ: "integer",
			description: "The version actually resolved, which is the concrete number even when the request asked for the latest."},
		resultField{name: "mime_type", typ: "string",
			description: "MIME type of the loaded artifact. May be empty, which is treated as text."},
		resultField{name: "size_bytes", typ: "integer",
			description: "Size of the artifact content in bytes."},
		resultField{name: "content", typ: "string", optional: true,
			description: "The text content. Absent when load_metadata_only is set; on a binary artifact this is a placeholder description, not the bytes."},
		resultField{name: "message_to_llm", typ: "string", optional: true,
			description: "Guidance about truncation or version resolution. Present but empty when there is nothing to say."},
		resultField{name: "total_lines", typ: "integer", optional: true,
			description: "Total line count, carried only when offset or limit was supplied."},
		resultField{name: "description", typ: "string", optional: true,
			description: "The artifact's description, on the load_metadata_only path only, and only when one is set."},
		resultField{name: "schema", typ: "object", optional: true,
			description: "The artifact's declared schema, on the load_metadata_only path only. Open: shape is whatever the producer stored."},
		resultField{name: "metadata", typ: "object", optional: true,
			description: "Full metadata map, on the load_metadata_only path only. Open: spreads the artifact's caller-controlled custom keys."},
	)
}

func missingArtifactError(ctx context.Context, facade *tool.Facade, filename string, version *int) *tool.Result {
	if version != nil {
		if versions, err := facade.ListArtifactVersions(ctx, filename); err == nil && len(versions) > 0 {
			return tool.Error(fmt.Sprintf("Version %d of artifact '%s' does not exist. Available versions: %v.", *version, filename, versions),
				tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound))
		}
	}
	return tool.Error(fmt.Sprintf("Artifact '%s' not found. Use list_artifacts to see available files in this session.", filename),
		tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound))
}

func negativeVersionError(version int) *tool.Result {
	return invalidArgument(fmt.Sprintf("Version number must be a positive integer. Got: %d", version))
}

func (t *LoadArtifactTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	filename, _ := args["filename"].(string)
	if filename == "" {
		return invalidArgument("filename is required"), nil
	}

	var version *int
	switch v := args["version"].(type) {
	case float64:
		vi := int(v)
		if vi < 0 {
			return negativeVersionError(vi), nil
		}
		version = &vi
	case int:
		if v < 0 {
			return negativeVersionError(v), nil
		}
		vi := v
		version = &vi
	case int64:
		if v < 0 {
			return negativeVersionError(int(v)), nil
		}
		vi := int(v)
		version = &vi
	}

	filename, pinned := parseFilenameVersion(filename)
	if pinned != nil {
		if version != nil && *version != *pinned {
			return invalidArgument(fmt.Sprintf("filename '%s' pins version %d but version is %d; pass one or the other.", args["filename"], *pinned, *version)), nil
		}
		version = pinned
	}

	metadataOnly, _ := args["load_metadata_only"].(bool)
	includeLineNumbers, _ := args["include_line_numbers"].(bool)

	var lineOffset, lineLimit int
	if v, ok := args["offset"].(float64); ok && v >= 1 {
		lineOffset = int(v)
	}
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		lineLimit = int(v)
	}

	var maxContentLength int
	if v, ok := args["max_content_length"].(float64); ok {
		maxContentLength = int(v)
		if maxContentLength < 100 {
			maxContentLength = 100
		} else if maxContentLength > 100000 {
			maxContentLength = 100000
		}
	}

	facade := toolCtx.Facade()

	resolvedVersion, allVersions, versionKnown := resolveArtifactVersion(ctx, facade, filename, version)

	if metadataOnly {
		meta, err := facade.LoadArtifactMetadata(ctx, filename, version)
		if err != nil {
			return tool.Error("Failed to load metadata: " + err.Error()), nil
		}
		if meta == nil {
			return missingArtifactError(ctx, facade, filename, version), nil
		}
		data := map[string]any{
			"status":     "success",
			"filename":   filename,
			"version":    resolvedVersion,
			"mime_type":  meta.MIMEType,
			"size_bytes": meta.SizeBytes,
		}
		if meta.Description != "" {
			data["description"] = meta.Description
		}
		if meta.Schema != nil {
			data["schema"] = meta.Schema
		}
		metadataMap := map[string]any{
			"mime_type":  meta.MIMEType,
			"size_bytes": meta.SizeBytes,
		}
		if meta.Description != "" {
			metadataMap["description"] = meta.Description
		}
		if meta.Schema != nil {
			metadataMap["schema"] = meta.Schema
		}
		if meta.Custom != nil {
			for k, v := range meta.Custom {
				metadataMap[k] = v
			}
		}
		data["metadata"] = metadataMap
		return tool.OK("Artifact metadata", tool.WithData(data)), nil
	}

	content, mimeType, err := facade.LoadArtifact(ctx, filename, versionToLoad(version, resolvedVersion, versionKnown))
	if err != nil {
		return tool.Error("Failed to load artifact: " + err.Error()), nil
	}
	if content == nil {
		return missingArtifactError(ctx, facade, filename, version), nil
	}

	sizeBytes := len(content)
	contentStr := string(content)

	isText := mimeType == "" || embed.IsTextBasedMIME(mimeType)

	if !isText {
		return binaryArtifactResult(binaryArtifactInfo{
			filename:  filename,
			mimeType:  mimeType,
			version:   resolvedVersion,
			sizeBytes: sizeBytes,
			content:   content,
		}), nil
	}

	totalLines := 0
	if lineOffset > 0 || lineLimit > 0 {
		lines := strings.Split(contentStr, "\n")
		totalLines = len(lines)
		start := 0
		if lineOffset > 0 {
			start = lineOffset - 1
			if start >= len(lines) {
				start = len(lines)
			}
		}
		end := len(lines)
		if lineLimit > 0 && start+lineLimit < end {
			end = start + lineLimit
		}
		lines = lines[start:end]
		contentStr = strings.Join(lines, "\n")
	}

	truncated := false
	if maxContentLength > 0 && len(contentStr) > maxContentLength {
		contentStr = contentStr[:maxContentLength]
		truncated = true
	}

	if includeLineNumbers {
		startLine := 1
		if lineOffset > 0 {
			startLine = lineOffset
		}
		contentStr = addLineNumbersFrom(contentStr, startLine)
	}

	var messageParts []string
	if truncated {
		contentStr += "..."
		messageParts = append(messageParts, fmt.Sprintf("Content truncated at %d characters. Original size: %d bytes.", maxContentLength, sizeBytes))
	}
	if hint := versionHint(allVersions, version, resolvedVersion); hint != "" {
		messageParts = append(messageParts, hint)
	}

	data := map[string]any{
		"status":         "success",
		"filename":       filename,
		"version":        resolvedVersion,
		"mime_type":      mimeType,
		"content":        contentStr,
		"size_bytes":     sizeBytes,
		"message_to_llm": strings.Join(messageParts, " "),
	}
	if totalLines > 0 {
		data["total_lines"] = totalLines
	}
	opts := []tool.ResultOption{tool.WithData(data)}
	if versionKnown {
		opts = append(opts, tool.WithContentWindow(filename, resolvedVersion, max(lineOffset, 1)))
	}
	return tool.OK(fmt.Sprintf(loadedArtifactFormat, filename), opts...), nil
}

type DeleteArtifactTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *DeleteArtifactTool) Name() string { return "delete_artifact" }

func (t *DeleteArtifactTool) Description() string {
	return "Deletes every version of an artifact. Without confirm_delete=true it only reports what would be deleted."
}

func (t *DeleteArtifactTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"filename": map[string]any{
				"type":        "string",
				"description": "Artifact filename.",
			},
			"version": map[string]any{
				"type":        "integer",
				"description": "Not supported; setting it is an error.",
			},
			"confirm_delete": map[string]any{
				"type":        "boolean",
				"description": "Set to true to delete.",
			},
		},
		Required: []string{"filename"},
	}
}

func (t *DeleteArtifactTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "filename", typ: "string",
			description: "Name of the artifact addressed."},
		resultField{name: "versions_deleted", typ: "integer", optional: true,
			description: "How many versions were removed. Absent on the confirmation-prompt path, where nothing was deleted."},
		resultField{name: "confirmation_required", typ: "boolean", optional: true,
			description: "True when the call returned the confirmation prompt instead of deleting. Re-call with confirm_delete to proceed."},
		resultField{name: "version_count", typ: "integer", optional: true,
			description: "How many versions would be deleted. Confirmation-prompt path only."},
		resultField{name: "versions", typ: "array", optional: true,
			description: "The versions that would be deleted. Confirmation-prompt path only."},
	)
}

func (t *DeleteArtifactTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	filename, _ := args["filename"].(string)
	if filename == "" {
		return tool.Error("filename is required"), nil
	}

	svc := toolCtx.ArtifactService
	if svc == nil {
		return tool.Error("no artifact service configured"), nil
	}

	if name, pinned := parseFilenameVersion(filename); name != filename {
		what := "names a single version, and deleting a single version is not supported"
		if pinned == nil {
			what = "is a version reference, not a filename"
		}
		return tool.Error(
			fmt.Sprintf("filename '%s' %s. To delete every version, pass '%s' and set confirm_delete=True.", filename, what, name),
			tool.WithData(map[string]any{"filename": filename}),
		), nil
	}
	if _, hasVersion := args["version"]; hasVersion {
		return tool.Error(
			"Deleting a specific version is not currently supported. Only deletion of ALL versions is supported. To delete all versions, omit 'version' and set confirm_delete=True.",
			tool.WithData(map[string]any{"filename": filename}),
		), nil
	}

	loc := svc.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename)

	versions, vErr := svc.Inner.ListVersions(ctx, loc)
	if vErr != nil {
		return tool.Error("Failed to list versions: " + vErr.Error()), nil
	}

	confirmDelete, _ := args["confirm_delete"].(bool)
	if !confirmDelete {
		versionCount := len(versions)
		return tool.Partial(
			fmt.Sprintf("WARNING: This operation is irreversible and will permanently delete artifact '%s' and ALL %d version(s). To proceed, call this tool again with confirm_delete=True.", filename, versionCount),
			tool.WithData(map[string]any{
				"filename":              filename,
				"version_count":         versionCount,
				"versions":              versions,
				"confirmation_required": true,
			}),
		), nil
	}

	if err := svc.Inner.Delete(ctx, loc); err != nil {
		return tool.Error("Failed to delete artifact: " + err.Error()), nil
	}

	return tool.OK(
		fmt.Sprintf("Artifact '%s' deleted successfully.", filename),
		tool.WithData(map[string]any{
			"filename":         filename,
			"versions_deleted": len(versions),
		}),
	), nil
}

func formatVersionRange(versions []int) string {
	if len(versions) == 0 {
		return ""
	}
	if len(versions) <= 3 {
		parts := make([]string, len(versions))
		for i, v := range versions {
			parts[i] = strconv.Itoa(v)
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%d-%d", versions[0], versions[len(versions)-1])
}

type binaryArtifactInfo struct {
	filename  string
	mimeType  string
	version   int
	sizeBytes int
	content   []byte
}

const binaryExtractionAdvice = "This file's contents are not shown here. If a tool available to you is " +
	"designed to read or convert this file type, run it on this artifact by filename. Do not try to parse " +
	"the file's bytes yourself in a code sandbox; if no such tool is available, report that the file could " +
	"not be read."

const unfittableImageContent = "Image of type %s is too large to view inline and could not be reduced " +
	"to the provider's limit. Content not displayed; report that the image could not be viewed."

const loadedArtifactFormat = "Loaded artifact %s"

func binaryArtifactResult(info binaryArtifactInfo) *tool.Result {
	data := map[string]any{
		"status":     "success",
		"filename":   info.filename,
		"version":    info.version,
		"mime_type":  info.mimeType,
		"size_bytes": info.sizeBytes,
	}
	if isSupportedImageType(info.mimeType) && info.sizeBytes <= maxImageSizeBytes {
		content, mimeType, ok := visionimage.Clamp(info.content, info.mimeType)
		if !ok {
			slog.Default().With("logger_name", "load_artifact").Debug("image cannot be fitted to the provider limit",
				"filename", info.filename, "size_bytes", info.sizeBytes)
			data["content"] = fmt.Sprintf(unfittableImageContent, info.mimeType)
			return tool.OK(fmt.Sprintf(loadedArtifactFormat, info.filename), tool.WithData(data))
		}
		data["content"] = "Image loaded. The image content is included as a visual attachment in this message — you can see it directly."
		result := tool.OK(fmt.Sprintf("Loaded image artifact %s", info.filename), tool.WithData(data))
		result.ImageBlocks = []tool.ImageBlock{{MIMEType: mimeType, Data: content}}
		return result
	}
	data["content"] = fmt.Sprintf("Binary data of type %s. Content not displayed.", info.mimeType)
	data["message_to_llm"] = binaryExtractionAdvice
	return tool.OK(fmt.Sprintf(loadedArtifactFormat, info.filename), tool.WithData(data))
}

func resolveArtifactVersion(ctx context.Context, facade *tool.Facade, filename string,
	requested *int) (resolved int, all []int, known bool) {
	if requested != nil {
		return *requested, nil, true
	}
	versions, err := facade.ListArtifactVersions(ctx, filename)
	if err != nil || len(versions) == 0 {
		return 0, nil, false
	}
	return versions[len(versions)-1], versions, true
}

func versionToLoad(requested *int, resolved int, known bool) *int {
	if known {
		return &resolved
	}
	return requested
}

func versionHint(versions []int, requestedVersion *int, resolvedVersion int) string {
	if requestedVersion != nil || len(versions) <= 1 {
		return ""
	}
	return fmt.Sprintf("Loaded latest version %d. %d versions available [%s]. Specify version to load a specific one.",
		resolvedVersion, len(versions), formatVersionRange(versions))
}

func addLineNumbers(s string) string {
	return addLineNumbersFrom(s, 1)
}

func addLineNumbersFrom(s string, startLine int) string {
	lines := strings.Split(s, "\n")
	var sb strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&sb, "%d\t%s", startLine+i, line)
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func DetectMIMEType(filename string, content []byte) string {
	return artifact.DetectMIMEType(filename, content)
}

const maxImageSizeBytes = 20 * 1024 * 1024

func isSupportedImageType(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif":
		return true
	}
	return false
}
