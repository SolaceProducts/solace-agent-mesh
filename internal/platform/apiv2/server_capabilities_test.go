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

func TestServerCapabilities(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		rec := newRBACAPI(t).call(http.MethodGet, "/serverCapabilities", nil)
		expectStatus(t, rec, http.StatusOK)
		if got := rec.Body.String(); got != `{"data":{"manageRbac":true,"createDecisionModels":true}}`+"\n" {
			t.Errorf("body %q", got)
		}
	})

	t.Run("mini IDP", func(t *testing.T) {
		rec := newRBACAPI(t, withMiniIDP).call(http.MethodGet, "/serverCapabilities", nil)
		expectStatus(t, rec, http.StatusOK)
		if got := decodeData[ServerCapabilities](t, rec); got.ManageRbac {
			t.Errorf("manageRbac under the mini IDP = true")
		}
	})

	t.Run("any authenticated caller", func(t *testing.T) {
		rec := newRBACAPI(t).call(http.MethodGet, "/serverCapabilities", nil, rbac.ScopeConnectorRead)
		expectStatus(t, rec, http.StatusOK)
	})
}
