// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"gopkg.in/yaml.v3"
)

type TemplateMeta struct {
	File           string `yaml:"file"`
	OutputFilename string `yaml:"output_filename"`
	MIMEType       string `yaml:"mime_type"`
	Description    string `yaml:"description"`
}

type Substitution struct {
	Description string  `yaml:"description"`
	Default     *string `yaml:"default"`
	Required    bool    `yaml:"required"`
}

type DataInput struct {
	Description string         `yaml:"description"`
	Artifact    string         `yaml:"artifact"`
	MIMEType    string         `yaml:"mime_type"`
	Required    bool           `yaml:"required"`
	Schema      map[string]any `yaml:"schema"`
	Columns     []ColumnSpec   `yaml:"columns"`
}

type ColumnSpec struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Required bool   `yaml:"required"`
}

type Template struct {
	Meta          TemplateMeta            `yaml:"template"`
	Substitutions map[string]Substitution `yaml:"substitutions"`
	DataInputs    map[string]DataInput    `yaml:"data_inputs"`
}

type TemplateInfo struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	OutputFilename string `json:"output_filename,omitempty"`
	Asset          string `json:"asset,omitempty"`
}

func ScanTemplates(assetsDir string) []TemplateInfo {
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		return nil
	}
	logger := slog.Default().With("logger_name", "skill-template-scanner")
	var out []TemplateInfo
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), TemplateArtifactExt) {
			if strings.HasSuffix(de.Name(), templateSidecarSuffix) {
				logger.Warn("ignoring loose .template.yaml sidecar; templates must be packaged as a single .samt file (see package_template)", "sidecar", de.Name())
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(assetsDir, de.Name()))
		if err != nil {
			continue
		}
		_, _, sidecar, err := ReadTemplateZip(data)
		if err != nil {
			logger.Warn("skipping invalid skill template artifact", "asset", de.Name(), "error", err)
			continue
		}
		tmpl, err := ParseTemplateSidecar([]byte(sidecar))
		if err != nil {
			logger.Warn("skipping skill template artifact with invalid sidecar", "asset", de.Name(), "error", err)
			continue
		}
		out = append(out, TemplateInfo{
			Name:           tmpl.Meta.File,
			Description:    tmpl.Meta.Description,
			OutputFilename: tmpl.Meta.OutputFilename,
			Asset:          assetsRoot + "/" + de.Name(),
		})
	}
	slices.SortFunc(out, func(a, b TemplateInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

var residualTokenRegex = regexp.MustCompile(`@@([A-Za-z0-9_]+)@@`)

func ParseTemplateSidecar(data []byte) (*Template, error) {
	var t Template
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: parsing template sidecar: %v", ErrInvalidParam, err)
	}

	if strings.TrimSpace(t.Meta.File) == "" {
		return nil, fmt.Errorf("%w: template.file is required", ErrInvalidParam)
	}
	if strings.TrimSpace(t.Meta.MIMEType) == "" {
		return nil, fmt.Errorf("%w: template.mime_type is required", ErrInvalidParam)
	}
	for name, di := range t.DataInputs {
		if len(di.Schema) > 0 && len(di.Columns) > 0 {
			return nil, fmt.Errorf("%w: data_input %q declares both schema and columns (use one)", ErrInvalidParam, name)
		}
	}
	return &t, nil
}

func substitute(body string, decl map[string]Substitution, provided map[string]string) (string, []InstantiateError) {
	var errs []InstantiateError

	for _, key := range sortedKeys(provided) {
		if _, ok := decl[key]; !ok {
			detail := fmt.Sprintf("unknown key %q not declared in template", key)
			if suggestion := closestKey(key, decl); suggestion != "" {
				detail += fmt.Sprintf(" (did you mean %q?)", suggestion)
			}
			errs = append(errs, InstantiateError{Kind: errKindSubstitution, Detail: detail})
		}
	}

	result := body
	for _, key := range sortedKeys(decl) {
		spec := decl[key]
		val, ok := provided[key]
		if !ok {
			switch {
			case spec.Default != nil:
				val = *spec.Default
			case spec.Required:
				errs = append(errs, InstantiateError{
					Kind:   errKindSubstitution,
					Detail: fmt.Sprintf("required substitution %q has no value and no default", key),
				})
				continue
			default:
				val = ""
			}
		}
		result = strings.ReplaceAll(result, "@@"+key+"@@", val)
	}

	if len(errs) > 0 {
		return "", errs
	}

	seen := map[string]struct{}{}
	for _, m := range residualTokenRegex.FindAllStringSubmatch(result, -1) {
		if _, dup := seen[m[1]]; dup {
			continue
		}
		seen[m[1]] = struct{}{}
		errs = append(errs, InstantiateError{
			Kind:   errKindSubstitution,
			Detail: fmt.Sprintf("residual template token @@%s@@ — not a declared substitution (misspelled or undeclared key)", m[1]),
		})
	}
	if len(errs) > 0 {
		return "", errs
	}
	return result, nil
}

func validateDataInput(binding string, di DataInput, content []byte) []InstantiateError {
	switch {
	case len(di.Schema) > 0:
		var data any
		if err := json.Unmarshal(content, &data); err != nil {
			return []InstantiateError{{
				Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
				Detail: fmt.Sprintf("content is not valid JSON: %v", err), Expected: di.Schema,
			}}
		}
		var errs []InstantiateError
		for _, msg := range structured.Validate(data, di.Schema) {
			errs = append(errs, InstantiateError{
				Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
				Detail: msg, Expected: di.Schema,
			})
		}
		return errs
	case len(di.Columns) > 0:
		return validateColumns(binding, di, content)
	default:
		return nil
	}
}

func validateColumns(binding string, di DataInput, content []byte) []InstantiateError {
	r := csv.NewReader(bytes.NewReader(content))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return []InstantiateError{{
			Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
			Detail: fmt.Sprintf("content is not valid CSV: %v", err),
		}}
	}
	if len(records) == 0 {
		return []InstantiateError{{
			Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
			Detail: "CSV is empty (no header row)",
		}}
	}

	header := records[0]
	colIndex := map[string]int{}
	for i, name := range header {
		colIndex[strings.TrimSpace(name)] = i
	}

	var errs []InstantiateError
	for _, col := range di.Columns {
		idx, present := colIndex[col.Name]
		if !present {
			if col.Required {
				errs = append(errs, InstantiateError{
					Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
					Detail: fmt.Sprintf("required column %q missing from CSV header", col.Name),
				})
			}
			continue
		}
		for rowNum, row := range records[1:] {
			if idx >= len(row) {
				if col.Required {
					errs = append(errs, InstantiateError{
						Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
						Detail: fmt.Sprintf("row %d missing required column %q", rowNum+1, col.Name),
					})
				}
				continue
			}
			cell := strings.TrimSpace(row[idx])
			if cell == "" {
				if col.Required {
					errs = append(errs, InstantiateError{
						Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
						Detail: fmt.Sprintf("row %d column %q is empty but required", rowNum+1, col.Name),
					})
				}
				continue
			}
			if msg := checkColumnType(col, cell); msg != "" {
				errs = append(errs, InstantiateError{
					Kind: errKindSchema, Binding: binding, Artifact: di.Artifact,
					Detail: fmt.Sprintf("row %d column %q: %s", rowNum+1, col.Name, msg),
				})
			}
		}
	}
	return errs
}

func checkColumnType(col ColumnSpec, cell string) string {
	switch strings.ToLower(col.Type) {
	case "", "string":
		return ""
	case "number", "float":
		if _, err := strconv.ParseFloat(cell, 64); err != nil {
			return fmt.Sprintf("value %q is not a number", cell)
		}
	case "integer", "int":
		if _, err := strconv.ParseInt(cell, 10, 64); err != nil {
			return fmt.Sprintf("value %q is not an integer", cell)
		}
	case "boolean", "bool":
		if _, err := strconv.ParseBool(cell); err != nil {
			return fmt.Sprintf("value %q is not a boolean", cell)
		}
	}
	return ""
}

func closestKey(key string, decl map[string]Substitution) string {
	norm := normalizeKey(key)
	for _, candidate := range sortedKeys(decl) {
		if normalizeKey(candidate) == norm {
			return candidate
		}
	}
	return ""
}

func normalizeKey(k string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(k))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
