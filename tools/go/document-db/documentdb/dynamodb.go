// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

type dynamoBackend struct {
	tableName string
	region    string
	client    *dynamodb.Client
	config    map[string]any
}

func newDynamoBackend(config map[string]any) *dynamoBackend {
	return &dynamoBackend{
		tableName: configString(config, "table_name", ""),
		region:    configString(config, "region", "us-east-1"),
		config:    config,
	}
}

func (d *dynamoBackend) Connect(ctx context.Context) error {
	auth := authFromConfig(d.config)
	cfg, err := loadAWSConfig(ctx, auth)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	optFns := []func(*dynamodb.Options){legacyRetryerOption}
	if endpoint := configString(d.config, "endpoint_url", ""); endpoint != "" {
		optFns = append(optFns, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(endpoint) })
	}
	d.client = dynamodb.NewFromConfig(cfg, optFns...)
	return nil
}

func legacyRetryerOption(o *dynamodb.Options) {
	o.Retryer = awsretry.Legacy(retry.DefaultMaxAttempts)
}

func (d *dynamoBackend) Close(_ context.Context) error {
	return nil
}

func (d *dynamoBackend) ExecuteQuery(ctx context.Context, args map[string]any) (*QueryResult, error) {
	var params DynamoQueryParams
	if err := decodeTypedParams(args, &params); err != nil {
		return nil, err
	}

	statement := strings.TrimSpace(params.Statement)
	if statement == "" {
		return nil, fmt.Errorf("statement is required")
	}

	limit := int32(configInt(d.config, "dynamo_select_limit", 20))
	if limit <= 0 {
		limit = 20
	}

	input := &dynamodb.ExecuteStatementInput{
		Statement: &statement,
	}
	if isPartiQLSelect(statement) {
		input.Limit = &limit
	}

	result, err := d.client.ExecuteStatement(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("execute statement: %w", err)
	}

	if isPartiQLSelect(statement) {
		docs := unmarshalItems(result.Items)
		return &QueryResult{
			Documents:         docs,
			Message:           fmt.Sprintf("Query returned %d documents", len(docs)),
			ResponseFormat:    derefString(params.ResponseFormat, "json"),
			ResultDescription: derefString(params.ResultDescription, ""),
			QueryText:         statement,
		}, nil
	}

	return &QueryResult{
		IsWrite:       true,
		AffectedCount: int64(len(result.Items)),
		Message:       "Statement executed successfully",
	}, nil
}

func isPartiQLSelect(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upper, "SELECT")
}

func unmarshalItems(items []map[string]types.AttributeValue) []map[string]any {
	docs := make([]map[string]any, 0, len(items))
	for _, item := range items {
		doc := make(map[string]any, len(item))
		for k, v := range item {
			doc[k] = unmarshalAttributeValue(v)
		}
		docs = append(docs, doc)
	}
	return docs
}

func unmarshalAttributeValue(av types.AttributeValue) any {
	switch v := av.(type) {
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return v.Value
	case *types.AttributeValueMemberBOOL:
		return v.Value
	case *types.AttributeValueMemberNULL:
		return nil
	case *types.AttributeValueMemberL:
		arr := make([]any, len(v.Value))
		for i, item := range v.Value {
			arr[i] = unmarshalAttributeValue(item)
		}
		return arr
	case *types.AttributeValueMemberM:
		m := make(map[string]any, len(v.Value))
		for k, item := range v.Value {
			m[k] = unmarshalAttributeValue(item)
		}
		return m
	case *types.AttributeValueMemberB:
		return fmt.Sprintf("<binary %d bytes>", len(v.Value))
	case *types.AttributeValueMemberSS:
		return v.Value
	case *types.AttributeValueMemberNS:
		return v.Value
	case *types.AttributeValueMemberBS:
		return fmt.Sprintf("<binary set %d items>", len(v.Value))
	default:
		return fmt.Sprintf("%v", av)
	}
}
