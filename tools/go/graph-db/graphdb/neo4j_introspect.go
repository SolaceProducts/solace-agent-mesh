// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"gopkg.in/yaml.v3"
)

type graphSchema struct {
	NodeLabels        map[string]*labelSchema `yaml:"node_labels,omitempty"`
	RelationshipTypes []relTypeInfo           `yaml:"relationship_types,omitempty"`
	Constraints       []constraintInfo        `yaml:"constraints,omitempty"`
}

type labelSchema struct {
	Count      *int64               `yaml:"count,omitempty"`
	Properties map[string]*propInfo `yaml:"properties,omitempty"`
	Indexes    []indexInfo          `yaml:"indexes,omitempty"`
}

type propInfo struct {
	Type string   `yaml:"type"`
	Enum []string `yaml:"enum,omitempty"`
}

type relTypeInfo struct {
	Type  string `yaml:"type"`
	From  string `yaml:"from,omitempty"`
	To    string `yaml:"to,omitempty"`
	Count *int64 `yaml:"count,omitempty"`
}

type indexInfo struct {
	Property string `yaml:"property"`
	Type     string `yaml:"type"`
}

type constraintInfo struct {
	Label    string `yaml:"label"`
	Property string `yaml:"property"`
	Type     string `yaml:"type"`
}

func (n *neo4jBackend) IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error) {
	if cfg.MaxSampleSize <= 0 {
		cfg.MaxSampleSize = 100
	}
	if cfg.MaxLabels <= 0 {
		cfg.MaxLabels = 50
	}

	session := n.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: n.database,
		AccessMode:   neo4j.AccessModeRead,
	})
	defer func() { _ = session.Close(ctx) }()

	schema := &graphSchema{
		NodeLabels: make(map[string]*labelSchema),
	}

	labels, err := getLabels(ctx, session)
	if err != nil {
		return "", fmt.Errorf("get labels: %w", err)
	}
	if len(labels) > cfg.MaxLabels {
		labels = labels[:cfg.MaxLabels]
	}

	for _, label := range labels {
		ls := &labelSchema{
			Properties: make(map[string]*propInfo),
		}

		if count, err := getLabelCount(ctx, session, label); err == nil {
			ls.Count = &count
		}

		if props, err := sampleProperties(ctx, session, label, cfg.MaxSampleSize); err == nil {
			ls.Properties = props
		}

		schema.NodeLabels[label] = ls
	}

	if rels, err := getRelationshipTypes(ctx, session); err == nil {
		schema.RelationshipTypes = rels
	}

	if indexes, err := getIndexes(ctx, session); err == nil {
		for label, idxs := range indexes {
			if ls, ok := schema.NodeLabels[label]; ok {
				ls.Indexes = idxs
			}
		}
	}

	if constraints, err := getConstraints(ctx, session); err == nil {
		schema.Constraints = constraints
	}

	return marshalGraphYAML(schema, labels)
}

func getLabels(ctx context.Context, session neo4j.SessionWithContext) ([]string, error) {
	result, err := session.Run(ctx, "CALL db.labels()", nil)
	if err != nil {
		return nil, err
	}

	var labels []string
	for result.Next(ctx) {
		if label, ok := result.Record().Values[0].(string); ok {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	return labels, result.Err()
}

func getLabelCount(ctx context.Context, session neo4j.SessionWithContext, label string) (int64, error) {
	query := fmt.Sprintf("MATCH (n:`%s`) RETURN count(n) AS count", escapeCypher(label))
	result, err := session.Run(ctx, query, nil)
	if err != nil {
		return 0, err
	}
	if result.Next(ctx) {
		if count, ok := result.Record().Values[0].(int64); ok {
			return count, nil
		}
	}
	return 0, result.Err()
}

func sampleProperties(ctx context.Context, session neo4j.SessionWithContext, label string, sampleSize int) (map[string]*propInfo, error) {
	query := fmt.Sprintf("MATCH (n:`%s`) RETURN n LIMIT %d", escapeCypher(label), sampleSize)
	result, err := session.Run(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	propTypes := make(map[string]map[string]int)
	propStringVals := make(map[string]map[string]struct{})
	totalSamples := 0

	for result.Next(ctx) {
		totalSamples++
		val := result.Record().Values[0]
		node, ok := val.(neo4j.Node)
		if !ok {
			continue
		}
		for k, v := range node.Props {
			typeName := neo4jTypeName(v)
			if propTypes[k] == nil {
				propTypes[k] = make(map[string]int)
			}
			propTypes[k][typeName]++

			if s, ok := v.(string); ok && s != "" {
				if propStringVals[k] == nil {
					propStringVals[k] = make(map[string]struct{})
				}
				propStringVals[k][s] = struct{}{}
			}
		}
	}
	if err := result.Err(); err != nil {
		return nil, err
	}

	props := make(map[string]*propInfo)
	for name, types := range propTypes {
		bestType := dominantType(types)
		pi := &propInfo{Type: bestType}

		if bestType == "STRING" {
			if vals, ok := propStringVals[name]; ok {
				pi.Enum = detectEnumValues(name, vals, totalSamples)
			}
		}

		props[name] = pi
	}

	return props, nil
}

func getRelationshipTypes(ctx context.Context, session neo4j.SessionWithContext) ([]relTypeInfo, error) {
	result, err := session.Run(ctx, "CALL db.relationshipTypes()", nil)
	if err != nil {
		return nil, err
	}

	var types []string
	for result.Next(ctx) {
		if t, ok := result.Record().Values[0].(string); ok {
			types = append(types, t)
		}
	}
	if err := result.Err(); err != nil {
		return nil, err
	}

	slices.Sort(types)
	var rels []relTypeInfo
	for _, t := range types {
		ri := relTypeInfo{Type: t}

		countQuery := fmt.Sprintf("MATCH ()-[r:`%s`]->() RETURN count(r) AS count", escapeCypher(t))
		countResult, err := session.Run(ctx, countQuery, nil)
		if err == nil && countResult.Next(ctx) {
			if count, ok := countResult.Record().Values[0].(int64); ok {
				ri.Count = &count
			}
		}

		endpointQuery := fmt.Sprintf("MATCH (a)-[:`%s`]->(b) RETURN labels(a)[0] AS from_label, labels(b)[0] AS to_label LIMIT 1", escapeCypher(t))
		epResult, err := session.Run(ctx, endpointQuery, nil)
		if err == nil && epResult.Next(ctx) {
			rec := epResult.Record()
			if from, ok := rec.Values[0].(string); ok {
				ri.From = from
			}
			if to, ok := rec.Values[1].(string); ok {
				ri.To = to
			}
		}

		rels = append(rels, ri)
	}

	return rels, nil
}

func getIndexes(ctx context.Context, session neo4j.SessionWithContext) (map[string][]indexInfo, error) {
	result, err := session.Run(ctx, "SHOW INDEXES", nil)
	if err != nil {
		return nil, err
	}

	indexes := make(map[string][]indexInfo)
	for result.Next(ctx) {
		record := result.Record()
		recordMap := make(map[string]any, len(record.Keys))
		for i, key := range record.Keys {
			recordMap[key] = record.Values[i]
		}

		labelsOrTypes, _ := recordMap["labelsOrTypes"].([]any)
		properties, _ := recordMap["properties"].([]any)
		indexType, _ := recordMap["type"].(string)

		if len(labelsOrTypes) > 0 && len(properties) > 0 {
			label, _ := labelsOrTypes[0].(string)
			prop, _ := properties[0].(string)
			if label != "" && prop != "" {
				indexes[label] = append(indexes[label], indexInfo{
					Property: prop,
					Type:     indexType,
				})
			}
		}
	}
	return indexes, result.Err()
}

func getConstraints(ctx context.Context, session neo4j.SessionWithContext) ([]constraintInfo, error) {
	result, err := session.Run(ctx, "SHOW CONSTRAINTS", nil)
	if err != nil {
		return nil, err
	}

	var constraints []constraintInfo
	for result.Next(ctx) {
		record := result.Record()
		recordMap := make(map[string]any, len(record.Keys))
		for i, key := range record.Keys {
			recordMap[key] = record.Values[i]
		}

		labelsOrTypes, _ := recordMap["labelsOrTypes"].([]any)
		properties, _ := recordMap["properties"].([]any)
		cType, _ := recordMap["type"].(string)

		if len(labelsOrTypes) > 0 && len(properties) > 0 {
			label, _ := labelsOrTypes[0].(string)
			prop, _ := properties[0].(string)
			if label != "" && prop != "" {
				constraints = append(constraints, constraintInfo{
					Label:    label,
					Property: prop,
					Type:     cType,
				})
			}
		}
	}
	return constraints, result.Err()
}

func neo4jTypeName(v any) string {
	switch v.(type) {
	case string:
		return "STRING"
	case int64:
		return "INTEGER"
	case float64:
		return "FLOAT"
	case bool:
		return "BOOLEAN"
	case []any:
		return "LIST"
	case map[string]any:
		return "MAP"
	case nil:
		return "NULL"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func dominantType(typeCounts map[string]int) string {
	var best string
	var bestCount int
	for t, c := range typeCounts {
		if c > bestCount || (c == bestCount && t < best) {
			best = t
			bestCount = c
		}
	}
	return best
}

var enumPatterns = []string{
	"status", "state", "type", "kind", "category",
	"level", "priority", "role", "gender", "country",
	"region", "phase", "stage", "mode", "flag", "method",
	"visibility", "access", "permission", "scope",
}

func detectEnumValues(field string, vals map[string]struct{}, totalSamples int) []string {
	cardinality := len(vals)
	if cardinality <= 1 || cardinality > 100 {
		return nil
	}

	cardRatio := float64(cardinality) / float64(max(totalSamples, 1))
	isLikelyEnum := looksLikeEnumField(field) || cardRatio < 0.3

	if !isLikelyEnum || cardinality > 20 {
		return nil
	}

	sorted := make([]string, 0, cardinality)
	for v := range vals {
		sorted = append(sorted, v)
	}
	slices.Sort(sorted)
	return sorted
}

func looksLikeEnumField(name string) bool {
	lower := strings.ToLower(name)
	for _, pattern := range enumPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func escapeCypher(s string) string {
	return strings.ReplaceAll(s, "`", "``")
}

func marshalGraphYAML(schema *graphSchema, labelOrder []string) (string, error) {
	rootNode := yaml.Node{Kind: yaml.MappingNode}

	if len(schema.NodeLabels) > 0 {
		labelsNode := yaml.Node{Kind: yaml.MappingNode}
		for _, label := range labelOrder {
			ls, ok := schema.NodeLabels[label]
			if !ok {
				continue
			}
			var valueNode yaml.Node
			if err := valueNode.Encode(ls); err != nil {
				continue
			}
			labelsNode.Content = append(labelsNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: label},
				&valueNode,
			)
		}
		rootNode.Content = append(rootNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "node_labels"},
			&labelsNode,
		)
	}

	if len(schema.RelationshipTypes) > 0 {
		var relsNode yaml.Node
		if err := relsNode.Encode(schema.RelationshipTypes); err == nil {
			rootNode.Content = append(rootNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "relationship_types"},
				&relsNode,
			)
		}
	}

	if len(schema.Constraints) > 0 {
		var consNode yaml.Node
		if err := consNode.Encode(schema.Constraints); err == nil {
			rootNode.Content = append(rootNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "constraints"},
				&consNode,
			)
		}
	}

	doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&rootNode}}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("encode schema YAML: %w", err)
	}
	return buf.String(), nil
}
