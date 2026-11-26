// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"encoding/json"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func writeJSONError(w http.ResponseWriter, status int, message string) string {
	errorID := idgen.New()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": message,
		"errorId": errorID,
	})
	return errorID
}
