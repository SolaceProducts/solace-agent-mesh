// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

var (
	toolBinaryPath string
	buildOnce      sync.Once
	buildErr       error
)

func getToolBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "imagemagick-test-*") //nolint:usetesting // sync.Once cache shared by every test; t.TempDir would delete it after the first
		if err != nil {
			buildErr = err
			return
		}
		toolBinaryPath = filepath.Join(tmpDir, "imagemagick-test")
		cmd := exec.Command("go", "build", "-o", toolBinaryPath, "..")
		cmd.Dir = "."
		cmd.Stderr = os.Stderr
		buildErr = cmd.Run()
	})
	if buildErr != nil {
		t.Fatalf("failed to build imagemagick tool: %v", buildErr)
	}
	return toolBinaryPath
}

func requireMagick(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("magick"); err != nil {
		t.Skip("ImageMagick not installed, skipping")
	}
}

func newRunner(t *testing.T) *str.SandboxRunner {
	t.Helper()
	cfg := str.SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   30,
		MaxConcurrentExecutions: 2,
	}
	return str.NewSandboxRunner(cfg, slog.Default())
}

func newArtifactService() *artifact.ScopedService {
	return &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: "test",
	}
}

func TestSchema(t *testing.T) {
	binary := getToolBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--schema")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("--schema failed: %v", err)
	}

	var schema struct {
		Tools map[string]struct {
			Description  string         `json:"description"`
			Parameters   map[string]any `json:"parameters"`
			Instructions string         `json:"instructions"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(output, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	tool, ok := schema.Tools["image_magick"]
	if !ok {
		t.Fatal("image_magick tool not in schema")
	}
	if tool.Description == "" {
		t.Error("description is empty")
	}
	if tool.Instructions == "" {
		t.Error("instructions is empty")
	}
	if !strings.Contains(tool.Instructions, "magick") {
		t.Error("instructions should mention magick")
	}
}

func TestCreateCanvas(t *testing.T) {
	requireMagick(t)
	binary := getToolBinary(t)
	runner := newRunner(t)

	entry := &str.ManifestEntry{
		ToolName:   "image_magick",
		Executable: binary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-canvas-1",
		ToolName:  "image_magick",
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		Args: map[string]any{
			"command":         "-size 100x100 canvas:red {output}",
			"input_images":    []any{},
			"output_filename": "red.png",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	artifactSvc := newArtifactService()
	result, invokeErr := runner.Execute(ctx, params, entry, artifactSvc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Fatalf("status: got %v, message: %v", result.ToolResult["status"], result.ToolResult["message"])
	}

	if len(result.CreatedArtifacts) == 0 {
		t.Fatal("expected at least 1 output artifact")
	}

	found := false
	for _, ca := range result.CreatedArtifacts {
		if ca.Filename == "red.png" {
			found = true
			if ca.SizeBytes == 0 {
				t.Error("output artifact has zero size")
			}
		}
	}
	if !found {
		t.Errorf("expected artifact 'red.png', got %v", result.CreatedArtifacts)
	}
}

func TestResizeImage(t *testing.T) {
	requireMagick(t)
	binary := getToolBinary(t)
	runner := newRunner(t)
	artifactSvc := newArtifactService()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.png")
	cmd := exec.Command("magick", "-size", "200x200", "canvas:blue", srcPath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create test image: %v", err)
	}
	srcBytes, _ := os.ReadFile(srcPath)

	loc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", "source.png")
	if _, err := artifactSvc.Inner.Save(ctx, loc, srcBytes, "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	entry := &str.ManifestEntry{
		ToolName:   "image_magick",
		Executable: binary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-resize-1",
		ToolName:  "image_magick",
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		Args: map[string]any{
			"command":         "{input[0]} -resize 50x50 {output}",
			"output_filename": "resized.png",
		},
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input_images[0]": {Filename: "source.png", Version: 0},
		},
	}

	result, invokeErr := runner.Execute(ctx, params, entry, artifactSvc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "success" {
		t.Fatalf("status: got %v, message: %v", result.ToolResult["status"], result.ToolResult["message"])
	}

	if len(result.CreatedArtifacts) == 0 {
		t.Fatal("expected resized output artifact")
	}

	found := false
	for _, ca := range result.CreatedArtifacts {
		if ca.Filename == "resized.png" {
			found = true
			if ca.SizeBytes == 0 {
				t.Error("resized artifact has zero size")
			}
		}
	}
	if !found {
		t.Error("expected artifact 'resized.png'")
	}
}

func TestFilenamesWithSpacesAndParentheses(t *testing.T) {
	requireMagick(t)
	binary := getToolBinary(t)
	runner := newRunner(t)
	artifactSvc := newArtifactService()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srcPath := filepath.Join(t.TempDir(), "source.png")
	if err := exec.Command("magick", "-size", "200x200", "canvas:blue", srcPath).Run(); err != nil {
		t.Fatalf("failed to create test image: %v", err)
	}
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	const inputName = "banner (1).png"
	loc := artifactSvc.Resolve("test-agent", "user-1", "sess-1", inputName)
	if _, err := artifactSvc.Inner.Save(ctx, loc, srcBytes, "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	const outputName = "my out (2).png"
	params := &remotetool.InvokeParams{
		TaskID:    "task-names-1",
		ToolName:  "image_magick",
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		Args: map[string]any{
			"command":         "{input[0]} -resize 50x50 {output}",
			"output_filename": outputName,
		},
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input_images[0]": {Filename: inputName, Version: 0},
		},
	}

	result, invokeErr := runner.Execute(ctx, params, &str.ManifestEntry{ToolName: "image_magick", Executable: binary}, artifactSvc, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute failed: %s: %s", invokeErr.Code, invokeErr.Message)
	}
	if result.ToolResult["status"] != "success" {
		t.Fatalf("status: got %v, message: %v", result.ToolResult["status"], result.ToolResult["message"])
	}
	for _, ca := range result.CreatedArtifacts {
		if ca.Filename == outputName && ca.SizeBytes > 0 {
			return
		}
	}
	t.Errorf("expected a non-empty artifact %q, got %v", outputName, result.CreatedArtifacts)
}

func TestBadCommand(t *testing.T) {
	requireMagick(t)
	binary := getToolBinary(t)
	runner := newRunner(t)

	entry := &str.ManifestEntry{
		ToolName:   "image_magick",
		Executable: binary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-bad-1",
		ToolName:  "image_magick",
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		Args: map[string]any{
			"command":         "--invalid-flag {output}",
			"input_images":    []any{},
			"output_filename": "bad.png",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute returned invokeErr (expected tool-level error): %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "error" {
		t.Fatalf("expected error status, got %v", result.ToolResult["status"])
	}

	data, ok := result.ToolResult["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data map with error details")
	}
	stderr, _ := data["stderr"].(string)
	if stderr == "" {
		t.Error("expected stderr output from failed magick command")
	}
}

func TestMissingInputPlaceholder(t *testing.T) {
	binary := getToolBinary(t)
	runner := newRunner(t)

	entry := &str.ManifestEntry{
		ToolName:   "image_magick",
		Executable: binary,
	}

	params := &remotetool.InvokeParams{
		TaskID:    "task-missing-1",
		ToolName:  "image_magick",
		AppName:   "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
		Args: map[string]any{
			"command":         "{input[0]} -resize 50% {output}",
			"input_images":    []any{},
			"output_filename": "out.png",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, invokeErr := runner.Execute(ctx, params, entry, nil, func(_ string) {})
	if invokeErr != nil {
		t.Fatalf("Execute returned invokeErr: %s: %s", invokeErr.Code, invokeErr.Message)
	}

	if result.ToolResult["status"] != "error" {
		t.Fatalf("expected error status, got %v", result.ToolResult["status"])
	}

	msg, _ := result.ToolResult["message"].(string)
	if !strings.Contains(msg, "{input[0]}") {
		t.Errorf("error message should mention unreplaced placeholder, got: %s", msg)
	}
}
