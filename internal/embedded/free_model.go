// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !solace_id

package embedded

import "github.com/SolaceProducts/solace-agent-mesh/internal/llm"

var grantsLeftByOtherEditions = []string{llm.FreeModelGrantID}
