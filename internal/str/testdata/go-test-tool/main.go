// Test Go tool binary for STR integration tests.
// Exercises: multi-tool dispatch, input artifacts, output artifacts,
// status updates, tool config, and DataObject production.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

// --- echo tool: basic parameters, status updates, config access ---

type EchoParams struct {
	Message string  `json:"message" desc:"Message to echo"`
	Prefix  *string `json:"prefix" desc:"Optional prefix"`
}

func echo(_ context.Context, p EchoParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if err := tc.SendStatus("Echoing message..."); err != nil {
		return nil, err
	}

	prefix := tc.GetConfigString("default_prefix", "")
	if p.Prefix != nil {
		prefix = *p.Prefix
	}

	msg := p.Message
	if prefix != "" {
		msg = prefix + ": " + msg
	}

	if err := tc.SendStatus("Done"); err != nil {
		return nil, err
	}

	return sdk.OK(msg, sdk.WithData(map[string]any{
		"user_id":    tc.UserID,
		"session_id": tc.SessionID,
		"app_name":   tc.AppName,
	})), nil
}

// --- process_file tool: input artifact, output artifact, DataObjects ---

type ProcessFileParams struct {
	Input    sdk.Artifact  `json:"input" desc:"Input file to process"`
	Template *sdk.Artifact `json:"template" desc:"Optional template file"`
}

func processFile(_ context.Context, p ProcessFileParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if err := tc.SendStatus("Processing file: " + p.Input.Filename); err != nil {
		return nil, err
	}

	content := p.Input.AsText()

	// If template provided, apply simple replacement.
	if p.Template != nil {
		content = strings.ReplaceAll(p.Template.AsText(), "{{content}}", content)
	}

	// Convert to uppercase as our "processing".
	processed := strings.ToUpper(content)

	// Produce output artifact via DataObject.
	outputName := "processed_" + p.Input.Filename
	result := sdk.OK(
		fmt.Sprintf("Processed %s (%d bytes → %d bytes)", p.Input.Filename, len(content), len(processed)),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        outputName,
			Content:     []byte(processed),
			MIMEType:    p.Input.MIMEType,
			Disposition: sdk.DispositionArtifact,
			Description: "Processed version of " + p.Input.Filename,
		}),
		sdk.WithData(map[string]any{
			"input_filename":  p.Input.Filename,
			"input_mime_type": p.Input.MIMEType,
			"output_filename": outputName,
		}),
	)

	return result, nil
}

// --- multi_artifact tool: multiple input artifacts ---

type MultiArtifactParams struct {
	Files []sdk.Artifact `json:"files" desc:"Multiple input files"`
}

func multiArtifact(_ context.Context, p MultiArtifactParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if err := tc.SendStatus(fmt.Sprintf("Processing %d files...", len(p.Files))); err != nil {
		return nil, err
	}

	var names []string
	totalSize := 0
	for _, f := range p.Files {
		names = append(names, f.Filename)
		totalSize += len(f.Content)
	}

	return sdk.OK(
		fmt.Sprintf("Processed %d files, total %d bytes", len(p.Files), totalSize),
		sdk.WithData(map[string]any{
			"filenames":  names,
			"file_count": len(p.Files),
			"total_size": totalSize,
		}),
	), nil
}

// --- authed_tool: tool that declares auth requirements ---

type AuthedParams struct {
	Query string `json:"query" desc:"Query to execute"`
}

func authedTool(_ context.Context, p AuthedParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	token := tc.GetAuthToken()
	if token == "" {
		return sdk.AuthRequired("No auth token available"), nil
	}
	return sdk.OK("Executed with auth: "+p.Query, sdk.WithData(map[string]any{
		"has_token": true,
	})), nil
}

// --- error_tool: returns an error result ---

type ErrorParams struct {
	Message string `json:"message" desc:"Error message"`
}

func errorTool(_ context.Context, p ErrorParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	return sdk.Error(p.Message, sdk.WithErrorCode("TEST_ERROR")), nil
}

// --- save_artifact tool: uses SaveArtifact via ToolContext ---

type SaveParams struct {
	Filename string `json:"filename" desc:"Output filename"`
	Content  string `json:"content" desc:"File content"`
}

func saveArtifact(_ context.Context, p SaveParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if err := tc.SaveArtifact(p.Filename, []byte(p.Content), "text/plain"); err != nil {
		return sdk.Error(fmt.Sprintf("save failed: %v", err)), nil
	}
	return sdk.OK("Saved " + p.Filename), nil
}

// --- summarize tool: uses CallLLM to invoke the LLM via IPC ---

type SummarizeParams struct {
	Text string `json:"text" desc:"Text to summarize"`
}

func summarize(ctx context.Context, p SummarizeParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if err := tc.SendStatus("Calling LLM..."); err != nil {
		return nil, err
	}

	response, err := tc.CallLLM(ctx,
		"You are a summarizer. Be concise.",
		"Summarize: "+p.Text,
		0.3,
	)
	if err != nil {
		return sdk.Error(fmt.Sprintf("LLM call failed: %v", err), sdk.WithErrorCode("LLM_ERROR")), nil
	}

	return sdk.OK(response, sdk.WithData(map[string]any{
		"original_length": len(p.Text),
		"summary_length":  len(response),
	})), nil
}

// --- bulk_output tool: returns a configurable-size payload via Data or DataObjects ---
// Exercises the STR's spill post-processor without requiring an LLM or agent.

type BulkOutputParams struct {
	SizeBytes int    `json:"size_bytes" desc:"Size of the synthetic payload in bytes"`
	Mode      string `json:"mode" desc:"data | data_objects"`
}

func bulkOutput(_ context.Context, p BulkOutputParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	size := p.SizeBytes
	if size <= 0 {
		size = 1024
	}
	blob := strings.Repeat("A", size)
	switch p.Mode {
	case "data_objects":
		return sdk.OK("bulk output via data_objects",
			sdk.WithDataObjects(sdk.DataObject{
				Name:        "bulk.txt",
				Content:     []byte(blob),
				MIMEType:    "text/plain",
				Disposition: sdk.DispositionInline,
			}),
		), nil
	default:
		return sdk.OK("bulk output via data", sdk.WithData(map[string]any{
			"payload": blob,
		})), nil
	}
}

type ChatParams struct {
	Text  string `json:"text" desc:"Text to send"`
	Model string `json:"model" desc:"Model alias the chat call asks for"`
}

func chat(ctx context.Context, p ChatParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	res, err := tc.Chat(ctx, []sdk.ChatMessage{{Role: "user", Content: p.Text}}, sdk.ChatOptions{Model: p.Model})
	if llmErr, ok := errors.AsType[*sdk.LLMError](err); ok {
		return sdk.Error(llmErr.Message, sdk.WithErrorCode(llmErr.Category)), nil
	}
	if err != nil {
		return nil, err
	}
	return sdk.OK(res.Content), nil
}

func main() {
	sdk.Run(
		sdk.NewTool("echo", "Echoes a message with optional prefix", echo,
			sdk.WithInstructions("Use this tool to echo messages")),
		sdk.NewTool("process_file", "Processes an input file", processFile),
		sdk.NewTool("multi_artifact", "Processes multiple files", multiArtifact),
		sdk.NewTool("error_tool", "Always returns an error", errorTool),
		sdk.NewTool("save_artifact", "Saves content as an artifact", saveArtifact),
		sdk.NewTool("summarize", "Summarizes text using LLM", summarize),
		sdk.NewTool("chat", "Sends one chat turn on a chosen model alias", chat),
		sdk.NewTool("bulk_output", "Returns a configurable-size payload", bulkOutput),
		sdk.NewTool("authed_tool", "Tool requiring OAuth2 authentication", authedTool,
			sdk.WithAuth(sdk.AuthSchemaConfig{
				Type: "oauth2",
				Scheme: &sdk.AuthSchemaOAuth{
					AuthorizationURL: "https://auth.example.com/authorize",
					TokenURL:         "https://auth.example.com/token",
					Scopes:           []string{"read", "write"},
				},
			})),
	)
}
