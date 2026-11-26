// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	httpBasicUsernameMaxLength = 128
	httpBasicPasswordMaxLength = 512
	httpBearerTokenMaxLength   = 4096

	oauth2AuthorizationURLMinLength = 11
	oauth2AuthorizationURLMaxLength = 2048
	oauth2TokenURLMinLength         = 11
	oauth2TokenURLMaxLength         = 2048
	oauth2ClientIDMinLength         = 1
	oauth2ClientIDMaxLength         = 512
	oauth2ClientSecretMinLength     = 4
	oauth2ClientSecretMaxLength     = 1024
)

var oauth2TokenEndpointAuthMethods = []string{
	"client_secret_basic", "client_secret_post", "none",
}

const defaultTokenEndpointAuthMethod = "client_secret_basic"

var httpMethods = map[string]struct{}{
	"get": {}, "post": {}, "put": {}, "patch": {},
	"delete": {}, "options": {}, "head": {}, "trace": {},
}

type rawSpecDict struct {
	Swagger string                    `json:"swagger" yaml:"swagger"`
	OpenAPI any                       `json:"openapi" yaml:"openapi"`
	Paths   map[string]map[string]any `json:"paths" yaml:"paths"`
}

func parseRawSpec(data []byte) (*rawSpecDict, string, error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("empty specification")
	}
	var spec rawSpecDict
	if err := json.Unmarshal(data, &spec); err == nil {
		return &spec, "json", nil
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, "", fmt.Errorf("failed to parse specification: %w", err)
	}
	return &spec, "yaml", nil
}

func validateSpecSources(cfg *ToolsetConfig) error {
	sources := 0
	for _, s := range []string{cfg.SpecFile, cfg.SpecURL, cfg.SpecInline} {
		if s != "" {
			sources++
		}
	}
	if sources > 1 {
		return fmt.Errorf(
			"OpenAPI tool configuration error: Cannot specify more than one of " +
				"'specification_file', 'specification', or 'specification_url'. Please use only one.",
		)
	}
	if sources == 0 {
		return fmt.Errorf(
			"OpenAPI tool configuration error: Must specify one of 'specification_file', " +
				"'specification', or 'specification_url'.",
		)
	}
	return nil
}

func validateOpenAPIVersion(rawSpec []byte) error {
	spec, _, err := parseRawSpec(rawSpec)
	if err != nil {
		return fmt.Errorf("failed to parse specification for version validation: %w", err)
	}

	if spec.Swagger != "" {
		return fmt.Errorf(
			"this appears to be a Swagger 2.0 specification (found 'swagger: %s'). "+
				"SAM only supports OpenAPI 3.x. "+
				"Please convert your specification using https://converter.swagger.io",
			spec.Swagger,
		)
	}

	if spec.OpenAPI == nil {
		return fmt.Errorf(
			"the provided content does not appear to be a valid OpenAPI specification " +
				"(missing 'openapi' version field). Please ensure the file is an OpenAPI 3.x specification.",
		)
	}
	major, versionStr, ok := openAPIMajorVersion(spec.OpenAPI)
	if !ok {
		return fmt.Errorf(
			"the provided content does not appear to be a valid OpenAPI specification " +
				"(missing or malformed 'openapi' version field). Please ensure the file is an OpenAPI 3.x specification.",
		)
	}
	if major != 3 {
		return fmt.Errorf(
			"OpenAPI version %s is not supported. "+
				"SAM only supports OpenAPI 3.x. "+
				"Please convert your specification to OpenAPI 3.x.",
			versionStr,
		)
	}
	return nil
}

func openAPIMajorVersion(v any) (int, string, bool) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return 0, "", false
		}
		end := 0
		for end < len(x) && x[end] >= '0' && x[end] <= '9' {
			end++
		}
		if end == 0 {
			return 0, x, false
		}
		n := 0
		for i := range end {
			n = n*10 + int(x[i]-'0')
		}
		return n, x, true
	case float64:
		return int(x), strconv.FormatFloat(x, 'f', -1, 64), true
	case float32:
		return int(x), strconv.FormatFloat(float64(x), 'f', -1, 32), true
	case int:
		return x, strconv.Itoa(x), true
	case int64:
		return int(x), strconv.FormatInt(x, 10), true
	}
	return 0, fmt.Sprintf("%v", v), false
}

func validateFilterLists(allow, deny []string) error {
	for i, s := range allow {
		if s == "" {
			return fmt.Errorf("allow_list[%d] must be a non-empty string", i)
		}
	}
	for i, s := range deny {
		if s == "" {
			return fmt.Errorf("deny_list[%d] must be a non-empty string", i)
		}
	}
	return nil
}

func ValidateOperationIDUniqueness(rawSpec []byte, allow, deny []string) error {
	return validateOperationIDUniqueness(rawSpec, allow, deny)
}

func validateOperationIDUniqueness(rawSpec []byte, allow, deny []string) error {
	spec, _, err := parseRawSpec(rawSpec)
	if err != nil {
		return fmt.Errorf("failed to parse OpenAPI specification for validation: %w", err)
	}

	type opLocation struct {
		id     string
		locs   []string
		exists bool
	}

	order := []string{}
	byID := map[string]*opLocation{}

	for path, pathItem := range spec.Paths {
		for method, op := range pathItem {
			if _, ok := httpMethods[strings.ToLower(method)]; !ok {
				continue
			}
			opMap, ok := op.(map[string]any)
			if !ok {
				var asIface map[string]any
				if b, mErr := json.Marshal(op); mErr == nil {
					if uErr := json.Unmarshal(b, &asIface); uErr == nil {
						opMap = asIface
					}
				}
				if opMap == nil {
					continue
				}
			}
			opID, _ := opMap["operationId"].(string)
			if opID == "" {
				continue
			}
			loc := strings.ToUpper(method) + " " + path
			entry, ok := byID[opID]
			if !ok {
				entry = &opLocation{id: opID, locs: []string{loc}, exists: true}
				byID[opID] = entry
				order = append(order, opID)
			} else {
				entry.locs = append(entry.locs, loc)
			}
		}
	}

	if len(allow) > 0 {
		keep := map[string]struct{}{}
		for _, a := range allow {
			keep[a] = struct{}{}
		}
		filteredOrder := order[:0:0]
		for _, id := range order {
			if _, ok := keep[id]; ok {
				filteredOrder = append(filteredOrder, id)
			} else {
				delete(byID, id)
			}
		}
		order = filteredOrder
	} else if len(deny) > 0 {
		skip := map[string]struct{}{}
		for _, d := range deny {
			skip[d] = struct{}{}
		}
		filteredOrder := order[:0:0]
		for _, id := range order {
			if _, ok := skip[id]; ok {
				delete(byID, id)
				continue
			}
			filteredOrder = append(filteredOrder, id)
		}
		order = filteredOrder
	}

	var sourceDups []string
	for _, id := range order {
		entry := byID[id]
		if len(entry.locs) > 1 {
			sourceDups = append(sourceDups, fmt.Sprintf(
				"'%s' appears in %s and %s",
				id, entry.locs[0], strings.Join(entry.locs[1:], " and "),
			))
		}
	}
	if len(sourceDups) > 0 {
		var b strings.Builder
		b.WriteString("Duplicate operation IDs found in OpenAPI specification:\n")
		for _, d := range sourceDups {
			b.WriteString("  - " + d + "\n")
		}
		b.WriteString("\nEach operation must have a unique operationId per OpenAPI specification.")
		return fmt.Errorf("%s", b.String())
	}

	snakeMap := map[string]string{}
	var collisions []string
	for _, id := range order {
		snake := toSnakeCase(id)
		if prev, ok := snakeMap[snake]; ok && prev != id {
			collisions = append(collisions, fmt.Sprintf(
				"'%s' and '%s' both convert to '%s'", prev, id, snake,
			))
		} else {
			snakeMap[snake] = id
		}
	}
	if len(collisions) > 0 {
		var b strings.Builder
		b.WriteString("Operation ID collision after snake_case conversion:\n")
		for _, c := range collisions {
			b.WriteString("  - " + c + "\n")
		}
		b.WriteString("\nPlease ensure operation IDs don't collide when converted to snake_case.")
		return fmt.Errorf("%s", b.String())
	}

	return nil
}

func validateBasicAuthConfig(auth *AuthConfig) error {
	if auth.Username == "" {
		return fmt.Errorf("auth.username is required for basic auth")
	}
	if auth.Password == "" {
		return fmt.Errorf("auth.password is required for basic auth")
	}
	if len(auth.Username) > httpBasicUsernameMaxLength {
		return fmt.Errorf("auth.username too long (max %d characters)", httpBasicUsernameMaxLength)
	}
	if len(auth.Password) > httpBasicPasswordMaxLength {
		return fmt.Errorf("auth.password too long (max %d characters)", httpBasicPasswordMaxLength)
	}
	return nil
}

func validateBearerAuthConfig(auth *AuthConfig) error {
	token := strings.TrimSpace(auth.Token)
	if token == "" {
		return fmt.Errorf("auth.token is required for bearer auth")
	}
	if len(token) > httpBearerTokenMaxLength {
		return fmt.Errorf("auth.token too long (max %d characters)", httpBearerTokenMaxLength)
	}
	return nil
}

func validateAPIKeyAuthConfig(auth *AuthConfig) error {
	if auth.HeaderName == "" {
		return fmt.Errorf("auth.name is required for apikey auth")
	}
	if auth.APIKey == "" {
		return fmt.Errorf("auth.value is required for apikey auth")
	}
	switch auth.In {
	case "", "header", "query":
	case "cookie":
		return fmt.Errorf("auth.in=cookie is not supported via YAML config; cookie-placed apikeys are only reachable through spec-driven auto-wiring")
	default:
		return fmt.Errorf("auth.in=%q is invalid; must be one of \"header\" or \"query\"", auth.In)
	}
	return nil
}

type oauth2AuthConfig struct {
	AuthorizationURL        string
	TokenURL                string
	ClientID                string
	ClientSecret            string
	TokenEndpointAuthMethod string
	Scopes                  []string
}

func validateOAuth2Config(cfg oauth2AuthConfig) error {
	if cfg.AuthorizationURL == "" {
		return fmt.Errorf(
			"auth.authorization_url is required for OAuth2 authentication. " +
				"You can find this endpoint in your OAuth provider's documentation.",
		)
	}
	if cfg.TokenURL == "" {
		return fmt.Errorf(
			"auth.token_url is required for OAuth2 authentication. " +
				"You can find this endpoint in your OAuth provider's documentation.",
		)
	}
	if len(cfg.AuthorizationURL) < oauth2AuthorizationURLMinLength ||
		len(cfg.AuthorizationURL) > oauth2AuthorizationURLMaxLength {
		return fmt.Errorf(
			"auth.authorization_url length must be between %d and %d characters",
			oauth2AuthorizationURLMinLength, oauth2AuthorizationURLMaxLength,
		)
	}
	if len(cfg.TokenURL) < oauth2TokenURLMinLength ||
		len(cfg.TokenURL) > oauth2TokenURLMaxLength {
		return fmt.Errorf(
			"auth.token_url length must be between %d and %d characters",
			oauth2TokenURLMinLength, oauth2TokenURLMaxLength,
		)
	}
	if cfg.ClientID != "" {
		if len(cfg.ClientID) < oauth2ClientIDMinLength || len(cfg.ClientID) > oauth2ClientIDMaxLength {
			return fmt.Errorf(
				"auth.client_id length must be between %d and %d characters",
				oauth2ClientIDMinLength, oauth2ClientIDMaxLength,
			)
		}
	}
	if cfg.ClientSecret != "" {
		if len(cfg.ClientSecret) < oauth2ClientSecretMinLength ||
			len(cfg.ClientSecret) > oauth2ClientSecretMaxLength {
			return fmt.Errorf(
				"auth.client_secret length must be between %d and %d characters",
				oauth2ClientSecretMinLength, oauth2ClientSecretMaxLength,
			)
		}
	}
	method := cfg.TokenEndpointAuthMethod
	if method == "" {
		method = defaultTokenEndpointAuthMethod
	}
	if !slices.Contains(oauth2TokenEndpointAuthMethods, method) {
		return fmt.Errorf(
			"auth.token_endpoint_auth_method must be one of: %s",
			strings.Join(oauth2TokenEndpointAuthMethods, ", "),
		)
	}
	return nil
}

func validateServiceAccountAuthConfig(auth *AuthConfig) error {
	if strings.TrimSpace(auth.ServiceAccountJSON) == "" {
		return fmt.Errorf(
			"auth.service_account_json is required for serviceaccount authentication",
		)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(auth.ServiceAccountJSON), &probe); err != nil {
		return fmt.Errorf(
			"auth.service_account_json must be valid JSON: %w", err,
		)
	}
	if len(auth.Scopes) == 0 {
		return fmt.Errorf(
			"auth.scopes must contain at least one scope for serviceaccount authentication",
		)
	}
	return nil
}

func validateOAuth2AuthConfigRuntime(auth *AuthConfig) error {
	isClientCredentials := auth.Type == AuthTypeOAuth2ClientCredentials

	authURL := auth.AuthorizationURL
	if isClientCredentials && authURL == "" {
		authURL = auth.TokenURL
	}
	method := auth.TokenEndpointAuthMethod
	if method == "" {
		method = defaultTokenEndpointAuthMethod
	}
	if err := validateOAuth2Config(oauth2AuthConfig{
		AuthorizationURL:        authURL,
		TokenURL:                auth.TokenURL,
		ClientID:                auth.ClientID,
		ClientSecret:            auth.ClientSecret,
		TokenEndpointAuthMethod: method,
		Scopes:                  auth.Scopes,
	}); err != nil {
		return err
	}

	if isClientCredentials {
		if auth.ClientID == "" {
			return fmt.Errorf(
				"auth.client_id is required for oauth2_client_credentials",
			)
		}
		if auth.ClientSecret == "" && method != "none" {
			return fmt.Errorf(
				"auth.client_secret is required for oauth2_client_credentials " +
					"unless token_endpoint_auth_method=\"none\" (public client)",
			)
		}
		return nil
	}

	if auth.AuthorizationURL == "" {
		return fmt.Errorf(
			"auth.authorization_url is required for oauth2 authorization_code",
		)
	}
	if auth.ClientID == "" {
		return fmt.Errorf(
			"auth.client_id is required for oauth2 authorization_code",
		)
	}
	if auth.ClientSecret == "" && method != "none" {
		return fmt.Errorf(
			"auth.client_secret is required for oauth2 authorization_code " +
				"unless token_endpoint_auth_method=\"none\" (public client)",
		)
	}
	return nil
}

func validateAuthConfig(auth *AuthConfig) error {
	if auth == nil || auth.Type == AuthTypeUnset || auth.Type == AuthTypeNone {
		return nil
	}
	switch auth.Type {
	case AuthTypeBasic:
		return validateBasicAuthConfig(auth)
	case AuthTypeBearer:
		return validateBearerAuthConfig(auth)
	case AuthTypeAPIKey:
		return validateAPIKeyAuthConfig(auth)
	case AuthTypeOAuth2AuthorizationCode, AuthTypeOAuth2ClientCredentials:
		return validateOAuth2AuthConfigRuntime(auth)
	case AuthTypeServiceAccount:
		return validateServiceAccountAuthConfig(auth)
	}
	return fmt.Errorf(
		"unsupported auth type %q (expected one of: bearer, api_key, basic, "+
			"oauth2_authorization_code, oauth2_client_credentials, serviceaccount, none)",
		auth.Type,
	)
}

func runConfigValidators(cfg *ToolsetConfig, rawSpec []byte) error {
	if err := validateSpecSources(cfg); err != nil {
		return err
	}
	if err := validateOpenAPIVersion(rawSpec); err != nil {
		return err
	}
	if err := validateFilterLists(cfg.AllowList, cfg.DenyList); err != nil {
		return err
	}
	if err := validateOperationIDUniqueness(rawSpec, cfg.AllowList, cfg.DenyList); err != nil {
		return err
	}
	if err := validateAuthConfig(cfg.Auth); err != nil {
		return err
	}
	return nil
}
