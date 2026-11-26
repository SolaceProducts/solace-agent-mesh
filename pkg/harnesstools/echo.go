// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnesstools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const EchoBuiltinToolID = "OB.echo_builtin.v1"

const EchoBuiltinName = "echo_builtin"

type EchoBuiltinTool struct {
	tool.ReadOnlyTool
	config map[string]any
}

func NewEchoBuiltinTool(config map[string]any) *EchoBuiltinTool {
	return &EchoBuiltinTool{config: config}
}

func (t *EchoBuiltinTool) Name() string { return EchoBuiltinName }

func (t *EchoBuiltinTool) Description() string {
	return "Test-harness echo tool. Returns a structured fingerprint identifying " +
		"which built-in implementation served the call. Not for production use."
}

func (t *EchoBuiltinTool) Instructions() string { return "" }

func (t *EchoBuiltinTool) IsLongRunning() bool { return false }

func (t *EchoBuiltinTool) RawStringArgs() []string { return nil }

func (t *EchoBuiltinTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *EchoBuiltinTool) ContextParam() string { return "" }

func (t *EchoBuiltinTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"sentinel": map[string]any{
				"type":        "string",
				"description": "Test case identifier to echo back in the fingerprint.",
			},
		},
		Required: []string{"sentinel"},
	}
}

func (t *EchoBuiltinTool) AuthoredSchemas() bool { return true }

func (t *EchoBuiltinTool) ResultSchema() *tool.JSONSchema {
	field := func(typ, description string) map[string]any {
		return map[string]any{"type": typ, "description": description}
	}
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"sentinel":        field("string", "The test case identifier the caller passed in."),
			"toolId":          field("string", "Identifies which copy of the tool ran."),
			"process":         field("object", "Name and PID of the process that executed the tool."),
			"invokedByAgent":  field("string", "Agent that invoked the tool, empty when none."),
			"loadedFromSkill": field("string", "Loaded skill names joined with '+', empty when none."),
			"receivedConfig":  field("object", "The tool_config the tool was constructed with."),
		},
		Required: []string{
			"invokedByAgent", "loadedFromSkill", "process",
			"receivedConfig", "sentinel", "toolId",
		},
	}
}

func (t *EchoBuiltinTool) Execute(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	sentinel, _ := args["sentinel"].(string)

	resp := EchoResponse{
		Sentinel: sentinel,
		ToolID:   EchoBuiltinToolID,
		Process: ProcessRef{
			Name: filepath.Base(os.Args[0]),
			PID:  os.Getpid(),
		},
		InvokedByAgent:  toolCtx.AgentName,
		LoadedFromSkill: joinLoadedSkills(toolCtx),
		ReceivedConfig:  t.config,
	}

	return tool.OK(
		mustJSON(resp),
		tool.WithData(map[string]any{
			"sentinel":        resp.Sentinel,
			"toolId":          resp.ToolID,
			"process":         map[string]any{"name": resp.Process.Name, "pid": resp.Process.PID},
			"invokedByAgent":  resp.InvokedByAgent,
			"loadedFromSkill": resp.LoadedFromSkill,
			"receivedConfig":  resp.ReceivedConfig,
		}),
	), nil
}

func joinLoadedSkills(toolCtx *tool.Context) string {
	if toolCtx == nil || toolCtx.SkillState == nil {
		return ""
	}
	names := toolCtx.SkillState.LoadedNames()
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, "+")
}

func mustJSON(r EchoResponse) string {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"error":"harnesstools: failed to marshal EchoResponse: %v"}`, err)
	}
	return string(b)
}
