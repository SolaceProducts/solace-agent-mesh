// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package documentdb

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var tableCounter atomic.Uint64

func uniqueTable(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("tt_%d", tableCounter.Add(1))
}

func dynamoControlClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(localstackEndpoint)
	})
}

func newDynamoTable(t *testing.T) string {
	t.Helper()
	if dynamoSkip != "" {
		t.Skipf("dynamodb: %s", dynamoSkip)
	}
	name := uniqueTable(t)
	cl := dynamoControlClient(t)

	if _, err := cl.CreateTable(t.Context(), &dynamodb.CreateTableInput{
		TableName:   aws.String(name),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: types.KeyTypeHash},
		},
	}); err != nil {
		t.Fatalf("create table %s: %v", name, err)
	}

	if err := dynamodb.NewTableExistsWaiter(cl).Wait(t.Context(),
		&dynamodb.DescribeTableInput{TableName: aws.String(name)}, 30*time.Second); err != nil {
		t.Fatalf("wait table active: %v", err)
	}

	t.Cleanup(func() {
		_, _ = cl.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(name)})
	})
	return name
}

func connectedDynamo(t *testing.T, table string) *dynamoBackend {
	t.Helper()
	if dynamoSkip != "" {
		t.Skipf("dynamodb: %s", dynamoSkip)
	}
	b := newDynamoBackend(map[string]any{
		"backend":               "dynamodb",
		"region":                "us-east-1",
		"table_name":            table,
		"endpoint_url":          localstackEndpoint,
		"aws_access_key_id":     "test",
		"aws_secret_access_key": "test",
	})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect dynamodb: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func TestDynamoDB_InsertSelectUpdateDelete(t *testing.T) {
	table := newDynamoTable(t)
	b := connectedDynamo(t, table)

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`INSERT INTO "%s" VALUE {'pk':'item1','name':'Widget','price':10}`, table),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	read, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`SELECT * FROM "%s" WHERE pk = 'item1'`, table),
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	requireDocs(t, read, 1)
	if read.Documents[0]["name"] != "Widget" {
		t.Errorf("name = %v, want Widget", read.Documents[0]["name"])
	}

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`UPDATE "%s" SET price = 20 WHERE pk = 'item1'`, table),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`SELECT * FROM "%s" WHERE pk = 'item1'`, table),
	})
	if err != nil {
		t.Fatalf("select after update: %v", err)
	}
	requireDocs(t, updated, 1)
	if got := updated.Documents[0]["price"]; got != "20" {
		t.Errorf("price = %v, want 20", got)
	}

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`DELETE FROM "%s" WHERE pk = 'item1'`, table),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	gone, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`SELECT * FROM "%s" WHERE pk = 'item1'`, table),
	})
	if err != nil {
		t.Fatalf("select after delete: %v", err)
	}
	requireDocs(t, gone, 0)
}

func TestDynamoDB_SelectEmpty(t *testing.T) {
	table := newDynamoTable(t)
	b := connectedDynamo(t, table)

	res, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`SELECT * FROM "%s" WHERE pk = 'nope'`, table),
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	requireDocs(t, res, 0)
}

func TestDynamoDB_InvalidStatement(t *testing.T) {
	table := newDynamoTable(t)
	b := connectedDynamo(t, table)

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`SELCT * FROM "%s"`, table),
	}); err == nil {
		t.Fatal("expected error for malformed PartiQL statement")
	}
}

func TestDynamoDB_Introspect(t *testing.T) {
	table := newDynamoTable(t)
	b := connectedDynamo(t, table)

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"statement": fmt.Sprintf(`INSERT INTO "%s" VALUE {'pk':'item1','name':'Widget'}`, table),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	yaml, err := b.IntrospectSchema(t.Context(), IntrospectConfig{MaxSampleSize: 10, MaxCollections: 10})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if !strings.Contains(yaml, table) {
		t.Errorf("introspection output missing table %q:\n%s", table, yaml)
	}
}
