// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type CreateGatewayRequest struct {
	Name        string `json:"name" validate:"required,min=3,max=255,safename"`
	Slug        string `json:"slug" validate:"omitempty,min=3,max=63,gatewayslug" cfgapply:"omit-hash"`
	Description string `json:"description" validate:"required,min=10,max=1000,safetext"`
	Type        string `json:"type" validate:"required,min=2,max=50" cfgapply:"discriminator"`
	PublicURL   string `json:"publicUrl,omitempty"`
	Values      any    `json:"values" cfgapply:"secret"`
	Deploy      bool   `json:"deploy" cfgapply:"omit-hash"`
}

type UpdateGatewayRequest struct {
	Name        string  `json:"name" validate:"omitempty,min=3,max=255,safename"`
	Description string  `json:"description" validate:"omitempty,min=10,max=1000,safetext"`
	Type        string  `json:"type" validate:"-" cfgapply:"discriminator"`
	PublicURL   *string `json:"publicUrl,omitempty"`
	Values      any     `json:"values" cfgapply:"secret"`
	Deploy      bool    `json:"deploy" cfgapply:"omit-hash"`
}

type GatewayResponse struct {
	ID               string        `json:"id" cfgapply:"read-only"`
	Slug             string        `json:"slug" cfgapply:"read-only"`
	DNSID            string        `json:"dnsId,omitempty" cfgapply:"read-only"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	Type             string        `json:"type" cfgapply:"discriminator"`
	PublicURL        string        `json:"publicUrl,omitempty"`
	Values           any           `json:"values" cfgapply:"secret"`
	DeploymentStatus string        `json:"deploymentStatus" cfgapply:"read-only"`
	SyncStatus       *string       `json:"syncStatus" cfgapply:"read-only"`
	ConnectionStatus *string       `json:"connectionStatus" cfgapply:"read-only"`
	Deployed         any           `json:"deployed" cfgapply:"read-only"`
	RuntimeStatus    RuntimeStatus `json:"runtimeStatus" cfgapply:"read-only"`
	CreatedBy        string        `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy        string        `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime      Timestamp     `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime      Timestamp     `json:"updatedAt" cfgapply:"read-only"`
}

type GatewaySchemaResponse struct {
	Type             string   `json:"type"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Warning          string   `json:"warning,omitempty"`
	Schema           any      `json:"schema"`
	YAMLTemplatePath string   `json:"yamlTemplatePath"`
	RequiredEnvVars  []string `json:"requiredEnvVars"`
}

type GatewayNetworkingResponse struct {
	DNSName     string           `json:"dnsName"`
	Port        int              `json:"port"`
	BackendPort int              `json:"backendPort"`
	URL         string           `json:"url"`
	WebhookURL  string           `json:"webhookUrl,omitempty"`
	APIPaths    []GatewayAPIPath `json:"apiPaths"`
	PublicURL   string           `json:"publicUrl,omitempty"`
}

type GatewayAPIPath struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}
