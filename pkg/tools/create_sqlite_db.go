// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	_ "modernc.org/sqlite"
)

const (
	defaultSQLiteTableName = "data"

	sqliteDBMIMEType = "application/vnd.sqlite3"
)

type CreateSQLiteDBTool struct {
	tool.SideEffectingTool
	artifactToolBase
	config map[string]any
}

func NewCreateSQLiteDBTool(config map[string]any) *CreateSQLiteDBTool {
	return &CreateSQLiteDBTool{config: config}
}

func (t *CreateSQLiteDBTool) Name() string { return "create_sqlite_db" }

func (t *CreateSQLiteDBTool) Description() string {
	return "Converts a CSV or JSON artifact into an SQLite database artifact. " +
		"Input filename can include ':version'. Specify the desired output DB filename and " +
		"optionally the table name within the DB (default 'data'). Use this for large datasets " +
		"you plan to query multiple times with query_data_with_sql."
}

func (t *CreateSQLiteDBTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_filename": map[string]any{
				"type":        "string",
				"description": "The filename (and optional ':version') of the input CSV or JSON artifact.",
			},
			"output_db_filename": map[string]any{
				"type":        "string",
				"description": "The desired filename for the output SQLite database artifact.",
			},
			"table_name": map[string]any{
				"type":        "string",
				"description": "The name of the table to create in the SQLite database. Defaults to 'data'.",
				"nullable":    true,
			},
		},
		Required: []string{"input_filename", "output_db_filename"},
	}
}

func (t *CreateSQLiteDBTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "output_filename", typ: "string",
			description: "Name of the saved SQLite database artifact."},
		resultField{name: "output_version", typ: "integer",
			description: "Version of the saved database artifact."},
		resultField{name: "table_name", typ: "string",
			description: "Name of the table the rows were loaded into."},
		resultField{name: "row_count", typ: "integer",
			description: "Number of rows written to the table."},
		resultField{name: "status", typ: "string",
			description: "Always \"success\" on this path."},
		resultField{name: "message", typ: "string",
			description: "Human-readable summary of what was created."},
	)
}

func (t *CreateSQLiteDBTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	inputFilename, _ := args["input_filename"].(string)
	if inputFilename == "" {
		return tool.Error("input_filename is required"), nil
	}

	outputDBFilename, _ := args["output_db_filename"].(string)
	if outputDBFilename == "" {
		return tool.Error("output_db_filename is required"), nil
	}

	tableName, _ := args["table_name"].(string)
	if tableName == "" {
		tableName = defaultSQLiteTableName
	}

	facade := toolCtx.Facade()
	maxCols := layeredIntConfig(toolCtx, t.config, "max_columns", defaultMaxColumns)

	artFilename, versionPtr := parseFilenameVersion(inputFilename)

	content, mimeType, loadErr := facade.LoadArtifact(ctx, artFilename, versionPtr)
	if loadErr != nil {
		return tool.Error(fmt.Sprintf("Failed to load artifact '%s': %s", inputFilename, loadErr)), nil
	}
	if content == nil {
		return tool.Error(fmt.Sprintf("Artifact '%s' not found", inputFilename)), nil
	}
	mimeType = artifact.ResolveDataMIME(artFilename, mimeType, content)

	rows, parseErr := parseCSVOrJSONForSQLiteDB(content, mimeType, maxCols)
	if parseErr != nil {
		return tool.Error(capitalizeFirst(parseErr.Error())), nil
	}
	if len(rows) == 0 {
		return tool.Error(fmt.Sprintf("No data rows found in '%s'", inputFilename)), nil
	}

	dbBytes, writeErr := writeRowsToSQLiteBytes(ctx, rows, tableName)
	if writeErr != nil {
		return tool.Error(fmt.Sprintf("Failed to load data into SQLite table '%s': %s", tableName, writeErr)), nil
	}

	finalOutputFilename := sanitizeFilename(ensureCorrectExtension(outputDBFilename, "sqlite"))
	if finalOutputFilename == "" || !artifact.IsFilenameSafe(finalOutputFilename) {
		return invalidArgument(fmt.Sprintf("output_db_filename %q is not a valid artifact name", outputDBFilename)), nil
	}

	sourceVersion := 0
	if versionPtr != nil {
		sourceVersion = *versionPtr
	}

	saveResult, saveErr := facade.SaveArtifactWithMetadata(ctx, finalOutputFilename, dbBytes, sqliteDBMIMEType, artifact.MetadataOpts{
		Description: fmt.Sprintf("SQLite database created from %s", inputFilename),
		Custom: map[string]any{
			"source_artifact":         inputFilename,
			"source_artifact_version": sourceVersion,
			"table_name":              tableName,
		},
	})
	if saveErr != nil {
		return tool.Error(fmt.Sprintf("Failed to save SQLite DB artifact: %s", saveErr)), nil
	}

	data := map[string]any{
		"status":          "success",
		"message":         fmt.Sprintf("SQLite database '%s' v%d created successfully from '%s'.", finalOutputFilename, saveResult.DataVersion, inputFilename),
		"output_filename": finalOutputFilename,
		"output_version":  saveResult.DataVersion,
		"table_name":      tableName,
		"row_count":       len(rows),
	}

	return tool.OK(
		fmt.Sprintf("Created SQLite DB '%s' (table %q, %d rows)", finalOutputFilename, tableName, len(rows)),
		tool.WithData(data),
	), nil
}

func parseCSVOrJSONForSQLiteDB(content []byte, mimeType string, maxCols int) ([]map[string]any, error) {
	switch mimeType {
	case "text/csv":
		return parseCSVToRows(content)
	case "application/json":
		return parseJSONToRows(content, maxCols)
	default:
		return nil, fmt.Errorf("unsupported input MIME type for DB creation: %s. Expected 'text/csv' or 'application/json'", mimeType)
	}
}

func writeRowsToSQLiteBytes(ctx context.Context, rows []map[string]any, tableName string) ([]byte, error) {
	db, err := openInMemoryQueryDB()
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = db.Close() }()

	if createErr := createTableFromRows(ctx, db, tableName, rows, nil); createErr != nil {
		return nil, createErr
	}

	var dbBytes []byte
	serialize := func(s sqliteSerializer) error {
		var serializeErr error
		dbBytes, serializeErr = s.Serialize()
		return serializeErr
	}
	if err = withSQLiteSerializer(ctx, db, serialize); err != nil {
		return nil, fmt.Errorf("serialize sqlite: %w", err)
	}
	if len(dbBytes) == 0 {
		return nil, errors.New("serialize sqlite: empty database image")
	}
	return dbBytes, nil
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	if c := s[0]; c >= 'a' && c <= 'z' {
		return string(c-'a'+'A') + s[1:]
	}
	return s
}

func ensureCorrectExtension(filename, ext string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "unnamed." + strings.ToLower(ext)
	}
	desired := "." + strings.ToLower(strings.TrimPrefix(ext, "."))
	currentExt := strings.ToLower(filepath.Ext(filename))
	if currentExt == desired {
		return filename
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	return base + desired
}
