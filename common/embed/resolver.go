// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type Resolver struct {
	loader ArtifactLoader
	config Config
}

func NewResolver(loader ArtifactLoader, config Config) *Resolver {
	return &Resolver{
		loader: loader,
		config: config,
	}
}

func (r *Resolver) ResolveEmbedsInString(ctx context.Context, text string, mode ResolutionMode) (string, []Signal, error) {
	embeds := FindAll(text)
	if len(embeds) == 0 {
		return text, nil, nil
	}

	var signals []Signal
	for i := len(embeds) - 1; i >= 0; i-- {
		e := embeds[i]
		if !shouldResolve(e.Type, mode) {
			continue
		}

		resolved, extraSignals, signal, err := r.evaluateEmbed(ctx, e, mode, 0, nil)
		signals = append(signals, extraSignals...)
		if err != nil {
			if _, ok := err.(*StructuralError); ok {
				resolved = fmt.Sprintf("[:warning: Error resolving embed %s:%s - %s.]", e.Type, e.Expression, err.Error())
			} else {
				resolved = fmt.Sprintf("[Error: %s]", err.Error())
			}
		}
		if signal != nil {
			signals = append(signals, *signal)
		}

		text = text[:e.Start] + resolved + text[e.End:]
	}

	return text, signals, nil
}

func shouldResolve(embedType string, mode ResolutionMode) bool {
	earlyTypes := map[string]bool{
		"math": true, "datetime": true, "uuid": true,
		"artifact_meta": true, "status_update": true,
	}
	lateTypes := map[string]bool{
		"artifact_content": true, "artifact_return": true,
	}

	switch mode {
	case ToolParameter, ArtifactStreaming:
		return earlyTypes[embedType]
	case RecursiveArtifactContent, A2AMessageToUser:
		return earlyTypes[embedType] || lateTypes[embedType]
	}
	return false
}

func (r *Resolver) evaluateEmbed(ctx context.Context, e ParsedEmbed, mode ResolutionMode, depth int, visited map[string]bool) (string, []Signal, *Signal, error) {
	switch e.Type {
	case "artifact_content":
		text, extraSignals, err := r.evalArtifactContent(ctx, e, mode, depth, visited)
		return text, extraSignals, nil, err
	case "artifact_return":
		text, sig, err := r.evalArtifactReturn(ctx, e, mode)
		return text, nil, sig, err
	case "artifact_meta":
		text, sig, err := r.evalArtifactMeta(ctx, e)
		return text, nil, sig, err
	case "status_update":
		return "", nil, &Signal{Type: "status_update", Data: e.Expression}, nil
	default:
		return "", nil, nil, fmt.Errorf("unsupported embed type: %s", e.Type)
	}
}

func (r *Resolver) evalArtifactContent(ctx context.Context, e ParsedEmbed, mode ResolutionMode, depth int, visited map[string]bool) (string, []Signal, error) {
	if depth >= r.config.MaxRecursionDepth {
		return "", nil, fmt.Errorf("max embed depth (%d) exceeded", r.config.MaxRecursionDepth)
	}

	chain, chainErr := ParseModifierChain(e.Expression)
	if chainErr != nil {
		return "", nil, &StructuralError{Message: chainErr.Error()}
	}
	filename, version := ParseArtifactSpec(chain.ArtifactSpec)

	if r.loader == nil {
		return "", nil, fmt.Errorf("no artifact loader configured")
	}

	visitKey := filename
	if version != nil {
		visitKey = fmt.Sprintf("%s:%d", filename, *version)
	}
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[visitKey] {
		return "", nil, fmt.Errorf("circular reference detected for %s", visitKey)
	}
	visited[visitKey] = true
	defer func() { delete(visited, visitKey) }()

	content, mimeType, err := r.loader.Load(ctx, filename, version)
	if err != nil {
		return "", nil, fmt.Errorf("load artifact %q: %w", filename, err)
	}
	if content == nil {
		return "", nil, fmt.Errorf("artifact %q not found", filename)
	}

	data, format := initialParse(content, mimeType)

	var recursiveSignals []Signal
	if format == FormatString {
		s := data.(string)
		resolved, sigs, rErr := r.resolveRecursive(ctx, s, depth+1, visited)
		if rErr == nil {
			data = resolved
			recursiveSignals = sigs
		}
	}

	for _, step := range chain.Modifiers {
		if step.Name == "apply_to_template" {
			data, format, err = r.applyTemplate(ctx, data, format, step.Param, depth, visited)
		} else {
			data, format, err = applyModifier(data, format, step)
		}
		if err != nil {
			return "", nil, err
		}
	}

	result, err := serializeData(data, format, chain.OutputFormat, mimeType, content)
	if err != nil {
		return "", nil, err
	}

	return result, recursiveSignals, nil
}

func (r *Resolver) resolveRecursive(ctx context.Context, text string, depth int, visited map[string]bool) (string, []Signal, error) {
	embeds := FindAll(text)
	if len(embeds) == 0 {
		return text, nil, nil
	}

	var signals []Signal
	for i := len(embeds) - 1; i >= 0; i-- {
		e := embeds[i]
		if !shouldResolve(e.Type, RecursiveArtifactContent) {
			continue
		}
		resolved, extraSignals, signal, err := r.evaluateEmbed(ctx, e, RecursiveArtifactContent, depth, visited)
		signals = append(signals, extraSignals...)
		if err != nil {
			if _, ok := err.(*StructuralError); ok {
				resolved = fmt.Sprintf("[:warning: Error resolving embed %s:%s - %s.]", e.Type, e.Expression, err.Error())
			} else {
				resolved = fmt.Sprintf("[Error: %s]", err.Error())
			}
		}
		if signal != nil {
			signals = append(signals, *signal)
		}
		text = text[:e.Start] + resolved + text[e.End:]
	}

	return text, signals, nil
}

func (r *Resolver) evalArtifactReturn(ctx context.Context, e ParsedEmbed, mode ResolutionMode) (string, *Signal, error) {
	if mode != A2AMessageToUser && mode != RecursiveArtifactContent {
		return "", nil, nil
	}
	filename, version := ParseArtifactSpec(e.Expression)
	data := map[string]any{
		"filename": filename,
	}
	if version != nil {
		data["version"] = *version
	} else if r.loader != nil {
		versions, err := r.loader.ListVersions(ctx, filename)
		if err == nil && len(versions) > 0 {
			data["version"] = versions[len(versions)-1]
		}
	}
	return "", &Signal{Type: "artifact_return", Data: data}, nil
}

func (r *Resolver) evalArtifactMeta(ctx context.Context, e ParsedEmbed) (string, *Signal, error) {
	if r.loader == nil {
		return "", nil, fmt.Errorf("no artifact loader configured")
	}
	filename, version := ParseArtifactSpec(e.Expression)
	meta, err := r.loader.GetMetadata(ctx, filename, version)
	if err != nil {
		return "", nil, fmt.Errorf("get metadata for %q: %w", filename, err)
	}
	if meta == nil {
		return "null", nil, nil
	}
	b, _ := json.Marshal(meta)
	return string(b), nil, nil
}

func (r *Resolver) applyTemplate(ctx context.Context, data any, format DataFormat, templateFilename string, depth int, visited map[string]bool) (any, DataFormat, error) {
	if r.loader == nil {
		return nil, format, fmt.Errorf("no artifact loader for template")
	}

	templateBytes, _, err := r.loader.Load(ctx, templateFilename, nil)
	if err != nil {
		return nil, format, fmt.Errorf("load template %q: %w", templateFilename, err)
	}
	if templateBytes == nil {
		return nil, format, fmt.Errorf("template %q not found", templateFilename)
	}
	template := string(templateBytes)

	renderCtx := buildTemplateContext(data, format)

	result := renderMustache(template, renderCtx)

	resolved, _, rErr := r.resolveRecursive(ctx, result, depth+1, visited)
	if rErr == nil {
		result = resolved
	}

	return result, FormatString, nil
}

func buildTemplateContext(data any, format DataFormat) map[string]any {
	switch format {
	case FormatJSONObject:
		if m, ok := data.(map[string]any); ok {
			return m
		}
		if list, ok := data.([]any); ok {
			return map[string]any{"items": list}
		}
		return map[string]any{"value": data}
	case FormatListOfDicts:
		if rows, ok := data.([]map[string]any); ok {
			items := make([]any, len(rows))
			for i, r := range rows {
				items[i] = r
			}
			return map[string]any{"items": items}
		}
		return map[string]any{"value": data}
	case FormatString:
		if s, ok := data.(string); ok {
			return map[string]any{"text": s}
		}
		return map[string]any{"text": fmt.Sprint(data)}
	default:
		return map[string]any{"value": data}
	}
}

func renderMustache(template string, ctx map[string]any) string {
	result := template
	for {
		start := strings.Index(result, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		key := strings.TrimSpace(result[start+2 : end-2])

		val := resolveKey(ctx, key)
		replacement := ""
		switch v := val.(type) {
		case nil:
			replacement = ""
		case string:
			replacement = v
		case float64:
			if v == float64(int(v)) {
				replacement = fmt.Sprintf("%d", int(v))
			} else {
				replacement = fmt.Sprintf("%g", v)
			}
		default:
			b, _ := json.Marshal(v)
			replacement = string(b)
		}

		result = result[:start] + replacement + result[end:]
	}
	return result
}

func resolveKey(ctx map[string]any, key string) any {
	parts := strings.Split(key, ".")
	var current any = ctx
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

func initialParse(content []byte, mimeType string) (any, DataFormat) {
	if isTextMIME(mimeType) {
		s := string(content)
		switch {
		case mimeType == "application/json" || strings.HasSuffix(mimeType, "+json"):
			var obj any
			if err := json.Unmarshal(content, &obj); err == nil {
				return obj, FormatJSONObject
			}
		case mimeType == "text/csv":
			if result, _, err := convertData(s, FormatString, FormatListOfDicts); err == nil {
				return result, FormatListOfDicts
			}
		case mimeType == "application/yaml" || mimeType == "application/x-yaml" ||
			mimeType == "text/yaml" || mimeType == "text/x-yaml":
			var obj any
			if err := json.Unmarshal(content, &obj); err == nil {
				return obj, FormatJSONObject
			}
		}
		return s, FormatString
	}
	return content, FormatBytes
}

func serializeData(data any, format DataFormat, outputFormat, mimeType string, rawContent []byte) (string, error) {
	switch outputFormat {
	case "text":
		switch format {
		case FormatString:
			if s, ok := data.(string); ok {
				return s, nil
			}
			return fmt.Sprint(data), nil
		case FormatJSONObject:
			b, _ := json.Marshal(data)
			return string(b), nil
		case FormatListOfDicts:
			if rows, ok := data.([]map[string]any); ok {
				return listOfDictsToCSV(rows), nil
			}
			b, _ := json.Marshal(data)
			return string(b), nil
		case FormatBytes:
			if b, ok := data.([]byte); ok {
				return string(b), nil
			}
			return fmt.Sprint(data), nil
		}

	case "json":
		b, err := json.Marshal(stripInternalKeys(data))
		if err != nil {
			return "", fmt.Errorf("marshal JSON: %w", err)
		}
		return string(b), nil

	case "json_pretty":
		b, err := json.MarshalIndent(stripInternalKeys(data), "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal JSON: %w", err)
		}
		return string(b), nil

	case "csv":
		switch v := data.(type) {
		case []map[string]any:
			return listOfDictsToCSV(v), nil
		case string:
			return v, nil
		default:
			return fmt.Sprint(data), nil
		}

	case "datauri":
		var rawBytes []byte
		switch v := data.(type) {
		case []byte:
			rawBytes = v
		case string:
			rawBytes = []byte(v)
		default:
			if rawContent != nil {
				rawBytes = rawContent
			} else {
				b, _ := json.Marshal(data)
				rawBytes = b
			}
		}
		encoded := base64.StdEncoding.EncodeToString(rawBytes)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil

	default:
		return serializeData(data, format, "text", mimeType, rawContent)
	}

	return fmt.Sprint(data), nil
}

func stripInternalKeys(data any) any {
	switch v := data.(type) {
	case []map[string]any:
		result := make([]map[string]any, len(v))
		for i, row := range v {
			cleaned := make(map[string]any, len(row))
			for k, val := range row {
				if k != "_column_order" {
					cleaned[k] = val
				}
			}
			result[i] = cleaned
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = stripInternalKeys(item)
		}
		return result
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for k, val := range v {
			if k != "_column_order" {
				cleaned[k] = val
			}
		}
		return cleaned
	}
	return data
}

func isTextMIME(mimeType string) bool {
	if mimeType == "" {
		return true
	}
	textTypes := []string{
		"text/", "application/json", "application/xml",
		"application/javascript", "application/yaml",
		"application/x-yaml",
	}
	for _, prefix := range textTypes {
		if strings.HasPrefix(mimeType, prefix) {
			return true
		}
	}
	if strings.HasSuffix(mimeType, "+json") || strings.HasSuffix(mimeType, "+xml") {
		return true
	}
	return false
}
