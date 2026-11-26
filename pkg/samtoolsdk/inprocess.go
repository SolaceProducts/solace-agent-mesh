// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"fmt"
)

// InProcessBindings supplies host services to a tool invoked in-process (no
// subprocess, no file transport). Every field is optional: a tool that needs a
// binding the host did not supply gets the same not-available behaviour it
// would in a subprocess with that transport absent (e.g. CallLLM errors when
// LLM is nil, SendStatus is a no-op when Status is nil).
type InProcessBindings struct {
	UserID     string
	SessionID  string
	AppName    string
	TaskID     string
	OAuthToken string

	// AuthHeaders are framework-injected static auth headers (basic/bearer),
	// surfaced to the tool via ToolContext.AuthorizationHeader / AuthHeaders.
	AuthHeaders map[string]string

	VolumeMounts []VolumeMount

	// A2AUserConfig is the gateway-populated user-config JSON blob (carries
	// user_profile), surfaced to the tool via ToolContext.UserProfile exactly
	// as the subprocess runner_args do. Unsigned: never an authorization input.
	A2AUserConfig string

	// SearchTurn is the search turn the agent assigned to this call, surfaced
	// via ToolContext.SearchTurn. Nil when the agent assigned none.
	SearchTurn *int

	// OutputDir is where the tool's SaveArtifact writes and where DataObjects
	// with an artifact disposition are extracted. The host collects files left
	// here after the call returns. Empty disables artifact output.
	OutputDir string

	// ArtifactPaths / ArtifactMeta pre-stage input artifacts exactly as the
	// subprocess runner_args do, keyed by param name or "param[i]".
	ArtifactPaths map[string]string
	ArtifactMeta  map[string]map[string]any

	// Status receives status updates from the tool (ToolContext.SendStatus).
	Status func(string) error

	// LLM backs ToolContext.CallLLM.
	LLM func(ctx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error)

	// Chat backs ToolContext.Chat.
	Chat func(ctx context.Context, messages []ChatMessage, opts ChatOptions) (*ChatResult, error)
}

// SchemaJSON returns the same JSON document that `--schema` (or
// `--schema --config` when config is non-nil) writes to stdout for the given
// tools. The host can hand the bytes to the same parser it uses for subprocess
// schema discovery, so the in-process and subprocess schema shapes are
// byte-identical.
func SchemaJSON(tools []*ToolDef, config map[string]any) ([]byte, error) {
	return json.Marshal(buildSchemaOutput(tools, config))
}

// InvokeInProcess runs a registered tool in the current process — no
// subprocess, no runner_args.json/result.json round-trip. It mirrors run.go's
// execute(): deserialize params, pre-load artifacts, call the handler (with
// panic recovery), extract artifact outputs, and return the tool's wire result
// — the same value the subprocess path writes under result.json "result".
//
// A handler that returns a Go error (or panics) surfaces here as a non-nil
// error, mirroring the result.json "error" field. A tool-level failure
// expressed via Error(...) flows back as a normal (success=false) wire result,
// exactly as it does over the subprocess transport.
func InvokeInProcess(
	ctx context.Context,
	tools []*ToolDef,
	toolName string,
	args map[string]any,
	toolConfig map[string]any,
	b InProcessBindings,
) (any, error) {
	td := findTool(tools, toolName)
	if td == nil {
		return nil, fmt.Errorf("tool %q not found in in-process set", toolName)
	}

	tc := &ToolContext{
		UserID:        b.UserID,
		SessionID:     b.SessionID,
		AppName:       b.AppName,
		TaskID:        b.TaskID,
		VolumeMounts:  b.VolumeMounts,
		config:        toolConfig,
		outputDir:     b.OutputDir,
		oauthToken:    b.OAuthToken,
		authHeaders:   b.AuthHeaders,
		artifactPaths: b.ArtifactPaths,
		artifactMeta:  b.ArtifactMeta,
		a2aUserConfig: b.A2AUserConfig,
		searchTurn:    b.SearchTurn,
		statusFn:      b.Status,
		llmFn:         b.LLM,
		chatFn:        b.Chat,
	}

	artifactObjects, err := loadArtifacts(td.artifactInfo, b.ArtifactPaths, b.ArtifactMeta)
	if err != nil {
		return nil, fmt.Errorf("loading artifacts: %w", err)
	}

	result, execErr := invokeHandler(ctx, td, args, artifactObjects, tc)
	if execErr != nil {
		return nil, execErr
	}

	if result != nil && len(result.DataObjects) > 0 && b.OutputDir != "" {
		extractArtifactOutputs(result, b.OutputDir)
	}

	if result == nil {
		return nil, nil
	}

	wireBytes, err := marshalWire(result)
	if err != nil {
		return nil, fmt.Errorf("marshaling result: %w", err)
	}
	var wire any
	if err := json.Unmarshal(wireBytes, &wire); err != nil {
		return nil, fmt.Errorf("re-parsing wire result: %w", err)
	}
	return wire, nil
}
