// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import "github.com/SolaceProducts/solace-agent-mesh/pkg/harnesstools"

func additionalConformanceCases() []conformanceCase {
	return []conformanceCase{
		{
			name:     harnesstools.EchoBuiltinName,
			tl:       &harnesstools.EchoBuiltinTool{},
			args:     map[string]any{"sentinel": "conformance"},
			wantKeys: []string{"sentinel", "toolId", "process"},
		},
	}
}
