// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type MCPDiscoverRequest struct {
	ServerURL      string            `json:"serverUrl" validate:"required"`
	ConnectionType string            `json:"connectionType,omitempty"`
	Auth           *MCPAuthInfo      `json:"auth,omitempty"`
	CustomHeaders  map[string]string `json:"customHeaders,omitempty"`
	SSLConfig      *MCPSSLConfig     `json:"sslConfig,omitempty"`
}

type MCPSSLConfig struct {
	CABundle   string `json:"caBundle,omitempty"`
	ClientCert string `json:"clientCert,omitempty"`
	ClientKey  string `json:"clientKey,omitempty"`
	ServerName string `json:"serverName,omitempty"`
}

type MCPAuthInfo struct {
	Type   string         `json:"type"`
	HTTP   *MCPHTTPAuth   `json:"http,omitempty"`
	APIKey *MCPAPIKeyAuth `json:"apikey,omitempty"`
	OAuth  *MCPOAuthAuth  `json:"oauth,omitempty"`
}

type MCPOAuthAuth struct {
	AccessToken string `json:"accessToken,omitempty"`
}

type MCPHTTPAuth struct {
	Scheme   string `json:"scheme"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type MCPAPIKeyAuth struct {
	In    string `json:"in"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type MCPDiscoveredTool struct {
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	InputSchema  any                 `json:"inputSchema"`
	OutputSchema any                 `json:"outputSchema,omitempty"`
	Annotations  *MCPToolAnnotations `json:"annotations,omitempty"`
}

type MCPToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

type MCPDiscoveryResponse struct {
	Tools []MCPDiscoveredTool `json:"tools"`
}

type MCPOAuthInitiateRequest struct {
	ServerURL               string `json:"serverUrl" validate:"required"`
	ConnectionType          string `json:"connectionType,omitempty"`
	Mode                    string `json:"mode"`
	AuthorizationURL        string `json:"authorizationUrl,omitempty"`
	TokenURL                string `json:"tokenUrl,omitempty"`
	ClientID                string `json:"clientId,omitempty"`
	ClientSecret            string `json:"clientSecret,omitempty"`
	Scopes                  string `json:"scopes,omitempty"`
	TokenEndpointAuthMethod string `json:"tokenEndpointAuthMethod,omitempty"`
}

type MCPOAuthInitiateResponse struct {
	AuthorizationURL string `json:"authorizationUrl"`
	State            string `json:"state"`
	SessionID        string `json:"sessionId"`
}

type MCPOAuthStatusResponse struct {
	Status string `json:"status"`
}

type MCPOAuthDiscoverRequest struct {
	SessionID      string            `json:"sessionId" validate:"required"`
	ServerURL      string            `json:"serverUrl"`
	ConnectionType string            `json:"connectionType,omitempty"`
	CustomHeaders  map[string]string `json:"customHeaders,omitempty"`
	SSLConfig      *MCPSSLConfig     `json:"sslConfig,omitempty"`
}
