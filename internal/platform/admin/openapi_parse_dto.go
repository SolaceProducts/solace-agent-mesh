// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import openapitool "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"

type OpenAPIParseRequest struct {
	SpecURL    string   `json:"specUrl"`
	Operations []string `json:"operations,omitempty"`
}

type OpenAPIParseResponse struct {
	OpenAPIDetectAuthResponse
	Operations           []openapitool.OperationDescription `json:"operations"`
	OperationsTotal      int                                `json:"operationsTotal"`
	Truncated            bool                               `json:"truncated"`
	OperationsError      string                             `json:"operationsError,omitempty"`
	OperationsWarning    string                             `json:"operationsWarning,omitempty"`
	ClashingOperationIDs []string                           `json:"clashingOperationIds,omitempty"`
	Note                 string                             `json:"note"`
}
