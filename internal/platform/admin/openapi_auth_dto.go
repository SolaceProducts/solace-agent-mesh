// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type SecuritySchemeInfo struct {
	SchemeName string `json:"schemeName"`
	AuthType   string `json:"authType"`
	Details    any    `json:"details"`
}

type SuggestedAuth struct {
	AuthType string `json:"authType"`
	Config   any    `json:"config"`
}

type OpenAPIDetectAuthResponse struct {
	Schemes       []SecuritySchemeInfo `json:"schemes"`
	SuggestedAuth *SuggestedAuth       `json:"suggestedAuth,omitempty"`
}
