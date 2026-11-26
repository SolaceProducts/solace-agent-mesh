// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type CreateRemoteAgentRequest struct {
	AgentURL              string            `json:"agentUrl" validate:"required,min=1,max=2048"`
	Name                  string            `json:"name" validate:"required,min=3,max=40,safename"`
	AgentCardLocation     string            `json:"agentCardLocation" validate:"required,oneof=well_known custom none"`
	Description           string            `json:"description,omitempty" validate:"omitempty,safetext"`
	AgentCardURL          string            `json:"agentCardUrl,omitempty"`
	AgentCardHeaders      map[string]string `json:"agentCardHeaders,omitempty"`
	AgentCardAuthType     string            `json:"agentCardAuthType,omitempty" validate:"omitempty,oneof=none bearer apikey basic oauth2"`
	AgentCardAuthConfig   any               `json:"agentCardAuthConfig,omitempty"`
	AgentAuthType         string            `json:"agentAuthType" validate:"omitempty,oneof=none bearer apikey basic oauth2"`
	AgentAuthConfig       any               `json:"agentAuthConfig,omitempty"`
	TaskHeaders           map[string]string `json:"taskHeaders,omitempty"`
	RequestTimeoutSeconds int               `json:"requestTimeoutSeconds,omitempty" validate:"omitempty,min=10,max=600"`
	Skills                any               `json:"skills,omitempty"`
	InputModes            []string          `json:"inputModes,omitempty"`
	OutputModes           []string          `json:"outputModes,omitempty"`
	ProtocolVersion       string            `json:"protocolVersion,omitempty" validate:"omitempty,oneof=auto v1_0 v0_3"`
	TrustedCardJWKSURLs   []string          `json:"trustedCardJwksUrls,omitempty"`
	RequireSignedCard     *bool             `json:"requireSignedCard,omitempty"`
}

type UpdateRemoteAgentRequest struct {
	AgentURL              string            `json:"agentUrl,omitempty" validate:"omitempty,min=1,max=2048"`
	Name                  string            `json:"name,omitempty" validate:"omitempty,min=3,max=40,safename"`
	AgentCardLocation     string            `json:"agentCardLocation,omitempty" validate:"omitempty,oneof=well_known custom none"`
	Description           string            `json:"description,omitempty" validate:"omitempty,safetext"`
	AgentCardURL          string            `json:"agentCardUrl,omitempty"`
	AgentCardHeaders      map[string]string `json:"agentCardHeaders,omitempty"`
	AgentCardAuthType     string            `json:"agentCardAuthType,omitempty" validate:"omitempty,oneof=none bearer apikey basic oauth2"`
	AgentCardAuthConfig   any               `json:"agentCardAuthConfig,omitempty"`
	AgentAuthType         string            `json:"agentAuthType,omitempty" validate:"omitempty,oneof=none bearer apikey basic oauth2"`
	AgentAuthConfig       any               `json:"agentAuthConfig,omitempty"`
	TaskHeaders           map[string]string `json:"taskHeaders,omitempty"`
	RequestTimeoutSeconds int               `json:"requestTimeoutSeconds,omitempty" validate:"omitempty,min=10,max=600"`
	Skills                any               `json:"skills,omitempty"`
	InputModes            []string          `json:"inputModes,omitempty"`
	OutputModes           []string          `json:"outputModes,omitempty"`
	ProtocolVersion       string            `json:"protocolVersion,omitempty" validate:"omitempty,oneof=auto v1_0 v0_3"`
	TrustedCardJWKSURLs   []string          `json:"trustedCardJwksUrls,omitempty"`
	RequireSignedCard     *bool             `json:"requireSignedCard,omitempty"`
}

type RemoteAgentResponse struct {
	ID                    string                  `json:"id"`
	AgentURL              string                  `json:"agentUrl"`
	Name                  string                  `json:"name"`
	InstanceName          string                  `json:"instanceName,omitempty"`
	Description           string                  `json:"description"`
	AgentCardLocation     string                  `json:"agentCardLocation"`
	AgentCardURL          string                  `json:"agentCardUrl,omitempty"`
	AgentCardHeaders      any                     `json:"agentCardHeaders,omitempty"`
	AgentCardAuthType     string                  `json:"agentCardAuthType,omitempty"`
	AgentCardAuthConfig   any                     `json:"agentCardAuthConfig,omitempty"`
	AgentAuthType         string                  `json:"agentAuthType"`
	AgentAuthConfig       any                     `json:"agentAuthConfig,omitempty"`
	TaskHeaders           any                     `json:"taskHeaders,omitempty"`
	RequestTimeoutSeconds int                     `json:"requestTimeoutSeconds"`
	Skills                any                     `json:"skills"`
	InputModes            any                     `json:"inputModes"`
	OutputModes           any                     `json:"outputModes"`
	ProtocolVersion       string                  `json:"protocolVersion"`
	TrustedCardJWKSURLs   []string                `json:"trustedCardJwksUrls"`
	RequireSignedCard     bool                    `json:"requireSignedCard"`
	RemoteProtocol        *RemoteProtocolStatus   `json:"remoteProtocol"`
	DeploymentStatus      string                  `json:"deploymentStatus"`
	SyncStatus            *string                 `json:"syncStatus"`
	RuntimeStatus         RuntimeStatus           `json:"runtimeStatus"`
	Deployed              any                     `json:"deployed"`
	CardData              *FetchAgentCardResponse `json:"cardData,omitempty"`
	CardFetchError        string                  `json:"cardFetchError,omitempty"`
	CreatedBy             string                  `json:"createdBy"`
	UpdatedBy             string                  `json:"updatedBy"`
	CreatedTime           Timestamp               `json:"createdAt"`
	UpdatedTime           Timestamp               `json:"updatedAt"`
}

type DeployedRemoteAgentInfo struct {
	AgentURL              string    `json:"agentUrl"`
	Name                  string    `json:"name"`
	Description           string    `json:"description,omitempty"`
	AgentCardLocation     string    `json:"agentCardLocation"`
	AgentCardURL          string    `json:"agentCardUrl,omitempty"`
	AgentCardAuthType     string    `json:"agentCardAuthType,omitempty"`
	AgentAuthType         string    `json:"agentAuthType"`
	RequestTimeoutSeconds int       `json:"requestTimeoutSeconds"`
	Skills                any       `json:"skills"`
	InputModes            any       `json:"inputModes"`
	OutputModes           any       `json:"outputModes"`
	ProtocolVersion       string    `json:"protocolVersion"`
	TrustedCardJWKSURLs   []string  `json:"trustedCardJwksUrls"`
	RequireSignedCard     bool      `json:"requireSignedCard"`
	DeployedAt            Timestamp `json:"deployedAt"`
	DeployedBy            string    `json:"deployedBy"`
}

type FetchAgentCardRequest struct {
	AgentCardURL       string            `json:"agentCardUrl" validate:"required,min=11,max=2048"`
	AuthenticationType string            `json:"authenticationType"`
	AuthConfig         *CardAuthConfig   `json:"authConfig,omitempty"`
	CustomHeaders      map[string]string `json:"customHeaders,omitempty"`
}

type CardAuthConfig struct {
	BearerToken        string `json:"bearerToken,omitempty"`
	ApikeyValue        string `json:"apikeyValue,omitempty"`
	ApikeyHeaderName   string `json:"apikeyHeaderName,omitempty"`
	OAuth2TokenURL     string `json:"oauth2TokenUrl,omitempty"`
	OAuth2ClientID     string `json:"oauth2ClientId,omitempty"`
	OAuth2ClientSecret string `json:"oauth2ClientSecret,omitempty"`
	OAuth2Scope        string `json:"oauth2Scope,omitempty"`
	BasicUsername      string `json:"basicUsername,omitempty"`
	BasicPassword      string `json:"basicPassword,omitempty"`
}

type FetchAgentCardResponse struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Skills      any                   `json:"skills"`
	InputModes  any                   `json:"inputModes"`
	OutputModes any                   `json:"outputModes"`
	Interfaces  []AdvertisedInterface `json:"interfaces"`
	Signed      bool                  `json:"signed"`
}
