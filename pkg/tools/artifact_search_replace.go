// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type SearchAndReplaceInArtifactTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *SearchAndReplaceInArtifactTool) Name() string {
	return "artifact_search_and_replace_regex"
}

func (t *SearchAndReplaceInArtifactTool) Description() string {
	return "Replaces text in an artifact, literal or regex, one search or a 'replacements' batch, and saves a new version. Regex uses Go RE2 syntax (no lookahead, lookbehind or backreferences). In a 'replacements' batch every match is replaced."
}

func (t *SearchAndReplaceInArtifactTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"filename": map[string]any{
				"type":        "string",
				"description": "Artifact filename, optionally 'name:version'.",
			},
			"search_expression": map[string]any{
				"type":        "string",
				"description": "Text or pattern to find; a regex must be valid Go RE2 (no lookahead, lookbehind or backreferences). Without the g flag only the first regex match is replaced; in a 'replacements' batch every match is replaced.",
			},
			"replace_expression": map[string]any{
				"type":        "string",
				"description": "Replacement; $1, $2 for groups, $$ for a dollar sign.",
			},
			"is_regexp": map[string]any{
				"type":        "boolean",
				"description": "Treat search_expression as a regex.",
			},
			"regexp_flags": map[string]any{
				"type":        "string",
				"description": "Any of g, i, m, s.",
			},
			"new_filename": map[string]any{
				"type":        "string",
				"description": "Save under this name instead.",
			},
			"new_description": map[string]any{
				"type":        "string",
				"description": "Description for the result.",
			},
			"replacements": map[string]any{
				"type":        "array",
				"description": "Several replacements in one call.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"search":       map[string]any{"type": "string"},
						"replace":      map[string]any{"type": "string"},
						"is_regexp":    map[string]any{"type": "boolean"},
						"regexp_flags": map[string]any{"type": "string"},
					},
					"required": []string{"search", "replace", "is_regexp"},
				},
			},
		},
		Required: []string{"filename"},
	}
}

func (t *SearchAndReplaceInArtifactTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{
		"filename": {IsList: false, IsOptional: false},
	}
}

func (t *SearchAndReplaceInArtifactTool) ArtifactNotFoundHint() string {
	return createArtifactHint
}

func (t *SearchAndReplaceInArtifactTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "filename", typ: "string",
			description: "Name of the artifact that was read. Not the output filename, even when new_filename was supplied."},
		resultField{name: "version", typ: "integer",
			description: "Version of the artifact that was read."},
		resultField{name: "source_filename", typ: "string", optional: true,
			description: "Duplicate of filename. Absent on the zero-match path."},
		resultField{name: "source_version", typ: "integer", optional: true,
			description: "Duplicate of version. Absent on the zero-match path."},
		resultField{name: "no_matches", typ: "boolean", optional: true,
			description: "True when the search matched nothing and the artifact was left unmodified. Single-replacement mode only; branch on this rather than on match_count."},
		resultField{name: "match_count", typ: "integer", optional: true,
			description: "Matches found. Single-replacement mode only; 0 together with no_matches on the zero-match path."},
		resultField{name: "replacements_made", typ: "integer", optional: true,
			description: "Replacements actually performed, which is 1 rather than match_count when only the first occurrence was replaced. Single-replacement mode only."},
		resultField{name: "total_replacements", typ: "integer", optional: true,
			description: "Number of operations REQUESTED, not performed. Batch mode only."},
		resultField{name: "replacement_results", typ: "array", optional: true,
			description: "One {search, match_count, status} record per operation. Batch mode only. Nil when an empty replacements array was passed."},
		resultField{name: "total_matches", typ: "integer", optional: true,
			description: "Matches summed across every operation. Batch mode only; 0 is a valid success here, unlike single mode."},
	)
}

func (t *SearchAndReplaceInArtifactTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	artObj, ok := args["filename"]
	if !ok {
		return invalidArgument("filename is required"), nil
	}
	art, ok := artObj.(*tool.Artifact)
	if !ok {
		filename, _ := artObj.(string)
		if filename == "" {
			return invalidArgument("filename is required"), nil
		}
		return tool.Error(fmt.Sprintf("artifact '%s' could not be loaded. Use list_artifacts to check available files and verify the filename is correct.%s", filename, createArtifactHint)), nil
	}

	content := art.AsText()
	sourceFilename := art.Filename
	sourceVersion := art.Version

	newFilename, _ := args["new_filename"].(string)
	outputFilename := sourceFilename
	if newFilename != "" {
		outputFilename = newFilename
	}

	newDescription, _ := args["new_description"].(string)

	replacementsRaw, hasBatch := args["replacements"]
	searchExpr, _ := args["search_expression"].(string)

	if hasBatch && replacementsRaw != nil && searchExpr != "" {
		return invalidArgument("Cannot provide both 'replacements' and 'search_expression'. Use one or the other."), nil
	}

	var resultContent string
	var resultData map[string]any

	if hasBatch && replacementsRaw != nil {
		replacements, ok := replacementsRaw.([]any)
		if !ok {
			return invalidArgument("'replacements' must be an array"), nil
		}
		var err error
		resultContent, resultData, err = executeBatchReplacements(content, replacements, sourceFilename, sourceVersion)
		if err != nil {
			return tool.Error(err.Error(), tool.WithData(resultData), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
		}
	} else {
		if searchExpr == "" {
			return invalidArgument("Either 'search_expression' or 'replacements' must be provided."), nil
		}
		replaceExpr, _ := args["replace_expression"].(string)
		isRegexp, _ := args["is_regexp"].(bool)
		regexpFlags, _ := args["regexp_flags"].(string)

		var err error
		resultContent, resultData, err = executeSingleReplacement(content, replacementSpec{
			search:   searchExpr,
			replace:  replaceExpr,
			isRegexp: isRegexp,
			flags:    regexpFlags,
		}, sourceFilename, sourceVersion)
		if err != nil {
			return tool.Error(err.Error(), tool.WithData(resultData), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
		}

		if matchCount, ok := resultData["match_count"].(int); ok && matchCount == 0 {
			return tool.Partial(
				fmt.Sprintf("No matches found for pattern '%s'. Artifact not modified.", searchExpr),
				tool.WithData(map[string]any{
					"filename":    sourceFilename,
					"version":     sourceVersion,
					"match_count": 0,
					"no_matches":  true,
				}),
			), nil
		}
	}

	resultBytes := []byte(resultContent)
	mimeType := art.MIMEType
	if mimeType == "" {
		mimeType = "text/plain"
	}

	dataObj := tool.DataObject{
		Name:        outputFilename,
		Content:     resultBytes,
		MIMEType:    mimeType,
		Disposition: tool.DispositionArtifact,
	}
	if newDescription != "" {
		dataObj.Description = newDescription
	}

	resultData["source_filename"] = sourceFilename
	resultData["source_version"] = sourceVersion

	msg := fmt.Sprintf("Successfully performed search and replace on '%s'.", sourceFilename)
	if _, ok := resultData["total_replacements"]; ok {
		msg = fmt.Sprintf("Batch replacement completed on '%s'.", sourceFilename)
	}

	return tool.OK(msg, tool.WithData(resultData), tool.WithDataObjects(dataObj)), nil
}

type replacementSpec struct {
	search              string
	replace             string
	isRegexp            bool
	flags               string
	replaceAllByDefault bool
}

func executeSingleReplacement(content string, spec replacementSpec, filename string, version int) (string, map[string]any, error) {
	data := map[string]any{
		"filename": filename,
		"version":  version,
	}

	if spec.isRegexp {
		return executeRegexReplacement(content, spec.search, spec.replace, spec.flags, data, spec.replaceAllByDefault)
	}
	return executeLiteralReplacement(content, spec.search, spec.replace, data)
}

func executeLiteralReplacement(content, search, replace string, data map[string]any) (string, map[string]any, error) {
	matchCount := strings.Count(content, search)
	data["match_count"] = matchCount
	data["replacements_made"] = matchCount

	if matchCount == 0 {
		return content, data, nil
	}

	result := strings.ReplaceAll(content, search, replace)
	return result, data, nil
}

func executeRegexReplacement(content, searchExpr, replaceExpr, flags string, data map[string]any, replaceAllByDefault bool) (string, map[string]any, error) {
	globalReplace := false
	var goFlags string
	for _, ch := range flags {
		switch ch {
		case 'g':
			globalReplace = true
		case 'i':
			goFlags += "(?i)"
		case 'm':
			goFlags += "(?m)"
		case 's':
			goFlags += "(?s)"
		}
	}

	pattern := goFlags + searchExpr
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", data, fmt.Errorf("invalid regular expression '%s': %v.%s", searchExpr, err, re2SyntaxHint)
	}

	matches := re.FindAllStringIndex(content, -1)
	matchCount := len(matches)
	data["match_count"] = matchCount

	if matchCount == 0 {
		data["replacements_made"] = 0
		return content, data, nil
	}

	if replaceAllByDefault {
		globalReplace = true
	}

	goReplace := convertJSReplaceToGo(replaceExpr)

	var result string
	if globalReplace {
		result = re.ReplaceAllString(content, goReplace)
		data["replacements_made"] = matchCount
	} else {
		loc := re.FindStringIndex(content)
		if loc != nil {
			matched := content[loc[0]:loc[1]]
			replacement := re.ReplaceAllString(matched, goReplace)
			result = content[:loc[0]] + replacement + content[loc[1]:]
		} else {
			result = content
		}
		data["replacements_made"] = 1
	}

	return result, data, nil
}

func convertJSReplaceToGo(expr string) string {
	const placeholder = "\x00DOLLAR\x00"
	result := strings.ReplaceAll(expr, "$$", placeholder)

	dollarNum := regexp.MustCompile(`\$(\d+)`)
	result = dollarNum.ReplaceAllString(result, "${$1}")

	result = strings.ReplaceAll(result, placeholder, "$")

	return result
}

func executeBatchReplacements(content string, replacements []any, filename string, version int) (string, map[string]any, error) {
	current := content
	var results []map[string]any
	totalMatches := 0

	for i, item := range replacements {
		rep, ok := item.(map[string]any)
		if !ok {
			for j := i; j < len(replacements); j++ {
				results = append(results, map[string]any{"status": "skipped"})
			}
			data := map[string]any{
				"filename":            filename,
				"version":             version,
				"replacement_results": results,
			}
			return "", data, fmt.Errorf("replacement[%d]: must be an object with 'search', 'replace', 'is_regexp'", i)
		}

		search, _ := rep["search"].(string)
		replace, _ := rep["replace"].(string)
		isRegexp, _ := rep["is_regexp"].(bool)
		flags, _ := rep["regexp_flags"].(string)

		newContent, opData, err := executeSingleReplacement(current, replacementSpec{
			search:              search,
			replace:             replace,
			isRegexp:            isRegexp,
			flags:               flags,
			replaceAllByDefault: true,
		}, filename, version)
		if err != nil {
			results = append(results, map[string]any{
				"search": search,
				"status": "error",
				"error":  err.Error(),
			})
			for j := i + 1; j < len(replacements); j++ {
				results = append(results, map[string]any{"status": "skipped"})
			}
			data := map[string]any{
				"filename":            filename,
				"version":             version,
				"replacement_results": results,
				"failed_replacement":  map[string]any{"index": i, "search": search, "error": err.Error()},
			}
			return "", data, fmt.Errorf("batch replacement failed at operation %d: %v", i, err)
		}

		matchCount, _ := opData["match_count"].(int)
		totalMatches += matchCount
		results = append(results, map[string]any{
			"search":      search,
			"match_count": matchCount,
			"status":      "success",
		})
		current = newContent
	}

	data := map[string]any{
		"filename":            filename,
		"version":             version,
		"total_replacements":  len(replacements),
		"replacement_results": results,
		"total_matches":       totalMatches,
	}

	return current, data, nil
}
