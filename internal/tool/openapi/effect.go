// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var _ tool.EffectClassifier = (*OpenAPITool)(nil)

func (t *OpenAPITool) EffectClass() tool.EffectClass {
	switch strings.ToUpper(t.method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return tool.DerivedEffect(tool.EffectReadOnly)
	default:
		return tool.DerivedEffect(tool.EffectSideEffecting)
	}
}
