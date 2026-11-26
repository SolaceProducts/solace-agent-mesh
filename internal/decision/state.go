// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"encoding/json"
	"fmt"
)

func StateText(state any) (string, error) {
	if text, ok := state.(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode decision state: %w", err)
	}
	return string(encoded), nil
}
