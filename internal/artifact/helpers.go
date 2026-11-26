// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

const (
	MetadataSuffix = ".metadata.json"

	ConvertedTextSuffix = ".converted.txt"

	BM25IndexFilename = "project_bm25_index.zip"
)

type MetadataOpts struct {
	Description    string
	Source         string
	Custom         map[string]any
	ExplicitSchema map[string]any
	SchemaMaxDepth int
	SchemaMaxKeys  int
	Timestamp      time.Time
	SuppressSchema bool
	Tags           []string
}

type SaveResult struct {
	DataVersion      int
	MetadataVersion  int
	MetadataFilename string
}

func SaveWithMetadata(
	ctx context.Context,
	svc *ScopedService,
	agentName, userID, sessionID string,
	filename string,
	content []byte,
	mimeType string,
	opts MetadataOpts,
	logger *slog.Logger,
) (*SaveResult, error) {
	loc := svc.Resolve(agentName, userID, sessionID, filename)
	dataVersion, err := svc.Inner.Save(ctx, loc, content, mimeType)
	if err != nil {
		return nil, fmt.Errorf("save data artifact %q: %w", filename, err)
	}

	verMeta := &VersionMetadata{
		MIMEType:    mimeType,
		Description: opts.Description,
		SizeBytes:   int64(len(content)),
	}
	if metaErr := svc.Inner.SaveMetadata(ctx, loc, dataVersion, verMeta); metaErr != nil {
		logger.Warn("failed to save version metadata", "filename", filename, "error", metaErr)
	}

	ts := opts.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	meta := newOrderedMap(8)
	meta.set("filename", filename)
	meta.set("mime_type", mimeType)
	meta.set("size_bytes", len(content))
	meta.set("timestamp_utc", float64(ts.UnixMicro())/1e6)

	if opts.Custom != nil {
		for k, v := range opts.Custom {
			meta.set(k, v)
		}
	}

	if opts.Description != "" {
		meta.set("description", opts.Description)
	}
	if opts.Source != "" {
		meta.set("source", opts.Source)
	}

	if len(opts.Tags) > 0 {
		meta.set("tags", opts.Tags)
	}

	if !opts.SuppressSchema {
		if opts.ExplicitSchema != nil {
			meta.set("schema", opts.ExplicitSchema)
		} else {
			schema := InferSchema(content, mimeType, opts.SchemaMaxDepth, opts.SchemaMaxKeys)
			meta.set("schema", schema)
		}
	}

	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		logger.Warn("failed to marshal metadata JSON", "filename", filename, "error", err)
		return &SaveResult{
			DataVersion:      dataVersion,
			MetadataVersion:  -1,
			MetadataFilename: filename + MetadataSuffix,
		}, nil
	}

	metaFilename := filename + MetadataSuffix
	metaLoc := svc.Resolve(agentName, userID, sessionID, metaFilename)
	metaVersion, err := svc.Inner.Save(ctx, metaLoc, metaJSON, "application/json")
	if err != nil {
		logger.Warn("data artifact saved but metadata save failed",
			"filename", filename, "dataVersion", dataVersion, "error", err)
		return &SaveResult{
			DataVersion:      dataVersion,
			MetadataVersion:  -1,
			MetadataFilename: metaFilename,
		}, nil
	}

	return &SaveResult{
		DataVersion:      dataVersion,
		MetadataVersion:  metaVersion,
		MetadataFilename: metaFilename,
	}, nil
}

func IsInternalArtifact(filename string) bool {
	return slices.ContainsFunc(internalArtifactSuffixes, func(suffix string) bool {
		return strings.HasSuffix(filename, suffix)
	}) || slices.Contains(internalArtifactNames, filename)
}

var (
	internalArtifactSuffixes = []string{MetadataSuffix, ConvertedTextSuffix}
	internalArtifactNames    = []string{BM25IndexFilename}
)

func LoadCompanionMetadata(
	ctx context.Context,
	svc *ScopedService,
	agentName, userID, sessionID string,
	filename string,
	version *int,
) map[string]any {
	dataLoc := svc.Resolve(agentName, userID, sessionID, filename)
	return LoadCompanionMetadataAtLocator(ctx, svc, dataLoc, version)
}

func LoadCompanionMetadataAtLocator(
	ctx context.Context,
	svc *ScopedService,
	loc Locator,
	version *int,
) map[string]any {
	metaLoc := Locator{
		AppName:   loc.AppName,
		UserID:    loc.UserID,
		SessionID: loc.SessionID,
		Filename:  loc.Filename + MetadataSuffix,
	}
	content, _, err := svc.Inner.Load(ctx, metaLoc, version)
	if err != nil || content == nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		return nil
	}
	return result
}

func FormatMetadataForLLM(metadata map[string]any) string {
	var lines []string

	filename, _ := metadata["filename"].(string)
	if filename == "" {
		filename = "Unknown Filename"
	}
	version := "N/A"
	if v, ok := metadata["version"]; ok {
		version = fmt.Sprintf("%v", v)
	}

	lines = append(lines, fmt.Sprintf("--- Metadata for artifact '%s' (v%s) ---", filename, version))
	lines = append(lines, "**Artifact Metadata:**")

	if desc, ok := metadata["description"]; ok {
		lines = append(lines, fmt.Sprintf("*   **Description:** %v", desc))
	}
	if source, ok := metadata["source"]; ok {
		lines = append(lines, fmt.Sprintf("*   **Source:** %v", source))
	}
	if mt, ok := metadata["mime_type"]; ok {
		lines = append(lines, fmt.Sprintf("*   **Type:** %v", mt))
	}
	if sb, ok := metadata["size_bytes"]; ok {
		lines = append(lines, fmt.Sprintf("*   **Size:** %v bytes", sb))
	}

	if schema, ok := metadata["schema"].(map[string]any); ok {
		schemaType, _ := schema["type"].(string)
		if schemaType == "" {
			if mt, ok := metadata["mime_type"].(string); ok {
				schemaType = mt
			}
		}
		var details []string
		if inferred, ok := schema["inferred"].(bool); ok && inferred {
			details = append(details, "(Inferred)")
		}
		if cols, ok := schema["columns"].([]any); ok && len(cols) > 0 {
			colStrs := make([]string, len(cols))
			for i, c := range cols {
				colStrs[i] = fmt.Sprintf("[%d] %v", i, c)
			}
			details = append(details, "Columns (0-indexed): "+strings.Join(colStrs, ", "))
		}
		if structure, ok := schema["structure"]; ok {
			structJSON, _ := json.Marshal(structure)
			details = append(details, fmt.Sprintf("Structure: %s", string(structJSON)))
		}
		if len(details) > 0 {
			lines = append(lines, fmt.Sprintf("*   **Schema:** %s %s", schemaType, strings.Join(details, " ")))
		} else if schemaType != "" {
			lines = append(lines, fmt.Sprintf("*   **Schema Type:** %s", schemaType))
		}
	} else if mt, ok := metadata["mime_type"].(string); ok && mt != "" {
		lines = append(lines, fmt.Sprintf("*   **Schema Type:** %s", mt))
	}

	excludedKeys := map[string]bool{
		"filename": true, "mime_type": true, "size_bytes": true,
		"timestamp_utc": true, "schema": true, "version": true,
		"description": true, "source": true,
	}
	var customKeys []string
	for k := range metadata {
		if !excludedKeys[k] {
			customKeys = append(customKeys, k)
		}
	}
	if len(customKeys) > 0 {
		slices.Sort(customKeys)
		lines = append(lines, "*   **Other:**")
		for _, k := range customKeys {
			lines = append(lines, fmt.Sprintf("    *   %s: %s", k, formatPythonValue(metadata[k])))
		}
	}

	lines = append(lines, "--- End Metadata ---")
	return strings.Join(lines, "\n")
}

func formatPythonValue(v any) string {
	switch val := v.(type) {
	case bool:
		if val {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("'%s': %s", k, formatPythonValue(val[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = formatPythonValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return val
	default:
		return fmt.Sprintf("%v", v)
	}
}

type orderedMap struct {
	keys   []string
	values map[string]any
}

func newOrderedMap(cap int) *orderedMap {
	return &orderedMap{
		keys:   make([]string, 0, cap),
		values: make(map[string]any, cap),
	}
}

func (o *orderedMap) set(key string, value any) {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *orderedMap) MarshalJSON() ([]byte, error) {
	var buf []byte
	buf = append(buf, '{')
	for i, k := range o.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf = append(buf, keyBytes...)
		buf = append(buf, ':')
		valBytes, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		buf = append(buf, valBytes...)
	}
	buf = append(buf, '}')
	return buf, nil
}

var ErrNoArtifactService = errors.New("no artifact service configured")

var ErrUnsafeFilename = errors.New("unsafe or reserved artifact filename")

type UploadScope struct {
	AgentName string
	UserID    string
	SessionID string
}

type Upload struct {
	Filename   string
	MIMEType   string
	Content    []byte
	DeriveMIME bool
}

func SaveUpload(
	ctx context.Context,
	svc *ScopedService,
	scope UploadScope,
	up Upload,
	opts MetadataOpts,
	logger *slog.Logger,
) (uri string, version int, err error) {
	if svc == nil {
		return "", 0, ErrNoArtifactService
	}
	if !IsFilenameSafe(up.Filename) || IsInternalArtifact(up.Filename) {
		return "", 0, ErrUnsafeFilename
	}

	mimeType := up.MIMEType
	if up.DeriveMIME {
		mimeType = ResolveDataMIME(up.Filename, mimeType, up.Content)
	}

	loc := svc.Resolve(scope.AgentName, scope.UserID, scope.SessionID, up.Filename)
	if _, uriErr := FormatURI(loc, 0); uriErr != nil {
		return "", 0, uriErr
	}

	result, err := SaveWithMetadata(ctx, svc,
		scope.AgentName, scope.UserID, scope.SessionID,
		up.Filename, up.Content, mimeType, opts, logger)
	if err != nil {
		return "", 0, err
	}

	uri, err = FormatURI(loc, result.DataVersion)
	if err != nil {
		return "", 0, err
	}
	return uri, result.DataVersion, nil
}
