// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

type SecuritySchemeInfo struct {
	SchemeName string `json:"schemeName"`
	AuthType   string `json:"authType"`
	Details    any    `json:"details"`
}

type SuggestedAuth struct {
	AuthType string `json:"authType"`
	Config   any    `json:"config"`
}

type DetectAuthResponse struct {
	Schemes       []SecuritySchemeInfo `json:"schemes"`
	SuggestedAuth *SuggestedAuth       `json:"suggestedAuth,omitempty"`
}

const specFetchTimeout = 30 * time.Second

type AuthDetector struct {
	logger     *slog.Logger
	httpClient *http.Client
}

func NewAuthDetector(logger *slog.Logger, httpClient *http.Client) *AuthDetector {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthDetector{logger: logger, httpClient: httpClient}
}

func (d *AuthDetector) DetectAuth(ctx context.Context, specURL string, specContent []byte) (*DetectAuthResponse, error) {
	if specURL == "" && len(specContent) == 0 {
		return nil, &DetectAuthError{Message: "either specification_url or file must be provided"}
	}
	if specURL != "" && len(specContent) > 0 {
		return nil, &DetectAuthError{Message: "provide either specification_url or file, not both"}
	}

	raw := specContent
	if specURL != "" {
		fetched, err := d.FetchSpec(ctx, specURL)
		if err != nil {
			return nil, err
		}
		raw = fetched
	}

	schemes, err := ParseSecuritySchemes(raw)
	if err != nil {
		return nil, &DetectAuthError{Message: err.Error(), Err: err}
	}

	return &DetectAuthResponse{
		Schemes:       schemes,
		SuggestedAuth: DetectPrimaryAuth(schemes),
	}, nil
}

func (d *AuthDetector) FetchSpec(ctx context.Context, specURL string) ([]byte, error) {
	if d.httpClient == nil {
		return nil, &DetectAuthError{Message: "fetching a specification from a URL is not available; provide the file instead"}
	}
	return fetchSpecBytes(ctx, d.httpClient, specURL)
}

type DetectAuthError struct {
	Message string
	Err     error
}

func (e *DetectAuthError) Error() string { return e.Message }

func (e *DetectAuthError) Unwrap() error { return e.Err }

var ErrSpecNotJSONOrYAML = errors.New("failed to parse spec as JSON or YAML")

func ParseSecuritySchemes(specRaw []byte) ([]SecuritySchemeInfo, error) {
	spec, err := parseDetectorSpec(specRaw)
	if err != nil {
		return nil, err
	}
	return extractSecuritySchemes(spec), nil
}

func DetectPrimaryAuth(schemes []SecuritySchemeInfo) *SuggestedAuth {
	if len(schemes) == 0 {
		return nil
	}

	for _, s := range schemes {
		if s.AuthType != "oauth" {
			continue
		}
		flow, scopes, ok := oauthFlowFromDetails(s.Details, "authorizationCode")
		if !ok {
			continue
		}
		authURL, _ := flow["authorization_url"].(string)
		tokURL, _ := flow["token_url"].(string)
		if authURL == "" || tokURL == "" {
			continue
		}
		config := map[string]any{
			"schemeName":       s.SchemeName,
			"authorizationUrl": authURL,
			"tokenUrl":         tokURL,
		}
		if v, ok := flow["refresh_url"].(string); ok && v != "" {
			config["refreshUrl"] = v
		}
		if len(scopes) > 0 {
			config["scopes"] = scopes
		}
		return &SuggestedAuth{AuthType: "oauth", Config: config}
	}

	for _, s := range schemes {
		if s.AuthType != "oauth" {
			continue
		}
		flow, scopes, ok := oauthFlowFromDetails(s.Details, "clientCredentials")
		if !ok {
			continue
		}
		config := map[string]any{"schemeName": s.SchemeName, "grantType": "clientCredentials"}
		if v, ok := flow["token_url"].(string); ok {
			config["tokenUrl"] = v
		}
		if v, ok := flow["refresh_url"].(string); ok && v != "" {
			config["refreshUrl"] = v
		}
		if len(scopes) > 0 {
			config["scopes"] = scopes
		}
		return &SuggestedAuth{AuthType: "oauth", Config: config}
	}

	for _, s := range schemes {
		if s.AuthType != "http" {
			continue
		}
		details, ok := s.Details.(map[string]string)
		if !ok {
			continue
		}
		if strings.EqualFold(details["scheme"], "bearer") {
			config := map[string]any{
				"schemeName": s.SchemeName,
				"httpScheme": "bearer",
			}
			if bf := details["bearer_format"]; bf != "" {
				config["bearerFormat"] = bf
			}
			return &SuggestedAuth{AuthType: "http", Config: config}
		}
	}

	for _, s := range schemes {
		if s.AuthType != "http" {
			continue
		}
		details, ok := s.Details.(map[string]string)
		if !ok {
			continue
		}
		if strings.EqualFold(details["scheme"], "basic") {
			return &SuggestedAuth{
				AuthType: "http",
				Config: map[string]any{
					"schemeName": s.SchemeName,
					"httpScheme": "basic",
				},
			}
		}
	}

	for _, s := range schemes {
		if s.AuthType != "apikey" {
			continue
		}
		details, ok := s.Details.(map[string]string)
		if !ok {
			continue
		}
		if details["in"] == "header" {
			return &SuggestedAuth{
				AuthType: "apikey",
				Config: map[string]any{
					"schemeName":  s.SchemeName,
					"keyName":     details["name"],
					"keyLocation": "header",
				},
			}
		}
	}

	for _, s := range schemes {
		if s.AuthType != "apikey" {
			continue
		}
		details, ok := s.Details.(map[string]string)
		if !ok {
			continue
		}
		return &SuggestedAuth{
			AuthType: "apikey",
			Config: map[string]any{
				"schemeName":  s.SchemeName,
				"keyName":     details["name"],
				"keyLocation": details["in"],
			},
		}
	}

	first := schemes[0]
	return &SuggestedAuth{
		AuthType: first.AuthType,
		Config: map[string]any{
			"schemeName": first.SchemeName,
		},
	}
}

func DetectAuthFromSpec(specRaw []byte) (*AuthConfig, error) {
	schemes, err := ParseSecuritySchemes(specRaw)
	if err != nil {
		return nil, err
	}
	suggested := DetectPrimaryAuth(schemes)
	if suggested == nil {
		return nil, nil
	}
	cfg, ok := suggested.Config.(map[string]any)
	if !ok {
		return nil, nil
	}
	switch suggested.AuthType {
	case "http":
		scheme, _ := cfg["httpScheme"].(string)
		switch strings.ToLower(scheme) {
		case "bearer":
			return &AuthConfig{Type: AuthTypeBearer}, nil
		case "basic":
			return &AuthConfig{Type: AuthTypeBasic}, nil
		}
	case "apikey":
		name, _ := cfg["keyName"].(string)
		in, _ := cfg["keyLocation"].(string)
		in = strings.ToLower(in)
		if in == "" {
			in = "header"
		}
		return &AuthConfig{Type: AuthTypeAPIKey, HeaderName: name, In: in}, nil
	case "oauth":
		ac := &AuthConfig{}
		if authURL, _ := cfg["authorizationUrl"].(string); authURL != "" {
			ac.AuthorizationURL = authURL
		}
		if tokURL, _ := cfg["tokenUrl"].(string); tokURL != "" {
			ac.TokenURL = tokURL
		}
		if refURL, _ := cfg["refreshUrl"].(string); refURL != "" {
			ac.RefreshURL = refURL
		}
		if scopes, ok := cfg["scopes"].([]string); ok && len(scopes) > 0 {
			ac.Scopes = scopes
		}
		if _, ok := cfg["grantType"]; ok {
			ac.Type = AuthTypeOAuth2ClientCredentials
		} else {
			ac.Type = AuthTypeOAuth2AuthorizationCode
		}
		return ac, nil
	}
	return nil, nil
}

type SpecFetchError struct {
	StatusCode int
	Err        error
}

func (e *SpecFetchError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("fetch OpenAPI spec: upstream returned status %d", e.StatusCode)
	}
	if e.Err == nil {
		return "fetch OpenAPI spec failed"
	}
	return "fetch OpenAPI spec: " + logging.UnwrapURLError(e.Err).Error()
}

func (e *SpecFetchError) Unwrap() error { return e.Err }

func fetchSpecBytes(ctx context.Context, client *http.Client, specURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, specFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return nil, &DetectAuthError{Message: "specification URL is not a valid URL"}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, &SpecFetchError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &SpecFetchError{StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecSize))
	if err != nil {
		return nil, &SpecFetchError{Err: err}
	}
	return body, nil
}

type detectorSpec struct {
	Swagger    string `json:"swagger" yaml:"swagger"`
	OpenAPI    string `json:"openapi" yaml:"openapi"`
	Components struct {
		SecuritySchemes map[string]detectorSecurityScheme `json:"securitySchemes" yaml:"securitySchemes"`
	} `json:"components" yaml:"components"`
}

type detectorSecurityScheme struct {
	Type             string                       `json:"type" yaml:"type"`
	Description      string                       `json:"description" yaml:"description"`
	Name             string                       `json:"name" yaml:"name"`
	In               string                       `json:"in" yaml:"in"`
	Scheme           string                       `json:"scheme" yaml:"scheme"`
	BearerFormat     string                       `json:"bearerFormat" yaml:"bearerFormat"`
	Flows            map[string]detectorOAuthFlow `json:"flows" yaml:"flows"`
	OpenIDConnectURL string                       `json:"openIdConnectUrl" yaml:"openIdConnectUrl"`
}

type detectorOAuthFlow struct {
	AuthorizationURL string            `json:"authorizationUrl" yaml:"authorizationUrl"`
	TokenURL         string            `json:"tokenUrl" yaml:"tokenUrl"`
	RefreshURL       string            `json:"refreshUrl" yaml:"refreshUrl"`
	Scopes           map[string]string `json:"scopes" yaml:"scopes"`
}

func parseDetectorSpec(data []byte) (*detectorSpec, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty spec")
	}
	var spec detectorSpec

	if err := json.Unmarshal(data, &spec); err != nil {
		if yamlErr := yaml.Unmarshal(data, &spec); yamlErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrSpecNotJSONOrYAML, yamlErr)
		}
	}

	if spec.Swagger != "" {
		return nil, fmt.Errorf(
			"this appears to be a Swagger %s specification; "+
				"only OpenAPI 3.x is supported — please convert via https://converter.swagger.io",
			spec.Swagger,
		)
	}
	if spec.OpenAPI == "" {
		return nil, fmt.Errorf("not a valid OpenAPI spec: missing 'openapi' field")
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		return nil, fmt.Errorf("only OpenAPI 3.x is supported, found version %q", spec.OpenAPI)
	}
	return &spec, nil
}

func extractSecuritySchemes(spec *detectorSpec) []SecuritySchemeInfo {
	if len(spec.Components.SecuritySchemes) == 0 {
		return []SecuritySchemeInfo{}
	}

	names := make([]string, 0, len(spec.Components.SecuritySchemes))
	for name := range spec.Components.SecuritySchemes {
		names = append(names, name)
	}
	sort.Strings(names)

	schemes := make([]SecuritySchemeInfo, 0, len(names))
	for _, name := range names {
		s := spec.Components.SecuritySchemes[name]
		info := SecuritySchemeInfo{SchemeName: name}

		switch s.Type {
		case "apiKey":
			info.AuthType = "apikey"
			info.Details = map[string]string{
				"name":        s.Name,
				"in":          s.In,
				"description": s.Description,
			}

		case "http":
			info.AuthType = "http"
			details := map[string]string{
				"scheme":      strings.ToLower(s.Scheme),
				"description": s.Description,
			}
			if s.BearerFormat != "" {
				details["bearer_format"] = s.BearerFormat
			}
			info.Details = details

		case "oauth2":
			info.AuthType = "oauth"
			flows := make(map[string]any, len(s.Flows))
			for flowName, flow := range s.Flows {
				scopes := make([]string, 0, len(flow.Scopes))
				for scope := range flow.Scopes {
					scopes = append(scopes, scope)
				}
				sort.Strings(scopes)
				flowMap := map[string]any{"scopes": scopes}
				if flow.AuthorizationURL != "" {
					flowMap["authorization_url"] = flow.AuthorizationURL
				}
				if flow.TokenURL != "" {
					flowMap["token_url"] = flow.TokenURL
				}
				if flow.RefreshURL != "" {
					flowMap["refresh_url"] = flow.RefreshURL
				}
				flows[flowName] = flowMap
			}
			info.Details = map[string]any{
				"description": s.Description,
				"flows":       flows,
			}

		case "openIdConnect":
			info.AuthType = "oauth"
			info.Details = map[string]any{
				"openid_connect_url": s.OpenIDConnectURL,
				"description":        s.Description,
				"flows":              oidcSyntheticAuthCodeFlowForPriority(),
			}

		case "mutualTLS":
			info.AuthType = "mutualTLS"
			info.Details = map[string]string{
				"description": s.Description,
			}

		default:
			continue
		}

		schemes = append(schemes, info)
	}
	return schemes
}

func oidcSyntheticAuthCodeFlowForPriority() map[string]any {
	return map[string]any{
		"authorizationCode": map[string]any{
			"authorization_url": "",
			"token_url":         "",
			"scopes":            []string{},
		},
	}
}

func oauthFlowFromDetails(details any, flowName string) (map[string]any, []string, bool) {
	m, ok := details.(map[string]any)
	if !ok {
		return nil, nil, false
	}
	flows, ok := m["flows"].(map[string]any)
	if !ok {
		return nil, nil, false
	}
	flow, ok := flows[flowName].(map[string]any)
	if !ok {
		return nil, nil, false
	}
	scopes, _ := flow["scopes"].([]string)
	return flow, scopes, true
}
