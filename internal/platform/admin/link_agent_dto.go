// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type LinkAgentWakeRequest struct {
	URL            string            `json:"url"`
	TokenURL       string            `json:"tokenUrl,omitempty"`
	ClientID       string            `json:"clientId,omitempty"`
	ClientSecret   string            `json:"clientSecret,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	OnStart        *bool             `json:"onStart,omitempty"`
}

type CreateLinkAgentRequest struct {
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	EntrypointID     string                `json:"entrypointId"`
	TokenSHA256      string                `json:"tokenSha256,omitempty"`
	Enabled          *bool                 `json:"enabled,omitempty"`
	Owner            string                `json:"owner,omitempty"`
	Team             string                `json:"team,omitempty"`
	Peers            []string              `json:"peers,omitempty"`
	ShareUserProfile bool                  `json:"shareUserProfile,omitempty"`
	Wake             *LinkAgentWakeRequest `json:"wake,omitempty"`
}

type UpdateLinkAgentRequest CreateLinkAgentRequest

type LinkAgentSecretsCandidate struct {
	TokenSHA256      string
	WakeClientSecret string
	WakeHeaders      map[string]string
}

type LinkAgentWakeResponse struct {
	URL            string            `json:"url"`
	TokenURL       string            `json:"tokenUrl,omitempty"`
	ClientID       string            `json:"clientId,omitempty"`
	ClientSecret   string            `json:"clientSecret,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Headers        map[string]string `json:"headers"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	OnStart        bool              `json:"onStart"`
}

type DeployedLinkAgentWake struct {
	URL            string   `json:"url"`
	TokenURL       string   `json:"tokenUrl,omitempty"`
	ClientID       string   `json:"clientId,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	HeaderNames    []string `json:"headerNames"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	OnStart        bool     `json:"onStart"`
}

type DeployedLinkAgentInfo struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description,omitempty"`
	EntrypointID     string                 `json:"entrypointId"`
	TokenPrefix      string                 `json:"tokenPrefix"`
	Enabled          bool                   `json:"enabled"`
	Owner            string                 `json:"owner,omitempty"`
	Team             string                 `json:"team,omitempty"`
	Peers            []string               `json:"peers"`
	ShareUserProfile bool                   `json:"shareUserProfile"`
	Wake             *DeployedLinkAgentWake `json:"wake"`
	DeployedAt       Timestamp              `json:"deployedAt"`
	DeployedBy       string                 `json:"deployedBy"`
}

type LinkAgentResponse struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	InstanceName     string                 `json:"instanceName"`
	Description      string                 `json:"description"`
	EntrypointID     string                 `json:"entrypointId"`
	ConnectURL       string                 `json:"connectUrl,omitempty"`
	ConnectPath      string                 `json:"connectPath"`
	TokenPrefix      string                 `json:"tokenPrefix"`
	Enabled          bool                   `json:"enabled"`
	Owner            string                 `json:"owner"`
	Team             string                 `json:"team"`
	Peers            []string               `json:"peers"`
	ShareUserProfile bool                   `json:"shareUserProfile"`
	Wake             *LinkAgentWakeResponse `json:"wake"`
	DeploymentStatus string                 `json:"deploymentStatus"`
	SyncStatus       *string                `json:"syncStatus"`
	RuntimeStatus    RuntimeStatus          `json:"runtimeStatus"`
	Deployed         *DeployedLinkAgentInfo `json:"deployed"`
	CreatedBy        string                 `json:"createdBy"`
	UpdatedBy        string                 `json:"updatedBy"`
	CreatedTime      Timestamp              `json:"createdAt"`
	UpdatedTime      Timestamp              `json:"updatedAt"`
}
