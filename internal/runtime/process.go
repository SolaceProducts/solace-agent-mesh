// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import "github.com/SolaceProducts/solace-agent-mesh/internal/idgen"

var processID = idgen.New()

func ProcessID() string { return processID }
