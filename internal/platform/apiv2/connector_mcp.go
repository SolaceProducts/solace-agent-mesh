// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	msgMCPRegistrationRefused = "The OAuth provider refused to register a client for this server."

	mcpCallbackPath = "/connectors/mcp/oauth/callback"
	htmlContentType = "text/html; charset=utf-8"
)

const publicReasonMCPOAuthCallback = "MCP OAuth callback: an identity provider redirects the user's browser here, so the request carries no token; it is authorized instead by the random, server-issued, expiring state value from a flow an authorized caller started, and a state matching no live entry is refused"

func connectorMCPRoutes(h *connectorHandler) []Route {
	return []Route{
		{Method: http.MethodPut, Path: "/connectors/mcp/discover", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "discoverMCPTools", Summary: "Get the tools of an MCP server",
				Text: "Connects to an MCP server, lists the tools it publishes, and disconnects. Nothing is saved. If the server cannot be reached, the request fails with 502.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}},
			Handler: Handle(h.discoverMCPTools, "Unable to list the MCP server's tools")},
		{Method: http.MethodPut, Path: "/connectors/mcp/oauth/initiate", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "initiateMCPOAuth", Summary: "Initiate an MCP OAuth authorization",
				Text: "Starts an authorization that a user completes in their browser, and returns the address to send them to. In discovery mode, Agent Mesh reads the server's OAuth settings and registers a client with the provider. In manual mode, you supply the settings. If the provider's settings cannot be read or it refuses to register a client, the request fails with 502.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity, http.StatusBadGateway, http.StatusServiceUnavailable}},
			Handler: Handle(h.initiateMCPOAuth, "Unable to start the MCP authorization")},
		{Method: http.MethodGet, Path: mcpCallbackPath,
			Gate:      GatePublic,
			Stability: StabilityDraft,
			Doc: Doc{ID: "completeMCPOAuth", Summary: "Update an MCP OAuth authorization from the provider's callback",
				Text: "Exchanges the authorization code for a token and shows a page telling the user to close the window. The identity provider sends the user's browser here after they authorize. Not called directly.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity, http.StatusServiceUnavailable}},
			Handler: Handle(h.completeMCPOAuth, "Unable to complete the MCP authorization")},
		{Method: http.MethodGet, Path: "/connectors/mcp/oauth/status", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getMCPOAuthStatus", Summary: "Get an MCP OAuth authorization's status",
				Text: "Reports whether the authorization named by sessionId has obtained its access token. Poll it while the user authorizes in the browser, because the callback page cannot reach a desktop app's window. Reports only the caller's own authorizations.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity, http.StatusServiceUnavailable}},
			Handler: Handle(h.getMCPOAuthStatus, "Unable to get the MCP authorization status")},
		{Method: http.MethodPut, Path: "/connectors/mcp/oauth/discoverWithToken", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "discoverMCPToolsWithToken", Summary: "Get the tools of an MCP server using an authorization",
				Text: "Lists the tools of the MCP server a completed authorization was granted for, using its access token. The first request spends the authorization, whether or not it succeeds, so a retry or a second page needs a new authorization. Ask for the whole list in one page (pageSize=100) and read meta.pagination.count to tell whether anything was cut off. If the server cannot be reached, the request fails with 502.", Tag: connectorsTag,
				Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusBadGateway, http.StatusServiceUnavailable}},
			Handler: Handle(h.discoverMCPToolsWithToken, "Unable to list the MCP server's tools")},
	}
}

type MCPTool struct {
	Name         string              `json:"name" example:"search_orders"`
	Description  string              `json:"description" example:"Search orders by customer, status or date range."`
	InputSchema  any                 `json:"inputSchema" example:"{\"type\":\"object\",\"properties\":{\"customerId\":{\"type\":\"string\"}}}"`
	OutputSchema any                 `json:"outputSchema" example:"{\"type\":\"object\",\"properties\":{\"orders\":{\"type\":\"array\",\"items\":{\"type\":\"object\"}}}}"`
	Annotations  *MCPToolAnnotations `json:"annotations"`
}

type MCPToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint" example:"true"`
	DestructiveHint *bool `json:"destructiveHint" example:"false"`
	OpenWorldHint   *bool `json:"openWorldHint" example:"false"`
}

type MCPDiscoverRequest struct {
	Page           Page              `json:"-"`
	ServerURL      string            `json:"serverUrl" validate:"required" example:"https://mcp.example.com/mcp"`
	ConnectionType string            `json:"connectionType" validate:"omitempty,oneof=sse streamable_http" example:"streamable_http"`
	Auth           *MCPAuth          `json:"auth"`
	CustomHeaders  map[string]string `json:"customHeaders" example:"{\"X-Tenant\":\"acme\"}"`
	SSLConfig      *MCPSSLConfig     `json:"sslConfig"`
}

type MCPSSLConfig struct {
	CABundle   string `json:"caBundle" example:"/etc/sam/mcp-tls/ca.pem"`
	ClientCert string `json:"clientCert" example:"/etc/sam/mcp-tls/client.crt"`
	ClientKey  string `json:"clientKey" example:"/etc/sam/mcp-tls/client.key"`
	ServerName string `json:"serverName" example:"backend.mcp.internal"`
}

type MCPAuth struct {
	Type   string         `json:"type" validate:"omitempty,oneof=none http apikey oauth" example:"http"`
	HTTP   *MCPHTTPAuth   `json:"http"`
	APIKey *MCPAPIKeyAuth `json:"apiKey"`
	OAuth  *MCPOAuthAuth  `json:"oauth"`
}

type MCPHTTPAuth struct {
	Scheme   string `json:"scheme" validate:"omitempty,oneof=bearer basic" example:"bearer"`
	Token    string `json:"token" example:"placeholder-bearer-token"`
	Username string `json:"username" example:"discovery"`
	Password string `json:"password" example:"placeholder-password"`
}

type MCPAPIKeyAuth struct {
	In    string `json:"in" validate:"omitempty,oneof=header query" example:"header"`
	Name  string `json:"name" example:"X-API-Key"`
	Value string `json:"value" example:"placeholder-api-key"`
}

type MCPOAuthAuth struct {
	AccessToken string `json:"accessToken" example:"placeholder-access-token"`
}

type MCPOAuthInitiateRequest struct {
	Origin                  RequestOrigin `json:"-"`
	ServerURL               string        `json:"serverUrl" validate:"required" example:"https://mcp.example.com/mcp"`
	ConnectionType          string        `json:"connectionType" validate:"omitempty,oneof=sse streamable_http" example:"streamable_http"`
	Mode                    string        `json:"mode" validate:"omitempty,oneof=discovery manual" example:"discovery"`
	AuthorizationURL        string        `json:"authorizationUrl" example:"https://auth.example.com/authorize"`
	TokenURL                string        `json:"tokenUrl" example:"https://auth.example.com/token"`
	ClientID                string        `json:"clientId" example:"sam-mcp-discovery"`
	ClientSecret            string        `json:"clientSecret" example:"placeholder-client-secret"`
	Scopes                  string        `json:"scopes" example:"mcp:read mcp:tools"`
	TokenEndpointAuthMethod string        `json:"tokenEndpointAuthMethod" example:"client_secret_basic"`
}

type MCPOAuthInitiateResult struct {
	AuthorizationURL string `json:"authorizationUrl" example:"https://auth.example.com/authorize?client_id=sam-mcp-discovery&response_type=code&state=4f3c1d2e5a6b7c8d"`
	State            string `json:"state" example:"4f3c1d2e5a6b7c8d9e0f1a2b3c4d5e6f"`
	SessionID        string `json:"sessionId" example:"7c2e44118aa04a559f0d6b2a1e3c4d5f"`
}

type mcpOAuthCallbackRequest struct {
	Code  string `query:"code" json:"-" required:"true" example:"6d9f0a1b2c3d4e5f"`
	State string `query:"state" json:"-" required:"true" example:"4f3c1d2e5a6b7c8d9e0f1a2b3c4d5e6f"`
}

type mcpOAuthStatusRequest struct {
	SessionID string `query:"sessionId" json:"-" required:"true" example:"7c2e44118aa04a559f0d6b2a1e3c4d5f"`
}

type MCPOAuthStatusResult struct {
	Status string `json:"status" example:"pending"`
}

type MCPOAuthDiscoverRequest struct {
	Page           Page          `json:"-"`
	SessionID      string        `json:"sessionId" validate:"required" example:"7c2e44118aa04a559f0d6b2a1e3c4d5f"`
	ServerURL      string        `json:"serverUrl" example:"https://mcp.example.com/mcp"`
	ConnectionType string        `json:"connectionType" validate:"omitempty,oneof=sse streamable_http" example:"streamable_http"`
	SSLConfig      *MCPSSLConfig `json:"sslConfig"`
}

func (h *connectorHandler) discoverMCPTools(ctx context.Context, req MCPDiscoverRequest) (List[MCPTool], error) {
	if err := refuseTLSFilesToConnectorReaders(ctx, req.SSLConfig); err != nil {
		return List[MCPTool]{}, err
	}
	tools, err := h.mcp.DiscoverTools(ctx, entplatform.MCPDiscoverRequest{
		ServerURL:      req.ServerURL,
		ConnectionType: transportName(req.ConnectionType),
		Auth:           toMCPAuthInfo(req.Auth),
		CustomHeaders:  req.CustomHeaders,
		SSLConfig:      toMCPSSLConfig(req.SSLConfig),
	})
	if err != nil {
		return List[MCPTool]{}, mcpDiscoveryError(err)
	}
	return PageOf(toMCPTools(tools), req.Page), nil
}

func refuseTLSFilesToConnectorReaders(ctx context.Context, ssl *MCPSSLConfig) error {
	if ssl == nil || (ssl.CABundle == "" && ssl.ClientCert == "" && ssl.ClientKey == "") {
		return nil
	}
	scopes := entplatform.ScopesFromContext(ctx)
	if coreauth.HasScope(scopes, rbac.ScopeConnectorCreate) || coreauth.HasScope(scopes, rbac.ScopeConnectorUpdate) {
		return nil
	}
	return &CallerError{Status: http.StatusForbidden, Message: msgNoPermission}
}

func toMCPSSLConfig(ssl *MCPSSLConfig) *entplatform.MCPSSLConfig {
	if ssl == nil {
		return nil
	}
	return &entplatform.MCPSSLConfig{
		CABundle:   ssl.CABundle,
		ClientCert: ssl.ClientCert,
		ClientKey:  ssl.ClientKey,
		ServerName: ssl.ServerName,
	}
}

func mcpDiscoveryError(err error) error {
	failure, ok := errors.AsType[*apierr.ExternalServiceError](err)
	if !ok {
		return err
	}
	return &CallerError{Status: http.StatusBadGateway, Message: failure.Message, Cause: redactedCause{entplatform.LogCause(err)}}
}

func (h *connectorHandler) initiateMCPOAuth(ctx context.Context, req MCPOAuthInitiateRequest) (MCPOAuthInitiateResult, error) {
	resp, err := h.mcp.BeginOAuth(ctx, entplatform.MCPOAuthInitiateRequest{
		ServerURL:               req.ServerURL,
		ConnectionType:          transportName(req.ConnectionType),
		Mode:                    req.Mode,
		AuthorizationURL:        req.AuthorizationURL,
		TokenURL:                req.TokenURL,
		ClientID:                req.ClientID,
		ClientSecret:            req.ClientSecret,
		Scopes:                  req.Scopes,
		TokenEndpointAuthMethod: req.TokenEndpointAuthMethod,
	}, req.Origin.URL(h.callbackPrefix+mcpCallbackPath))
	if err != nil {
		return MCPOAuthInitiateResult{}, mcpOAuthError(err)
	}
	return MCPOAuthInitiateResult{
		AuthorizationURL: resp.AuthorizationURL,
		State:            resp.State,
		SessionID:        resp.SessionID,
	}, nil
}

func (h *connectorHandler) completeMCPOAuth(ctx context.Context, req mcpOAuthCallbackRequest) (HTMLPage, error) {
	if missing := missingCallbackFields(req); len(missing) > 0 {
		return nil, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam, Details: missing}
	}
	sessionID, err := h.mcp.CompleteOAuth(ctx, req.Code, req.State)
	if err != nil {
		if refusal, ok := entplatform.MCPOAuthRefusal(err); ok && refusal.TokenExchange {
			return mcpOAuthPage(`{"type":"mcp-oauth-error","error":"Token exchange failed"}`,
				"Authorization failed. You may close this window."), nil
		}
		return nil, mcpOAuthError(err)
	}
	return mcpOAuthPage(
		mcpCallbackMessage(sessionID, req.State), "Authorization complete. You may close this window."), nil
}

func missingCallbackFields(req mcpOAuthCallbackRequest) map[string][]string {
	missing := map[string][]string{}
	for name, value := range map[string]string{"code": req.Code, "state": req.State} {
		if value == "" {
			missing[name] = []string{"is required"}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

func (h *connectorHandler) getMCPOAuthStatus(ctx context.Context, req mcpOAuthStatusRequest) (MCPOAuthStatusResult, error) {
	if req.SessionID == "" {
		return MCPOAuthStatusResult{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam,
			Details: map[string][]string{"sessionId": {"is required"}}}
	}
	found, err := h.mcp.FindOAuthStateBySession(ctx, req.SessionID, entplatform.UserIDFromContext(ctx))
	switch {
	case err != nil:
		return MCPOAuthStatusResult{}, err
	case found == nil:
		return MCPOAuthStatusResult{Status: "expired"}, nil
	case found.AccessToken != "":
		return MCPOAuthStatusResult{Status: "completed"}, nil
	default:
		return MCPOAuthStatusResult{Status: "pending"}, nil
	}
}

func (h *connectorHandler) discoverMCPToolsWithToken(ctx context.Context, req MCPOAuthDiscoverRequest) (List[MCPTool], error) {
	if err := refuseTLSFilesToConnectorReaders(ctx, req.SSLConfig); err != nil {
		return List[MCPTool]{}, err
	}
	tools, err := h.mcp.DiscoverWithSessionToken(ctx, entplatform.MCPOAuthDiscoverRequest{
		SessionID:      req.SessionID,
		ServerURL:      req.ServerURL,
		ConnectionType: transportName(req.ConnectionType),
		SSLConfig:      toMCPSSLConfig(req.SSLConfig),
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return List[MCPTool]{}, mcpDiscoveryError(mcpOAuthError(err))
	}
	return PageOf(toMCPTools(tools), req.Page), nil
}

func mcpOAuthError(err error) error {
	refusal, ok := entplatform.MCPOAuthRefusal(err)
	if !ok {
		return err
	}
	if refusal.Registration {
		return &CallerError{Status: refusal.Status, Message: msgMCPRegistrationRefused, Cause: err}
	}
	status := refusal.Status
	if status == http.StatusBadRequest {
		status = http.StatusUnprocessableEntity
	}
	return &CallerError{Status: status, Message: refusal.Message, Details: refusal.Details, Cause: err}
}

func transportName(connectionType string) string {
	if connectionType == "streamable_http" {
		return "streamable-http"
	}
	return connectionType
}

func toMCPAuthInfo(auth *MCPAuth) *entplatform.MCPAuthInfo {
	if auth == nil {
		return nil
	}
	info := &entplatform.MCPAuthInfo{Type: auth.Type}
	if auth.HTTP != nil {
		info.HTTP = &entplatform.MCPHTTPAuth{
			Scheme: auth.HTTP.Scheme, Token: auth.HTTP.Token,
			Username: auth.HTTP.Username, Password: auth.HTTP.Password,
		}
	}
	if auth.APIKey != nil {
		info.APIKey = &entplatform.MCPAPIKeyAuth{In: auth.APIKey.In, Name: auth.APIKey.Name, Value: auth.APIKey.Value}
	}
	if auth.OAuth != nil {
		info.OAuth = &entplatform.MCPOAuthAuth{AccessToken: auth.OAuth.AccessToken}
	}
	return info
}

func toMCPTools(tools []entplatform.MCPDiscoveredTool) []MCPTool {
	out := make([]MCPTool, len(tools))
	for i, t := range tools {
		out[i] = MCPTool{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  t.InputSchema,
			OutputSchema: t.OutputSchema,
			Annotations:  toMCPToolAnnotations(t.Annotations),
		}
	}
	return out
}

func toMCPToolAnnotations(a *entplatform.MCPToolAnnotations) *MCPToolAnnotations {
	if a == nil {
		return nil
	}
	return &MCPToolAnnotations{ReadOnlyHint: a.ReadOnlyHint, DestructiveHint: a.DestructiveHint, OpenWorldHint: a.OpenWorldHint}
}

func mcpCallbackMessage(sessionID, state string) string {
	message, err := json.Marshal(map[string]string{
		"type": "mcp-oauth-callback", "sessionId": sessionID, "state": state,
	})
	if err != nil {
		return `{"type":"mcp-oauth-error","error":"Callback could not be rendered"}`
	}
	return string(message)
}

func mcpOAuthPage(message, text string) HTMLPage {
	return fmt.Appendf(nil, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Authorization</title></head><body><script>
	if (window.opener) { window.opener.postMessage(%s, location.origin); }
	window.close();
</script><p>%s</p></body></html>`, message, text)
}
