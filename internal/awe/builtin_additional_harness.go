// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build harness

package awe

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/harnesstools"
)

func lookupAdditionalBuiltin(name string, toolMap map[string]any) tool.BuiltinTool {
	switch name {
	case harnesstools.EchoBuiltinName:
		var cfg map[string]any
		if tc, ok := toolMap["tool_config"].(map[string]any); ok {
			cfg = tc
		}
		return harnesstools.NewEchoBuiltinTool(cfg)
	default:
		return nil
	}
}
