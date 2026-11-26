// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

func decodeJSON[T any](r *http.Request, dst *T) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func badRequestFromDecode(w http.ResponseWriter, err error) {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		errID := idgen.New()
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message: "validation error",
			ErrorID: errID,
			ValidationDetails: map[string][]string{
				typeErr.Field: {fmt.Sprintf("must be %s", typeErr.Type)},
			},
		})
		return
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field ") {
		field := strings.Trim(strings.TrimPrefix(msg, "json: unknown field "), `"`)
		errID := idgen.New()
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message: "unknown field in request body",
			ErrorID: errID,
			ValidationDetails: map[string][]string{
				field: {"unknown field"},
			},
		})
		return
	}
	writeError(w, http.StatusBadRequest, "invalid request body: "+msg)
}

func validatePathUUID(id string) error {
	if id == "" {
		return errors.New("path id is required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("path id must be a valid UUID")
	}
	return nil
}
