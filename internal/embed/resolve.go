// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
)

const (
	DefaultMaxArtifactSizeBytes = 100 * 1024 * 1024

	DefaultMaxTotalSizeBytes = 150 * 1024 * 1024

	truncationMarkerArtifactSize = "[Truncated: artifact size limit reached]"

	truncationMarkerTotalSize = "[Truncated: total size limit reached]"

	TruncationMarkerRenderDeadline = "[Truncated: render deadline exceeded]"
)

type ResolveConfig struct {
	MaxDepth             int
	MaxArtifactSizeBytes int
	MaxTotalSizeBytes    int
}

func DefaultResolveConfig() ResolveConfig {
	return ResolveConfig{
		MaxDepth:             12,
		MaxArtifactSizeBytes: DefaultMaxArtifactSizeBytes,
		MaxTotalSizeBytes:    DefaultMaxTotalSizeBytes,
	}
}

type ResolveContext struct {
	Artifacts *artifact.ScopedService
	AgentName string
	UserID    string
	SessionID string
	Config    ResolveConfig
	Logger    *slog.Logger
}

type ResolutionMode int

const (
	ModeA2AMessageToUser ResolutionMode = iota
	ModeToolParameter
	ModeRecursiveArtifactContent
)

var embedRegex = directive.InlineEmbedRegex

func ResolveEmbedsInString(
	ctx context.Context,
	text string,
	mode ResolutionMode,
	rctx *ResolveContext,
) (string, []Signal, error) {
	if rctx == nil {
		return text, nil, nil
	}

	var signals []Signal
	visited := make(map[string]bool)
	var totalSize int64

	resolved, err := resolveEmbedsInternal(ctx, text, mode, rctx, 0, visited, &totalSize, &signals)
	if err != nil {
		return text, signals, err
	}

	resolveEmbedsFn := func(ctx context.Context, t string) (string, error) {
		return resolveEmbedsInternal(ctx, t, mode, rctx, 1, visited, &totalSize, &signals)
	}

	resolved, err = ResolveTemplateBlocks(ctx, resolved, rctx.Artifacts, rctx.AgentName, rctx.UserID, rctx.SessionID, resolveEmbedsFn, rctx.Logger)
	if err != nil {
		rctx.Logger.Warn("template block resolution failed", "error", err)
	}

	return resolved, signals, nil
}

func resolveEmbedsInternal(
	ctx context.Context,
	text string,
	mode ResolutionMode,
	rctx *ResolveContext,
	depth int,
	visited map[string]bool,
	totalSize *int64,
	signals *[]Signal,
) (string, error) {
	if depth >= rctx.Config.MaxDepth {
		return "[Error: Max embed depth exceeded]", nil
	}

	matches := embedRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	result := text
	for i := len(matches) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		m := matches[i]
		fullStart, fullEnd := m[0], m[1]
		raw := text[fullStart:fullEnd]

		ref := agent.ParseEmbed(raw)
		if ref == nil {
			continue
		}

		if ref.IsEarly && ref.Type != "status_update" {
			if replacement, ok := resolveStatelessEarlyEmbed(ref); ok {
				result = result[:fullStart] + replacement + result[fullEnd:]
			}
			continue
		}

		replacement, err := resolveEmbed(ctx, ref, mode, rctx, depth, visited, totalSize, signals)
		if err != nil {
			rctx.Logger.With("logger_name", "embed").Warn("embed resolution failed",
				"type", ref.Type, "artifact", ref.Params["value"], "error", err)
			replacement = fmt.Sprintf("[Embed Error: %s]", err)
		}

		result = result[:fullStart] + replacement + result[fullEnd:]
	}

	return result, nil
}

func resolveStatelessEarlyEmbed(ref *agent.EmbedRef) (string, bool) {
	switch ref.Type {
	case "math":
		result, _ := agent.ResolveMathExpression(ref.Params["value"])
		return result, true
	case "datetime":
		result, _ := agent.ResolveDatetimeExpression(ref.Params["value"], "")
		return result, true
	case "uuid":
		return agent.ResolveUUIDExpression(), true
	default:
		return "", false
	}
}

func resolveEmbed(
	ctx context.Context,
	ref *agent.EmbedRef,
	mode ResolutionMode,
	rctx *ResolveContext,
	depth int,
	visited map[string]bool,
	totalSize *int64,
	signals *[]Signal,
) (string, error) {
	switch ref.Type {
	case "artifact_content":
		return resolveArtifactContent(ctx, ref, mode, rctx, depth, visited, totalSize, signals)

	case "artifact_return":
		return resolveArtifactReturn(ref, mode, signals)

	case "status_update":
		return resolveStatusUpdate(ref, mode, signals)

	default:
		return ref.Raw, nil
	}
}

func resolveArtifactContent(
	ctx context.Context,
	ref *agent.EmbedRef,
	mode ResolutionMode,
	rctx *ResolveContext,
	depth int,
	visited map[string]bool,
	totalSize *int64,
	signals *[]Signal,
) (string, error) {
	expression := ref.Params["value"]
	if expression == "" {
		return "", fmt.Errorf("artifact_content: empty expression")
	}

	artifactSpec, modifiers, outputFormat := ParseModifierChain(expression)

	if rctx.Artifacts == nil {
		return "", fmt.Errorf("no artifact service available")
	}

	filename, version := agent.ParseArtifactSpec(artifactSpec)

	visitKey := filename
	if version != nil {
		visitKey = fmt.Sprintf("%s:%d", filename, *version)
	}
	if visited[visitKey] {
		return fmt.Sprintf("[Error: Circular embed reference detected for '%s']", filename), nil
	}

	loc := rctx.Artifacts.Resolve(rctx.AgentName, rctx.UserID, rctx.SessionID, filename)

	content, mimeType, err := rctx.Artifacts.Inner.Load(ctx, loc, version)
	if err != nil {
		return "", fmt.Errorf("load artifact '%s': %w", filename, err)
	}
	if content == nil {
		return "", fmt.Errorf("Artifact '%s' not found", filename)
	}

	if rctx.Config.MaxArtifactSizeBytes > 0 && len(content) > rctx.Config.MaxArtifactSizeBytes {
		rctx.Logger.Warn("artifact exceeds per-artifact size cap; truncating",
			"filename", filename, "size", len(content), "cap", rctx.Config.MaxArtifactSizeBytes)
		return truncationMarkerArtifactSize, nil
	}

	if rctx.Config.MaxTotalSizeBytes > 0 && totalSize != nil {
		if *totalSize+int64(len(content)) > int64(rctx.Config.MaxTotalSizeBytes) {
			rctx.Logger.Warn("cumulative resolved bytes would exceed cap; truncating",
				"filename", filename, "runningTotal", *totalSize,
				"artifactSize", len(content), "cap", rctx.Config.MaxTotalSizeBytes)
			*totalSize = int64(rctx.Config.MaxTotalSizeBytes)
			return truncationMarkerTotalSize, nil
		}
		*totalSize += int64(len(content))
	}

	var textContent string
	isText := IsTextBasedMIME(mimeType)
	if isText {
		textContent = string(content)

		visitedCopy := copyVisited(visited)
		visitedCopy[visitKey] = true

		var resolved string
		resolved, err = resolveEmbedsInternal(ctx, textContent, ModeRecursiveArtifactContent, rctx, depth+1, visitedCopy, totalSize, signals)
		if err != nil {
			rctx.Logger.Warn("recursive resolution in artifact failed", "filename", filename, "error", err)
		} else {
			textContent = resolved
		}

		resolveEmbedsFn := func(ctx context.Context, t string) (string, error) {
			return resolveEmbedsInternal(ctx, t, ModeRecursiveArtifactContent, rctx, depth+1, visitedCopy, totalSize, signals)
		}
		resolved, err = ResolveTemplateBlocks(ctx, textContent, rctx.Artifacts, rctx.AgentName, rctx.UserID, rctx.SessionID, resolveEmbedsFn, rctx.Logger)
		if err != nil {
			rctx.Logger.Warn("template resolution in artifact failed", "filename", filename, "error", err)
		} else {
			textContent = resolved
		}
	}

	var data any
	var format DataFormat

	if isText {
		needsStructured := len(modifiers) > 0 || (outputFormat != "" && !strings.EqualFold(outputFormat, "text"))
		if needsStructured {
			data, format = preParseByMIME(textContent, mimeType)
		} else {
			data, format = textContent, FormatString
		}
	} else {
		data = content
		format = FormatBytes
	}

	if len(modifiers) > 0 {
		applyTemplate := func(ctx context.Context, templateData any, templateSpec string, dataMIME string) (string, error) {
			return applyToTemplate(ctx, templateData, templateSpec, dataMIME, rctx, depth, visited, totalSize, signals)
		}

		var modified any
		var newFormat DataFormat
		modified, newFormat, err = ExecuteChain(ctx, data, format, mimeType, modifiers, applyTemplate, rctx.Logger)
		if err != nil {
			return "", fmt.Errorf("modifier chain failed: %w", err)
		}
		data = modified
		format = newFormat
	}

	if format == FormatBytes && mode == ModeA2AMessageToUser && outputFormat == "" {
		b, ok := data.([]byte)
		if ok {
			*signals = append(*signals, Signal{
				Type: SignalInlineBinaryContent,
				Data: InlineBinaryData{
					Bytes:    b,
					MIMEType: mimeType,
					Name:     filename,
				},
			})
			return "", nil
		}
	}

	serialized, err := SerializeData(data, format, outputFormat, mimeType)
	if err != nil {
		return "", fmt.Errorf("serialization failed: %w", err)
	}

	if mode == ModeA2AMessageToUser && strings.HasSuffix(filename, "_report.md") {
		*signals = append(*signals, Signal{
			Type: SignalDeepResearchReport,
			Data: DeepResearchReportData{
				Filename: filename,
				Content:  serialized,
			},
		})
	}

	return serialized, nil
}

func preParseByMIME(text string, mimeType string) (any, DataFormat) {
	lower := strings.ToLower(mimeType)

	if strings.Contains(lower, "json") {
		var parsed any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			return parsed, FormatJSON
		}
	}

	if strings.Contains(lower, "yaml") || strings.Contains(lower, "yml") {
		var parsed any
		if err := yaml.Unmarshal([]byte(text), &parsed); err == nil {
			return parsed, FormatJSON
		}
	}

	if strings.Contains(lower, "csv") {
		lod, err := csvToListOfDicts(text)
		if err == nil && len(lod) > 0 {
			result := make([]any, len(lod))
			for i, d := range lod {
				result[i] = d
			}
			return result, FormatJSON
		}
	}

	return text, FormatString
}

func applyToTemplate(
	ctx context.Context,
	data any,
	templateSpec string,
	mimeType string,
	rctx *ResolveContext,
	depth int,
	visited map[string]bool,
	totalSize *int64,
	signals *[]Signal,
) (string, error) {
	if rctx.Artifacts == nil {
		return "", fmt.Errorf("no artifact service available for apply_to_template")
	}

	filename, version := agent.ParseArtifactSpec(templateSpec)
	loc := rctx.Artifacts.Resolve(rctx.AgentName, rctx.UserID, rctx.SessionID, filename)

	content, _, err := rctx.Artifacts.Inner.Load(ctx, loc, version)
	if err != nil {
		return "", fmt.Errorf("failed to load template artifact '%s': %w", filename, err)
	}
	if content == nil {
		return "", fmt.Errorf("template artifact '%s' not found", filename)
	}

	templateCtx := agent.StructureForTemplate(data)

	engine := liquidsafe.NewEngine()
	rendered, err := liquidsafe.RenderStringWithDeadline(engine, string(content), templateCtx, liquidsafe.DefaultRenderDeadline)
	if errors.Is(err, liquidsafe.ErrRenderDeadlineExceeded) {
		rctx.Logger.Warn("apply_to_template render deadline exceeded; truncating",
			"template", filename)
		return TruncationMarkerRenderDeadline, nil
	}
	if err != nil {
		return "", fmt.Errorf("template rendering failed: %w", err)
	}

	visitedCopy := copyVisited(visited)

	resolved, err := resolveEmbedsInternal(ctx, rendered, ModeRecursiveArtifactContent, rctx, depth+1, visitedCopy, totalSize, signals)
	if err != nil {
		rctx.Logger.Warn("recursive resolution in template output failed", "error", err)
		return rendered, nil
	}

	return resolved, nil
}

func resolveArtifactReturn(ref *agent.EmbedRef, mode ResolutionMode, signals *[]Signal) (string, error) {
	if mode == ModeA2AMessageToUser {
		spec := ref.Params["value"]
		filename, _ := agent.ParseArtifactSpec(spec)

		*signals = append(*signals, Signal{
			Type: SignalArtifactReturn,
			Data: ArtifactReturnData{
				Filename: filename,
				Version:  spec,
			},
		})
		return "", nil
	}

	return ref.Raw, nil
}

func resolveStatusUpdate(ref *agent.EmbedRef, mode ResolutionMode, signals *[]Signal) (string, error) {
	if mode == ModeA2AMessageToUser {
		msg := ref.Params["value"]
		*signals = append(*signals, Signal{
			Type: SignalStatusUpdate,
			Data: msg,
		})
		return "", nil
	}

	return ref.Raw, nil
}

func copyVisited(visited map[string]bool) map[string]bool {
	cp := make(map[string]bool, len(visited)+1)
	for k, v := range visited {
		cp[k] = v
	}
	return cp
}
