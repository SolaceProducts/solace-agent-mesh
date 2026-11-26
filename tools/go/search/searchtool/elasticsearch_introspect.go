// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type indexSchema struct {
	DocumentCount *int64              `yaml:"document_count,omitempty"`
	Mappings      map[string]*mapping `yaml:"mappings,omitempty"`
	Aliases       []string            `yaml:"aliases,omitempty"`
}

type mapping struct {
	Type     string   `yaml:"type"`
	Analyzer *string  `yaml:"analyzer,omitempty"`
	Enum     []string `yaml:"enum,omitempty"`
}

func (e *elasticsearchBackend) IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error) {
	if cfg.MaxIndices <= 0 {
		cfg.MaxIndices = 20
	}
	if cfg.MaxSampleSize <= 0 {
		cfg.MaxSampleSize = 100
	}

	indices, err := e.listIndices(ctx, cfg.MaxIndices)
	if err != nil {
		return "", fmt.Errorf("list indices: %w", err)
	}

	schemas := make(map[string]*indexSchema)
	var orderedNames []string

	for _, idx := range indices {
		schema, err := e.introspectIndex(ctx, idx, cfg)
		if err != nil {
			continue
		}
		schemas[idx] = schema
		orderedNames = append(orderedNames, idx)
	}

	return marshalOrderedYAML(orderedNames, schemas)
}

func (e *elasticsearchBackend) listIndices(ctx context.Context, maxIndices int) ([]string, error) {
	res, err := e.client.Cat.Indices(
		e.client.Cat.Indices.WithContext(ctx),
		e.client.Cat.Indices.WithFormat("json"),
		e.client.Cat.Indices.WithH("index"),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("cat indices error: %s", readBody(res.Body))
	}

	var catResult []struct {
		Index string `json:"index"`
	}
	if err := json.NewDecoder(res.Body).Decode(&catResult); err != nil {
		return nil, fmt.Errorf("decode cat indices: %w", err)
	}

	var indices []string
	for _, r := range catResult {
		if strings.HasPrefix(r.Index, ".") {
			continue
		}
		indices = append(indices, r.Index)
	}

	slices.Sort(indices)
	if len(indices) > maxIndices {
		indices = indices[:maxIndices]
	}

	return indices, nil
}

func (e *elasticsearchBackend) introspectIndex(ctx context.Context, index string, cfg IntrospectConfig) (*indexSchema, error) {
	schema := &indexSchema{
		Mappings: make(map[string]*mapping),
	}

	if err := e.getMappings(ctx, index, schema); err != nil {
		return nil, err
	}

	if count, err := e.getDocCount(ctx, index); err == nil {
		schema.DocumentCount = &count
	}

	if aliases, err := e.getAliases(ctx, index); err == nil && len(aliases) > 0 {
		schema.Aliases = aliases
	}

	keywordFields := collectKeywordFields(schema.Mappings)
	if len(keywordFields) > 0 && schema.DocumentCount != nil && *schema.DocumentCount > 0 {
		e.detectEnums(ctx, index, keywordFields, schema.Mappings, cfg.MaxSampleSize)
	}

	return schema, nil
}

func (e *elasticsearchBackend) getMappings(ctx context.Context, index string, schema *indexSchema) error {
	res, err := e.client.Indices.GetMapping(
		e.client.Indices.GetMapping.WithContext(ctx),
		e.client.Indices.GetMapping.WithIndex(index),
	)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return fmt.Errorf("get mapping error: %s", readBody(res.Body))
	}

	var result map[string]struct {
		Mappings struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode mapping: %w", err)
	}

	if idxData, ok := result[index]; ok {
		flattenMappings("", idxData.Mappings.Properties, schema.Mappings)
	}

	return nil
}

func flattenMappings(prefix string, properties map[string]json.RawMessage, out map[string]*mapping) {
	for field, raw := range properties {
		fullName := field
		if prefix != "" {
			fullName = prefix + "." + field
		}

		var prop struct {
			Type       string                     `json:"type"`
			Analyzer   string                     `json:"analyzer"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			continue
		}

		if prop.Properties != nil {
			flattenMappings(fullName, prop.Properties, out)
			continue
		}

		if prop.Type == "" {
			continue
		}

		m := &mapping{Type: prop.Type}
		if prop.Analyzer != "" {
			m.Analyzer = &prop.Analyzer
		}
		out[fullName] = m
	}
}

func (e *elasticsearchBackend) getDocCount(ctx context.Context, index string) (int64, error) {
	res, err := e.client.Count(
		e.client.Count.WithContext(ctx),
		e.client.Count.WithIndex(index),
	)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return 0, fmt.Errorf("count error: %s", readBody(res.Body))
	}

	var result countResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Count, nil
}

func (e *elasticsearchBackend) getAliases(ctx context.Context, index string) ([]string, error) {
	res, err := e.client.Indices.GetAlias(
		e.client.Indices.GetAlias.WithContext(ctx),
		e.client.Indices.GetAlias.WithIndex(index),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, nil
	}

	var result map[string]struct {
		Aliases map[string]any `json:"aliases"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, err
	}

	if idxData, ok := result[index]; ok {
		var aliases []string
		for alias := range idxData.Aliases {
			aliases = append(aliases, alias)
		}
		slices.Sort(aliases)
		return aliases, nil
	}
	return nil, nil
}

func collectKeywordFields(mappings map[string]*mapping) []string {
	var fields []string
	for name, m := range mappings {
		if m.Type == "keyword" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}

func (e *elasticsearchBackend) detectEnums(ctx context.Context, index string, fields []string, mappings map[string]*mapping, maxCardinality int) {
	for _, field := range fields {
		agg := map[string]any{
			"size": 0,
			"aggs": map[string]any{
				"values": map[string]any{
					"terms": map[string]any{
						"field": field,
						"size":  maxCardinality + 1,
					},
				},
			},
		}
		buf, _ := json.Marshal(agg)

		res, err := e.client.Search(
			e.client.Search.WithContext(ctx),
			e.client.Search.WithIndex(index),
			e.client.Search.WithBody(bytes.NewReader(buf)),
		)
		if err != nil {
			continue
		}

		var result struct {
			Aggregations struct {
				Values struct {
					Buckets []struct {
						Key string `json:"key"`
					} `json:"buckets"`
				} `json:"values"`
			} `json:"aggregations"`
		}
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			_ = res.Body.Close()
			continue
		}
		_ = res.Body.Close()

		buckets := result.Aggregations.Values.Buckets
		if len(buckets) > 0 && len(buckets) <= maxCardinality {
			vals := make([]string, len(buckets))
			for i, b := range buckets {
				vals[i] = b.Key
			}
			if m, ok := mappings[field]; ok {
				m.Enum = vals
			}
		}
	}
}

func marshalOrderedYAML(names []string, schemas map[string]*indexSchema) (string, error) {
	orderedSchema := yaml.Node{Kind: yaml.MappingNode}
	for _, name := range names {
		s, ok := schemas[name]
		if !ok {
			continue
		}
		var valueNode yaml.Node
		if err := valueNode.Encode(s); err != nil {
			continue
		}
		orderedSchema.Content = append(orderedSchema.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: name},
			&valueNode,
		)
	}

	doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&orderedSchema}}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("encode schema YAML: %w", err)
	}
	return buf.String(), nil
}
