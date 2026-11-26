// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestAuthFromConfig(t *testing.T) {
	config := map[string]any{
		"region":                "eu-west-1",
		"aws_access_key_id":     "AKID",
		"aws_secret_access_key": "SECRET",
		"role_arn":              "arn:aws:iam::123456789012:role/TestRole",
		"external_id":           "ext-123",
		"session_name":          "test-session",
	}
	auth := authFromConfig(config)
	if auth.region != "eu-west-1" {
		t.Errorf("region = %q, want %q", auth.region, "eu-west-1")
	}
	if auth.accessKeyID != "AKID" {
		t.Errorf("accessKeyID = %q, want %q", auth.accessKeyID, "AKID")
	}
	if auth.secretAccessKey != "SECRET" {
		t.Errorf("secretAccessKey = %q, want %q", auth.secretAccessKey, "SECRET")
	}
	if auth.roleARN != "arn:aws:iam::123456789012:role/TestRole" {
		t.Errorf("roleARN = %q", auth.roleARN)
	}
	if auth.externalID != "ext-123" {
		t.Errorf("externalID = %q", auth.externalID)
	}
	if auth.sessionName != "test-session" {
		t.Errorf("sessionName = %q", auth.sessionName)
	}
}

func TestAuthFromConfig_Defaults(t *testing.T) {
	auth := authFromConfig(map[string]any{})
	if auth.region != "us-east-1" {
		t.Errorf("default region = %q, want %q", auth.region, "us-east-1")
	}
	if auth.accessKeyID != "" {
		t.Errorf("default accessKeyID = %q, want empty", auth.accessKeyID)
	}
}

func TestIsPartiQLSelect(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM users", true},
		{"select * from users", true},
		{"  SELECT pk FROM orders WHERE pk = 'x'", true},
		{"INSERT INTO users VALUE {'pk': 'x'}", false},
		{"UPDATE users SET name = 'y' WHERE pk = 'x'", false},
		{"DELETE FROM users WHERE pk = 'x'", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got := isPartiQLSelect(tt.query); got != tt.want {
				t.Errorf("isPartiQLSelect(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestNewDynamoBackend_Config(t *testing.T) {
	config := map[string]any{
		"region":     "ap-southeast-1",
		"table_name": "orders",
	}
	db := newDynamoBackend(config)
	if db.region != "ap-southeast-1" {
		t.Errorf("region = %q, want %q", db.region, "ap-southeast-1")
	}
	if db.tableName != "orders" {
		t.Errorf("tableName = %q, want %q", db.tableName, "orders")
	}
}

func TestDynamoExecuteQuery_MissingStatement(t *testing.T) {
	db := newDynamoBackend(map[string]any{
		"region":     "us-east-1",
		"table_name": "orders",
	})
	_, err := db.ExecuteQuery(t.Context(), map[string]any{
		"statement": "",
	})
	if err == nil {
		t.Fatal("expected error when statement is empty")
	}
}

func TestUnmarshalAttributeValue(t *testing.T) {
	tests := []struct {
		name string
		av   types.AttributeValue
		want any
	}{
		{"string", &types.AttributeValueMemberS{Value: "hello"}, "hello"},
		{"number", &types.AttributeValueMemberN{Value: "42"}, "42"},
		{"bool", &types.AttributeValueMemberBOOL{Value: true}, true},
		{"null", &types.AttributeValueMemberNULL{Value: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unmarshalAttributeValue(tt.av)
			if got != tt.want {
				t.Errorf("unmarshalAttributeValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnmarshalAttributeValue_List(t *testing.T) {
	av := &types.AttributeValueMemberL{
		Value: []types.AttributeValue{
			&types.AttributeValueMemberS{Value: "a"},
			&types.AttributeValueMemberN{Value: "1"},
		},
	}
	got := unmarshalAttributeValue(av)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 items, got %d", len(arr))
	}
	if arr[0] != "a" {
		t.Errorf("arr[0] = %v, want %q", arr[0], "a")
	}
}

func TestUnmarshalAttributeValue_Map(t *testing.T) {
	av := &types.AttributeValueMemberM{
		Value: map[string]types.AttributeValue{
			"name": &types.AttributeValueMemberS{Value: "Alice"},
			"age":  &types.AttributeValueMemberN{Value: "30"},
		},
	}
	got := unmarshalAttributeValue(av)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["name"] != "Alice" {
		t.Errorf("name = %v, want %q", m["name"], "Alice")
	}
}

func TestUnmarshalItems(t *testing.T) {
	items := []map[string]types.AttributeValue{
		{
			"pk":   &types.AttributeValueMemberS{Value: "user#1"},
			"name": &types.AttributeValueMemberS{Value: "Alice"},
		},
		{
			"pk":   &types.AttributeValueMemberS{Value: "user#2"},
			"name": &types.AttributeValueMemberS{Value: "Bob"},
		},
	}
	docs := unmarshalItems(items)
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}
	if docs[0]["pk"] != "user#1" {
		t.Errorf("docs[0][pk] = %v, want %q", docs[0]["pk"], "user#1")
	}
	if docs[1]["name"] != "Bob" {
		t.Errorf("docs[1][name] = %v, want %q", docs[1]["name"], "Bob")
	}
}

func TestDynamoScalarType(t *testing.T) {
	tests := []struct {
		input types.ScalarAttributeType
		want  string
	}{
		{types.ScalarAttributeTypeS, "S"},
		{types.ScalarAttributeTypeN, "N"},
		{types.ScalarAttributeTypeB, "B"},
	}
	for _, tt := range tests {
		if got := dynamoScalarType(tt.input); got != tt.want {
			t.Errorf("dynamoScalarType(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDynamoAttributeValueType(t *testing.T) {
	tests := []struct {
		name string
		av   types.AttributeValue
		want string
	}{
		{"S", &types.AttributeValueMemberS{}, "S"},
		{"N", &types.AttributeValueMemberN{}, "N"},
		{"B", &types.AttributeValueMemberB{}, "B"},
		{"BOOL", &types.AttributeValueMemberBOOL{}, "BOOL"},
		{"NULL", &types.AttributeValueMemberNULL{}, "NULL"},
		{"L", &types.AttributeValueMemberL{}, "L"},
		{"M", &types.AttributeValueMemberM{}, "M"},
		{"SS", &types.AttributeValueMemberSS{}, "SS"},
		{"NS", &types.AttributeValueMemberNS{}, "NS"},
		{"BS", &types.AttributeValueMemberBS{}, "BS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dynamoAttributeValueType(tt.av); got != tt.want {
				t.Errorf("dynamoAttributeValueType() = %q, want %q", got, tt.want)
			}
		})
	}
}
