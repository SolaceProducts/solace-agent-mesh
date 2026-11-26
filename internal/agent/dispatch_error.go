// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func classifyDispatchError(err error) string { return tool.ClassifyDispatchError(err) }
