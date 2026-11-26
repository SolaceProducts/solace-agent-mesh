// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ServiceAccountAgentToolAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ServiceAccountAgentToolAuth is the tool credentials one agent holds for one custom
service account. Every task, scheduled run or evaluation experiment that runs as the
service account and calls the agent uses them.`,
		Fields: map[string]apidoc.Field{
			"ServiceAccountID": {Doc: "ServiceAccountID is the UUID of the service account the credentials are held for."},
			"Sub":              {Doc: "Sub is the service account's principal, as system:<name>."},
			"AgentName":        {Doc: "AgentName is the agent that holds the credentials."},
			"Credentials":      {Doc: "Credentials are the credentials the agent can hold, empty when it has none."},
		},
	}
}

func (ServiceAccountToolCredential) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ServiceAccountToolCredential is one credential an agent holds, or can hold, for a
service account.`,
		Fields: map[string]apidoc.Field{
			"CredentialKey":          {Doc: "CredentialKey names the credential. Unique only within one agent."},
			"DisplayName":            {Doc: "DisplayName is the credential's label, or null when it has none."},
			"AuthType":               {Doc: "AuthType is how the credential authenticates, such as oauth2, or null when the agent does not say."},
			"Interactive":            {Doc: "Interactive is true when a person can authorize the credential from a browser with PUT /rbac/serviceAccounts/{serviceAccountId}/toolAuth/begin."},
			"Status":                 {Doc: "Status is the credential's state as the agent reports it: connected when the service account holds a credential, otherwise not_connected."},
			"ExpiresTime":            {Doc: "ExpiresTime is when the credential's access token expires, or null when it does not or is not connected. An expired access token is renewed on its next use when the provider issued a refresh token."},
			"AuthorizationBegunBy":   {Doc: "AuthorizationBegunBy identifies the user who began an authorization of this credential that has not been seen to complete, in the form of createdBy, or is null when none is under way."},
			"AuthorizationBegunTime": {Doc: "AuthorizationBegunTime is when that authorization began, or null when none is under way."},
			"LastAuthorizedBy":       {Doc: "LastAuthorizedBy identifies the user who began the last authorization of this credential through this API that was seen to complete, in the form of createdBy, or is null when none was."},
			"LastAuthorizedTime":     {Doc: "LastAuthorizedTime is when that authorization began, or null when none completed."},
			"LastDisconnectedBy":     {Doc: "LastDisconnectedBy identifies the user who last disconnected this credential through this API, in the form of createdBy, or is null when nobody did."},
			"LastDisconnectedTime":   {Doc: "LastDisconnectedTime is when the credential was last disconnected through this API, or null when it never was."},
		},
	}
}

func (ServiceAccountToolAuthorization) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ServiceAccountToolAuthorization is an authorization flow begun on one credential for a service account.",
		Fields: map[string]apidoc.Field{
			"AuthURI":       {Doc: "AuthURI is the provider's page to open in a browser. The person who signs in there supplies the account every run as the service account then acts with."},
			"CredentialKey": {Doc: "CredentialKey names the credential being authorized."},
			"ExpiresTime":   {Doc: "ExpiresTime is when the authorization link stops working."},
		},
	}
}

func (ServiceAccountToolCredentialRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ServiceAccountToolCredentialRequest names one credential of one agent held for a service account.",
		Fields: map[string]apidoc.Field{
			"ServiceAccountID": {Doc: "ServiceAccountID is the UUID of the custom service account."},
			"AgentName":        {Doc: "AgentName is the running agent that holds the credential."},
			"CredentialKey":    {Doc: "CredentialKey names the credential, as GET /rbac/serviceAccounts/{serviceAccountId}/toolAuth/agents/{agentName} lists it."},
		},
	}
}

func (serviceAccountAgentToolAuthRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "serviceAccountAgentToolAuthRequest addresses one agent's credentials for a service account.",
		Fields: map[string]apidoc.Field{
			"ServiceAccountID": {Doc: "ServiceAccountID is the UUID of the custom service account."},
			"AgentName":        {Doc: "AgentName is the name of a running agent."},
		},
	}
}
