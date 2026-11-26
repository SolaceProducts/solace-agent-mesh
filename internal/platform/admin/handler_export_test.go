// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestAuthError_DocumentsEveryFieldTheAuthCheckSends(t *testing.T) {
	rec := httptest.NewRecorder()
	auth.WriteError(rec, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, "sign in")

	sent := jsonFieldNames(t, rec.Body.Bytes())
	documented, err := json.Marshal(AuthError{Message: "m", ErrorID: "e", ErrorType: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if want := jsonFieldNames(t, documented); !slices.Equal(sent, want) {
		t.Errorf("auth.WriteError sends %v, AuthError documents %v", sent, want)
	}
}

func jsonFieldNames(t *testing.T, body []byte) []string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(fields))
}
