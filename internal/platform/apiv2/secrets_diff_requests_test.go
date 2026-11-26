// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type secretsDiffKind struct {
	name       string
	collection string
	field      string
	read       string
	builtinIDs bool
}

var secretsDiffKinds = []secretsDiffKind{
	{"entrypoint", "/entrypoints/", "values", rbac.ScopeEntrypointRead, false},
	{"connector", "/connectors/", "values", rbac.ScopeConnectorRead, false},
	{"model", "/models/", "authConfig", rbac.ScopeModelConfigRead, false},
	{"toolset", "/toolsets/", "values", rbac.ScopeToolsetRead, true},
}

func (k secretsDiffKind) path(id string) string { return k.collection + id + "/secretsDiff" }

func TestSecretsDiff_RefusesACallerWhoCannotUpdateTheResource(t *testing.T) {
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)
	for _, kind := range secretsDiffKinds {
		t.Run(kind.name, func(t *testing.T) {
			body := `{"` + kind.field + `":{}}`
			expectStatus(t, api.call(http.MethodPut, kind.path(missingID), body, kind.read), http.StatusForbidden)
			expectStatus(t, api.anonymous().call(http.MethodPut, kind.path(missingID), body), http.StatusUnauthorized)
		})
	}
}

func TestSecretsDiff_RefusesARequestItCannotRead(t *testing.T) {
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)
	for _, kind := range secretsDiffKinds {
		badIDStatus, badIDDetail := http.StatusBadRequest, "id"
		if kind.builtinIDs {
			badIDStatus, badIDDetail = http.StatusNotFound, ""
		}
		for _, tc := range []struct {
			name       string
			id         string
			body       string
			wantStatus int
			wantDetail string
		}{
			{"an id that is not a UUID", "not-a-uuid", `{"` + kind.field + `":{}}`, badIDStatus, badIDDetail},
			{"malformed JSON", missingID, `{not valid json`, http.StatusBadRequest, ""},
			{"a misspelled field", missingID, `{"` + kind.field + `s":{"token":"x"}}`, http.StatusBadRequest, kind.field + "s"},
			{"null values", missingID, `{"` + kind.field + `":null}`, http.StatusUnprocessableEntity, kind.field},
			{"string values", missingID, `{"` + kind.field + `":"not-an-object"}`, http.StatusBadRequest, kind.field},
			{"number values", missingID, `{"` + kind.field + `":42}`, http.StatusBadRequest, kind.field},
			{"array values", missingID, `{"` + kind.field + `":[]}`, http.StatusBadRequest, kind.field},
		} {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				rec := api.call(http.MethodPut, kind.path(tc.id), tc.body)
				if tc.wantDetail == "" {
					expectStatus(t, rec, tc.wantStatus)
					decodeError(t, rec)
					return
				}
				expectDetail(t, rec, tc.wantStatus, tc.wantDetail)
			})
		}
	}
}
