// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/osteele/liquid"
	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
)

const TemplateRenderDeadlineExceededMarker = "[Truncated: render deadline exceeded]"

type TemplateRenderer struct {
	artifacts *artifact.ScopedService
	agentName string
	userID    string
	sessionID string
	logger    *slog.Logger
	engine    *liquid.Engine
}

func NewTemplateRenderer(arts *artifact.ScopedService, agentName, userID, sessionID string, logger *slog.Logger) *TemplateRenderer {
	return &TemplateRenderer{
		artifacts: arts,
		agentName: agentName,
		userID:    userID,
		sessionID: sessionID,
		logger:    logger,
		engine:    liquidsafe.NewEngine(),
	}
}

const MaxTemplateDataSize = 2_000_000

type templateErrorKind string

const (
	templateErrorMissingData       templateErrorKind = "missing_data"
	templateErrorNoArtifactService templateErrorKind = "no_artifact_service"
	templateErrorNotFound          templateErrorKind = "not_found"
	templateErrorLoad              templateErrorKind = "load"
	templateErrorTooLarge          templateErrorKind = "too_large"
	templateErrorDataParse         templateErrorKind = "data_parse"
	templateErrorJSONPath          templateErrorKind = "jsonpath"
	templateErrorRender            templateErrorKind = "render"
	templateErrorUnknown           templateErrorKind = "unknown"
)

type templateRenderError struct {
	kind templateErrorKind
	err  error
}

func (e *templateRenderError) Error() string { return e.err.Error() }
func (e *templateRenderError) Unwrap() error { return e.err }

func wrapTemplateErr(kind templateErrorKind, err error) error {
	return &templateRenderError{kind: kind, err: err}
}

func templateRenderErrorKind(err error) templateErrorKind {
	if tre, ok := errors.AsType[*templateRenderError](err); ok {
		return tre.kind
	}
	return templateErrorUnknown
}

func TemplateErrorKind(err error) string {
	return string(templateRenderErrorKind(err))
}

func templateErrorText(kind templateErrorKind, filename string) string {
	switch kind {
	case templateErrorMissingData:
		return "[Template Error: Missing 'data' parameter]"
	case templateErrorNoArtifactService:
		return "[Template Error: No artifact service available]"
	case templateErrorLoad:
		return fmt.Sprintf("[Template Error: Failed to load data artifact '%s']", filename)
	case templateErrorNotFound:
		return fmt.Sprintf("[Template Error: Data artifact '%s' not found]", filename)
	case templateErrorTooLarge:
		return fmt.Sprintf("[Template Error: Data artifact '%s' exceeds size limit (%d bytes)]", filename, MaxTemplateDataSize)
	case templateErrorDataParse:
		return fmt.Sprintf("[Template Error: Data artifact '%s' could not be parsed]", filename)
	case templateErrorJSONPath:
		return fmt.Sprintf("[Template Error: The jsonpath filter on data artifact '%s' failed]", filename)
	default:
		return fmt.Sprintf("[Template Error: Rendering against '%s' failed]", filename)
	}
}

func (tr *TemplateRenderer) RenderBlock(ctx context.Context, block *FencedBlockEvent) (string, error) {
	filename, _ := ParseArtifactSpec(block.DataSource)
	fail := func(kind templateErrorKind, err error) (string, error) {
		return templateErrorText(kind, filename), wrapTemplateErr(kind, err)
	}
	if block.DataSource == "" {
		return fail(templateErrorMissingData, errors.New("missing data parameter"))
	}
	if tr.artifacts == nil {
		return fail(templateErrorNoArtifactService, errors.New("no artifact service"))
	}

	data, kind, err := loadTemplateData(ctx, tr.artifacts, tr.agentName, tr.userID, tr.sessionID, block.DataSource)
	if err != nil {
		return fail(kind, err)
	}

	templateCtx, parseErr, err := prepareResolvedTemplateContext(data, block.JSONPath, block.Limit)
	if err != nil {
		return templateErrorText(templateRenderErrorKind(err), filename), err
	}

	engine := tr.engine
	if parseErr != nil {
		engine = liquidsafe.NewStrictEngine()
	}

	rendered, err := liquidsafe.RenderStringWithDeadline(engine, block.Content, templateCtx, liquidsafe.DefaultRenderDeadline)
	if parseErr != nil && !errors.Is(err, liquidsafe.ErrRenderDeadlineExceeded) {
		if err != nil {
			return templateErrorText(templateRenderErrorKind(parseErr), filename), parseErr
		}
		tr.logger.Debug("template data parse failed; rendered from the text context",
			"dataArtifact", filename,
			"storedMime", data.storedMIME,
			"resolvedMime", data.mimeType,
			"errorKind", string(templateRenderErrorKind(parseErr)),
		)
		return rendered, nil
	}
	if errors.Is(err, liquidsafe.ErrRenderDeadlineExceeded) {
		tr.logger.Warn("template render deadline exceeded; truncating",
			"dataArtifact", block.DataSource)
		return TemplateRenderDeadlineExceededMarker, nil
	}
	if err != nil {
		return fail(templateErrorRender, err)
	}

	tr.logger.Debug("template rendered",
		"dataArtifact", block.DataSource,
		"outputLen", len(rendered),
	)

	return rendered, nil
}

func rawTemplateBlock(block *FencedBlockEvent) string {
	raw := rawUnclosedTemplateBlock(block)
	if block.Content != "" && !strings.HasSuffix(block.Content, "\n") {
		raw += "\n"
	}
	return raw + BlockDelimiterClose + "\n"
}

func rawUnclosedTemplateBlock(block *FencedBlockEvent) string {
	params := []string{templateParam("data", block.DataSource)}
	if block.JSONPath != "" {
		params = append(params, templateParam("jsonpath", block.JSONPath))
	}
	if block.Limit >= 0 {
		params = append(params, fmt.Sprintf(`limit="%d"`, block.Limit))
	}
	return BlockPrefixTemplateLiquid + " " + strings.Join(params, " ") + "\n" + block.Content
}

func templateParam(key, value string) string {
	return key + `="` + value + `"`
}

func validateTemplateSyntax(content string) error {
	engine := liquidsafe.NewEngine()
	_, err := engine.ParseString(content)
	return err
}

var indexAccessPattern = regexp.MustCompile(`(\w+)\[(\d+)\]`)

func validateTemplateIndexBounds(ctx context.Context, block *FencedBlockEvent, arts *artifact.ScopedService, agentName, userID, sessionID string) error {
	if block.DataSource == "" || arts == nil {
		return nil
	}

	data, _, err := loadTemplateData(ctx, arts, agentName, userID, sessionID, block.DataSource)
	if err != nil {
		return nil
	}

	bounds := dataArrayBounds(string(data.content), data.mimeType)
	if len(bounds) == 0 {
		return nil
	}

	matches := indexAccessPattern.FindAllStringSubmatch(block.Content, -1)
	if len(matches) == 0 {
		return nil
	}

	for _, m := range matches {
		varName := m[1]
		idx, _ := strconv.Atoi(m[2])

		if maxLen, ok := bounds[varName]; ok && idx >= maxLen {
			return fmt.Errorf(
				"template uses %s[%d] but the data only has %d columns (valid indices: 0-%d, 0-indexed). "+
					"Arrays in Liquid templates are 0-indexed: use %s[0] for the first element, %s[%d] for the last",
				varName, idx, maxLen, maxLen-1,
				varName, varName, maxLen-1)
		}
	}

	return nil
}

func dataArrayBounds(data, mimeType string) map[string]int {
	bounds := make(map[string]int)
	mimeType = strings.ToLower(mimeType)

	isCSV := strings.Contains(mimeType, "csv")
	if isCSV {
		reader := csv.NewReader(strings.NewReader(data))
		headers, err := reader.Read()
		if err == nil && len(headers) > 0 {
			bounds["row"] = len(headers)
		}
		return bounds
	}

	isJSON := strings.Contains(mimeType, "json")
	if isJSON {
		var parsed any
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return bounds
		}
		detectArrayBounds(parsed, bounds)
	}

	return bounds
}

func detectArrayBounds(data any, bounds map[string]int) {
	switch v := data.(type) {
	case []any:
		if len(v) > 0 {
			if inner, ok := v[0].([]any); ok {
				bounds["row"] = len(inner)
				bounds["item"] = len(inner)
			}
		}
	case map[string]any:
		if rows, ok := v["data_rows"].([]any); ok && len(rows) > 0 {
			if firstRow, ok := rows[0].([]any); ok {
				bounds["row"] = len(firstRow)
			}
		}
		if items, ok := v["items"].([]any); ok && len(items) > 0 {
			if firstItem, ok := items[0].([]any); ok {
				bounds["item"] = len(firstItem)
				bounds["row"] = len(firstItem)
			}
		}
	}
}

type templateData struct {
	content    []byte
	storedMIME string
	mimeType   string
}

func loadTemplateData(ctx context.Context, arts *artifact.ScopedService, agentName, userID, sessionID, dataSource string) (templateData, templateErrorKind, error) {
	filename, version := ParseArtifactSpec(dataSource)
	loc := arts.Resolve(agentName, userID, sessionID, filename)
	content, storedMIME, err := arts.Inner.Load(ctx, loc, version)
	if err != nil {
		return templateData{}, templateErrorLoad, err
	}
	if content == nil {
		return templateData{}, templateErrorNotFound, fmt.Errorf("artifact not found: %s", filename)
	}
	if len(content) > MaxTemplateDataSize {
		return templateData{}, templateErrorTooLarge, fmt.Errorf("data artifact is %d bytes", len(content))
	}
	return templateData{
		content:    content,
		storedMIME: storedMIME,
		mimeType:   artifact.ResolveDataMIME(filename, storedMIME, content),
	}, "", nil
}

func prepareResolvedTemplateContext(data templateData, jsonpath string, limit int) (templateCtx map[string]any, parseErr error, err error) {
	raw := string(data.content)
	if data.mimeType == data.storedMIME {
		templateCtx, err = PrepareTemplateContext(raw, data.mimeType, jsonpath, limit)
		return templateCtx, nil, err
	}
	templateCtx, structuredErr := PrepareTemplateContext(raw, data.mimeType, jsonpath, limit)
	if structuredErr != nil {
		if templateRenderErrorKind(structuredErr) != templateErrorDataParse {
			return nil, nil, structuredErr
		}
		return map[string]any{"text": raw}, structuredErr, nil
	}
	if _, taken := templateCtx["text"]; !taken {
		templateCtx["text"] = raw
	}
	return templateCtx, nil, nil
}

func PrepareTemplateContext(data, mimeType, jsonpath string, limit int) (map[string]any, error) {
	var parsed any
	mimeType = strings.ToLower(mimeType)
	isCSV := strings.Contains(mimeType, "csv")
	isJSON := strings.Contains(mimeType, "json")
	isYAML := strings.Contains(mimeType, "yaml") || strings.Contains(mimeType, "yml")

	if isCSV {
		ctx, err := ParseCSVToContext(data)
		if err != nil {
			return nil, wrapTemplateErr(templateErrorDataParse, err)
		}
		parsed = ctx
	} else if isJSON {
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return nil, wrapTemplateErr(templateErrorDataParse, fmt.Errorf("failed to parse JSON data: %w", err))
		}
	} else if isYAML {
		if err := yaml.Unmarshal([]byte(data), &parsed); err != nil {
			return nil, wrapTemplateErr(templateErrorDataParse, fmt.Errorf("failed to parse YAML data: %w", err))
		}
	} else {
		parsed = map[string]any{"text": data}
	}

	if jsonpath != "" && jsonpath != "$" {
		filtered, err := ApplyJSONPath(parsed, jsonpath)
		if err != nil {
			return nil, wrapTemplateErr(templateErrorJSONPath, fmt.Errorf("JSONPath filter failed: %w", err))
		}
		parsed = filtered
	}

	ctx := StructureForTemplate(parsed)

	if limit > 0 {
		ApplyLimit(ctx, limit)
	}

	return ctx, nil
}

func ParseCSVToContext(data string) (map[string]any, error) {
	reader := csv.NewReader(strings.NewReader(data))
	var rows [][]string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV parsing failed: %w", err)
		}
		rows = append(rows, record)
	}

	if len(rows) == 0 {
		return map[string]any{"headers": []any{}, "data_rows": []any{}}, nil
	}

	headers := make([]any, len(rows[0]))
	for i, h := range rows[0] {
		headers[i] = h
	}

	dataRows := make([]any, 0, len(rows)-1)
	for _, row := range rows[1:] {
		r := make([]any, len(row))
		for i, v := range row {
			r[i] = v
		}
		dataRows = append(dataRows, r)
	}

	return map[string]any{
		"headers":   headers,
		"data_rows": dataRows,
	}, nil
}

func StructureForTemplate(parsed any) map[string]any {
	switch v := parsed.(type) {
	case map[string]any:
		if _, hasHeaders := v["headers"]; hasHeaders {
			if _, hasRows := v["data_rows"]; hasRows {
				return v
			}
		}
		return v
	case []any:
		return map[string]any{"items": v}
	default:
		return map[string]any{"value": v}
	}
}

func ApplyLimit(ctx map[string]any, limit int) {
	if rows, ok := ctx["data_rows"].([]any); ok && len(rows) > limit {
		ctx["data_rows"] = rows[:limit]
	}
	if items, ok := ctx["items"].([]any); ok && len(items) > limit {
		ctx["items"] = items[:limit]
	}
}
