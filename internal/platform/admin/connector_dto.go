// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type CreateConnectorRequest struct {
	Name        string `json:"name" validate:"required,min=3,max=255,safename"`
	Description string `json:"description" validate:"required,min=10,max=1000,safetext"`
	Type        string `json:"type" validate:"required,max=50" cfgapply:"discriminator"`
	Subtype     string `json:"subtype" validate:"required,max=50" cfgapply:"discriminator"`
	Values      any    `json:"values" cfgapply:"secret"`
}

type UpdateConnectorRequest struct {
	Name        string `json:"name"`
	Description string `json:"description" validate:"omitempty,min=10,max=1000"`
	Values      any    `json:"values" cfgapply:"secret"`
}

type TestConnectorConnectionRequest struct {
	ConnectorID string `json:"connectorId,omitempty" validate:"omitempty,uuid"`
	Values      any    `json:"values,omitempty"`
}

type ConnectorResponse struct {
	ID          string    `json:"id" cfgapply:"read-only"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Type        string    `json:"type" cfgapply:"discriminator"`
	Subtype     string    `json:"subtype" cfgapply:"discriminator"`
	Values      any       `json:"values" cfgapply:"secret"`
	AgentCount  int       `json:"agentCount,omitempty" cfgapply:"read-only"`
	HasResource bool      `json:"hasResource,omitempty" cfgapply:"read-only"`
	ContentHash string    `json:"contentHash,omitempty" cfgapply:"read-only"`
	CreatedBy   string    `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy   string    `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime Timestamp `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime Timestamp `json:"updatedAt" cfgapply:"read-only"`

	Warnings []string `json:"warnings,omitempty" cfgapply:"read-only"`

	AgentRegistrations []ConnectorAgentRegistration `json:"agentRegistrations,omitempty" cfgapply:"read-only"`
}

type ConnectorAgentRegistration struct {
	Agent     ConnectorAgentRef `json:"agent"`
	Status    string            `json:"status"`
	Error     string            `json:"error,omitempty"`
	ToolCount int               `json:"toolCount"`
}

type ConnectorAgentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ConnectorSchemaFieldOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type ConnectorFieldValidation struct {
	MinLength *int   `json:"minLength,omitempty"`
	MaxLength *int   `json:"maxLength,omitempty"`
	Min       *int   `json:"min,omitempty"`
	Max       *int   `json:"max,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	ErrorMsg  string `json:"errorMsg,omitempty"`
}

type ConnectorSchemaField struct {
	Label              string                       `json:"label"`
	Type               string                       `json:"type"`
	Required           bool                         `json:"required"`
	Secret             bool                         `json:"secret"`
	Hidden             bool                         `json:"hidden"`
	Order              int                          `json:"order"`
	Default            any                          `json:"default,omitempty"`
	Placeholder        string                       `json:"placeholder,omitempty"`
	Hint               string                       `json:"hint,omitempty"`
	Options            []ConnectorSchemaFieldOption `json:"options,omitempty"`
	Validation         *ConnectorFieldValidation    `json:"validation,omitempty"`
	ConditionalDisplay map[string]string            `json:"conditionalDisplay,omitempty"`
}

type ConnectorSubtypeSchema struct {
	Subtype            string                          `json:"subtype"`
	Name               string                          `json:"name"`
	Description        string                          `json:"description"`
	UsageGuidance      string                          `json:"usageGuidance,omitempty"`
	FieldSchema        map[string]ConnectorSchemaField `json:"fieldSchema"`
	ConnectionTemplate string                          `json:"connectionTemplate,omitempty"`
	Warning            string                          `json:"warning,omitempty"`
	Experimental       bool                            `json:"experimental,omitempty"`
}

type ConnectorSchemaResponse struct {
	Type     string                   `json:"type"`
	Subtypes []ConnectorSubtypeSchema `json:"subtypes"`
}
