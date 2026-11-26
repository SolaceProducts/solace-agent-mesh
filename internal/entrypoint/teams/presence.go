// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

type teamsProactiveAddress struct {
	ServiceURL     string `json:"service_url"`
	ConversationID string `json:"conversation_id"`
	AadObjectID    string `json:"aad_object_id,omitempty"`
	FromID         string `json:"from_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
}
