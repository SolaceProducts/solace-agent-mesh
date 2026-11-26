// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

var specHTTPClient = &http.Client{Timeout: 30 * time.Second}

const maxSpecSize = 10 << 20

type SpecStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

const objStoreScheme = "objstore://"

type operationInfo struct {
	OperationID string
	Method      string
	Path        string
	Summary     string
	Description string
	Parameters  []*openapi3.ParameterRef
	RequestBody *openapi3.RequestBodyRef
	Responses   *openapi3.Responses
}

func operationDescriptionText(op operationInfo) string {
	switch {
	case op.Summary != "":
		return op.Summary
	case op.Description != "":
		return op.Description
	default:
		return fmt.Sprintf("%s %s", op.Method, op.Path)
	}
}

func (m *Manager) readSpecBytes(ctx context.Context, cfg *ToolsetConfig) ([]byte, error) {
	var (
		data       []byte
		sourceDesc string
		err        error
	)
	switch {
	case cfg.SpecFile != "":
		sourceDesc = cfg.SpecFile
		data, err = loadFile(cfg.SpecFile)
	case strings.HasPrefix(cfg.SpecURL, objStoreScheme):
		sourceDesc = cfg.SpecURL
		data, err = m.readFromObjectStore(ctx, strings.TrimPrefix(cfg.SpecURL, objStoreScheme))
	case cfg.SpecURL != "":
		sourceDesc = cfg.SpecURL
		data, err = fetchURL(ctx, cfg.SpecURL)
	case cfg.SpecInline != "":
		sourceDesc = "inline"
		data = []byte(cfg.SpecInline)
	default:
		return nil, fmt.Errorf("openapi: no spec source provided")
	}
	if err != nil {
		return nil, fmt.Errorf("load spec from %s: %w", sourceDesc, err)
	}
	return data, nil
}

func (m *Manager) readFromObjectStore(ctx context.Context, key string) ([]byte, error) {
	if m.specStore == nil {
		return nil, fmt.Errorf("openapi: connector resource store not configured for %s spec", objStoreScheme)
	}
	rc, err := m.specStore.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read spec from object store key %q: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxSpecSize))
}

func (m *Manager) loadSpec(ctx context.Context, cfg *ToolsetConfig) (*openapi3.T, error) {
	data, err := m.readSpecBytes(ctx, cfg)
	if err != nil {
		return nil, err
	}

	sourceDesc := "inline"
	switch {
	case cfg.SpecFile != "":
		sourceDesc = cfg.SpecFile
	case cfg.SpecURL != "":
		sourceDesc = cfg.SpecURL
	}

	if cfg.BaseURL != "" {
		data, err = applyBaseURLOverride(data, cfg.BaseURL, cfg.SpecFormat)
		if err != nil {
			return nil, fmt.Errorf("apply base_url override: %w", err)
		}
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.Context = ctx

	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("parse spec from %s: %w", sourceDesc, err)
	}
	return doc, nil
}

func loadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxSpecSize))
}

func fetchURL(ctx context.Context, specURL string) ([]byte, error) {
	u, err := url.Parse(specURL)
	if err != nil {
		return nil, fmt.Errorf("parse spec URL: %w", err)
	}
	if u.Scheme == "file" {
		return loadFile(u.Path)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", specURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml")

	resp, err := specHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch spec: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching spec from %s", resp.StatusCode, specURL)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxSpecSize))
}

func applyBaseURLOverride(data []byte, baseURL, formatHint string) ([]byte, error) {
	format := formatHint
	if format == "" {
		format = detectFormat(data)
	}

	var spec map[string]any
	var unmarshal func([]byte, any) error
	var marshal func(any) ([]byte, error)

	switch format {
	case "json":
		unmarshal = json.Unmarshal
		marshal = func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
	default:
		unmarshal = yaml.Unmarshal
		marshal = yaml.Marshal
	}

	if err := unmarshal(data, &spec); err != nil {
		if format == "json" {
			if err2 := yaml.Unmarshal(data, &spec); err2 == nil {
				marshal = yaml.Marshal
			} else {
				return nil, fmt.Errorf("parse spec: %w", err)
			}
		} else {
			if err2 := json.Unmarshal(data, &spec); err2 == nil {
				marshal = func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
			} else {
				return nil, fmt.Errorf("parse spec: %w", err)
			}
		}
	}

	baseNorm := strings.TrimRight(baseURL, "/")

	servers, _ := spec["servers"].([]any)
	if len(servers) == 0 {
		spec["servers"] = []any{map[string]any{"url": baseNorm}}
	} else {
		for i, srv := range servers {
			srvMap, ok := srv.(map[string]any)
			if !ok {
				srvMap = map[string]any{}
			}
			srvMap["url"] = baseNorm
			servers[i] = srvMap
		}
		spec["servers"] = servers
	}

	out, err := marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("serialize spec: %w", err)
	}
	return out, nil
}

func detectFormat(data []byte) string {
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var tmp any
		if json.Unmarshal(data, &tmp) == nil {
			return "json"
		}
	}
	return "yaml"
}

func extractOperations(doc *openapi3.T) []operationInfo {
	var ops []operationInfo

	if doc.Paths == nil {
		return ops
	}

	for path, pathItem := range doc.Paths.Map() {
		for method, op := range pathItem.Operations() {
			if op == nil {
				continue
			}

			info := operationInfo{
				OperationID: op.OperationID,
				Method:      strings.ToUpper(method),
				Path:        path,
				Summary:     op.Summary,
				Description: op.Description,
				Parameters:  effectiveParameters(pathItem.Parameters, op.Parameters),
				RequestBody: op.RequestBody,
				Responses:   op.Responses,
			}

			if info.OperationID == "" {
				info.OperationID = generateOperationID(method, path)
			}

			ops = append(ops, info)
		}
	}

	return ops
}

func generateOperationID(method, path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var segments []string
	for _, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			segments = append(segments, "by_"+strings.Trim(p, "{}"))
		} else {
			segments = append(segments, p)
		}
	}
	name := strings.ToLower(method) + "_" + strings.Join(segments, "_")
	re := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	return re.ReplaceAllString(name, "_")
}

func getBaseURL(doc *openapi3.T) string {
	if doc != nil && len(doc.Servers) > 0 {
		return strings.TrimRight(doc.Servers[0].URL, "/")
	}
	return ""
}

func shouldInclude(opID string, allow, deny []string) bool {
	if len(allow) > 0 {
		return slices.ContainsFunc(allow, func(a string) bool { return EntryNamesOperation(a, opID) })
	}
	return !slices.ContainsFunc(deny, func(d string) bool { return EntryNamesOperation(d, opID) })
}

func EntryNamesOperation(entry, name string) bool {
	return toSnakeCase(entry) == toSnakeCase(name)
}
