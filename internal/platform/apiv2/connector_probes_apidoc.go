// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (TestConnectorConnectionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "TestConnectorConnectionRequest names the connector or the settings to probe.",
		Fields: map[string]apidoc.Field{
			"ConnectorID": {Doc: "ConnectorID is the UUID of a saved event_mesh connector to probe, with values laid over its settings. Send connectorId, values, or both. The saved settings apply only while values keeps the saved broker_url."},
			"Values":      {Doc: "Values holds the settings to probe: broker_url, broker_vpn, broker_username, and broker_password. All four are required after the saved connector's settings are merged in. The event_mesh subtype's schema, served at /connectorSchemas, sets the keys."},
		},
	}
}

func (ConnectorSecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ConnectorSecretsDiffRequest carries the setting values to compare with the
connector's stored secrets.`,
		Fields: map[string]apidoc.Field{
			"ID":     {Doc: "ID is the UUID of the connector whose stored secrets are compared."},
			"Values": {Doc: "Values holds the settings to compare. Only secret settings are compared. The connector subtype sets the keys."},
		},
	}
}

func (DetectAuthRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DetectAuthRequest is the specification to read: its URL or its text.",
		Fields: map[string]apidoc.Field{
			"SpecificationURL":     {Doc: "SpecificationURL is where to fetch the specification from. Addresses on internal networks are refused. Send this or specificationContent, not both."},
			"SpecificationContent": {Doc: "SpecificationContent is the specification itself, as JSON or YAML text. The whole request body is capped at 16 MiB, which bounds this field. Send this or specificationUrl, not both."},
		},
	}
}

func (DetectAuthResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DetectAuthResult is what an OpenAPI specification says about authentication:
the schemes it declares and the one to configure.`,
		Fields: map[string]apidoc.Field{
			"Type":          {Doc: "Type is always \"detectAuthResult\"."},
			"Schemes":       {Doc: "Schemes lists every security scheme the specification declares, in name order. Empty when it declares none."},
			"SuggestedAuth": {Doc: "SuggestedAuth is the scheme to configure, chosen from schemes. Null when the specification declares none."},
		},
	}
}

func (SecurityScheme) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SecurityScheme is one authentication method an OpenAPI specification declares.",
		Fields: map[string]apidoc.Field{
			"SchemeName": {Doc: "SchemeName is the name the specification gives the scheme."},
			"AuthType":   {Doc: "AuthType is the kind of scheme: http, apikey, oauth, or mutual_tls. An OpenID Connect scheme is reported as oauth, with its discovery document in details.openIdConnectUrl."},
			"Details":    {Doc: "Details holds the scheme's own settings. A field that the scheme's kind does not use is null."},
		},
	}
}

func (SecuritySchemeDetails) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SecuritySchemeDetails holds a security scheme's settings. Which fields carry
a value depends on the scheme's authType.`,
		Fields: map[string]apidoc.Field{
			"Scheme":           {Doc: "Scheme is the HTTP authentication scheme in lowercase, such as bearer or basic. Null for a non-http scheme."},
			"BearerFormat":     {Doc: "BearerFormat names the bearer token's format, such as JWT. Null when the specification does not say."},
			"In":               {Doc: "In is where an API key travels: header, query, or cookie. Null for a non-apikey scheme."},
			"Name":             {Doc: "Name is the header, query, or cookie name an API key uses. Null for a non-apikey scheme."},
			"AuthorizationURL": {Doc: "AuthorizationURL is where the OAuth flow starts. Null when the scheme has no such flow. With several OAuth flows, it comes from authorizationCode, else clientCredentials, else the first by name."},
			"TokenURL":         {Doc: "TokenURL is where an OAuth code is exchanged for a token. Null when the scheme has no such flow. With several OAuth flows, it comes from authorizationCode, else clientCredentials, else the first by name."},
			"RefreshURL":       {Doc: "RefreshURL is where an OAuth token is refreshed. Null when the specification does not say."},
			"OpenIDConnectURL": {Doc: "OpenIDConnectURL is the OpenID Connect discovery document. Null for a scheme that is not OpenID Connect."},
		},
	}
}

func (SuggestedAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SuggestedAuth is the authentication scheme to configure.",
		Fields: map[string]apidoc.Field{
			"AuthType": {Doc: "AuthType is the kind of the suggested scheme, with the same values as a scheme's authType."},
			"Config":   {Doc: "Config holds the settings to start the connector's authentication from. It always has schemeName, and the other keys depend on authType: httpScheme and bearerFormat for http, keyName and keyLocation for apikey, and authorizationUrl, tokenUrl, refreshUrl, grantType, and scopes for oauth."},
		},
	}
}
