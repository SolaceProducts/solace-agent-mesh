// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"gopkg.in/yaml.v3"
)

type collectionSchema struct {
	DocumentCount *int64                `yaml:"document_count,omitempty"`
	Fields        map[string]*fieldInfo `yaml:"fields,omitempty"`
	Indexes       []indexInfo           `yaml:"indexes,omitempty"`
}

type fieldInfo struct {
	Type      string   `yaml:"type"`
	Frequency *string  `yaml:"frequency,omitempty"`
	Enum      []string `yaml:"enum,omitempty"`
}

type indexInfo struct {
	Keys   map[string]int `yaml:"keys"`
	Unique *bool          `yaml:"unique,omitempty"`
}

func (m *mongoBackend) IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error) {
	if cfg.MaxSampleSize <= 0 {
		cfg.MaxSampleSize = 100
	}
	if cfg.MaxCollections <= 0 {
		cfg.MaxCollections = 50
	}

	db := m.client.Database(m.database)

	collections, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return "", fmt.Errorf("list collections: %w", err)
	}
	slices.Sort(collections)

	if len(collections) > cfg.MaxCollections {
		collections = collections[:cfg.MaxCollections]
	}

	schemas := make(map[string]*collectionSchema)
	var orderedNames []string

	for _, name := range collections {
		cs, err := introspectCollection(ctx, db.Collection(name), cfg)
		if err != nil {
			continue
		}
		schemas[name] = cs
		orderedNames = append(orderedNames, name)
	}

	return marshalOrderedYAML(orderedNames, schemas)
}

func introspectCollection(ctx context.Context, coll *mongo.Collection, cfg IntrospectConfig) (*collectionSchema, error) {
	cs := &collectionSchema{
		Fields: make(map[string]*fieldInfo),
	}

	count, err := coll.EstimatedDocumentCount(ctx)
	if err == nil && count >= 0 {
		cs.DocumentCount = &count
	}

	samples, fieldOrder, err := sampleDocuments(ctx, coll, cfg.MaxSampleSize)
	if err != nil {
		return cs, nil
	}

	totalSamples := len(samples)
	fieldCounts := make(map[string]int)
	fieldTypes := make(map[string]map[string]int)
	fieldStringValues := make(map[string]map[string]struct{})

	for _, doc := range samples {
		collectFieldStats(doc, "", fieldCounts, fieldTypes, fieldStringValues)
	}

	for _, field := range fieldOrder {
		typeCounts := fieldTypes[field]
		bestType := dominantType(typeCounts)

		fi := &fieldInfo{Type: bestType}

		if totalSamples > 0 {
			pct := (fieldCounts[field] * 100) / totalSamples
			if pct < 100 {
				s := fmt.Sprintf("%d%%", pct)
				fi.Frequency = &s
			}
		}

		if vals, ok := fieldStringValues[field]; ok && bestType == "string" {
			fi.Enum = detectEnumValues(field, vals, totalSamples)
		}

		cs.Fields[field] = fi
	}

	indexes, err := getCollectionIndexes(ctx, coll)
	if err == nil && len(indexes) > 0 {
		cs.Indexes = indexes
	}

	return cs, nil
}

func sampleDocuments(ctx context.Context, coll *mongo.Collection, limit int) ([]bson.M, []string, error) {
	opts := options.Find().SetLimit(int64(limit))
	cursor, err := coll.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = cursor.Close(ctx) }()

	var docs []bson.M
	fieldSeen := make(map[string]bool)
	var fieldOrder []string

	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		docs = append(docs, doc)

		for key := range doc {
			if !fieldSeen[key] {
				fieldSeen[key] = true
				fieldOrder = append(fieldOrder, key)
			}
		}
	}

	return docs, fieldOrder, cursor.Err()
}

func collectFieldStats(doc bson.M, prefix string, counts map[string]int, types map[string]map[string]int, stringVals map[string]map[string]struct{}) {
	for key, val := range doc {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		counts[fullKey]++

		typeName := bsonTypeName(val)
		if types[fullKey] == nil {
			types[fullKey] = make(map[string]int)
		}
		types[fullKey][typeName]++

		if s, ok := val.(string); ok && s != "" {
			if stringVals[fullKey] == nil {
				stringVals[fullKey] = make(map[string]struct{})
			}
			stringVals[fullKey][s] = struct{}{}
		}
	}
}

func bsonTypeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int32:
		return "int32"
	case int64:
		return "int64"
	case float64:
		return "double"
	case bool:
		return "bool"
	case bson.ObjectID:
		return "ObjectId"
	case bson.M:
		return "object"
	case bson.A:
		return "array"
	case nil:
		return "null"
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

var mongoEnumPatterns = []string{
	"status", "state", "type", "kind", "category",
	"level", "priority", "role", "gender", "country",
	"region", "phase", "stage", "mode", "flag", "method",
	"payment", "fulfillment", "delivery", "shipping",
	"visibility", "access", "permission", "scope",
}

func detectEnumValues(field string, vals map[string]struct{}, totalSamples int) []string {
	cardinality := len(vals)
	if cardinality <= 1 || cardinality > 100 {
		return nil
	}

	cardRatio := float64(cardinality) / float64(max(totalSamples, 1))
	isLikelyEnum := looksLikeMongoEnumField(field) || cardRatio < 0.3

	if !isLikelyEnum {
		return nil
	}

	if cardinality > 20 {
		return nil
	}

	sorted := make([]string, 0, cardinality)
	for v := range vals {
		sorted = append(sorted, v)
	}
	slices.Sort(sorted)
	return sorted
}

func looksLikeMongoEnumField(name string) bool {
	lower := strings.ToLower(name)
	for _, pattern := range mongoEnumPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func getCollectionIndexes(ctx context.Context, coll *mongo.Collection) ([]indexInfo, error) {
	cursor, err := coll.Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(ctx) }()

	var indexes []indexInfo
	for cursor.Next(ctx) {
		var idx struct {
			Key    bson.D `bson:"key"`
			Unique bool   `bson:"unique"`
		}
		if err := cursor.Decode(&idx); err != nil {
			continue
		}

		keys := make(map[string]int, len(idx.Key))
		for _, elem := range idx.Key {
			if v, ok := elem.Value.(int32); ok {
				keys[elem.Key] = int(v)
			} else if v, ok := elem.Value.(int64); ok {
				keys[elem.Key] = int(v)
			} else {
				keys[elem.Key] = 1
			}
		}

		info := indexInfo{Keys: keys}
		if idx.Unique {
			t := true
			info.Unique = &t
		}
		indexes = append(indexes, info)
	}

	return indexes, cursor.Err()
}

func marshalOrderedYAML(names []string, schemas map[string]*collectionSchema) (string, error) {
	orderedSchema := yaml.Node{Kind: yaml.MappingNode}
	for _, name := range names {
		cs, ok := schemas[name]
		if !ok {
			continue
		}
		var valueNode yaml.Node
		if err := valueNode.Encode(cs); err != nil {
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
