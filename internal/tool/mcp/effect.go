// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ToolHints struct {
	ReadOnlyHint    *bool
	DestructiveHint *bool
	OpenWorldHint   *bool
}

func (h ToolHints) effectClass(trusted bool) tool.EffectClass {
	readOnly := h.ReadOnlyHint != nil && *h.ReadOnlyHint
	destructive := h.DestructiveHint != nil && *h.DestructiveHint
	statedOpenWorld := h.OpenWorldHint != nil && *h.OpenWorldHint
	openWorld := h.OpenWorldHint == nil || statedOpenWorld
	switch {
	case readOnly && !destructive && !openWorld && trusted:
		return tool.HintedEffect(tool.EffectReadOnly)
	case readOnly && !destructive && !statedOpenWorld && !trusted:
		return tool.UntrustedHintEffect(tool.EffectSideEffecting)
	case h.ReadOnlyHint != nil || h.DestructiveHint != nil || statedOpenWorld:
		return tool.HintedEffect(tool.EffectSideEffecting)
	default:
		return tool.DefaultEffectClass
	}
}

func hintsFromMCPGo(a mcpgo.ToolAnnotation) ToolHints {
	return ToolHints{ReadOnlyHint: a.ReadOnlyHint, DestructiveHint: a.DestructiveHint, OpenWorldHint: a.OpenWorldHint}
}

func hintsFromBifrost(a *schemas.MCPToolAnnotations) ToolHints {
	if a == nil {
		return ToolHints{}
	}
	return ToolHints{ReadOnlyHint: a.ReadOnlyHint, DestructiveHint: a.DestructiveHint, OpenWorldHint: a.OpenWorldHint}
}

func parseManifestHints(raw any) (ToolHints, error) {
	if raw == nil {
		return ToolHints{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ToolHints{}, fmt.Errorf("annotations must be a map, got %T", raw)
	}
	readOnly, err := optionalBool(m, "readOnlyHint")
	if err != nil {
		return ToolHints{}, err
	}
	destructive, err := optionalBool(m, "destructiveHint")
	if err != nil {
		return ToolHints{}, err
	}
	openWorld, err := optionalBool(m, "openWorldHint")
	if err != nil {
		return ToolHints{}, err
	}
	return ToolHints{ReadOnlyHint: readOnly, DestructiveHint: destructive, OpenWorldHint: openWorld}, nil
}

func ValidateManifestAnnotations(entry map[string]any) error {
	_, err := parseManifestHints(entry["annotations"])
	return err
}

func optionalBool(m map[string]any, key string) (*bool, error) {
	raw, present := m[key]
	if !present || raw == nil {
		return nil, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return nil, fmt.Errorf("annotations.%s must be a bool, got %T", key, raw)
	}
	return &b, nil
}

func (t *MCPTool) EffectClass() tool.EffectClass { return t.hints.effectClass(t.trustAnnotations) }

var (
	_ tool.EffectClassifier       = (*MCPTool)(nil)
	_ tool.ResolvedEffectExecutor = (*MCPTool)(nil)
)
