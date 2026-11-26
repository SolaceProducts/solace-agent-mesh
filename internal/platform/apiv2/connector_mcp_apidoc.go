// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (MCPTool) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `MCPTool is one tool an MCP server publishes. Agent Mesh reads it from the
server rather than storing it, so it has no id.`,
		Fields: map[string]apidoc.Field{
			"Name":         {Doc: "Name is the tool's name as the MCP server publishes it, which is what an agent calls."},
			"Description":  {Doc: "Description is what the server says the tool does. Empty when it says nothing."},
			"InputSchema":  {Doc: "InputSchema is the tool's arguments as a JSON Schema document, as the MCP server publishes it."},
			"OutputSchema": {Doc: "OutputSchema is the tool's result as a JSON Schema document, as the MCP server publishes it. Null when the server declares none, which means the result's shape is unknown, not that the tool returns nothing."},
			"Annotations":  {Doc: "Annotations is what the server says about the tool's side effects. Null when it says nothing. Save it with the tool in the connector's manifest. Agent Mesh shows it but does not act on it for a connector's tools."},
		},
	}
}

func (MCPToolAnnotations) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPToolAnnotations holds the side-effect hints an MCP server publishes for one tool. They are the server's claims. An agent acts on them only when its own inline MCP tool entry sets trust_annotations.",
		Fields: map[string]apidoc.Field{
			"ReadOnlyHint":    {Doc: "ReadOnlyHint is true when the server says the tool changes nothing. Null when it says nothing."},
			"DestructiveHint": {Doc: "DestructiveHint is true when the server says the tool may delete or overwrite data. Null when it says nothing."},
			"OpenWorldHint":   {Doc: "OpenWorldHint is true when the server says the tool reaches systems outside the server. Null when it says nothing."},
		},
	}
}

func (MCPDiscoverRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPDiscoverRequest names the MCP server whose tools to list.",
		Fields: map[string]apidoc.Field{
			"Page":           {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"ServerURL":      {Doc: "ServerURL is the MCP server's address. Must be http or https. Addresses on internal networks are refused."},
			"ConnectionType": {Doc: "ConnectionType is the transport to connect over: streamable_http or sse. Defaults to streamable_http."},
			"Auth":           {Doc: "Auth is how to authenticate to the server. Leave it out for a server that needs no credential."},
			"CustomHeaders":  {Doc: "CustomHeaders are extra headers to send with every request to the server. A header the credential already sets is not overridden. A name or value containing a line break fails with 422."},
			"SSLConfig":      {Doc: "SSLConfig carries CA bundle, mTLS client certificate and SNI override for MCP servers reached over https. Leave it out when the server uses a certificate the system trust store already covers and does not require mTLS. Naming a file needs permission to create or update connectors, and fails with 403 without it."},
		},
	}
}

func (MCPSSLConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `MCPSSLConfig is the TLS setup used to reach an MCP server over https.
caBundle, clientCert and clientKey are paths to PEM files on the Agent Mesh
host, not PEM content. There is no way to turn off certificate verification
here: only an administrator can, for the whole deployment.`,
		Fields: map[string]apidoc.Field{
			"CABundle":   {Doc: "CABundle is a path to a PEM-encoded CA bundle that replaces the system trust store when verifying the server certificate."},
			"ClientCert": {Doc: "ClientCert is a path to a PEM-encoded client certificate presented to the server during the TLS handshake. Pair with clientKey."},
			"ClientKey":  {Doc: "ClientKey is a path to the PEM-encoded private key for clientCert. Pair with clientCert."},
			"ServerName": {Doc: "ServerName overrides SNI and the hostname used for certificate verification. Leave it out to use the hostname from serverUrl."},
		},
	}
}

func (MCPAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `MCPAuth is the credential discovery presents to an MCP server. Fill in the
one block that matches type and leave the others out.`,
		Fields: map[string]apidoc.Field{
			"Type":   {Doc: "Type is the kind of credential: none, http, apikey, or oauth. Leave it out, or send none, for a server that needs no credential."},
			"HTTP":   {Doc: "HTTP is the credential for the http type. Ignored for any other type."},
			"APIKey": {Doc: "APIKey is the credential for the apikey type. Ignored for any other type."},
			"OAuth":  {Doc: "OAuth is the credential for the oauth type. Ignored for any other type."},
		},
	}
}

func (MCPHTTPAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPHTTPAuth is an HTTP authentication credential.",
		Fields: map[string]apidoc.Field{
			"Scheme":   {Doc: "Scheme is the HTTP authentication scheme: bearer or basic."},
			"Token":    {Doc: "Token is the bearer token, used by the bearer scheme."},
			"Username": {Doc: "Username is the username, used by the basic scheme."},
			"Password": {Doc: "Password is the password, used by the basic scheme."},
		},
	}
}

func (MCPAPIKeyAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPAPIKeyAuth is an API key credential.",
		Fields: map[string]apidoc.Field{
			"In":    {Doc: "In is where the key travels: header or query. Defaults to header."},
			"Name":  {Doc: "Name is the header or query parameter the key travels under."},
			"Value": {Doc: "Value is the key itself."},
		},
	}
}

func (MCPOAuthAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `MCPOAuthAuth is an access token already obtained for the MCP server. Use
discoverWithToken instead when Agent Mesh ran the authorization.`,
		Fields: map[string]apidoc.Field{
			"AccessToken": {Doc: "AccessToken is the token, sent to the server as bearer authentication."},
		},
	}
}

func (MCPOAuthInitiateRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPOAuthInitiateRequest is the MCP server to authorize against, and how.",
		Fields: map[string]apidoc.Field{
			"Origin":                  {Doc: "Origin is the scheme and host this call arrived on, which the callback URL sent to the provider is built from. It is read from the request, not from the body."},
			"ServerURL":               {Doc: "ServerURL is the MCP server the authorization is for."},
			"ConnectionType":          {Doc: "ConnectionType is the transport that discoverWithToken connects over after the authorization completes: streamable_http or sse. Defaults to streamable_http."},
			"Mode":                    {Doc: "Mode is how the OAuth settings are obtained: discovery reads them from the server and registers a client with the provider, and manual takes them from this request. Defaults to manual."},
			"AuthorizationURL":        {Doc: "AuthorizationURL is the provider's authorization address. Required in manual mode and ignored in discovery mode."},
			"TokenURL":                {Doc: "TokenURL is where the authorization code is exchanged for a token. Required in manual mode and ignored in discovery mode."},
			"ClientID":                {Doc: "ClientID is the OAuth client to authorize as. Required in manual mode and ignored in discovery mode, which registers a client instead."},
			"ClientSecret":            {Doc: "ClientSecret is the client's secret. Leave it out for a public client. Never returned."},
			"Scopes":                  {Doc: "Scopes are the scopes to request, separated by spaces. Leave it out to request none."},
			"TokenEndpointAuthMethod": {Doc: "TokenEndpointAuthMethod is how the client authenticates at the token endpoint, such as client_secret_basic, client_secret_post, or none. In manual mode, defaults to client_secret_basic, or to none when no client secret is sent. In discovery mode, the provider's own settings are used."},
		},
	}
}

func (MCPOAuthInitiateResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `MCPOAuthInitiateResult is where to send the user's browser, and how to follow
the authorization it begins.`,
		Fields: map[string]apidoc.Field{
			"AuthorizationURL": {Doc: "AuthorizationURL is the address to open in the user's browser. It already carries the client, the PKCE challenge, the state, any requested scopes, and the callback address on this server."},
			"State":            {Doc: "State is the value the authorization carries and the callback checks. The page the callback renders hands it back, so the caller can tell its own authorization from another tab's."},
			"SessionID":        {Doc: "SessionID identifies the authorization to the status poll and to discoverWithToken. Not a credential: the access token never leaves the server."},
		},
	}
}

func (mcpOAuthCallbackRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `mcpOAuthCallbackRequest is what the identity provider sends the browser back
with.`,
		Fields: map[string]apidoc.Field{
			"Code":  {Doc: "Code is the authorization code to exchange for an access token."},
			"State": {Doc: "State is the value initiate generated, which names the authorization the code belongs to."},
		},
	}
}

func (mcpOAuthStatusRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "mcpOAuthStatusRequest names the authorization to report on.",
		Fields: map[string]apidoc.Field{
			"SessionID": {Doc: "SessionID is the authorization, as initiate returned it."},
		},
	}
}

func (MCPOAuthStatusResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPOAuthStatusResult is the progress of an authorization.",
		Fields: map[string]apidoc.Field{
			"Status": {Doc: "Status is the authorization's progress: pending while the user is still authorizing, completed after the access token is obtained, or expired. Expired means the authorization is unknown to this server: never started, already spent by discoverWithToken, timed out, or another user's."},
		},
	}
}

func (MCPOAuthDiscoverRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MCPOAuthDiscoverRequest spends a completed authorization.",
		Fields: map[string]apidoc.Field{
			"Page":           {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters. The authorization is spent by the first request, so a later page cannot be fetched: ask for the whole list in one page (pageSize=100) and read meta.pagination.count to tell whether anything was cut off."},
			"SessionID":      {Doc: "SessionID is the authorization to spend, as initiate returned it."},
			"ServerURL":      {Doc: "ServerURL is the MCP server to list. Leave it out to use the one the authorization was granted for. A different server fails with 422, because the token belongs to that server."},
			"ConnectionType": {Doc: "ConnectionType is the transport to connect over: streamable_http or sse. Leave it out to use the one initiate was given."},
			"SSLConfig":      {Doc: "SSLConfig carries CA bundle, mTLS client certificate and SNI override for an MCP server reached over https. Leave it out when the server uses a certificate the system trust store already covers and does not require mTLS. Naming a file needs permission to create or update connectors, and fails with 403 without it."},
		},
	}
}
