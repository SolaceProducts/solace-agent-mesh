// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
)

type runnerArgs struct {
	ToolName         string                    `json:"tool_name"`
	Args             map[string]any            `json:"args"`
	ToolConfig       map[string]any            `json:"tool_config"`
	ArtifactPaths    map[string]string         `json:"artifact_paths"`
	ArtifactMetadata map[string]map[string]any `json:"artifact_metadata"`
	StatusPipe       string                    `json:"status_pipe"`
	ResultFile       string                    `json:"result_file"`
	OutputDir        string                    `json:"output_dir"`
	IPCSocket        string                    `json:"ipc_socket"`
	UserID           string                    `json:"user_id"`
	SessionID        string                    `json:"session_id"`
	AppName          string                    `json:"app_name"`
	ToolOAuthToken   string                    `json:"tool_oauth_token"`
	ToolAuthHeaders  map[string]string         `json:"tool_auth_headers,omitempty"`
	VolumeMounts     []VolumeMount             `json:"volume_mounts,omitempty"`
	TaskID           string                    `json:"task_id,omitempty"`
	A2AUserConfig    string                    `json:"a2a_user_config,omitempty"`
	SearchTurn       *int                      `json:"search_turn,omitempty"`
}

type resultJSON struct {
	Result any    `json:"result"`
	Error  string `json:"error"`
}

// Run is the main entry point for a Go tool binary.
// Call this from main() with one or more tool definitions.
//
// Behavior:
//   - --schema: outputs tool schemas as JSON and exits.
//   - --schema --config <file>: loads config JSON, applies dynamic schema
//     overrides, and outputs the config-aware schemas.
//   - Otherwise, os.Args[1] must be the path to runner_args.json.
//     Reads the args, dispatches to the matching tool handler, and writes result.json.
func Run(tools ...*ToolDef) {
	if len(tools) == 0 {
		fmt.Fprintln(os.Stderr, "samtoolsdk: no tools registered")
		os.Exit(1)
	}

	schemaMode := false
	var configFile string
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--schema" {
			schemaMode = true
		}
		if arg == "--config" && i+1 < len(args) {
			configFile = args[i+1]
		}
	}

	if schemaMode {
		var config map[string]any
		if configFile != "" {
			data, err := os.ReadFile(configFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "samtoolsdk: reading config %s: %v\n", configFile, err)
				os.Exit(1)
			}
			if err := json.Unmarshal(data, &config); err != nil {
				fmt.Fprintf(os.Stderr, "samtoolsdk: parsing config %s: %v\n", configFile, err)
				os.Exit(1)
			}
		}
		if err := outputSchema(tools, config); err != nil {
			fmt.Fprintf(os.Stderr, "samtoolsdk: schema output failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "samtoolsdk: usage: <binary> <runner_args.json> | --schema [--config <file>]")
		os.Exit(1)
	}

	argsFile := os.Args[1]
	if err := execute(tools, argsFile); err != nil {
		fmt.Fprintf(os.Stderr, "samtoolsdk: %v\n", err)
		os.Exit(1)
	}
}

func execute(tools []*ToolDef, argsFile string) error {
	data, err := os.ReadFile(argsFile)
	if err != nil {
		return fmt.Errorf("reading runner_args: %w", err)
	}

	var args runnerArgs
	if unmarshalErr := json.Unmarshal(data, &args); unmarshalErr != nil {
		return fmt.Errorf("parsing runner_args: %w", unmarshalErr)
	}

	toolDef := findTool(tools, args.ToolName)
	if toolDef == nil {
		return writeError(args.ResultFile, fmt.Sprintf(
			"tool %q not found in binary (registered: %s)",
			args.ToolName, toolNames(tools)))
	}

	tc := &ToolContext{
		UserID:        args.UserID,
		SessionID:     args.SessionID,
		AppName:       args.AppName,
		TaskID:        args.TaskID,
		VolumeMounts:  args.VolumeMounts,
		config:        args.ToolConfig,
		outputDir:     args.OutputDir,
		oauthToken:    args.ToolOAuthToken,
		authHeaders:   args.ToolAuthHeaders,
		artifactPaths: args.ArtifactPaths,
		artifactMeta:  args.ArtifactMetadata,
		a2aUserConfig: args.A2AUserConfig,
		searchTurn:    args.SearchTurn,
	}

	if args.StatusPipe != "" {
		tc.status = newStatusWriter(args.StatusPipe)
	}

	if args.IPCSocket != "" {
		ipc, ipcErr := newIPCClient(args.IPCSocket)
		if ipcErr != nil {
			fmt.Fprintf(os.Stderr, "samtoolsdk: IPC connection failed: %v\n", ipcErr)
		} else {
			tc.ipc = ipc
		}
	}

	defer tc.close()

	artifactObjects, err := loadArtifacts(toolDef.artifactInfo, args.ArtifactPaths, args.ArtifactMetadata)
	if err != nil {
		return writeError(args.ResultFile, fmt.Sprintf("loading artifacts: %v", err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, execErr := invokeHandler(ctx, toolDef, args.Args, artifactObjects, tc)
	if execErr != nil {
		return writeError(args.ResultFile, execErr.Error())
	}

	if result != nil && len(result.DataObjects) > 0 && args.OutputDir != "" {
		extractArtifactOutputs(result, args.OutputDir)
	}

	return writeResult(args.ResultFile, result)
}

func invokeHandler(
	ctx context.Context,
	toolDef *ToolDef,
	rawArgs map[string]any,
	artifactObjects map[string]any,
	tc *ToolContext,
) (result *Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := compactStack(debug.Stack(), 3)
			err = fmt.Errorf("panic in tool handler: %v | %s", r, stack)
		}
	}()
	return toolDef.execute(ctx, rawArgs, artifactObjects, tc)
}

var sdkFramePrefixes = []string{
	"runtime/debug.Stack",
	"runtime.Stack",
	"runtime.gopanic",
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk.invokeHandler",
}

func compactStack(stack []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
	var frames []string

	i := 1
	for i < len(lines) && len(frames) < n {
		funcLine := strings.TrimSpace(lines[i])
		if funcLine == "" {
			i++
			continue
		}
		fileLine := ""
		if i+1 < len(lines) {
			fileLine = strings.TrimSpace(lines[i+1])
		}

		skip := false
		for _, prefix := range sdkFramePrefixes {
			if strings.HasPrefix(funcLine, prefix) {
				skip = true
				break
			}
		}
		if skip {
			i += 2
			continue
		}

		if fileLine != "" {
			frames = append(frames, funcLine+" | "+fileLine)
			i += 2
		} else {
			frames = append(frames, funcLine)
			i++
		}
	}

	return strings.Join(frames, " | ")
}

const toolNamePrefixSep = "__"

func findTool(tools []*ToolDef, name string) *ToolDef {
	for _, t := range tools {
		if t.name == name {
			return t
		}
	}
	owner, bare, prefixed := strings.Cut(name, toolNamePrefixSep)
	if !prefixed || owner == "" || bare == "" {
		return nil
	}
	for _, t := range tools {
		if t.name == bare {
			warnPrefixedDispatch(name, bare)
			return t
		}
	}
	return nil
}

const maxReportedToolNames = 20

func toolNames(tools []*ToolDef) string {
	names := make([]string, 0, min(len(tools), maxReportedToolNames))
	for _, t := range tools {
		if len(names) == maxReportedToolNames {
			return strings.Join(names, ", ") +
				fmt.Sprintf(" (+%d more)", len(tools)-maxReportedToolNames)
		}
		names = append(names, t.name)
	}
	return strings.Join(names, ", ")
}

var prefixedDispatchWarned sync.Once

func resetPrefixedDispatchWarnForTest() {
	prefixedDispatchWarned = sync.Once{}
}

func warnPrefixedDispatch(dispatched, matched string) {
	prefixedDispatchWarned.Do(func() {
		w := schemaWarnSink.Load()
		if w == nil || *w == nil {
			return
		}
		_, _ = fmt.Fprintf(*w,
			"samtoolsdk: dispatched tool name %q is owner-prefixed; matched registered tool %q. "+
				"The runtime did not resolve a bare dispatch name for this tool.\n",
			dispatched, matched)
	})
}

func loadArtifacts(
	artifactInfo map[string]artifactParamInfo,
	artifactPaths map[string]string,
	artifactMeta map[string]map[string]any,
) (map[string]any, error) {
	objects := make(map[string]any, len(artifactInfo))

	for paramName, info := range artifactInfo {
		if info.FilenameKey != "" {
			continue
		}
		if info.IsList {
			var artifacts []*Artifact
			for i := 0; ; i++ {
				key := fmt.Sprintf("%s[%d]", paramName, i)
				path, exists := artifactPaths[key]
				if !exists {
					break
				}
				a, err := loadSingleArtifact(key, path, artifactMeta)
				if err != nil {
					return nil, err
				}
				artifacts = append(artifacts, a)
			}
			objects[paramName] = artifacts
		} else {
			path, exists := artifactPaths[paramName]
			if !exists {
				if info.IsOptional {
					objects[paramName] = (*Artifact)(nil)
					continue
				}
				return nil, fmt.Errorf("required artifact %q not provided", paramName)
			}
			a, err := loadSingleArtifact(paramName, path, artifactMeta)
			if err != nil {
				return nil, err
			}
			objects[paramName] = a
		}
	}

	return objects, nil
}

func loadSingleArtifact(key, path string, meta map[string]map[string]any) (*Artifact, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading artifact %q at %s: %w", key, path, err)
	}

	a := &Artifact{
		Content:  content,
		Metadata: make(map[string]any),
	}

	if m, ok := meta[key]; ok {
		if fn, ok := m["filename"].(string); ok {
			a.Filename = fn
		}
		if mt, ok := m["mime_type"].(string); ok {
			a.MIMEType = mt
		}
		if v, ok := m["version"].(float64); ok {
			a.Version = int(v)
		}
		a.Metadata = m
	}

	return a, nil
}

func extractArtifactOutputs(result *Result, outputDir string) {
	cleanOutputDir := filepath.Clean(outputDir)
	var remaining []DataObject
	for _, do := range result.DataObjects {
		if shouldExtractToFile(do.Disposition) && do.Content != nil && do.Name != "" {
			path := filepath.Join(outputDir, do.Name)
			if !strings.HasPrefix(filepath.Clean(path), cleanOutputDir+string(filepath.Separator)) && filepath.Clean(path) != cleanOutputDir {
				remaining = append(remaining, do)
				continue
			}
			if err := os.WriteFile(path, do.Content, 0o600); err != nil {
				remaining = append(remaining, do)
				continue
			}
			do.Content = nil
			remaining = append(remaining, do)
		} else {
			remaining = append(remaining, do)
		}
	}
	result.DataObjects = remaining
}

func shouldExtractToFile(d DataDisposition) bool {
	switch d {
	case DispositionArtifact, DispositionArtifactWithPreview, DispositionAuto:
		return true
	default:
		return false
	}
}

func writeResult(path string, result *Result) error {
	var wireData any
	if result != nil {
		wireBytes, err := marshalWire(result)
		if err != nil {
			return writeError(path, fmt.Sprintf("marshaling result: %v", err))
		}
		if err := json.Unmarshal(wireBytes, &wireData); err != nil {
			return writeError(path, fmt.Sprintf("re-parsing wire result: %v", err))
		}
	}

	return writeResultJSON(path, resultJSON{Result: wireData})
}

func writeError(path string, msg string) error {
	msg = strings.ReplaceAll(msg, "\n", " | ")
	return writeResultJSON(path, resultJSON{Error: msg})
}

func writeResultJSON(path string, r resultJSON) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshaling result.json: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
