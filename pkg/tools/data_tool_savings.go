// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func withheldBytes(saved []byte, preview any, previewTruncated bool) int64 {
	if !previewTruncated {
		return 0
	}
	previewJSON, err := json.Marshal(preview)
	if err != nil {
		return 0
	}
	return tool.WithheldNet(int64(compactJSONLen(saved)), int64(len(previewJSON)))
}

func compactJSONLen(b []byte) int {
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		return len(b)
	}
	return compact.Len()
}
