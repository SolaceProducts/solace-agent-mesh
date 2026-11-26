// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	pkgtools "github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
	"gopkg.in/yaml.v3"
)

const mcpJSONMIME = "application/json"

type HandlerConfig struct {
	Logger                      *slog.Logger
	SaveThresholdBytes          int
	LLMReturnMaxBytes           int
	EnableIntelligentProcessing *bool
	SchemaInferenceDepth        int

	EnableTextFormatDetection   bool
	EnableContentParsing        bool
	FallbackToRawOnError        bool
	SaveRawAlongsideIntelligent bool
	MaxContentItems             int
	MaxSingleItemSizeMB         int

	ResponseSkeleton       bool
	SkeletonDepth          int
	SkeletonMaxScalarBytes int
	SkeletonMaxObjectKeys  int
	ExtractFields          map[string]string
}

const (
	DefaultSaveThresholdBytes = 2048
	DefaultLLMReturnMaxBytes  = 4096
)

func DefaultHandlerConfig() HandlerConfig {
	return HandlerConfig{
		SaveThresholdBytes:          DefaultSaveThresholdBytes,
		LLMReturnMaxBytes:           DefaultLLMReturnMaxBytes,
		SchemaInferenceDepth:        4,
		EnableTextFormatDetection:   true,
		EnableContentParsing:        true,
		FallbackToRawOnError:        true,
		SaveRawAlongsideIntelligent: false,
		MaxContentItems:             50,
		MaxSingleItemSizeMB:         100,
		ResponseSkeleton:            true,
		SkeletonDepth:               defaultSkeletonDepth,
		SkeletonMaxScalarBytes:      defaultSkeletonMaxScalarBytes,
		SkeletonMaxObjectKeys:       defaultSkeletonMaxObjectKeys,
	}
}

type savedArtifactInfo struct {
	Status       string `json:"status"`
	DataFilename string `json:"data_filename"`
	DataVersion  int    `json:"data_version"`
}

type mcpSaveResult struct {
	Status           string              `json:"status"`
	Message          string              `json:"message,omitempty"`
	ArtifactsSaved   []savedArtifactInfo `json:"artifacts_saved,omitempty"`
	FallbackArtifact *savedArtifactInfo  `json:"fallback_artifact,omitempty"`
}

func HandleMCPResponse(
	ctx context.Context,
	items []ContentItem,
	toolName string,
	cfg HandlerConfig,
	toolCtx *tool.Context,
	rawResult *rawCallToolResult,
) (*tool.Result, error) {
	result, err := handleMCPResponse(ctx, items, toolName, cfg, toolCtx, rawResult)
	attachStructuredContent(result, rawResult, cfg, toolName)
	attachModelView(result)
	return result, err
}

const (
	mcpToolOutputKey     = "mcp_tool_output"
	structuredMirrorNote = "Same JSON as " + structuredContentKey + "."
)

func attachModelView(result *tool.Result) {
	if result == nil {
		return
	}
	out, ok := result.Data[mcpToolOutputKey].(map[string]any)
	if !ok {
		return
	}
	content, ok := out["content"].([]map[string]any)
	if !ok {
		return
	}
	structured, hasStructured := result.Data[structuredContentKey]
	view := make([]map[string]any, len(content))
	changed := false
	for i, entry := range content {
		view[i] = entry
		text, isText := entry["text"].(string)
		if !isText || entry["type"] != string(ContentTypeText) {
			continue
		}
		doc, isJSON := jsonDocument(text)
		if !isJSON {
			continue
		}
		item := maps.Clone(entry)
		item["text"] = doc
		if hasStructured && mirrors(doc, structured) {
			item["text"] = structuredMirrorNote
		}
		view[i] = item
		changed = true
	}
	if !changed {
		return
	}
	modelOut := maps.Clone(out)
	modelOut["content"] = view
	if result.LLMData == nil {
		result.LLMData = make(map[string]any, 1)
	}
	result.LLMData[mcpToolOutputKey] = modelOut
}

func jsonDocument(text string) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') || !utf8.ValidString(trimmed) || !json.Valid([]byte(trimmed)) {
		return nil, false
	}
	return json.RawMessage(trimmed), true
}

func mirrors(doc json.RawMessage, structured any) bool {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return false
	}
	exact, err := json.Marshal(decoded)
	if err != nil {
		return false
	}
	shown, err := json.Marshal(structured)
	return err == nil && bytes.Equal(exact, shown)
}

func handleMCPResponse(
	ctx context.Context,
	items []ContentItem,
	toolName string,
	cfg HandlerConfig,
	toolCtx *tool.Context,
	rawResult *rawCallToolResult,
) (*tool.Result, error) {
	if len(items) == 0 {
		return tool.OK(fmt.Sprintf("MCP tool %q returned empty response", toolName)), nil
	}

	items = enforceContentCaps(items, cfg.MaxContentItems, cfg.MaxSingleItemSizeMB)
	if len(items) == 0 {
		return tool.OK(fmt.Sprintf("MCP tool %q response dropped by content caps", toolName)), nil
	}

	totalSize := 0
	hasNonText := false
	for _, item := range items {
		totalSize += contentSize(item)
		if item.Type != ContentTypeText {
			hasNonText = true
		}
	}

	needsSaving := totalSize > cfg.SaveThresholdBytes || hasNonText
	needsTruncation := totalSize > cfg.LLMReturnMaxBytes

	if !needsSaving && !needsTruncation {
		return buildProcessedResponse(items, toolName), nil
	}

	saveResult, metadataText, err := saveContentItems(ctx, items, toolName, toolCtx, cfg)
	if err != nil {
		if cfg.FallbackToRawOnError && rawResult != nil {
			if fallback, fbErr := HandleRawFallback(ctx, rawResult, toolName, cfg, toolCtx); fbErr == nil {
				return fallback, nil
			}
		}
		return buildSaveFailedResponse(items, toolName, needsTruncation, err), nil
	}

	if cfg.SaveRawAlongsideIntelligent && rawResult != nil {
		if fb := saveRawFallbackArtifact(ctx, rawResult, toolName, toolCtx); fb != nil {
			saveResult.FallbackArtifact = fb
		}
	}

	digest := buildResponseDigest(items, cfg, toolName)

	if needsTruncation {
		return buildArtifactOnlyResponse(toolName, totalSize, saveResult, metadataText, digest), nil
	}

	return buildSavedResponse(items, toolName, saveResult, metadataText, digest), nil
}

func saveRawFallbackArtifact(ctx context.Context, result *rawCallToolResult, toolName string, toolCtx *tool.Context) *savedArtifactInfo {
	if result == nil {
		return nil
	}
	var rawContent []any
	for _, raw := range result.Content {
		var item any
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		rawContent = append(rawContent, item)
	}
	rawJSON, err := json.Marshal(rawContent)
	if err != nil {
		return nil
	}
	prefix := shortRandomID()
	filename := fmt.Sprintf("%s_%s_raw_%s.json", prefix, sanitizeName(toolName), shortRandomID())

	facade := toolCtx.Facade()
	saveResult, err := facade.SaveArtifactWithMetadata(ctx, filename, rawJSON, mcpJSONMIME, artifact.MetadataOpts{
		Custom: map[string]any{
			"source_tool_name": toolName,
			"processing_type":  "raw_fallback",
			"mime_type":        mcpJSONMIME,
		},
		Tags: []string{a2a.ArtifactTagWorking},
	})
	if err != nil {
		return nil
	}
	return &savedArtifactInfo{
		Status:       "success",
		DataFilename: filename,
		DataVersion:  saveResult.DataVersion,
	}
}

func enforceContentCaps(items []ContentItem, maxItems, maxSingleItemSizeMB int) []ContentItem {
	if len(items) == 0 {
		return items
	}
	filtered := items
	if maxSingleItemSizeMB > 0 {
		maxBytes := maxSingleItemSizeMB * 1024 * 1024
		out := make([]ContentItem, 0, len(items))
		for _, it := range items {
			if contentSize(it) > maxBytes {
				continue
			}
			out = append(out, it)
		}
		filtered = out
	}
	if maxItems > 0 && len(filtered) > maxItems {
		filtered = filtered[:maxItems]
	}
	return filtered
}

func HandleRawFallback(
	ctx context.Context,
	result *rawCallToolResult,
	toolName string,
	cfg HandlerConfig,
	toolCtx *tool.Context,
) (*tool.Result, error) {
	var rawContent []any
	for _, raw := range result.Content {
		var item any
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		rawContent = append(rawContent, item)
	}

	rawJSON, err := json.Marshal(rawContent)
	if err != nil {
		return tool.Error(fmt.Sprintf("MCP tool %q: failed to marshal raw content: %v", toolName, err)), nil
	}

	prefix := shortRandomID()
	filename := fmt.Sprintf("%s_%s_raw_%s.json", prefix, sanitizeName(toolName), shortRandomID())

	facade := toolCtx.Facade()
	saveResult, err := facade.SaveArtifactWithMetadata(ctx, filename, rawJSON, mcpJSONMIME, artifact.MetadataOpts{
		Custom: map[string]any{
			"source_tool_name": toolName,
			"processing_type":  "raw_fallback",
			"mime_type":        mcpJSONMIME,
		},
		Tags: []string{a2a.ArtifactTagWorking},
	})
	if err != nil {
		return tool.Error(fmt.Sprintf("MCP tool %q: failed to save raw artifact: %v", toolName, err)), nil
	}
	version := saveResult.DataVersion

	msg := fmt.Sprintf("The full response has been saved as artifact '%s' (version %d).", filename, version)
	return mcpResult("processed_and_saved", msg,
		map[string]any{
			"saved_mcp_response_artifact_details": &mcpSaveResult{
				Status: "success",
				FallbackArtifact: &savedArtifactInfo{
					Status:       "success",
					DataFilename: filename,
					DataVersion:  version,
				},
			},
		},
	), nil
}

func mcpResult(status string, messageToLLM string, data map[string]any) *tool.Result {
	if data == nil {
		data = make(map[string]any)
	}
	data["message_to_llm"] = messageToLLM
	return &tool.Result{
		Status: status,
		Data:   data,
	}
}

func buildProcessedResponse(items []ContentItem, toolName string) *tool.Result {
	return mcpResult("processed",
		fmt.Sprintf("Response from tool '%s' processed.", toolName),
		map[string]any{
			"mcp_tool_output": buildMCPToolOutput(items),
		},
	)
}

func buildSavedResponse(items []ContentItem, toolName string, saveResult *mcpSaveResult, metadataText string, digest map[string]any) *tool.Result {
	msg := buildSavedMessage(toolName, saveResult)
	data := map[string]any{
		"mcp_tool_output":                     buildMCPToolOutput(items),
		"saved_mcp_response_artifact_details": saveResult,
	}
	if metadataText != "" {
		data["appended_artifact_metadata"] = metadataText
	}
	mergeDigest(data, digest)
	return mcpResult("processed_and_saved", msg, data)
}

func mergeDigest(data, digest map[string]any) {
	maps.Copy(data, digest)
}

func buildArtifactOnlyResponse(toolName string, totalSize int, saveResult *mcpSaveResult, metadataText string, digest map[string]any) *tool.Result {
	artMsg := buildSavedMessage(toolName, saveResult)

	var firstFilename string
	if len(saveResult.ArtifactsSaved) > 0 {
		firstFilename = saveResult.ArtifactsSaved[0].DataFilename
	}

	msg := fmt.Sprintf(
		"The response from tool '%s' was too large (%d bytes) to display directly. "+
			"The data has NOT been included here to prevent incomplete information. "+
			"You MUST use template_liquid (for displaying to users) or load_artifact or other artifact analysis tools "+
			"(for processing) to access the full data. %s"+
			" To display this data to the user, use template_liquid with data=\"%s\"."+
			" To process the data yourself, use load_artifact(\"%s\").",
		toolName, totalSize, artMsg, firstFilename, firstFilename,
	)

	data := map[string]any{
		"mcp_tool_output": map[string]any{
			"type":    "data_in_artifact_only",
			"message": "Data exceeds size limit. Full data saved as artifact - use template_liquid, load_artifact or other artifact analysis tools to process and access.",
		},
		"saved_mcp_response_artifact_details": saveResult,
	}
	if metadataText != "" {
		data["appended_artifact_metadata"] = metadataText
	}
	mergeDigest(data, digest)
	if _, ok := digest["response_skeleton"]; ok {
		msg += " A response_skeleton (top-level fields, with arrays/large objects elided) is included" +
			" in this result — read pagination / loop-control fields (e.g. isLast, nextPageToken)" +
			" from it directly instead of loading the artifact."
	} else if _, ok := digest["extracted_fields"]; ok {
		msg += " The configured extracted_fields are included in this result —" +
			" read loop-control fields from there instead of loading the artifact."
	}
	return mcpResult("processed_saved_artifact_only", msg, data)
}

func buildSaveFailedResponse(items []ContentItem, toolName string, needsTruncation bool, err error) *tool.Result {
	status := "processed_save_failed"
	msg := "Saving the full response as an artifact failed."
	data := map[string]any{
		"saved_mcp_response_artifact_details": &mcpSaveResult{
			Status:  "error",
			Message: fmt.Sprintf("Failed to save MCP response as artifact: %v", err),
		},
	}
	if needsTruncation {
		status = "processed_artifact_only_save_failed"
		data["mcp_tool_output"] = map[string]any{
			"type":    "data_in_artifact_only",
			"message": "Data exceeds size limit. Full data saved as artifact - use template_liquid, load_artifact or other artifact analysis tools to process and access.",
		}
	} else {
		data["mcp_tool_output"] = buildMCPToolOutput(items)
	}
	return mcpResult(status, msg, data)
}

func buildSavedMessage(toolName string, result *mcpSaveResult) string {
	n := len(result.ArtifactsSaved)
	if n == 0 {
		return fmt.Sprintf("Response from tool '%s' processed.", toolName)
	}
	first := result.ArtifactsSaved[0]
	if n == 1 {
		return fmt.Sprintf("The full response has been saved as artifact '%s' (version %d).",
			first.DataFilename, first.DataVersion)
	}
	return fmt.Sprintf("The full response has been saved as %d artifacts, starting with '%s' (version %d).",
		n, first.DataFilename, first.DataVersion)
}

func saveContentItems(ctx context.Context, items []ContentItem, toolName string, toolCtx *tool.Context, cfg HandlerConfig) (*mcpSaveResult, string, error) {
	facade := toolCtx.Facade()
	var saved []savedArtifactInfo
	var metadataTexts []string

	for _, item := range items {
		content := contentBytes(item)
		mimeType := item.MIMEType
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = pkgtools.DetectMIMEType(item.Filename, content)
		}

		custom, description := buildMCPCustomMetadata(item, toolName, cfg)

		var tags []string
		if isWorkingContent(item, mimeType, content) {
			tags = []string{a2a.ArtifactTagWorking}
		}

		saveResult, err := facade.SaveArtifactWithMetadata(ctx, item.Filename, content, mimeType, artifact.MetadataOpts{
			Description:    description,
			Custom:         custom,
			SchemaMaxDepth: cfg.SchemaInferenceDepth,
			Tags:           tags,
		})
		if err != nil {
			return nil, "", fmt.Errorf("save artifact %q: %w", item.Filename, err)
		}

		ver := saveResult.DataVersion
		companionMeta := artifact.LoadCompanionMetadata(ctx, toolCtx.ArtifactService,
			toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, item.Filename, &ver)
		if companionMeta != nil {
			companionMeta["version"] = ver
			metadataTexts = append(metadataTexts, artifact.FormatMetadataForLLM(companionMeta))
		}

		saved = append(saved, savedArtifactInfo{
			Status:       "success",
			DataFilename: item.Filename,
			DataVersion:  saveResult.DataVersion,
		})
	}

	metadataText := strings.Join(metadataTexts, "\n\n")

	return &mcpSaveResult{
		Status:         "success",
		Message:        fmt.Sprintf("Successfully processed %d content items.", len(saved)),
		ArtifactsSaved: saved,
	}, metadataText, nil
}

func isWorkingContent(item ContentItem, mimeType string, content []byte) bool {
	switch item.Type {
	case ContentTypeText:
		return true
	case ContentTypeResource:
		return isTextualResource(item, mimeType, content)
	default:
		return false
	}
}

func isTextualResource(item ContentItem, mimeType string, content []byte) bool {
	if item.Text != "" {
		return true
	}
	if item.Data == nil {
		return false
	}
	if embed.IsTextBasedMIME(baseMIME(mimeType)) {
		return true
	}
	if isUntypedMIME(item.MIMEType) || isUntypedMIME(mimeType) {
		return looksLikeText(content)
	}
	return false
}

func isUntypedMIME(mimeType string) bool {
	base := baseMIME(mimeType)
	return base == "" || base == "application/octet-stream"
}

func baseMIME(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func looksLikeText(content []byte) bool {
	return len(content) > 0 && utf8.Valid(content) && !bytes.ContainsRune(content, 0)
}

func buildMCPCustomMetadata(item ContentItem, toolName string, cfg HandlerConfig) (map[string]any, string) {
	custom := map[string]any{
		"source_tool_name": toolName,
		"content_type":     string(item.Type),
		"mime_type":        item.MIMEType,
	}

	contentTypeTitle := string(item.Type)
	if len(contentTypeTitle) > 0 {
		contentTypeTitle = strings.ToUpper(contentTypeTitle[:1]) + contentTypeTitle[1:]
	}
	custom["description"] = fmt.Sprintf("%s content from MCP tool %s", contentTypeTitle, toolName)

	if item.Type == ContentTypeText {
		if df, ok := item.Metadata["detected_format"]; ok {
			custom["detected_format"] = df
			format := TextFormat(df)
			if cfg.EnableContentParsing {
				summary := buildParsedDataSummary(item.Text, format)
				parseSuccess := summary != nil
				custom["format_parse_success"] = parseSuccess
				if parseSuccess {
					custom["parsed_data_summary"] = summary
				}
				if format == TextFormatJSON {
					depth := cfg.SchemaInferenceDepth
					if depth <= 0 {
						depth = 4
					}
					if schema := inferJSONSchema(item.Text, depth); schema != nil {
						custom["schema"] = schema
					}
				}
			}
		}
	}

	if item.Type == ContentTypeImage || item.Type == ContentTypeAudio {
		custom["original_mime_type"] = item.MIMEType
	}

	if item.Type == ContentTypeResource {
		if item.URI != "" {
			custom["resource_uri"] = item.URI
		}
		custom["has_text_content"] = item.Text != ""
		custom["has_blob_content"] = len(item.Data) > 0
		if item.Text == "" && len(item.Data) == 0 {
			custom["is_placeholder"] = true
		} else {
			custom["is_placeholder"] = false
		}
		if len(item.Data) > 0 {
			custom["original_mime_type"] = item.MIMEType
		}
	}

	description, _ := custom["description"].(string)
	delete(custom, "description")

	return custom, description
}

func buildMCPToolOutput(items []ContentItem) map[string]any {
	content := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := map[string]any{"type": string(item.Type)}
		switch item.Type {
		case ContentTypeText:
			entry["text"] = item.Text
		case ContentTypeImage, ContentTypeAudio:
			entry["mimeType"] = item.MIMEType
			entry["data_size"] = len(item.Data)
		case ContentTypeResource:
			res := map[string]any{}
			if item.URI != "" {
				res["uri"] = item.URI
			}
			if item.Text != "" {
				res["text"] = item.Text
			}
			if item.Data != nil {
				res["has_blob"] = true
				res["blob_size"] = len(item.Data)
			}
			entry["resource"] = res
		}
		content = append(content, entry)
	}
	return map[string]any{"content": content}
}

func contentSize(item ContentItem) int {
	if item.Data != nil {
		return len(item.Data)
	}
	return len(item.Text)
}

func contentBytes(item ContentItem) []byte {
	if item.Type == ContentTypeResource && item.Text == "" && item.Data == nil {
		return []byte(fmt.Sprintf("Resource reference: %s", item.URI))
	}
	if item.Data != nil {
		return item.Data
	}
	return []byte(item.Text)
}

func buildParsedDataSummary(text string, format TextFormat) map[string]any {
	switch format {
	case TextFormatJSON:
		return parseJSONSummary(text)
	case TextFormatCSV:
		return parseCSVSummary(text)
	case TextFormatYAML:
		return parseYAMLSummary(text)
	default:
		return nil
	}
}

func inferJSONSchema(text string, maxDepth int) map[string]any {
	var data any
	if err := looseJSONUnmarshal([]byte(text), &data); err != nil {
		return nil
	}

	structure := inferStructure(data, 0, maxDepth)

	return map[string]any{
		"type":      mcpJSONMIME,
		"inferred":  true,
		"structure": structure,
	}
}

func inferStructure(data any, currentDepth, maxDepth int) any {
	switch v := data.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		if v == float64(int64(v)) {
			return "int"
		}
		return "float"
	case string:
		return "str"
	case []any:
		if len(v) == 0 {
			return "list"
		}
		if currentDepth >= maxDepth {
			return "list"
		}
		var elements []any
		for _, item := range v {
			elements = append(elements, inferStructure(item, currentDepth+1, maxDepth))
		}
		if len(elements) == 1 {
			return elements
		}
		return elements
	case map[string]any:
		if currentDepth >= maxDepth {
			return "dict"
		}
		result := make(map[string]any)
		for key, val := range v {
			result[key] = inferStructure(val, currentDepth+1, maxDepth)
		}
		return result
	default:
		return "unknown"
	}
}

func parseJSONSummary(text string) map[string]any {
	body := []byte(text)
	if !json.Valid(body) {
		if sanitized, changed := escapeUnescapedJSONControlChars(body); changed && json.Valid(sanitized) {
			body = sanitized
		} else {
			return nil
		}
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	if delim == '[' {
		var arr []any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil
		}
		return map[string]any{
			"type":  "array",
			"count": len(arr),
		}
	}

	if delim == '{' {
		var keys []string
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				break
			}
			if key, ok := keyTok.(string); ok {
				keys = append(keys, key)
			}
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				break
			}
		}
		return map[string]any{
			"type":      "object",
			"key_count": len(keys),
			"keys":      keys,
		}
	}

	return nil
}

func parseCSVSummary(text string) map[string]any {
	reader := csv.NewReader(strings.NewReader(text))
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil
	}
	headers := records[0]
	return map[string]any{
		"type":    "table",
		"rows":    len(records),
		"columns": len(headers),
		"headers": headers,
	}
}

func parseYAMLSummary(text string) map[string]any {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(text), &node); err != nil {
		return nil
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil
	}
	mapping := node.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	var keys []string
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		keys = append(keys, mapping.Content[i].Value)
	}
	return map[string]any{
		"type":      "object",
		"key_count": len(keys),
		"keys":      keys,
	}
}

const structuredContentKey = "mcp_structured_content"

func attachStructuredContent(result *tool.Result, rawResult *rawCallToolResult, cfg HandlerConfig, toolName string) {
	if result == nil || rawResult == nil || len(rawResult.StructuredContent) == 0 {
		return
	}

	decoded := map[string]any{}
	if err := json.Unmarshal(rawResult.StructuredContent, &decoded); err != nil {
		if len(decoded) == 0 {
			logStructuredContent(cfg, "mcp structuredContent discarded", toolName, err, len(rawResult.StructuredContent))
			return
		}
		logStructuredContent(cfg, "mcp structuredContent partially decoded", toolName, err, len(rawResult.StructuredContent))
	}
	if len(decoded) == 0 && !isEmptyJSONObject(rawResult.StructuredContent) {
		return
	}

	value := any(decoded)
	if maxBytes := structuredContentMaxBytes(cfg); len(rawResult.StructuredContent) > maxBytes {
		bounded := buildSkeleton(decoded, skeletonDepth(cfg), skeletonMaxScalarBytes(cfg), skeletonMaxObjectKeys(cfg))
		if bounded == nil {
			logStructuredContent(cfg, "mcp structuredContent too large to bound", toolName, nil, len(rawResult.StructuredContent))
			return
		}
		value = bounded
	}

	if result.Data == nil {
		result.Data = make(map[string]any, 1)
	}
	if _, exists := result.Data[structuredContentKey]; exists {
		logStructuredContent(cfg, "mcp structuredContent key already present", toolName, nil, len(rawResult.StructuredContent))
		return
	}
	result.Data[structuredContentKey] = value
}

func isEmptyJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return false
	}
	return len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0
}

func structuredContentMaxBytes(cfg HandlerConfig) int {
	if cfg.LLMReturnMaxBytes > 0 {
		return cfg.LLMReturnMaxBytes
	}
	return DefaultHandlerConfig().LLMReturnMaxBytes
}

func skeletonDepth(cfg HandlerConfig) int {
	if cfg.SkeletonDepth > 0 {
		return cfg.SkeletonDepth
	}
	return defaultSkeletonDepth
}

func skeletonMaxScalarBytes(cfg HandlerConfig) int {
	if cfg.SkeletonMaxScalarBytes > 0 {
		return cfg.SkeletonMaxScalarBytes
	}
	return defaultSkeletonMaxScalarBytes
}

func skeletonMaxObjectKeys(cfg HandlerConfig) int {
	if cfg.SkeletonMaxObjectKeys > 0 {
		return cfg.SkeletonMaxObjectKeys
	}
	return defaultSkeletonMaxObjectKeys
}

func logStructuredContent(cfg HandlerConfig, msg, toolName string, err error, rawBytes int) {
	if cfg.Logger == nil {
		return
	}
	attrs := []any{"toolName", toolName, "structuredContentBytes", rawBytes}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	cfg.Logger.Warn(msg, attrs...)
}
