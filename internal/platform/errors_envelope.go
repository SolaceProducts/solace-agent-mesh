// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"message": message,
		"errorId": idgen.New(),
	})
}
