// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type (
	HILToolConfig = tool.HILToolConfig
	ApprovalRule  = tool.ApprovalRule
	ApprovalOp    = tool.ApprovalOp
)

const (
	OpEq        = tool.OpEq
	OpIn        = tool.OpIn
	OpNotIn     = tool.OpNotIn
	OpExists    = tool.OpExists
	OpNotExists = tool.OpNotExists
	OpGt        = tool.OpGt
	OpLt        = tool.OpLt
	OpGte       = tool.OpGte
	OpLte       = tool.OpLte
)
