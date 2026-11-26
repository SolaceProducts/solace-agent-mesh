// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

const spillReason = "max_result_size_exceeded"

const mimeTypeJSON = "application/json"

const truncationMarker = "... [truncated by STR: artifact save failed]"

func spillOversizedResult(
	ctx context.Context,
	result map[string]any,
	artifactSvc *artifact.ScopedService,
	agentName, userID, sessionID, toolName string,
	maxBytes int,
	logger *slog.Logger,
) {
	if result == nil || maxBytes <= 0 || artifactSvc == nil {
		return
	}

	spillDataMap(ctx, result, artifactSvc, agentName, userID, sessionID, toolName, maxBytes, logger)
	spillDataObjects(ctx, result, artifactSvc, agentName, userID, sessionID, toolName, maxBytes, logger)
}

func spillDataMap(
	ctx context.Context,
	result map[string]any,
	artifactSvc *artifact.ScopedService,
	agentName, userID, sessionID, toolName string,
	maxBytes int,
	logger *slog.Logger,
) {
	raw, ok := result["data"]
	if !ok || raw == nil {
		return
	}
	data, ok := raw.(map[string]any)
	if !ok || len(data) == 0 {
		return
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		logger.Warn("spill: failed to marshal data map; leaving in place", "tool", toolName, "error", err)
		return
	}
	if len(encoded) <= maxBytes {
		return
	}

	filename := spillFilename(toolName, encoded, ".json")
	custom := map[string]any{
		"source_tool":         toolName,
		"original_size_bytes": len(encoded),
		"reason":              spillReason,
		"spilled_by_str":      true,
	}
	saveRes, saveErr := artifact.SaveWithMetadata(ctx, artifactSvc,
		agentName, userID, sessionID, filename,
		encoded, mimeTypeJSON,
		artifact.MetadataOpts{
			Description: "Auto-spilled tool result (data exceeded max return size)",
			Custom:      custom,
			Tags:        []string{a2a.ArtifactTagWorking},
		}, logger)
	if saveErr != nil {
		logger.Error("spill: failed to save data map; truncating in place",
			"tool", toolName, "size_bytes", len(encoded), "error", saveErr)
		truncated := string(truncateForFallback(encoded, maxBytes))
		result["data"] = map[string]any{"truncated_result": truncated}
		appendMessage(result, fmt.Sprintf(
			"Max result size exceeded but artifact save failed; data truncated to %d bytes.", maxBytes))
		return
	}

	delete(result, "data")
	stub := map[string]any{
		"name":        filename,
		"mime_type":   mimeTypeJSON,
		"disposition": "artifact",
		"description": "Auto-spilled tool result data map.",
		"content":     nil,
		"is_binary":   false,
		"artifact_ref": map[string]any{
			"filename":   filename,
			"version":    saveRes.DataVersion,
			"size_bytes": len(encoded),
		},
		"metadata": custom,
	}
	objs, _ := result["data_objects"].([]any)
	result["data_objects"] = append(objs, stub)
	appendMessage(result, fmt.Sprintf(
		"Max result size exceeded. Result stored to JSON artifact %q version %d (%d bytes). "+
			"It is single-line JSON, so anything that returns it whole exceeds the limit "+
			"again: project only the fields you need with query_data_with_sql / "+
			"transform_data_with_jmespath, and pass max_content_length if you read it "+
			"with load_artifact.",
		filename, saveRes.DataVersion, len(encoded)))
	logger.Info("spill: saved tool result data map to artifact",
		"tool", toolName, "artifact", filename, "version", saveRes.DataVersion,
		"size_bytes", len(encoded), "kind", "data")
}

func spillDataObjects(
	ctx context.Context,
	result map[string]any,
	artifactSvc *artifact.ScopedService,
	agentName, userID, sessionID, toolName string,
	maxBytes int,
	logger *slog.Logger,
) {
	objs, ok := result["data_objects"].([]any)
	if !ok || len(objs) == 0 {
		return
	}
	for i, item := range objs {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, has := obj["artifact_ref"]; has {
			continue
		}
		contentRaw, ok := obj["content"]
		if !ok || contentRaw == nil {
			continue
		}
		contentStr, ok := contentRaw.(string)
		if !ok || contentStr == "" {
			continue
		}
		isBinary, _ := obj["is_binary"].(bool)
		if estimatedContentSize(contentStr, isBinary) <= maxBytes {
			continue
		}
		var raw []byte
		if isBinary {
			decoded, err := base64.StdEncoding.DecodeString(contentStr)
			if err != nil {
				logger.Warn("spill: failed to decode binary content; leaving in place",
					"tool", toolName, "index", i, "error", err)
				continue
			}
			raw = decoded
		} else {
			raw = []byte(contentStr)
		}
		if len(raw) <= maxBytes {
			continue
		}

		declaredName, _ := obj["name"].(string)
		mime, _ := obj["mime_type"].(string)
		if mime == "" {
			if isBinary {
				mime = "application/octet-stream"
			} else {
				mime = "text/plain"
			}
		}
		filename := sanitizeFilenameForSpill(declaredName)
		if filename == "" {
			filename = spillFilename(toolName, raw, extForMIME(mime))
		}

		mergedMeta := map[string]any{}
		if existing, ok := obj["metadata"].(map[string]any); ok {
			for k, v := range existing {
				mergedMeta[k] = v
			}
		}
		mergedMeta["source_tool"] = toolName
		mergedMeta["original_size_bytes"] = len(raw)
		mergedMeta["reason"] = spillReason
		mergedMeta["spilled_by_str"] = true

		description, _ := obj["description"].(string)
		saveRes, saveErr := artifact.SaveWithMetadata(ctx, artifactSvc,
			agentName, userID, sessionID, filename,
			raw, mime,
			artifact.MetadataOpts{
				Description: description,
				Custom:      mergedMeta,
				Tags:        []string{a2a.ArtifactTagWorking},
			}, logger)
		if saveErr != nil {
			logger.Error("spill: failed to save data_object; truncating in place",
				"tool", toolName, "name", declaredName, "size_bytes", len(raw), "error", saveErr)
			if isBinary {
				obj["content"] = fmt.Sprintf("[binary content of %d bytes dropped: artifact save failed]", len(raw))
				obj["is_binary"] = false
			} else {
				obj["content"] = string(truncateForFallback(raw, maxBytes))
			}
			appendMessage(result, fmt.Sprintf(
				"DataObject %q (%d bytes) exceeded cap but artifact save failed; truncated.",
				declaredName, len(raw)))
			continue
		}

		obj["content"] = nil
		obj["is_binary"] = false
		obj["disposition"] = "artifact"
		obj["mime_type"] = mime
		if filename != declaredName {
			obj["name"] = filename
		}
		obj["artifact_ref"] = map[string]any{
			"filename":   filename,
			"version":    saveRes.DataVersion,
			"size_bytes": len(raw),
		}
		obj["metadata"] = mergedMeta
		appendMessage(result, fmt.Sprintf(
			"DataObject %q (%d bytes) spilled to artifact version %d.",
			filename, len(raw), saveRes.DataVersion))
		logger.Info("spill: saved data_object to artifact",
			"tool", toolName, "artifact", filename, "version", saveRes.DataVersion,
			"size_bytes", len(raw), "kind", "data_object")
	}
}

func spillFilename(toolName string, content []byte, ext string) string {
	if ext == "" {
		ext = ".txt"
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%s_result_%d_%s%s",
		sanitizeToolNameForFilename(toolName),
		time.Now().UnixMilli(),
		hex.EncodeToString(sum[:4]),
		ext)
}

func extForMIME(mime string) string {
	switch mime {
	case mimeTypeJSON:
		return ".json"
	case "text/csv":
		return ".csv"
	case "application/x-yaml", "application/yaml", "text/yaml":
		return ".yaml"
	case "application/xml", "text/xml":
		return ".xml"
	case "text/html":
		return ".html"
	case "text/markdown":
		return ".md"
	case "application/octet-stream":
		return ".bin"
	default:
		return ".txt"
	}
}

func sanitizeToolNameForFilename(toolName string) string {
	var b strings.Builder
	b.Grow(len(toolName))
	for _, r := range toolName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "tool"
	}
	return out
}

func sanitizeFilenameForSpill(name string) string {
	if name == "" || len(name) > 200 {
		return ""
	}
	if strings.HasPrefix(name, ".") {
		return ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
		if r == '/' || r == '\\' || r == ':' {
			return ""
		}
	}
	return name
}

func truncateForFallback(content []byte, maxBytes int) []byte {
	budget := maxBytes - len(truncationMarker)
	if budget < 0 {
		budget = 0
	}
	if budget >= len(content) {
		return content
	}
	out := make([]byte, 0, budget+len(truncationMarker))
	out = append(out, content[:budget]...)
	out = append(out, truncationMarker...)
	return out
}

func appendMessage(result map[string]any, addition string) {
	if addition == "" {
		return
	}
	existing, _ := result["message"].(string)
	if existing == "" {
		result["message"] = addition
		return
	}
	if !strings.HasSuffix(existing, " ") {
		existing += " "
	}
	result["message"] = existing + addition
}

func estimatedContentSize(content string, isBinary bool) int {
	if !isBinary {
		return len(content)
	}
	n := len(content) / 4 * 3
	if strings.HasSuffix(content, "==") {
		return n - 2
	}
	if strings.HasSuffix(content, "=") {
		return n - 1
	}
	return n
}
