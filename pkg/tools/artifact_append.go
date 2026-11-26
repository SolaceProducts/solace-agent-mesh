// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type AppendToArtifactTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

var appendLocks = artifact.NewLockPool()

func (t *AppendToArtifactTool) Name() string { return "append_to_artifact" }

func (t *AppendToArtifactTool) Description() string {
	return "Appends up to ~3 KB to an artifact and saves it as a new version, keeping its MIME type. " +
		"If the artifact does not exist yet it is created from the chunk as its first version, using mime_type, so you can build a large file in chunks starting with this tool."
}

func (t *AppendToArtifactTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"filename": map[string]any{
				"type":        "string",
				"description": "Artifact filename, optionally 'name:version'.",
			},
			"content_chunk": map[string]any{
				"type":        "string",
				"description": "Content to append; base64 for binary types.",
			},
			"mime_type": map[string]any{
				"type":        "string",
				"description": "MIME type of the chunk; decides whether it is base64-decoded.",
			},
		},
		Required: []string{"filename", "content_chunk", "mime_type"},
	}
}

func (t *AppendToArtifactTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "filename", typ: "string",
			description: "Name of the artifact the chunk was appended to."},
		resultField{name: "new_version", typ: "integer",
			description: "Version created by the append."},
		resultField{name: "appended_from_version", typ: "integer", optional: true,
			description: "Version whose content the chunk was appended to. Absent when it could not be determined."},
		resultField{name: "total_size_bytes", typ: "integer",
			description: "Size of the artifact after appending."},
	)
}

func (t *AppendToArtifactTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	filename, _ := args["filename"].(string)
	contentChunk, _ := args["content_chunk"].(string)
	mimeType, _ := args["mime_type"].(string)

	if filename == "" {
		return tool.Error("filename is required"), nil
	}
	if contentChunk == "" {
		return tool.Error("content_chunk is required"), nil
	}
	if mimeType == "" {
		return tool.Error("mime_type is required"), nil
	}

	svc := toolCtx.ArtifactService
	if svc == nil {
		return tool.Error("no artifact service configured"), nil
	}

	filename, pinned := parseFilenameVersion(filename)
	loc := svc.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename)

	lockKey := loc.AppName + "/" + loc.UserID + "/" + loc.SessionID + "/" + loc.Filename
	appendLocks.Lock(lockKey)
	defer appendLocks.Unlock(lockKey)

	originalContent, originalMIME, err := svc.Inner.Load(ctx, loc, pinned)
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to load original artifact content to append to: %v", err), tool.WithData(map[string]any{"filename": filename})), nil
	}
	var versions []int
	var listErr error
	if originalContent == nil {
		versions, listErr = svc.Inner.ListVersions(ctx, loc)
	}
	if originalContent == nil && pinned != nil {
		if listErr != nil {
			return tool.Error(fmt.Sprintf("Version %d of artifact '%s' could not be loaded, and its versions could not be listed: %v", *pinned, filename, listErr), tool.WithData(map[string]any{"filename": filename})), nil
		}
		if len(versions) == 0 {
			return tool.Error(fmt.Sprintf("Version %d of artifact '%s' does not exist and no versions exist yet. Omit the version to create the artifact from this chunk.", *pinned, filename), tool.WithData(map[string]any{"filename": filename}),
				tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound)), nil
		}
		if !slices.Contains(versions, *pinned) {
			return tool.Error(fmt.Sprintf("Version %d of artifact '%s' does not exist. Available versions: %v.", *pinned, filename, versions), tool.WithData(map[string]any{"filename": filename}),
				tool.WithErrorCode(taxonomy.ErrorCodeArtifactNotFound)), nil
		}
	}
	created := originalContent == nil && len(versions) == 0
	var existingNames []string
	if created {
		originalMIME = mimeType
		existingNames = existingArtifactNames(ctx, toolCtx)
	}

	originalMeta, _ := svc.Inner.GetVersionMetadata(ctx, loc, pinned)

	var originalVersion int
	originalVersionKnown := true
	if pinned != nil {
		originalVersion = *pinned
	} else if versions, listErr := svc.Inner.ListVersions(ctx, loc); listErr == nil && len(versions) > 0 {
		originalVersion = versions[len(versions)-1]
	} else {
		originalVersionKnown = false
	}

	chunkBytes := decodeContentBytes(contentChunk, mimeType)

	combined := make([]byte, len(originalContent)+len(chunkBytes))
	copy(combined, originalContent)
	copy(combined[len(originalContent):], chunkBytes)

	custom := make(map[string]any)
	if originalMeta != nil {
		for k, v := range originalMeta.Custom {
			custom[k] = v
		}
	}
	if originalVersionKnown {
		custom["appended_from_version"] = originalVersion
	} else {
		delete(custom, "appended_from_version")
	}
	custom["appended_chunk_declared_mime_type"] = mimeType

	description := ""
	if originalMeta != nil {
		description = originalMeta.Description
	}

	saveResult, err := artifact.SaveWithMetadata(ctx, svc,
		toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename,
		combined, originalMIME,
		artifact.MetadataOpts{
			Description: description,
			Custom:      custom,
		}, slog.Default())
	if err != nil {
		return tool.Error("Failed to save appended artifact: "+err.Error(), tool.WithData(map[string]any{"filename": filename})), nil
	}
	newVersion := saveResult.DataVersion

	data := map[string]any{
		"filename":         filename,
		"new_version":      newVersion,
		"total_size_bytes": len(combined),
	}
	if originalVersionKnown {
		data["appended_from_version"] = originalVersion
	}
	if created {
		if toolCtx.NotifyArtifactSaved != nil {
			toolCtx.NotifyArtifactSaved(ctx, tool.ArtifactSavedInfo{
				Filename:    filename,
				Version:     newVersion,
				MIMEType:    mimeType,
				SizeBytes:   int64(len(combined)),
				Description: description,
				ToolCallID:  toolCtx.ToolCallID,
			})
		}
		return tool.OK(
			createdMessage(filename, len(combined), existingNames),
			tool.WithData(data),
		), nil
	}
	return tool.OK(
		fmt.Sprintf("Chunk appended to '%s'. New version is %d with total size %d bytes.", filename, newVersion, len(combined)),
		tool.WithData(data),
	), nil
}

func decodeContentBytes(content, mimeType string) []byte {
	if embed.IsTextBasedMIME(mimeType) {
		return []byte(content)
	}
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return []byte(content)
	}
	return decoded
}

const maxListedExistingNames = 10

func existingArtifactNames(ctx context.Context, toolCtx *tool.Context) []string {
	all, err := toolCtx.Facade().ListArtifacts(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, maxListedExistingNames)
	for _, name := range all {
		if artifact.IsInternalArtifact(name) {
			continue
		}
		if len(names) == maxListedExistingNames {
			break
		}
		names = append(names, name)
	}
	return names
}

func createdMessage(filename string, size int, existingNames []string) string {
	msg := fmt.Sprintf("Artifact '%s' did not exist, so it was created as its first version with this chunk (%d bytes). Append further chunks to the same filename.", filename, size)
	if len(existingNames) > 0 {
		msg += fmt.Sprintf(" If you meant to add to an existing file, the name may be wrong; files already in this session: %s.", strings.Join(existingNames, ", "))
	}
	return msg
}
