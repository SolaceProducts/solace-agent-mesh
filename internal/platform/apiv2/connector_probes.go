// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func connectorProbeRoutes(h *connectorHandler) []Route {
	return []Route{
		{Method: http.MethodPut, Path: "/connectors/testConnection", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "testConnectorConnection", Summary: "Validate a connector's connection",
				Text: "Probes the event mesh a connector reaches, with the settings sent or a saved connector's. Only event_mesh connectors can be probed. A failed probe returns 200 with success false and a message.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity}},
			Handler: Handle(h.testConnection, "Unable to test the connector connection")},
		{Method: http.MethodPut, Path: connectorPath + "/secretsDiff", Scope: rbac.ScopeConnectorUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "compareConnectorSecrets", Summary: "Validate values against a connector's stored secrets",
				Text: "Returns, for each secret setting of the connector, whether saving the values sent would keep the stored secret. Secret values are never returned.", Tag: connectorsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare connector secrets")},
		{Method: http.MethodPut, Path: "/connectors/openapi/detectAuth", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "detectConnectorAuth", Summary: "Get the authentication an OpenAPI specification declares",
				Text: "Reads an OpenAPI 3.x specification, fetched from a URL or sent as text, and returns the security schemes it declares and the one to configure. Send exactly one of specificationUrl and specificationContent. If the specification cannot be fetched, the request fails with 502.", Tag: connectorsTag,
				Errors: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}},
			Handler: Handle(h.detectAuth, "Unable to detect OpenAPI authentication")},
	}
}

type TestConnectorConnectionRequest struct {
	ConnectorID string         `json:"connectorId" validate:"omitempty,uuid" example:"3f1a9c7e-2b44-4f8a-9a1e-77c0d9e5b210"`
	Values      map[string]any `json:"values" example:"{\"broker_url\":\"tcp://broker.example.com:55555\",\"broker_vpn\":\"default\",\"broker_username\":\"user\",\"broker_password\":\"placeholder-password\"}"`
}

type ConnectorSecretsDiffRequest struct {
	ID     string         `path:"id" json:"-" example:"3f1a9c7e-2b44-4f8a-9a1e-77c0d9e5b210"`
	Values map[string]any `json:"values" validate:"required" example:"{\"broker_password\":\"<REDACTED>\"}"`
}

type DetectAuthRequest struct {
	SpecificationURL     string `json:"specificationUrl" example:"https://api.example.com/openapi.json"`
	SpecificationContent string `json:"specificationContent" example:"{\"openapi\":\"3.0.0\",\"paths\":{}}"`
}

func (h *connectorHandler) testConnection(ctx context.Context, req TestConnectorConnectionRequest) (TestConnectionResult, error) {
	resp, err := h.svc.TestEventMeshConnection(ctx, entplatform.TestConnectorConnectionRequest{
		ConnectorID: req.ConnectorID, Values: optionalValues(req.Values),
	})
	if err != nil {
		return TestConnectionResult{}, err
	}
	return TestConnectionResult{
		Success:    resp.Success,
		Message:    NullIfZero(resp.Message),
		ErrorClass: NullIfZero(resp.ErrorClass),
		Stage:      NullIfZero(resp.Stage),
	}, nil
}

func (h *connectorHandler) secretsDiff(ctx context.Context, req ConnectorSecretsDiffRequest) (SecretsDiffResult, error) {
	diff, err := h.svc.GetSecretsDiff(ctx, req.ID, req.Values)
	if err != nil {
		return SecretsDiffResult{}, err
	}
	entplatform.LogSecretsDiff(ctx, h.logger, "connector", req.ID, diff)
	result := SecretsDiffResult{Secrets: make(map[string]SecretMatch, len(diff))}
	for field, d := range diff {
		result.Secrets[field] = SecretMatch{Matches: d.Matches}
	}
	return result, nil
}

func (h *connectorHandler) detectAuth(ctx context.Context, req DetectAuthRequest) (DetectAuthResult, error) {
	if h.authDetector == nil {
		return DetectAuthResult{}, errors.New("OpenAPI auth detection is not available")
	}
	field, err := specificationField(req)
	if err != nil {
		return DetectAuthResult{}, err
	}
	resp, err := h.authDetector.DetectAuth(ctx, req.SpecificationURL, []byte(req.SpecificationContent))
	if entplatform.IsUnreadableSpecification(err) {
		return DetectAuthResult{}, &entplatform.ValidationError{Message: "the specification is not valid JSON or YAML", Field: field}
	}
	if err != nil {
		return DetectAuthResult{}, entplatform.OnField(err, field)
	}
	result := DetectAuthResult{Type: "detectAuthResult", Schemes: make([]SecurityScheme, len(resp.Schemes))}
	for i, s := range resp.Schemes {
		result.Schemes[i] = SecurityScheme{
			SchemeName: s.SchemeName,
			AuthType:   wireAuthType(s.AuthType),
			Details:    toSecuritySchemeDetails(s.Details),
		}
	}
	if resp.SuggestedAuth != nil {
		config, _ := resp.SuggestedAuth.Config.(map[string]any)
		result.SuggestedAuth = &SuggestedAuth{AuthType: wireAuthType(resp.SuggestedAuth.AuthType), Config: nonNilMap(config)}
	}
	return result, nil
}

func specificationField(req DetectAuthRequest) (string, error) {
	const urlField, contentField = "specificationUrl", "specificationContent"
	switch {
	case req.SpecificationURL == "" && req.SpecificationContent == "":
		return "", &entplatform.ValidationError{Message: "send specificationUrl or specificationContent", Details: map[string][]string{
			urlField:     {"send this or specificationContent"},
			contentField: {"send this or specificationUrl"},
		}}
	case req.SpecificationURL != "" && req.SpecificationContent != "":
		return "", &entplatform.ValidationError{Message: "send only one of specificationUrl and specificationContent", Details: map[string][]string{
			urlField:     {"send this or specificationContent, not both"},
			contentField: {"send this or specificationUrl, not both"},
		}}
	case req.SpecificationURL != "":
		return urlField, nil
	default:
		return contentField, nil
	}
}

func wireAuthType(authType string) string {
	switch authType {
	case "mutualTLS":
		return "mutual_tls"
	case "openIdConnect":
		return "openid_connect"
	default:
		return authType
	}
}

var oauthFlowOrder = []string{"authorizationCode", "clientCredentials"}

func toSecuritySchemeDetails(raw any) SecuritySchemeDetails {
	fields := map[string]any{}
	switch d := raw.(type) {
	case map[string]string:
		for k, v := range d {
			fields[k] = v
		}
	case map[string]any:
		fields = d
	}
	flow := primaryOAuthFlow(fields["flows"])
	return SecuritySchemeDetails{
		Scheme:           stringField(fields, "scheme"),
		BearerFormat:     stringField(fields, "bearer_format"),
		In:               stringField(fields, "in"),
		Name:             stringField(fields, "name"),
		AuthorizationURL: stringField(flow, "authorization_url"),
		TokenURL:         stringField(flow, "token_url"),
		RefreshURL:       stringField(flow, "refresh_url"),
		OpenIDConnectURL: stringField(fields, "openid_connect_url"),
	}
}

func primaryOAuthFlow(raw any) map[string]any {
	flows, _ := raw.(map[string]any)
	names := make([]string, 0, len(flows))
	for name := range flows {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range slices.Concat(oauthFlowOrder, names) {
		if flow, ok := flows[name].(map[string]any); ok {
			return flow
		}
	}
	return nil
}

func stringField(fields map[string]any, key string) *string {
	value, _ := fields[key].(string)
	return NullIfZero(strings.TrimSpace(value))
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

type DetectAuthResult struct {
	Type          string           `json:"type" example:"detectAuthResult"`
	Schemes       []SecurityScheme `json:"schemes"`
	SuggestedAuth *SuggestedAuth   `json:"suggestedAuth"`
}

type SecurityScheme struct {
	SchemeName string                `json:"schemeName" example:"bearerAuth"`
	AuthType   string                `json:"authType" example:"http"`
	Details    SecuritySchemeDetails `json:"details"`
}

type SecuritySchemeDetails struct {
	Scheme           *string `json:"scheme" example:"bearer"`
	BearerFormat     *string `json:"bearerFormat" example:"JWT"`
	In               *string `json:"in" example:"header"`
	Name             *string `json:"name" example:"X-API-Key"`
	AuthorizationURL *string `json:"authorizationUrl" example:"https://auth.example.com/authorize"`
	TokenURL         *string `json:"tokenUrl" example:"https://auth.example.com/token"`
	RefreshURL       *string `json:"refreshUrl" example:"https://auth.example.com/refresh"`
	OpenIDConnectURL *string `json:"openIdConnectUrl" example:"https://auth.example.com/.well-known/openid-configuration"`
}

type SuggestedAuth struct {
	AuthType string         `json:"authType" example:"http"`
	Config   map[string]any `json:"config" example:"{\"schemeName\":\"bearerAuth\",\"httpScheme\":\"bearer\"}"`
}
