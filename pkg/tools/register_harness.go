// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build harness

package tools

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/harnesstools"
)

func additionalBuiltins() []tool.BuiltinTool {
	return harnesstools.BuiltinTools()
}
