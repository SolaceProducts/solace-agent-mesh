// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

func decodeJSON(r *http.Request, v any) error { return apicontract.DecodeJSON(r, v) }

const maxJSONBodyBytes = apicontract.MaxJSONBodyBytes

func badRequestFromDecode(w http.ResponseWriter, err error) {
	msg := fmt.Sprintf("invalid request body: %s", err.Error())
	var details map[string][]string
	if errStr := err.Error(); strings.HasPrefix(errStr, "json: unknown field ") {
		if start := strings.Index(errStr, `"`); start >= 0 {
			if end := strings.LastIndex(errStr, `"`); end > start {
				field := errStr[start+1 : end]
				if field != "" {
					details = map[string][]string{field: {"unknown field"}}
				}
			}
		}
	}
	writeErrorWithDetails(w, http.StatusBadRequest, msg, details)
}
