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

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.in/yaml.v3"
)

type dynamoTableSchema struct {
	KeySchema              *keySchemaInfo       `yaml:"key_schema,omitempty"`
	ItemCount              *int64               `yaml:"item_count,omitempty"`
	BillingMode            string               `yaml:"billing_mode,omitempty"`
	GlobalSecondaryIndexes []gsiInfo            `yaml:"global_secondary_indexes,omitempty"`
	LocalSecondaryIndexes  []lsiInfo            `yaml:"local_secondary_indexes,omitempty"`
	Attributes             map[string]*attrInfo `yaml:"attributes,omitempty"`
}

type keySchemaInfo struct {
	PartitionKey keyDetail  `yaml:"partition_key"`
	SortKey      *keyDetail `yaml:"sort_key,omitempty"`
}

type keyDetail struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type gsiInfo struct {
	Name   string            `yaml:"name"`
	Keys   map[string]string `yaml:"keys"`
	Status string            `yaml:"status,omitempty"`
}

type lsiInfo struct {
	Name string            `yaml:"name"`
	Keys map[string]string `yaml:"keys"`
}

type attrInfo struct {
	Type string `yaml:"type"`
}

func (d *dynamoBackend) IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error) {
	if cfg.MaxSampleSize <= 0 {
		cfg.MaxSampleSize = 100
	}
	if cfg.MaxCollections <= 0 {
		cfg.MaxCollections = 50
	}

	tables, err := d.listTables(ctx, cfg.MaxCollections)
	if err != nil {
		return "", fmt.Errorf("list tables: %w", err)
	}

	if d.tableName != "" {
		tables = []string{d.tableName}
	}

	schemas := make(map[string]*dynamoTableSchema)
	var orderedNames []string

	for _, table := range tables {
		ts, err := d.introspectTable(ctx, table, cfg)
		if err != nil {
			continue
		}
		schemas[table] = ts
		orderedNames = append(orderedNames, table)
	}

	return marshalDynamoYAML(orderedNames, schemas)
}

func (d *dynamoBackend) listTables(ctx context.Context, maxTables int) ([]string, error) {
	var tables []string
	var lastTable *string

	for {
		input := &dynamodb.ListTablesInput{
			ExclusiveStartTableName: lastTable,
		}
		result, err := d.client.ListTables(ctx, input)
		if err != nil {
			return nil, err
		}

		tables = append(tables, result.TableNames...)

		if result.LastEvaluatedTableName == nil || len(tables) >= maxTables {
			break
		}
		lastTable = result.LastEvaluatedTableName
	}

	if len(tables) > maxTables {
		tables = tables[:maxTables]
	}
	slices.Sort(tables)
	return tables, nil
}

func (d *dynamoBackend) introspectTable(ctx context.Context, tableName string, cfg IntrospectConfig) (*dynamoTableSchema, error) {
	desc, err := d.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: &tableName,
	})
	if err != nil {
		return nil, fmt.Errorf("describe table %s: %w", tableName, err)
	}

	table := desc.Table
	ts := &dynamoTableSchema{
		Attributes: make(map[string]*attrInfo),
	}

	attrTypes := make(map[string]string)
	for _, ad := range table.AttributeDefinitions {
		attrTypes[*ad.AttributeName] = dynamoScalarType(ad.AttributeType)
	}

	ks := &keySchemaInfo{}
	for _, key := range table.KeySchema {
		detail := keyDetail{
			Name: *key.AttributeName,
			Type: attrTypes[*key.AttributeName],
		}
		switch key.KeyType {
		case types.KeyTypeHash:
			ks.PartitionKey = detail
		case types.KeyTypeRange:
			ks.SortKey = &detail
		}
	}
	ts.KeySchema = ks

	if table.ItemCount != nil {
		ts.ItemCount = table.ItemCount
	}

	if table.BillingModeSummary != nil {
		ts.BillingMode = string(table.BillingModeSummary.BillingMode)
	}

	for _, gsi := range table.GlobalSecondaryIndexes {
		keys := make(map[string]string)
		for _, key := range gsi.KeySchema {
			keys[string(key.KeyType)] = *key.AttributeName
		}
		info := gsiInfo{
			Name: *gsi.IndexName,
			Keys: keys,
		}
		if gsi.IndexStatus != "" {
			info.Status = string(gsi.IndexStatus)
		}
		ts.GlobalSecondaryIndexes = append(ts.GlobalSecondaryIndexes, info)
	}

	for _, lsi := range table.LocalSecondaryIndexes {
		keys := make(map[string]string)
		for _, key := range lsi.KeySchema {
			keys[string(key.KeyType)] = *key.AttributeName
		}
		ts.LocalSecondaryIndexes = append(ts.LocalSecondaryIndexes, lsiInfo{
			Name: *lsi.IndexName,
			Keys: keys,
		})
	}

	for name, typ := range attrTypes {
		ts.Attributes[name] = &attrInfo{Type: typ}
	}

	sampleAttrs, err := d.sampleAttributes(ctx, tableName, cfg.MaxSampleSize)
	if err == nil {
		for name, typ := range sampleAttrs {
			if _, exists := ts.Attributes[name]; !exists {
				ts.Attributes[name] = &attrInfo{Type: typ}
			}
		}
	}

	return ts, nil
}

func (d *dynamoBackend) sampleAttributes(ctx context.Context, tableName string, limit int) (map[string]string, error) {
	scanLimit := int32(limit)
	result, err := d.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: &tableName,
		Limit:     &scanLimit,
	})
	if err != nil {
		return nil, err
	}

	attrs := make(map[string]string)
	for _, item := range result.Items {
		for k, v := range item {
			if _, exists := attrs[k]; !exists {
				attrs[k] = dynamoAttributeValueType(v)
			}
		}
	}
	return attrs, nil
}

func dynamoScalarType(t types.ScalarAttributeType) string {
	switch t {
	case types.ScalarAttributeTypeS:
		return "S"
	case types.ScalarAttributeTypeN:
		return "N"
	case types.ScalarAttributeTypeB:
		return "B"
	default:
		return string(t)
	}
}

func dynamoAttributeValueType(av types.AttributeValue) string {
	switch av.(type) {
	case *types.AttributeValueMemberS:
		return "S"
	case *types.AttributeValueMemberN:
		return "N"
	case *types.AttributeValueMemberB:
		return "B"
	case *types.AttributeValueMemberBOOL:
		return "BOOL"
	case *types.AttributeValueMemberNULL:
		return "NULL"
	case *types.AttributeValueMemberL:
		return "L"
	case *types.AttributeValueMemberM:
		return "M"
	case *types.AttributeValueMemberSS:
		return "SS"
	case *types.AttributeValueMemberNS:
		return "NS"
	case *types.AttributeValueMemberBS:
		return "BS"
	default:
		return "UNKNOWN"
	}
}

func marshalDynamoYAML(names []string, schemas map[string]*dynamoTableSchema) (string, error) {
	orderedSchema := yaml.Node{Kind: yaml.MappingNode}
	for _, name := range names {
		ts, ok := schemas[name]
		if !ok {
			continue
		}
		var valueNode yaml.Node
		if err := valueNode.Encode(ts); err != nil {
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
