// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolContext_GetConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]any
		key       string
		wantVal   any
		wantFound bool
	}{
		{
			name:      "nil config",
			config:    nil,
			key:       "foo",
			wantVal:   nil,
			wantFound: false,
		},
		{
			name:      "missing key",
			config:    map[string]any{"bar": 42},
			key:       "foo",
			wantVal:   nil,
			wantFound: false,
		},
		{
			name:      "existing key",
			config:    map[string]any{"foo": "hello"},
			key:       "foo",
			wantVal:   "hello",
			wantFound: true,
		},
		{
			name:      "non-string value",
			config:    map[string]any{"count": 99},
			key:       "count",
			wantVal:   99,
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &ToolContext{config: tt.config}
			val, found := tc.GetConfig(tt.key)
			if found != tt.wantFound {
				t.Errorf("found: got %v, want %v", found, tt.wantFound)
			}
			if val != tt.wantVal {
				t.Errorf("val: got %v, want %v", val, tt.wantVal)
			}
		})
	}
}

func TestToolContext_GetConfigString(t *testing.T) {
	tests := []struct {
		name       string
		config     map[string]any
		key        string
		defaultVal string
		want       string
	}{
		{
			name:       "nil config returns default",
			config:     nil,
			key:        "k",
			defaultVal: "def",
			want:       "def",
		},
		{
			name:       "missing key returns default",
			config:     map[string]any{"other": "val"},
			key:        "k",
			defaultVal: "def",
			want:       "def",
		},
		{
			name:       "non-string value returns default",
			config:     map[string]any{"k": 123},
			key:        "k",
			defaultVal: "def",
			want:       "def",
		},
		{
			name:       "string value returned",
			config:     map[string]any{"k": "actual"},
			key:        "k",
			defaultVal: "def",
			want:       "actual",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &ToolContext{config: tt.config}
			got := tc.GetConfigString(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolContext_GetAuthToken(t *testing.T) {
	tc := &ToolContext{oauthToken: "tok-123"}
	if got := tc.GetAuthToken(); got != "tok-123" {
		t.Errorf("got %q, want %q", got, "tok-123")
	}

	tc2 := &ToolContext{}
	if got := tc2.GetAuthToken(); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestToolContext_UserProfile(t *testing.T) {
	tests := []struct {
		name string
		blob string
		want map[string]any
	}{
		{
			name: "profile object returned as-is",
			blob: `{"_enterprise_capabilities":["tool:*:execute"],"user_profile":{"id":"u-1","workEmail":"u@x.com","country":"Netherlands"}}`,
			want: map[string]any{"id": "u-1", "workEmail": "u@x.com", "country": "Netherlands"},
		},
		{name: "empty blob", blob: "", want: map[string]any{}},
		{name: "malformed blob", blob: "{not json", want: map[string]any{}},
		{name: "no user_profile key", blob: `{"_enterprise_capabilities":["x"]}`, want: map[string]any{}},
		{name: "user_profile not an object", blob: `{"user_profile":"alice"}`, want: map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &ToolContext{a2aUserConfig: tt.blob}
			got := tc.UserProfile()
			if got == nil {
				t.Fatal("UserProfile returned nil; want empty non-nil map")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestToolContext_AuthorizationHeader(t *testing.T) {
	t.Run("injected header wins", func(t *testing.T) {
		tc := &ToolContext{
			authHeaders: map[string]string{"Authorization": "Basic dXNlcjpwYXNz"},
			oauthToken:  "tok-123",
		}
		if got := tc.AuthorizationHeader(); got != "Basic dXNlcjpwYXNz" {
			t.Errorf("got %q, want injected Basic header", got)
		}
	})

	t.Run("falls back to bearer token", func(t *testing.T) {
		tc := &ToolContext{oauthToken: "tok-123"}
		if got := tc.AuthorizationHeader(); got != "Bearer tok-123" {
			t.Errorf("got %q, want %q", got, "Bearer tok-123")
		}
	})

	t.Run("empty when neither present", func(t *testing.T) {
		if got := (&ToolContext{}).AuthorizationHeader(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestToolContext_AuthHeaders(t *testing.T) {
	tc := &ToolContext{authHeaders: map[string]string{"X-Api-Key": "k"}}
	got := tc.AuthHeaders()
	if got["X-Api-Key"] != "k" {
		t.Errorf("AuthHeaders = %v", got)
	}
	got["X-Api-Key"] = "mutated"
	if tc.authHeaders["X-Api-Key"] != "k" {
		t.Error("AuthHeaders must return a copy, not the backing map")
	}
	if (&ToolContext{}).AuthHeaders() != nil {
		t.Error("AuthHeaders should be nil when none present")
	}
}

func TestToolContext_SaveArtifact(t *testing.T) {
	t.Run("no output dir", func(t *testing.T) {
		tc := &ToolContext{}
		err := tc.SaveArtifact("test.txt", []byte("data"), "text/plain")
		if err == nil {
			t.Fatal("expected error when outputDir is empty")
		}
	})

	t.Run("successful write", func(t *testing.T) {
		dir := t.TempDir()
		tc := &ToolContext{outputDir: dir}
		err := tc.SaveArtifact("out.txt", []byte("content"), "text/plain")
		if err != nil {
			t.Fatalf("SaveArtifact: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
		if err != nil {
			t.Fatalf("reading saved artifact: %v", err)
		}
		if string(data) != "content" {
			t.Errorf("content: got %q, want %q", string(data), "content")
		}
	})
}

func TestToolContext_GetVolumeMountPath(t *testing.T) {
	tc := &ToolContext{
		VolumeMounts: []VolumeMount{
			{VolumeID: "v1", MountPath: "/workspace", ReadOnly: false},
			{VolumeID: "v2", MountPath: "/data", ReadOnly: true},
		},
	}

	tests := []struct {
		mountPath string
		wantPath  string
		wantOK    bool
	}{
		{"/workspace", "/workspace", true},
		{"/data", "/data", true},
		{"/missing", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.mountPath, func(t *testing.T) {
			path, ok := tc.GetVolumeMountPath(tt.mountPath)
			if ok != tt.wantOK {
				t.Errorf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if path != tt.wantPath {
				t.Errorf("path: got %q, want %q", path, tt.wantPath)
			}
		})
	}

	tcEmpty := &ToolContext{}
	_, ok := tcEmpty.GetVolumeMountPath("/workspace")
	if ok {
		t.Error("expected false for empty VolumeMounts")
	}
}

func TestToolContext_SendStatus_NilWriter(t *testing.T) {
	tc := &ToolContext{}
	if err := tc.SendStatus("hello"); err != nil {
		t.Errorf("expected nil error for nil status writer, got %v", err)
	}
}

func TestToolContext_CallLLM_NilIPC(t *testing.T) {
	tc := &ToolContext{}
	_, err := tc.CallLLM(context.Background(), "sys", "usr", 0.5)
	if err == nil {
		t.Fatal("expected error when ipc is nil")
	}
}

func TestToolContext_Close_NilFields(t *testing.T) {
	tc := &ToolContext{}
	tc.close()
}

func TestWithDynamicSchema(t *testing.T) {
	fn := func(config map[string]any) *SchemaOverride {
		name, _ := config["name"].(string)
		return &SchemaOverride{Name: name}
	}

	tool := NewTool("base", "Base tool", greetHandler, WithDynamicSchema(fn))
	if tool.dynamicSchema == nil {
		t.Fatal("dynamicSchema should not be nil")
	}

	override := tool.dynamicSchema(map[string]any{"name": "overridden"})
	if override.Name != "overridden" {
		t.Errorf("override name: got %q, want %q", override.Name, "overridden")
	}
}

func TestWithVolumeParams(t *testing.T) {
	vols := []VolumeParamDecl{
		{Name: "workspace", Description: "Working dir", Mode: "readwrite", MountPath: "/workspace"},
		{Name: "data", Description: "Data dir", Mode: "readonly", MountPath: "/data"},
	}

	tool := NewTool("proc", "Process", greetHandler, WithVolumeParams(vols...))
	if len(tool.volumeParams) != 2 {
		t.Fatalf("expected 2 volume params, got %d", len(tool.volumeParams))
	}
	if tool.volumeParams[0].Name != "workspace" {
		t.Errorf("volume[0] name: got %q", tool.volumeParams[0].Name)
	}
	if tool.volumeParams[1].Mode != "readonly" {
		t.Errorf("volume[1] mode: got %q", tool.volumeParams[1].Mode)
	}
}

func TestWithConfigSchema(t *testing.T) {
	fields := []ConfigSchemaField{
		{Key: "api_key", Type: "string", Description: "API key", Required: true, Secret: true},
		{Key: "timeout", Type: "integer", Description: "Timeout seconds", Default: 30},
	}

	tool := NewTool("svc", "Service", greetHandler, WithConfigSchema(fields...))
	if len(tool.configSchema) != 2 {
		t.Fatalf("expected 2 config schema fields, got %d", len(tool.configSchema))
	}
	if tool.configSchema[0].Key != "api_key" {
		t.Errorf("field[0] key: got %q", tool.configSchema[0].Key)
	}
	if !tool.configSchema[0].Secret {
		t.Error("field[0] should be secret")
	}
	if tool.configSchema[1].Default != 30 {
		t.Errorf("field[1] default: got %v", tool.configSchema[1].Default)
	}
}

func TestWithAuth(t *testing.T) {
	tool := NewTool("authed", "Authed tool", greetHandler, WithAuth(AuthSchemaConfig{
		Type: "bearer",
	}))
	if tool.authConfig == nil {
		t.Fatal("authConfig should not be nil")
	}
	if tool.authConfig.Type != "bearer" {
		t.Errorf("auth type: got %q", tool.authConfig.Type)
	}
	if tool.authConfig.Scheme != nil {
		t.Error("scheme should be nil for bearer")
	}
}

func TestWithInstructions(t *testing.T) {
	tool := NewTool("t", "desc", greetHandler, WithInstructions("Use carefully"))
	if tool.instructions != "Use carefully" {
		t.Errorf("instructions: got %q", tool.instructions)
	}
}

func TestAuthRequired(t *testing.T) {
	r := AuthRequired("need oauth token")
	if r.Status != StatusError {
		t.Errorf("status: got %q, want %q", r.Status, StatusError)
	}
	if r.ErrorCode != ErrorCodeAuthRequired {
		t.Errorf("error_code: got %q, want %q", r.ErrorCode, ErrorCodeAuthRequired)
	}
	if r.Message != "need oauth token" {
		t.Errorf("message: got %q", r.Message)
	}
}

func TestPartial_WithOptions(t *testing.T) {
	r := Partial("partial data",
		WithData(map[string]any{"processed": 5, "total": 10}),
		WithErrorCode("TIMEOUT"),
	)
	if r.Status != StatusPartial {
		t.Errorf("status: got %q", r.Status)
	}
	if r.Data["processed"] != 5 {
		t.Errorf("data[processed]: got %v", r.Data["processed"])
	}
	if r.ErrorCode != "TIMEOUT" {
		t.Errorf("error_code: got %q", r.ErrorCode)
	}
}

func TestError_WithDataObjects(t *testing.T) {
	do := DataObject{
		Name:    "debug.log",
		Content: []byte("error trace"),
	}
	r := Error("failed", WithDataObjects(do))
	if len(r.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(r.DataObjects))
	}
	if r.DataObjects[0].Name != "debug.log" {
		t.Errorf("name: got %q", r.DataObjects[0].Name)
	}
}

func TestArtifact_AsBytes(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var a *Artifact
		if got := a.AsBytes(); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("with content", func(t *testing.T) {
		a := &Artifact{Content: []byte{0x01, 0x02, 0x03}}
		got := a.AsBytes()
		if len(got) != 3 || got[0] != 0x01 {
			t.Errorf("unexpected bytes: %v", got)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		a := &Artifact{Content: []byte{}}
		got := a.AsBytes()
		if got == nil || len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
}

func TestArtifact_AsText(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var a *Artifact
		if got := a.AsText(); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("with content", func(t *testing.T) {
		a := &Artifact{Content: []byte("hello")}
		if got := a.AsText(); got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})
}

func TestLoadArtifacts_ListArtifact(t *testing.T) {
	dir := t.TempDir()

	f0 := filepath.Join(dir, "art0.txt")
	f1 := filepath.Join(dir, "art1.txt")
	if err := os.WriteFile(f0, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f1, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}

	info := map[string]artifactParamInfo{
		"files": {IsList: true, IsOptional: false},
	}
	paths := map[string]string{
		"files[0]": f0,
		"files[1]": f1,
	}
	meta := map[string]map[string]any{
		"files[0]": {"filename": "art0.txt", "mime_type": "text/plain", "version": float64(1)},
		"files[1]": {"filename": "art1.txt", "mime_type": "text/plain", "version": float64(2)},
	}

	objects, err := loadArtifacts(info, paths, meta)
	if err != nil {
		t.Fatalf("loadArtifacts: %v", err)
	}

	artifacts, ok := objects["files"].([]*Artifact)
	if !ok {
		t.Fatalf("expected []*Artifact, got %T", objects["files"])
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}
	if string(artifacts[0].Content) != "first" {
		t.Errorf("artifacts[0] content: got %q", string(artifacts[0].Content))
	}
	if artifacts[1].Filename != "art1.txt" {
		t.Errorf("artifacts[1] filename: got %q", artifacts[1].Filename)
	}
	if artifacts[1].Version != 2 {
		t.Errorf("artifacts[1] version: got %d", artifacts[1].Version)
	}
}

func TestLoadArtifacts_RequiredMissing(t *testing.T) {
	info := map[string]artifactParamInfo{
		"input": {IsList: false, IsOptional: false},
	}
	paths := map[string]string{}

	_, err := loadArtifacts(info, paths, nil)
	if err == nil {
		t.Fatal("expected error for missing required artifact")
	}
}

func TestLoadArtifacts_OptionalMissing(t *testing.T) {
	info := map[string]artifactParamInfo{
		"extra": {IsList: false, IsOptional: true},
	}
	paths := map[string]string{}

	objects, err := loadArtifacts(info, paths, nil)
	if err != nil {
		t.Fatalf("loadArtifacts: %v", err)
	}
	if objects["extra"] != (*Artifact)(nil) {
		t.Errorf("expected nil *Artifact for missing optional, got %v", objects["extra"])
	}
}

func TestLoadArtifacts_FileReadError(t *testing.T) {
	info := map[string]artifactParamInfo{
		"input": {IsList: false, IsOptional: false},
	}
	paths := map[string]string{
		"input": "/nonexistent/path/to/artifact.txt",
	}

	_, err := loadArtifacts(info, paths, nil)
	if err == nil {
		t.Fatal("expected error for unreadable artifact file")
	}
}

func TestLoadArtifacts_ListFileReadError(t *testing.T) {
	info := map[string]artifactParamInfo{
		"files": {IsList: true, IsOptional: false},
	}
	paths := map[string]string{
		"files[0]": "/nonexistent/file.txt",
	}

	_, err := loadArtifacts(info, paths, nil)
	if err == nil {
		t.Fatal("expected error for unreadable list artifact file")
	}
}

func TestLoadArtifacts_EmptyList(t *testing.T) {
	info := map[string]artifactParamInfo{
		"files": {IsList: true, IsOptional: false},
	}
	paths := map[string]string{}

	objects, err := loadArtifacts(info, paths, nil)
	if err != nil {
		t.Fatalf("loadArtifacts: %v", err)
	}
	artifacts, ok := objects["files"].([]*Artifact)
	if !ok {
		t.Fatalf("expected []*Artifact, got %T", objects["files"])
	}
	if artifacts != nil {
		t.Errorf("expected nil slice for empty list, got %v", artifacts)
	}
}

func TestExtractArtifactOutputs_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	result := &Result{
		DataObjects: []DataObject{
			{
				Name:        "../escape.txt",
				Content:     []byte("malicious"),
				Disposition: DispositionArtifact,
			},
		},
	}

	extractArtifactOutputs(result, dir)

	escapePath := filepath.Join(dir, "..", "escape.txt")
	if _, err := os.Stat(escapePath); err == nil {
		t.Error("path traversal was not blocked")
	}

	if result.DataObjects[0].Content == nil {
		t.Error("content should be preserved when path traversal is blocked")
	}
}

func TestExtractArtifactOutputs_DispositionTypes(t *testing.T) {
	tests := []struct {
		disposition DataDisposition
		extracted   bool
	}{
		{DispositionArtifact, true},
		{DispositionArtifactWithPreview, true},
		{DispositionAuto, true},
		{DispositionInline, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.disposition), func(t *testing.T) {
			dir := t.TempDir()
			result := &Result{
				DataObjects: []DataObject{
					{
						Name:        "file.txt",
						Content:     []byte("data"),
						MIMEType:    "text/plain",
						Disposition: tt.disposition,
					},
				},
			}

			extractArtifactOutputs(result, dir)

			path := filepath.Join(dir, "file.txt")
			_, err := os.Stat(path)
			if tt.extracted && err != nil {
				t.Errorf("expected file to be extracted for disposition %q", tt.disposition)
			}
			if !tt.extracted && err == nil {
				t.Errorf("file should not be extracted for disposition %q", tt.disposition)
			}

			if tt.extracted && result.DataObjects[0].Content != nil {
				t.Errorf("content should be nil after extraction for %q", tt.disposition)
			}
			if !tt.extracted && result.DataObjects[0].Content == nil {
				t.Errorf("content should be preserved for %q", tt.disposition)
			}
		})
	}
}

func TestExtractArtifactOutputs_EmptyName(t *testing.T) {
	dir := t.TempDir()
	result := &Result{
		DataObjects: []DataObject{
			{
				Name:        "",
				Content:     []byte("data"),
				Disposition: DispositionArtifact,
			},
		},
	}

	extractArtifactOutputs(result, dir)

	if result.DataObjects[0].Content == nil {
		t.Error("content should be preserved when name is empty")
	}
}

func TestExtractArtifactOutputs_NilContent(t *testing.T) {
	dir := t.TempDir()
	result := &Result{
		DataObjects: []DataObject{
			{
				Name:        "file.txt",
				Content:     nil,
				Disposition: DispositionArtifact,
			},
		},
	}

	extractArtifactOutputs(result, dir)

	path := filepath.Join(dir, "file.txt")
	if _, err := os.Stat(path); err == nil {
		t.Error("should not create file when content is nil")
	}
}

func TestWriteResult_NilResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	err := writeResult(path, nil)
	if err != nil {
		t.Fatalf("writeResult: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var r resultJSON
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Result != nil {
		t.Errorf("expected nil result, got %v", r.Result)
	}
	if r.Error != "" {
		t.Errorf("expected empty error, got %q", r.Error)
	}
}

func TestWriteError_NewlineSanitization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	err := writeError(path, "line1\nline2\nline3")
	if err != nil {
		t.Fatalf("writeError: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var r resultJSON
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != "line1 | line2 | line3" {
		t.Errorf("error: got %q, want %q", r.Error, "line1 | line2 | line3")
	}
}

func TestWriteResult_WithDataObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	result := OK("done", WithDataObjects(DataObject{
		Name:        "preview.txt",
		Content:     []byte("preview content"),
		MIMEType:    "text/plain",
		Disposition: DispositionArtifactWithPreview,
		Preview:     "short preview",
		Description: "A preview file",
		Metadata:    map[string]any{"key": "val"},
	}))

	err := writeResult(path, result)
	if err != nil {
		t.Fatalf("writeResult: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var r resultJSON
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}

	wireMap, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %T", r.Result)
	}

	objects, ok := wireMap["data_objects"].([]any)
	if !ok || len(objects) != 1 {
		t.Fatalf("expected 1 data object in wire output")
	}
	obj := objects[0].(map[string]any)
	if obj["disposition"] != "artifact_with_preview" {
		t.Errorf("disposition: got %v", obj["disposition"])
	}
	if obj["preview"] != "short preview" {
		t.Errorf("preview: got %v", obj["preview"])
	}
	if obj["description"] != "A preview file" {
		t.Errorf("description: got %v", obj["description"])
	}
}

func TestWriteResultJSON_InvalidPath(t *testing.T) {
	err := writeResultJSON("/nonexistent/dir/result.json", resultJSON{Error: "test"})
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestExecute_InvalidArgsFile(t *testing.T) {
	tool := NewTool("greet", "Greets", greetHandler)
	err := execute([]*ToolDef{tool}, "/nonexistent/runner_args.json")
	if err == nil {
		t.Fatal("expected error for nonexistent args file")
	}
}

func TestExecute_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "runner_args.json")
	if err := os.WriteFile(argsFile, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewTool("greet", "Greets", greetHandler)
	err := execute([]*ToolDef{tool}, argsFile)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestExecute_ToolReturnsError(t *testing.T) {
	type emptyParams struct{}
	handler := func(_ context.Context, _ emptyParams, _ *ToolContext) (*Result, error) {
		return nil, context.DeadlineExceeded
	}
	tool := NewTool("fail", "Fails", handler)

	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")

	args := runnerArgs{
		ToolName:   "fail",
		Args:       map[string]any{},
		ResultFile: resultFile,
	}
	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(dir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	err := execute([]*ToolDef{tool}, argsFile)
	if err != nil {
		t.Fatalf("execute should write error to result.json, not return it: %v", err)
	}

	data, _ := os.ReadFile(resultFile)
	var r resultJSON
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error == "" {
		t.Error("expected error in result.json for handler error")
	}
}

func TestExecute_ArtifactLoadError(t *testing.T) {
	type fileParams struct {
		Input Artifact `json:"input" desc:"Input file"`
	}
	handler := func(_ context.Context, p fileParams, _ *ToolContext) (*Result, error) {
		return OK("ok"), nil
	}
	tool := NewTool("read", "Reads", handler)

	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")

	args := runnerArgs{
		ToolName:      "read",
		Args:          map[string]any{},
		ArtifactPaths: map[string]string{"input": "/nonexistent/artifact.txt"},
		ResultFile:    resultFile,
	}
	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(dir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	err := execute([]*ToolDef{tool}, argsFile)
	if err != nil {
		t.Fatalf("execute should write error to result.json, not return it: %v", err)
	}

	data, _ := os.ReadFile(resultFile)
	var r resultJSON
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error == "" {
		t.Error("expected error in result.json for artifact load failure")
	}
}

func TestExecute_WithOAuthToken(t *testing.T) {
	type emptyParams struct{}
	var capturedToken string
	handler := func(_ context.Context, _ emptyParams, tc *ToolContext) (*Result, error) {
		capturedToken = tc.GetAuthToken()
		return OK("done"), nil
	}
	tool := NewTool("check_auth", "Check auth", handler)

	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")

	args := runnerArgs{
		ToolName:       "check_auth",
		Args:           map[string]any{},
		ResultFile:     resultFile,
		ToolOAuthToken: "bearer-xyz",
	}
	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(dir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := execute([]*ToolDef{tool}, argsFile); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if capturedToken != "bearer-xyz" {
		t.Errorf("oauth token: got %q, want %q", capturedToken, "bearer-xyz")
	}
}

func TestExecute_WithVolumeMounts(t *testing.T) {
	type emptyParams struct{}
	var capturedMounts []VolumeMount
	handler := func(_ context.Context, _ emptyParams, tc *ToolContext) (*Result, error) {
		capturedMounts = tc.VolumeMounts
		return OK("done"), nil
	}
	tool := NewTool("vol_check", "Check volumes", handler)

	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")

	args := runnerArgs{
		ToolName:   "vol_check",
		Args:       map[string]any{},
		ResultFile: resultFile,
		VolumeMounts: []VolumeMount{
			{VolumeID: "ws", MountPath: "/workspace", ReadOnly: false},
		},
	}
	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(dir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := execute([]*ToolDef{tool}, argsFile); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(capturedMounts) != 1 {
		t.Fatalf("expected 1 volume mount, got %d", len(capturedMounts))
	}
	if capturedMounts[0].MountPath != "/workspace" {
		t.Errorf("mount path: got %q", capturedMounts[0].MountPath)
	}
}

func TestOutputSchema_DynamicSchema(t *testing.T) {
	fn := func(config map[string]any) *SchemaOverride {
		prefix, _ := config["prefix"].(string)
		return &SchemaOverride{
			Name:         prefix + "_greet",
			Description:  "Dynamic desc",
			Instructions: "Dynamic instructions",
		}
	}

	tool := NewTool("greet", "Original desc", greetHandler,
		WithDynamicSchema(fn),
		WithInstructions("Original instructions"),
	)

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "schema.json")
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	os.Stdout = f
	err = outputSchema([]*ToolDef{tool}, map[string]any{"prefix": "custom"})
	os.Stdout = oldStdout
	_ = f.Close()

	if err != nil {
		t.Fatalf("outputSchema: %v", err)
	}

	data, _ := os.ReadFile(outFile)
	var schema schemaOutput
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	entry, ok := schema.Tools["custom_greet"]
	if !ok {
		t.Fatalf("expected tool 'custom_greet', got keys: %v", keysOf(schema.Tools))
	}
	if entry.Description != "Dynamic desc" {
		t.Errorf("description: got %q", entry.Description)
	}
	if entry.Instructions != "Dynamic instructions" {
		t.Errorf("instructions: got %q", entry.Instructions)
	}
}

func TestOutputSchema_DynamicSchemaReturnsNil(t *testing.T) {
	fn := func(_ map[string]any) *SchemaOverride {
		return nil
	}

	tool := NewTool("greet", "Original", greetHandler, WithDynamicSchema(fn))

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "schema.json")
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	os.Stdout = f
	err = outputSchema([]*ToolDef{tool}, map[string]any{})
	os.Stdout = oldStdout
	_ = f.Close()

	if err != nil {
		t.Fatalf("outputSchema: %v", err)
	}

	data, _ := os.ReadFile(outFile)
	var schema schemaOutput
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	entry, ok := schema.Tools["greet"]
	if !ok {
		t.Fatal("expected tool 'greet' with original name")
	}
	if entry.Description != "Original" {
		t.Errorf("description: got %q", entry.Description)
	}
}

func TestOutputSchema_WithConfigSchema(t *testing.T) {
	fields := []ConfigSchemaField{
		{Key: "db_url", Type: "string", Required: true},
	}
	tool := NewTool("db", "DB tool", greetHandler, WithConfigSchema(fields...))

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "schema.json")
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	os.Stdout = f
	err = outputSchema([]*ToolDef{tool}, nil)
	os.Stdout = oldStdout
	_ = f.Close()

	if err != nil {
		t.Fatalf("outputSchema: %v", err)
	}

	data, _ := os.ReadFile(outFile)
	var schema schemaOutput
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	entry := schema.Tools["db"]
	if len(entry.ConfigSchema) != 1 {
		t.Fatalf("expected 1 config schema field, got %d", len(entry.ConfigSchema))
	}
	if entry.ConfigSchema[0].Key != "db_url" {
		t.Errorf("key: got %q", entry.ConfigSchema[0].Key)
	}
}

func TestOutputSchema_WithVolumeParams(t *testing.T) {
	vols := []VolumeParamDecl{
		{Name: "ws", Mode: "readwrite", MountPath: "/workspace"},
	}
	tool := NewTool("proc", "Proc", greetHandler, WithVolumeParams(vols...))

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "schema.json")
	f, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}

	oldStdout := os.Stdout
	os.Stdout = f
	err = outputSchema([]*ToolDef{tool}, nil)
	os.Stdout = oldStdout
	_ = f.Close()

	if err != nil {
		t.Fatalf("outputSchema: %v", err)
	}

	data, _ := os.ReadFile(outFile)
	var schema schemaOutput
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	entry := schema.Tools["proc"]
	if len(entry.VolumeParams) != 1 {
		t.Fatalf("expected 1 volume param, got %d", len(entry.VolumeParams))
	}
	if entry.VolumeParams[0].MountPath != "/workspace" {
		t.Errorf("mount_path: got %q", entry.VolumeParams[0].MountPath)
	}
}

func TestMarshalWire_ErrorCodePresent(t *testing.T) {
	r := Error("auth needed", WithErrorCode(ErrorCodeAuthRequired))
	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["error_code"] != ErrorCodeAuthRequired {
		t.Errorf("error_code: got %v, want %q", wire["error_code"], ErrorCodeAuthRequired)
	}
}

func TestMarshalWire_NilContent(t *testing.T) {
	r := OK("done", WithDataObjects(DataObject{
		Name:        "empty.txt",
		Content:     nil,
		MIMEType:    "text/plain",
		Disposition: DispositionInline,
	}))

	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}

	objects := wire["data_objects"].([]any)
	obj := objects[0].(map[string]any)
	if obj["content"] != nil {
		t.Errorf("content should be nil for nil Content, got %v", obj["content"])
	}
}

func TestMarshalWire_EmptyDescription(t *testing.T) {
	r := OK("done", WithDataObjects(DataObject{
		Name:    "file.txt",
		Content: []byte("data"),
	}))

	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}

	objects := wire["data_objects"].([]any)
	obj := objects[0].(map[string]any)
	if obj["description"] != nil {
		t.Errorf("description should be nil for empty string, got %v", obj["description"])
	}
}

func TestFindTool(t *testing.T) {
	t1 := NewTool("a", "Tool A", greetHandler)
	t2 := NewTool("b", "Tool B", greetHandler)

	if found := findTool([]*ToolDef{t1, t2}, "a"); found != t1 {
		t.Error("expected to find tool 'a'")
	}
	if found := findTool([]*ToolDef{t1, t2}, "b"); found != t2 {
		t.Error("expected to find tool 'b'")
	}
	if found := findTool([]*ToolDef{t1, t2}, "c"); found != nil {
		t.Error("expected nil for unknown tool")
	}
	if found := findTool(nil, "a"); found != nil {
		t.Error("expected nil for empty tool list")
	}
}

func captureSDKWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := setSchemaWarnWriter(&buf)
	resetPrefixedDispatchWarnForTest()
	t.Cleanup(func() {
		setSchemaWarnWriter(prev)
		resetPrefixedDispatchWarnForTest()
	})
	return &buf
}

func TestFindToolPrefixedDispatchWarns(t *testing.T) {
	buf := captureSDKWarnings(t)
	tools := []*ToolDef{NewTool("generate_test_plan", "Plan", greetHandler)}

	for range 3 {
		if findTool(tools, "sam-test-harness__generate_test_plan") == nil {
			t.Fatal("fallback failed to resolve the tool")
		}
	}

	got := buf.String()
	if !strings.Contains(got, "owner-prefixed") || !strings.Contains(got, "generate_test_plan") {
		t.Errorf("no usable warning reached the sink; got %q", got)
	}
	if n := strings.Count(got, "samtoolsdk:"); n != 1 {
		t.Errorf("warning emitted %d times, want exactly 1 per process", n)
	}
}

func TestFindToolPrefixedDispatch(t *testing.T) {
	captureSDKWarnings(t)
	plan := NewTool("generate_test_plan", "Plan", greetHandler)
	recs := NewTool("generate_recommendations", "Recs", greetHandler)
	tools := []*ToolDef{plan, recs}

	if found := findTool(tools, "sam-test-harness__generate_test_plan"); found != plan {
		t.Error("expected prefixed name to resolve to the bare tool")
	}
	if found := findTool(tools, "any-package__generate_recommendations"); found != recs {
		t.Error("expected the owner prefix to be irrelevant to the match")
	}
	if found := findTool(tools, "sam-test-harness__nope"); found != nil {
		t.Error("expected nil when the bare half matches no registered tool")
	}
	if found := findTool(tools, "__generate_test_plan"); found != nil {
		t.Error("expected nil for an empty owner prefix")
	}
	if found := findTool(tools, "sam-test-harness__"); found != nil {
		t.Error("expected nil for an empty bare name")
	}
}

func TestFindToolPrefersExactMatch(t *testing.T) {
	prefixed := NewTool("owner__run", "Prefixed", greetHandler)
	bare := NewTool("run", "Bare", greetHandler)

	if found := findTool([]*ToolDef{bare, prefixed}, "owner__run"); found != prefixed {
		t.Error("exact match must win over the prefix-strip fallback")
	}
}

func TestShouldExtractToFile(t *testing.T) {
	tests := []struct {
		d    DataDisposition
		want bool
	}{
		{DispositionArtifact, true},
		{DispositionArtifactWithPreview, true},
		{DispositionAuto, true},
		{DispositionInline, false},
		{DataDisposition("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.d), func(t *testing.T) {
			if got := shouldExtractToFile(tt.d); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNilIfEmpty(t *testing.T) {
	if got := nilIfEmpty(""); got != nil {
		t.Errorf("expected nil for empty string, got %v", got)
	}
	if got := nilIfEmpty("hello"); got == nil || *got != "hello" {
		t.Errorf("expected pointer to 'hello', got %v", got)
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
