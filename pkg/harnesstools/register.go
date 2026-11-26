// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnesstools

import "github.com/SolaceProducts/solace-agent-mesh/internal/tool"

func BuiltinTools() []tool.BuiltinTool {
	return []tool.BuiltinTool{
		&EchoBuiltinTool{},
	}
}
