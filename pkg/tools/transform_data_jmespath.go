// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
)

type TransformDataWithJMESPathTool struct {
	tool.SideEffectingTool
	artifactToolBase
	config map[string]any
}

func NewTransformDataWithJMESPathTool(config map[string]any) *TransformDataWithJMESPathTool {
	return &TransformDataWithJMESPathTool{config: config}
}

func (t *TransformDataWithJMESPathTool) Name() string { return "transform_data_with_jmespath" }

func (t *TransformDataWithJMESPathTool) Description() string {
	return `Apply a JMESPath expression to a JSON, YAML or CSV artifact (CSV rows become objects keyed by header, with string values: compare with to_number(price)) and save the result as a JSON artifact. The expression starts from the data's top level: for {"users": [...]} use users[?active].name. Write numbers in backticks ([?price < ` + "`10`" + `]) and strings in single quotes ([?status == 'open']); double quotes name a field. JMESPath can test a string (contains, starts_with, ends_with) but cannot cut or rewrite one: it has no substring, split or replace function, and a slice such as name[0:4] works on arrays only and gives null for a string. To cut or reformat text, load the data with query_data_with_sql and use substr, instr or replace.`
}

func (t *TransformDataWithJMESPathTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_filename": map[string]any{
				"type":        "string",
				"description": "Filename of the input JSON, YAML, or CSV artifact (with optional ':version' suffix).",
			},
			"jmespath_expression": map[string]any{
				"type":        "string",
				"description": "JMESPath expression string. Example: users[?active].name",
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "Desired filename for the output JSON artifact. Auto-generated if omitted.",
				"nullable":    true,
			},
			"result_description": map[string]any{
				"type":        "string",
				"description": "Optional description for the result artifact's metadata.",
				"nullable":    true,
			},
		},
		Required: []string{"input_filename", "jmespath_expression"},
	}
}

const (
	maxJMESPathExpressionLen   = 4096
	maxJMESPathExpressionDepth = 32
)

const emptyAggregateExample = "{count: length(xs), avg: avg(xs)}"

func (t *TransformDataWithJMESPathTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "output_filename", typ: "string",
			description: "Name of the artifact holding the full transformed result."},
		resultField{name: "output_version", typ: "integer",
			description: "Version of the saved result artifact."},
		resultField{name: "status", typ: "string",
			description: "Always \"success\" on this path."},
		resultField{name: "message", typ: "string",
			description: "Human-readable summary of the transformation."},
		resultField{name: "result_preview", anyType: true,
			description: "Preview of the transformed value. Its shape is whatever the JMESPath expression produced — scalar, array or object. Read the artifact for the full result."},
		resultField{name: "result_truncated", typ: "boolean",
			description: "True when result_preview was clipped."},
		resultField{name: "result_rows", typ: "integer", optional: true,
			description: "Present only when the expression produced a list."},
	)
}

func (t *TransformDataWithJMESPathTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	inputFilename, _ := args["input_filename"].(string)
	if inputFilename == "" {
		return invalidArgument("input_filename is required"), nil
	}
	expression, _ := args["jmespath_expression"].(string)
	if expression == "" {
		return invalidArgument("jmespath_expression is required"), nil
	}
	if len(expression) > maxJMESPathExpressionLen {
		return tool.Error(fmt.Sprintf("jmespath_expression too long (%d bytes; max %d)", len(expression), maxJMESPathExpressionLen), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}
	if depth := maxJMESPathDepth(expression); depth > maxJMESPathExpressionDepth {
		return tool.Error(fmt.Sprintf("jmespath_expression too deeply nested (%d levels; max %d)", depth, maxJMESPathExpressionDepth), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}
	outputFilename, _ := args["output_filename"].(string)
	resultDescription, _ := args["result_description"].(string)

	facade := toolCtx.Facade()

	artFilename, version := parseFilenameVersion(inputFilename)

	content, mimeType, err := facade.LoadArtifact(ctx, artFilename, version)
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to load artifact '%s': %s", inputFilename, err)), nil
	}
	if content == nil {
		return missingArtifactError(ctx, facade, artFilename, version), nil
	}

	mimeType = artifact.ResolveDataMIME(artFilename, mimeType, content)

	data, parseErr := parseInputForJMESPath(content, mimeType)
	if parseErr != nil {
		return tool.Error(fmt.Sprintf("Failed to parse '%s': %s", inputFilename, parseErr)), nil
	}

	compiled, compileErr := jmespath.Compile(expression)
	if compileErr != nil {
		topKeys := describeTopLevel(data)
		return tool.Error(fmt.Sprintf("Invalid JMESPath expression '%s': %s. The top-level structure of '%s' is: %s.",
			expression, compileErr, artFilename, topKeys), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}

	result, jmesErr := safeJMESPathSearch(compiled, data)
	if jmesErr != nil {
		topKeys := describeTopLevel(data)
		return tool.Error(fmt.Sprintf("JMESPath expression '%s' failed during evaluation against '%s' (top-level structure: %s). Adjust the expression to match this structure.",
			expression, artFilename, topKeys), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}

	if f, ok := result.(float64); ok && isNonFinite(f) {
		return tool.Error(fmt.Sprintf("JMESPath expression '%s' evaluated to a non-finite number (NaN or Infinity). Possible causes: avg() of an empty list (including a filter or field name that matches no records), to_number() of a \"NaN\" or \"Infinity\" string, NaN or Infinity values in the input data, or numeric overflow. If the cause may be an empty list, return the count alongside the aggregate, e.g. %s, and treat a count of 0 as no values; if the source list is not empty, check the field names.",
			expression, emptyAggregateExample), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}

	if result == nil {
		topKeys := describeTopLevel(data)
		return tool.Error(fmt.Sprintf("JMESPath expression '%s' returned null (no match). The top-level structure of '%s' is: %s. Check that the expression path matches the data structure.",
			expression, artFilename, topKeys), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}

	result, nullified := NullifyNonFinite(result)

	resultJSON, marshalErr := json.MarshalIndent(result, "", "  ")
	if marshalErr != nil {
		return tool.Error(fmt.Sprintf("Failed to serialize result: %s", marshalErr)), nil
	}

	userNamedOutput := outputFilename != ""

	if outputFilename == "" {
		shortID := idgen.NewRandom()[:8]
		outputFilename = fmt.Sprintf("jmespath_result_%s.json", shortID)
	} else {
		outputFilename = sanitizeFilename(outputFilename)
		if !strings.HasSuffix(strings.ToLower(outputFilename), ".json") {
			outputFilename += ".json"
		}
	}

	var sourceVersion int
	if version != nil {
		sourceVersion = *version
	} else {
		if versions, vErr := facade.ListArtifactVersions(ctx, artFilename); vErr == nil && len(versions) > 0 {
			sourceVersion = versions[len(versions)-1]
		}
	}

	metaDesc := resultDescription
	if metaDesc == "" {
		metaDesc = fmt.Sprintf("Result of JMESPath transform on %s. JMESPath Expression used: `%s`", artFilename, expression)
	}

	var tags []string
	if !userNamedOutput {
		tags = []string{a2a.ArtifactTagWorking}
	}

	saveResult, saveErr := facade.SaveArtifactWithMetadata(ctx, outputFilename, resultJSON, "application/json", artifact.MetadataOpts{
		Description: metaDesc,
		Custom: map[string]any{
			"source_artifact":            artFilename,
			"source_artifact_version":    sourceVersion,
			"source_jmespath_expression": expression,
		},
		Tags: tags,
	})
	if saveErr != nil {
		return tool.Error(fmt.Sprintf("Failed to save result artifact: %s", saveErr)), nil
	}

	maxPreviewRows := layeredIntConfig(toolCtx, t.config, "max_result_preview_rows", defaultMaxResultPreviewRows)
	maxPreviewBytes := layeredIntConfig(toolCtx, t.config, "max_result_preview_bytes", defaultMaxResultPreviewBytes)
	preview, truncated := truncateJMESPathResult(result, maxPreviewRows, maxPreviewBytes)

	nullifiedNote := nonFiniteResultNote(nullified, "json")
	respData := map[string]any{
		"status":           "success",
		"message":          fmt.Sprintf("JMESPath expression applied successfully to '%s'.%s", artFilename, nullifiedNote),
		"output_filename":  outputFilename,
		"output_version":   saveResult.DataVersion,
		"result_preview":   preview,
		"result_truncated": truncated,
	}
	if arr, ok := result.([]any); ok {
		respData["result_rows"] = len(arr)
	}

	return tool.OK(
		fmt.Sprintf("JMESPath expression executed successfully. Full result saved as '%s' v%d.%s", outputFilename, saveResult.DataVersion, nullifiedNote),
		tool.WithData(respData),
		tool.WithWithheldBytes(withheldBytes(resultJSON, preview, truncated)),
	), nil
}

func parseInputForJMESPath(content []byte, mimeType string) (any, error) {
	switch {
	case artifact.IsJSONMIME(mimeType):
		var data any
		if err := json.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return data, nil

	case artifact.IsYAMLMIME(mimeType):
		var data any
		if err := yaml.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
		return NormalizeYAMLForJMESPath(data), nil

	case artifact.IsCSVMIME(mimeType):
		return parseCSVForJMESPath(content)

	default:
		return nil, fmt.Errorf("unsupported file type '%s'. transform_data_with_jmespath only works with JSON, YAML, or CSV files. Check the file extension and MIME type, or use load_artifact to inspect the file content", mimeType)
	}
}

func maxJMESPathDepth(s string) int {
	depth, max := 0, 0
	for _, r := range s {
		switch r {
		case '(', '[', '{', '!', '&':
			depth++
			if depth > max {
				max = depth
			}
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return max
}

func safeJMESPathSearch(compiled *jmespath.JMESPath, data any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("expression evaluation aborted: %v", r)
		}
	}()
	return compiled.Search(data)
}

func describeTopLevel(data any) string {
	switch d := data.(type) {
	case map[string]any:
		if len(d) == 0 {
			return "empty object {}"
		}
		keys := make([]string, 0, len(d))
		for k, v := range d {
			switch v.(type) {
			case []any:
				keys = append(keys, k+" (array)")
			case map[string]any:
				keys = append(keys, k+" (object)")
			default:
				keys = append(keys, k)
			}
			if len(keys) >= 10 {
				keys = append(keys, fmt.Sprintf("... and %d more keys", len(d)-10))
				break
			}
		}
		return "{" + strings.Join(keys, ", ") + "}"
	case []any:
		if len(d) == 0 {
			return "empty array []"
		}
		switch d[0].(type) {
		case map[string]any:
			return fmt.Sprintf("array of %d objects", len(d))
		default:
			return fmt.Sprintf("array of %d elements", len(d))
		}
	default:
		return fmt.Sprintf("scalar value (%T)", data)
	}
}

func parseCSVForJMESPath(data []byte) (any, error) {
	cleanData := tryDecodeCSV(data)
	r := csv.NewReader(bytes.NewReader(cleanData))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	var rows []any
	for {
		record, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read CSV row: %w", readErr)
		}
		row := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(record) && record[i] != "" {
				row[h] = record[i]
			} else {
				row[h] = nil
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func truncateJMESPathResult(result any, maxRows, maxBytes int) (any, bool) {
	if arr, ok := result.([]any); ok {
		return truncatePreviewList(arr, maxRows, maxBytes)
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return result, false
	}
	if len(resultJSON) > maxBytes {
		truncStr := string(resultJSON[:maxBytes]) + "... [truncated]"
		return truncStr, true
	}
	return result, false
}
