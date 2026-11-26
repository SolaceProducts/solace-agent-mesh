// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type greetParams struct {
	Name string `json:"name" desc:"The person to greet"`
}

func greetHandler(_ context.Context, p greetParams, tc *ToolContext) (*Result, error) {
	return OK("Hello, " + p.Name), nil
}

func TestNewTool_Schema(t *testing.T) {
	tool := NewTool("greet", "Greets a person", greetHandler)

	if tool.name != "greet" {
		t.Errorf("name: got %q", tool.name)
	}
	if tool.description != "Greets a person" {
		t.Errorf("description: got %q", tool.description)
	}
	if tool.schema.Type != "object" {
		t.Errorf("schema type: got %q", tool.schema.Type)
	}
	if len(tool.schema.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(tool.schema.Properties))
	}
	nameProp := tool.schema.Properties["name"].(map[string]any)
	if nameProp["type"] != "string" {
		t.Errorf("name type: got %v", nameProp["type"])
	}
	if nameProp["description"] != "The person to greet" {
		t.Errorf("name desc: got %v", nameProp["description"])
	}
}

func TestNewTool_WithInstructions(t *testing.T) {
	tool := NewTool("greet", "Greets", greetHandler, WithInstructions("Always be polite"))
	if tool.instructions != "Always be polite" {
		t.Errorf("instructions: got %q", tool.instructions)
	}
}

func TestNewTool_Execute(t *testing.T) {
	tool := NewTool("greet", "Greets a person", greetHandler)

	tc := &ToolContext{UserID: "test"}
	args := map[string]any{"name": "Alice"}
	result, err := tool.execute(context.Background(), args, nil, tc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("status: got %q", result.Status)
	}
	if result.Message != "Hello, Alice" {
		t.Errorf("message: got %q", result.Message)
	}
}

type artifactParams struct {
	Name  string    `json:"name"`
	Input Artifact  `json:"input" desc:"Input file"`
	Extra *Artifact `json:"extra" desc:"Optional file"`
}

func artifactHandler(_ context.Context, p artifactParams, _ *ToolContext) (*Result, error) {
	return OK("Got " + p.Name + " with file " + p.Input.Filename), nil
}

func TestNewTool_ArtifactInjection(t *testing.T) {
	tool := NewTool("process", "Process files", artifactHandler)

	if ai, ok := tool.artifactInfo["input"]; !ok || ai.IsList || ai.IsOptional {
		t.Errorf("input artifact info: got %+v", ai)
	}
	if ai, ok := tool.artifactInfo["extra"]; !ok || ai.IsList || !ai.IsOptional {
		t.Errorf("extra artifact info: got %+v", ai)
	}

	artObjs := map[string]any{
		"input": &Artifact{Content: []byte("data"), Filename: "test.txt", MIMEType: "text/plain"},
		"extra": (*Artifact)(nil),
	}
	tc := &ToolContext{}
	result, err := tool.execute(context.Background(), map[string]any{"name": "test"}, artObjs, tc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Message != "Got test with file test.txt" {
		t.Errorf("message: got %q", result.Message)
	}
}

func TestNewTool_WithAuth(t *testing.T) {
	tool := NewTool("greet", "Greets", greetHandler, WithAuth(AuthSchemaConfig{
		Type: "oauth2",
		Scheme: &AuthSchemaOAuth{
			AuthorizationURL: "https://example.com/authorize",
			TokenURL:         "https://example.com/token",
			Scopes:           []string{"read", "write"},
		},
	}))
	if tool.authConfig == nil {
		t.Fatal("authConfig should not be nil")
	}
	if tool.authConfig.Type != "oauth2" {
		t.Errorf("auth type: got %q", tool.authConfig.Type)
	}
	if tool.authConfig.Scheme == nil {
		t.Fatal("auth scheme should not be nil")
	}
	if tool.authConfig.Scheme.TokenURL != "https://example.com/token" {
		t.Errorf("token url: got %q", tool.authConfig.Scheme.TokenURL)
	}
	if len(tool.authConfig.Scheme.Scopes) != 2 {
		t.Errorf("scopes: got %v", tool.authConfig.Scheme.Scopes)
	}
}

func TestNewTool_NoAuth(t *testing.T) {
	tool := NewTool("greet", "Greets", greetHandler)
	if tool.authConfig != nil {
		t.Error("authConfig should be nil when WithAuth not used")
	}
}

func TestNewTool_RejectsReservedAuthConfigKey(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewTool with ConfigSchemaField{Key: \"auth\"} should have panicked")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T", r)
		}
		if !strings.Contains(msg, "reserved") || !strings.Contains(msg, "\"auth\"") {
			t.Errorf("panic message missing expected hints: %q", msg)
		}
	}()
	_ = NewTool("oops", "should fail", greetHandler,
		WithConfigSchema(ConfigSchemaField{Key: "auth", Type: "string"}),
	)
}

func TestNewTool_RejectsEmptyDescription(t *testing.T) {
	for _, desc := range []string{"", "   ", "\t\n"} {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("NewTool with description %q should have panicked", desc)
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("panic value is not a string: %T", r)
				}
				if !strings.Contains(msg, "empty description") {
					t.Errorf("panic message missing expected hint: %q", msg)
				}
			}()
			_ = NewTool("oops", desc, greetHandler)
		}()
	}
}

func TestOutputSchema(t *testing.T) {
	tool1 := NewTool("greet", "Greets a person", greetHandler)
	tool2 := NewTool("process", "Process files", artifactHandler, WithInstructions("Be careful"))

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "schema.json")
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	os.Stdout = f
	err = outputSchema([]*ToolDef{tool1, tool2}, nil)
	os.Stdout = oldStdout
	_ = f.Close()

	if err != nil {
		t.Fatalf("outputSchema: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}

	var schema schemaOutput
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	if len(schema.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(schema.Tools))
	}

	greetEntry := schema.Tools["greet"]
	if greetEntry.Description != "Greets a person" {
		t.Errorf("greet description: got %q", greetEntry.Description)
	}

	processEntry := schema.Tools["process"]
	if processEntry.Instructions != "Be careful" {
		t.Errorf("process instructions: got %q", processEntry.Instructions)
	}
	if len(processEntry.ArtifactParams) != 2 {
		t.Errorf("process artifact params: expected 2, got %d", len(processEntry.ArtifactParams))
	}
}

func TestOutputSchema_WithAuth(t *testing.T) {
	tool := NewTool("authed", "Needs auth", greetHandler, WithAuth(AuthSchemaConfig{
		Type: "oauth2",
		Scheme: &AuthSchemaOAuth{
			AuthorizationURL: "https://example.com/authorize",
			TokenURL:         "https://example.com/token",
			Scopes:           []string{"read"},
		},
	}))
	toolNoAuth := NewTool("public", "No auth", greetHandler)

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "schema.json")
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = f
	err = outputSchema([]*ToolDef{tool, toolNoAuth}, nil)
	os.Stdout = oldStdout
	_ = f.Close()
	if err != nil {
		t.Fatalf("outputSchema: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}

	var schema schemaOutput
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	authedEntry := schema.Tools["authed"]
	if authedEntry.Auth == nil {
		t.Fatal("authed tool should have auth in schema")
	}
	if authedEntry.Auth.Type != "oauth2" {
		t.Errorf("auth type: got %q", authedEntry.Auth.Type)
	}
	if authedEntry.Auth.Scheme == nil || authedEntry.Auth.Scheme.TokenURL != "https://example.com/token" {
		t.Errorf("auth scheme: got %v", authedEntry.Auth.Scheme)
	}

	publicEntry := schema.Tools["public"]
	if publicEntry.Auth != nil {
		t.Error("public tool should not have auth in schema")
	}
}
