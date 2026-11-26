// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !harness

package tools

import "github.com/SolaceProducts/solace-agent-mesh/internal/tool"

func additionalBuiltins() []tool.BuiltinTool { return nil }
