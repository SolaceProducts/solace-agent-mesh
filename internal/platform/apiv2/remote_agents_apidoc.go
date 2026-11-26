// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (RemoteAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RemoteAgent is an agent that Agent Mesh reaches over A2A at a URL the caller
gives. Stored authentication secrets come back as "<REDACTED>". Header values
come back as stored.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the remote agent's UUID."},
			"Type": {Doc: "Type is always \"remoteAgent\"."},
			"Name": {Doc: "Name is the remote agent's name, unique ignoring case and spaces."},
			"InstanceName": {Doc: `InstanceName is the name the remote agent goes by on the agent mesh after it is deployed: "agent_"
followed by id with its dashes replaced by underscores.`},
			"Description": {Doc: "Description says what the remote agent does. Null when none was given."},
			"AgentURL":    {Doc: "AgentURL is the remote agent's A2A endpoint URL, starting with http:// or https://."},
			"AgentCardLocation": {Doc: `AgentCardLocation says where the agent card comes from: well_known (the agent's
/.well-known/agent-card.json, falling back to /.well-known/agent.json), custom (the URL in
agentCardUrl), or none (no card is read, and skills, inputModes, and outputModes stand in for it).`},
			"AgentCardURL": {Doc: `AgentCardURL is the URL the agent card is read from when agentCardLocation is custom. Null
when none was set.`},
			"AgentCardHeaders": {Doc: `AgentCardHeaders are the extra HTTP headers sent when the agent card is fetched, keyed by header
name. Null when there are none. Values are returned as stored, not redacted.`},
			"AgentCardAuthType": {Doc: `AgentCardAuthType is how Agent Mesh authenticates when it fetches the agent card: none, bearer,
apikey, basic, or oauth2. Null when none was set.`},
			"AgentCardAuthConfig": {Doc: `AgentCardAuthConfig is the card authentication's settings with its secrets redacted. Null
when no card authentication is configured.`},
			"AgentAuthType": {Doc: `AgentAuthType is how Agent Mesh authenticates when it sends tasks to the remote agent: none,
bearer, apikey, basic, or oauth2.`},
			"AgentAuthConfig": {Doc: `AgentAuthConfig is the task authentication's settings with its secrets redacted. Null when
agentAuthType is none.`},
			"TaskHeaders": {Doc: `TaskHeaders are the extra HTTP headers sent with every task request to the remote agent, keyed by
header name. Null when there are none. Values are returned as stored, not redacted.`},
			"RequestTimeoutSeconds": {Doc: `RequestTimeoutSeconds is how long Agent Mesh waits on each HTTP request to the remote agent
before giving up.`},
			"Skills": {Doc: `Skills are the skills saved with the agent. Empty when none were saved. Agent Mesh uses them as
the agent's card only when agentCardLocation is none.`},
			"InputModes": {Doc: `InputModes are the input content modes saved with the agent, such as text. Empty when none were
saved. Agent Mesh uses them only when agentCardLocation is none.`},
			"OutputModes": {Doc: `OutputModes are the output content modes saved with the agent, such as text. Empty when none
were saved. Agent Mesh uses them only when agentCardLocation is none.`},
			"ProtocolVersion": {Doc: `ProtocolVersion is the A2A version Agent Mesh speaks to the remote agent: auto (the highest
version both support), v1_0 (A2A 1.0), or v0_3 (A2A 0.3).`},
			"TrustedCardJwksUrls": {Doc: `TrustedCardJwksUrls are the URLs of the JWK Sets whose keys are trusted to sign the agent card.
Empty when the card's signature is not checked.`},
			"RequireSignedCard": {Doc: "RequireSignedCard is true when an agent card that carries no signature is refused."},
			"RemoteProtocol": {Doc: `RemoteProtocol is how the deployed agent reaches the remote agent, as it last reported on the
agent mesh. Null until the deployed agent has published its card, or when that card names a
value outside the known set. When agentCardLocation is none, no card is fetched: the status
reflects the configured URL, at the pinned version or A2A 0.3, and cardSignature is unchecked.`},
			"DeploymentStatus": {Doc: `DeploymentStatus is the state the latest deployment left: deployed (a successful deploy or
update), deploy_failed (a failed deploy or update), or not_deployed (an undeploy, or no
deployment yet).`},
			"SyncStatus": {Doc: `SyncStatus compares the agent's current settings with its latest successful deploy: in_sync when
they match, out_of_sync when they have changed since. Credentials, headers, and card
authentication are not compared. Null when the agent is not deployed or never deployed
successfully.`},
			"RuntimeStatus": {Doc: `RuntimeStatus is the deployed remote agent's health as last seen on the agent mesh: running,
degraded when it reports problems, disconnected when it is deployed but not seen, and stopped
otherwise.`},
			"Deployed": {Doc: `Deployed is what the latest successful deploy or update ran with. Null when the agent is not
deployed or never deployed successfully.`},
			"CardData": {Doc: `CardData is the agent card read from the remote agent with the stored credentials. Set only on
a GET with expand=card when the fetch succeeds. Null otherwise, including when agentCardLocation
is none.`},
			"CardFetchError": {Doc: `CardFetchError replaces cardData when a GET with expand=card cannot fetch the card. The request
still succeeds. The text is always "Unable to fetch the agent card with the stored credentials".
Null otherwise.`},
			"CreatedBy":   {Doc: "CreatedBy identifies the user who created the remote agent: their email, or their user ID when no email is known."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the remote agent, in the same form as createdBy."},
			"CreatedTime": {Doc: "CreatedTime is when the remote agent was created."},
			"UpdatedTime": {Doc: "UpdatedTime is when the remote agent was last changed."},
		},
	}
}

func (DeployedRemoteAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DeployedRemoteAgent is what the running deployment was built from. It
carries no authentication settings.`,
		Fields: map[string]apidoc.Field{
			"AgentURL":    {Doc: "AgentURL is the remote agent's URL as it was when deployed."},
			"Name":        {Doc: "Name is the remote agent's name as it was when deployed."},
			"Description": {Doc: "Description is the remote agent's description as it was when deployed. Null when it had none."},
			"AgentCardLocation": {Doc: `AgentCardLocation is where the agent card was read from at deploy time: well_known, custom, or
none.`},
			"AgentCardURL": {Doc: "AgentCardURL is the custom agent card URL at deploy time. Null when none was set."},
			"AgentCardAuthType": {Doc: `AgentCardAuthType is the card authentication type at deploy time: none, bearer, apikey, basic,
or oauth2. Null when none was set.`},
			"AgentAuthType": {Doc: `AgentAuthType is the task authentication type at deploy time: none, bearer, apikey, basic, or
oauth2.`},
			"RequestTimeoutSeconds": {Doc: "RequestTimeoutSeconds is the per-request timeout to the remote agent at deploy time."},
			"Skills":                {Doc: "Skills are the skills saved with the agent at deploy time. Empty when there were none."},
			"InputModes": {Doc: `InputModes are the input content modes saved with the agent at deploy time. Empty when there
were none.`},
			"OutputModes": {Doc: `OutputModes are the output content modes saved with the agent at deploy time. Empty when there
were none.`},
			"ProtocolVersion":     {Doc: "ProtocolVersion is the A2A version pin at deploy time: auto, v1_0, or v0_3."},
			"TrustedCardJwksUrls": {Doc: "TrustedCardJwksUrls are the trusted JWK Set URLs at deploy time. Empty when there were none."},
			"RequireSignedCard":   {Doc: "RequireSignedCard is whether a signed agent card was required at deploy time."},
			"DeployedTime":        {Doc: "DeployedTime is when the deployment was requested."},
			"DeployedBy":          {Doc: "DeployedBy identifies the user who ran that deployment, in the same form as createdBy."},
		},
	}
}

func (RemoteAgentSkill) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RemoteAgentSkill is a skill as an A2A agent card lists it.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the skill's identifier within the agent card. Null when the card gives none."},
			"Name":        {Doc: "Name is the skill's display name."},
			"Description": {Doc: "Description says what the skill does."},
			"Tags":        {Doc: "Tags are keywords describing the skill. Empty when there are none."},
			"Examples":    {Doc: "Examples are sample prompts for the skill. Empty when there are none."},
			"InputModes":  {Doc: "InputModes are the input content modes this skill accepts. Empty when none are set."},
			"OutputModes": {Doc: "OutputModes are the output content modes this skill produces. Empty when none are set."},
		},
	}
}

func (RemoteAgentAuthConfig) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RemoteAgentAuthConfig is how Agent Mesh authenticates to a remote agent or
its card. On a write that keeps the type, a secret left out, sent as "", or
sent as "<REDACTED>" keeps the stored one. If the write changes where the
secret is sent (agentUrl, the card URL, or tokenEndpoint), the secret must be
sent again, or the request fails with 422.`,
		Fields: map[string]apidoc.Field{
			"Type": {Doc: "Type is the authentication method: none, bearer, apikey, basic, or oauth2."},
			"Token": {Doc: `Token is the bearer token, used when type is bearer. Required unless a replace keeps the stored
one. Never returned: responses show "<REDACTED>" when one is stored, and null otherwise.`},
			"APIKey": {Doc: `APIKey is the API key, used when type is apikey. Required unless a replace keeps the stored one.
Never returned: responses show "<REDACTED>" when one is stored, and null otherwise.`},
			"HeaderName": {Doc: `HeaderName is the HTTP header that carries the API key, used when type is apikey. Defaults to
X-API-Key.`},
			"TokenEndpoint": {Doc: `TokenEndpoint is the OAuth 2.0 token URL Agent Mesh requests access tokens from with the client
credentials grant, used when type is oauth2. Required unless a replace keeps the stored one.`},
			"ClientID": {Doc: `ClientID is the OAuth 2.0 client ID, used when type is oauth2. Required unless a replace keeps
the stored one.`},
			"ClientSecret": {Doc: `ClientSecret is the OAuth 2.0 client secret, used when type is oauth2. Required unless a replace
keeps the stored one. Never returned: responses show "<REDACTED>" when one is stored, and null
otherwise.`},
			"Scopes": {Doc: `Scopes are the OAuth 2.0 scopes to request, used when type is oauth2. When it is empty, none are
requested.`},
			"Username": {Doc: `Username is the basic authentication username, used when type is basic. Required unless a
replace keeps the stored one.`},
			"Password": {Doc: `Password is the basic authentication password, used when type is basic. Required unless a
replace keeps the stored one. Never returned: responses show "<REDACTED>" when one is stored,
and null otherwise.`},
		},
	}
}

func (RemoteAgentCard) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RemoteAgentCard is the part of an A2A agent card Agent Mesh reads.",
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the agent's name as the card gives it."},
			"Description": {Doc: "Description is the agent's description as the card gives it."},
			"Skills":      {Doc: "Skills are the skills the card lists. Empty when it lists none."},
			"InputModes": {Doc: `InputModes are the input content modes the card declares, such as text. Empty when it declares
none.`},
			"OutputModes": {Doc: `OutputModes are the output content modes the card declares, such as text. Empty when it declares
none.`},
			"Interfaces": {Doc: `Interfaces are the A2A versions and protocol bindings the card offers that Agent Mesh can speak,
each once, most preferred first: A2A 1.0 before 0.3, and JSON-RPC before HTTP+JSON. Empty when
the card offers none.`},
			"Signed": {Doc: "Signed is true when the card carries at least one signature."},
		},
	}
}

func (RemoteAgentProtocol) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RemoteAgentProtocol is how a deployed remote agent reaches the agent behind
it.`,
		Fields: map[string]apidoc.Field{
			"SelectedVersion": {Doc: "SelectedVersion is the A2A version in use: v1_0 (A2A 1.0) or v0_3 (A2A 0.3)."},
			"Binding":         {Doc: "Binding is the protocol binding in use: jsonrpc or http_json."},
			"Deprecated":      {Doc: "Deprecated is true when the selected A2A version is the older A2A 0.3, which Agent Mesh recommends moving off; the agent still works."},
			"CardSignature": {Doc: `CardSignature is how the agent card's signature checked out: verified (a trusted key signed
it), unsigned (it carries no signature), unchecked (no trusted JWK Set URLs are set), or refused
(the latest card failed the check, so tasks are not sent to the remote agent).`},
		},
	}
}

func (RemoteAgentInterface) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RemoteAgentInterface is one A2A version and protocol binding an agent card
offers.`,
		Fields: map[string]apidoc.Field{
			"Version": {Doc: "Version is the A2A version: v1_0 (A2A 1.0) or v0_3 (A2A 0.3)."},
			"Binding": {Doc: "Binding is the protocol binding: jsonrpc or http_json."},
		},
	}
}

func (CreateRemoteAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateRemoteAgentRequest connects a remote agent. If the name is already
taken, the request fails with 409.`,
		Fields: map[string]apidoc.Field{
			"Surface": {Doc: `Surface is the X-Sam-Client-Surface header, which says what kind of client is calling: ui, cli,
or api. Any other value, or no header, counts as api. Used only for usage statistics.`},
			"AgentURL": {Doc: `AgentURL is the remote agent's A2A endpoint URL, starting with http:// or https://. 1 to 2,048
characters.`},
			"Name": {Doc: `Name is the remote agent's name. 3 to 40 characters, with no control characters. It must be
unique, ignoring case and spaces.`},
			"AgentCardLocation": {Doc: `AgentCardLocation says where the agent card comes from: well_known (the agent's
/.well-known/agent-card.json, falling back to /.well-known/agent.json), custom (the URL in
agentCardUrl), or none (no card is read, and skills, inputModes, and outputModes stand in for it).`},
			"Description": {Doc: `Description says what the remote agent does. Optional. If sent, it must not be blank or contain
control characters other than tab and line breaks.`},
			"AgentCardURL": {Doc: "AgentCardURL is the URL the agent card is read from. Required when agentCardLocation is custom."},
			"AgentCardHeaders": {Doc: `AgentCardHeaders are extra HTTP headers to send when the agent card is fetched, keyed by header
name.`},
			"AgentCardAuthType": {Doc: `AgentCardAuthType is how Agent Mesh authenticates when it fetches the agent card: none, bearer,
apikey, basic, or oauth2. Leave it out to use the type inside agentCardAuthConfig.`},
			"AgentCardAuthConfig": {Doc: `AgentCardAuthConfig holds the credentials for fetching the agent card. Required when the card
authentication type is bearer, apikey, basic, or oauth2. Leave it out for none.`},
			"AgentAuthType": {Doc: `AgentAuthType is how Agent Mesh authenticates when it sends tasks to the remote agent: none,
bearer, apikey, basic, or oauth2. Leave it out to use the type inside agentAuthConfig.`},
			"AgentAuthConfig": {Doc: `AgentAuthConfig holds the credentials for sending tasks to the remote agent. Required. Send
{"type": "none"} for an agent without authentication.`},
			"TaskHeaders": {Doc: `TaskHeaders are extra HTTP headers to send with every task request to the remote agent, keyed by
header name.`},
			"RequestTimeoutSeconds": {Doc: `RequestTimeoutSeconds is how long Agent Mesh waits on each HTTP request to the remote agent
before giving up. 10 to 600 seconds. Defaults to 120.`},
			"Skills": {Doc: `Skills are the agent's skills. Required when agentCardLocation is none, where they stand in for
the card. Otherwise, the card read from the remote agent applies.`},
			"InputModes": {Doc: `InputModes are the input content modes the agent accepts, such as text. They are used when
agentCardLocation is none, and default to text.`},
			"OutputModes": {Doc: `OutputModes are the output content modes the agent produces, such as text. They are used when
agentCardLocation is none, and default to text.`},
			"ProtocolVersion": {Doc: `ProtocolVersion is the A2A version Agent Mesh speaks to the remote agent: auto (the highest
version both support), v1_0 (A2A 1.0), or v0_3 (A2A 0.3). Defaults to auto.`},
			"TrustedCardJwksUrls": {Doc: `TrustedCardJwksUrls are the URLs of the JWK Sets whose keys are trusted to sign the agent card.
Up to 10 absolute https URLs, or http on a loopback host only. A signed card is then accepted
only when a key from these sets verifies it, and no task is sent while the card fails that
check. Leave it out to skip the signature check. Cannot be set when agentCardLocation is none.`},
			"RequireSignedCard": {Doc: `RequireSignedCard, when true, refuses an agent card that carries no signature, and no task is
sent until a signed card is accepted. Requires trustedCardJwksUrls. Defaults to false.`},
		},
	}
}

func (UpdateRemoteAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateRemoteAgentRequest replaces a remote agent. An optional field left out
is cleared or reset to its default. Stored authentication secrets follow the
keep rules of agentAuthConfig and agentCardAuthConfig.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the remote agent to replace."},
			"AgentURL": {Doc: `AgentURL is the remote agent's new A2A endpoint URL, starting with http:// or https://. 1 to
2,048 characters.`},
			"Name": {Doc: `Name is the remote agent's new name. 3 to 40 characters, with no control characters. It must be
unique, ignoring case and spaces. If another remote agent has the name, the request fails with
409.`},
			"AgentCardLocation": {Doc: `AgentCardLocation replaces where the agent card comes from: well_known (the agent's
/.well-known/agent-card.json, falling back to /.well-known/agent.json), custom (the URL in
agentCardUrl), or none (no card is read, and skills, inputModes, and outputModes stand in for it).`},
			"Description": {Doc: `Description replaces the description. Leave it out to clear it. If sent, it must not be blank or
contain control characters other than tab and line breaks.`},
			"AgentCardURL": {Doc: `AgentCardURL replaces the URL the agent card is read from. Leave it out to clear it. Required
when agentCardLocation is custom.`},
			"AgentCardHeaders": {Doc: `AgentCardHeaders replaces the extra HTTP headers sent when the agent card is fetched. Leave it out
to clear them.`},
			"AgentCardAuthType": {Doc: `AgentCardAuthType is the new card authentication type: none, bearer, apikey, basic, or oauth2.
Leave it out to use the type inside agentCardAuthConfig.`},
			"AgentCardAuthConfig": {Doc: `AgentCardAuthConfig replaces the card authentication's settings. Leave it out to remove card
authentication, as long as agentCardAuthType is none or left out too.`},
			"AgentAuthType": {Doc: `AgentAuthType is the new task authentication type: none, bearer, apikey, basic, or oauth2. Leave
it out to use the type inside agentAuthConfig.`},
			"AgentAuthConfig": {Doc: `AgentAuthConfig replaces the task authentication's settings. Required. Send {"type": "none"} for
an agent without authentication.`},
			"TaskHeaders": {Doc: `TaskHeaders replaces the extra HTTP headers sent with every task request. Leave it out to clear
them.`},
			"RequestTimeoutSeconds": {Doc: `RequestTimeoutSeconds replaces how long Agent Mesh waits on each HTTP request to the remote
agent. 10 to 600 seconds. Leave it out to reset it to 120.`},
			"Skills": {Doc: `Skills replaces the agent's skills. Leave it out to clear them. If agentCardLocation is none,
clearing them fails with 422.`},
			"InputModes":  {Doc: "InputModes replaces the input content modes the agent accepts. Leave it out to clear them."},
			"OutputModes": {Doc: "OutputModes replaces the output content modes the agent produces. Leave it out to clear them."},
			"ProtocolVersion": {Doc: `ProtocolVersion replaces the A2A version Agent Mesh speaks to the remote agent: auto, v1_0, or
v0_3. Leave it out to reset it to auto.`},
			"TrustedCardJwksUrls": {Doc: `TrustedCardJwksUrls replaces the URLs of the JWK Sets whose keys are trusted to sign the agent
card. Up to 10 absolute https URLs, or http on a loopback host only. Leave it out to stop checking
card signatures. Cannot be set when agentCardLocation is none.`},
			"RequireSignedCard": {Doc: `RequireSignedCard replaces whether an agent card that carries no signature is refused. Leave it
out to reset it to false. Requires trustedCardJwksUrls.`},
		},
	}
}

func (FetchRemoteAgentCardRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "FetchRemoteAgentCardRequest reads an agent card before the agent is saved.",
		Fields: map[string]apidoc.Field{
			"AgentCardURL": {Doc: `AgentCardURL is the URL of the agent card to read, starting with http:// or https://. 11 to
2,048 characters.`},
			"AuthConfig": {Doc: `AuthConfig is how Agent Mesh authenticates to read the card, in the same shape as
agentCardAuthConfig. Leave it out for none. A card fetch stores nothing, so every secret its
type uses is required. Secrets are never returned.`},
			"CustomHeaders": {Doc: `CustomHeaders are extra HTTP headers to send with the card request, keyed by header name. They
override an authentication header of the same name.`},
		},
	}
}

func (getRemoteAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getRemoteAgentRequest addresses one remote agent and what to return with it.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the remote agent to fetch."},
			"Expand": {Doc: `Expand is a comma-separated list of extra data to return. The only value is card, which fetches
the agent card with the stored credentials and returns it as cardData. Other values are ignored.`},
		},
	}
}
