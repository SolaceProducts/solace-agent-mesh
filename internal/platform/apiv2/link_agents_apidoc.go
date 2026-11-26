// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (LinkAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `LinkAgent is a third-party agent that joins the mesh by connecting to an
agent_link entrypoint over a WebSocket. Its token is never returned here, and a
stored wake client secret and its wake header values come back as "<REDACTED>".`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the link agent's UUID."},
			"Type": {Doc: "Type is always \"linkAgent\"."},
			"Name": {Doc: "Name is the link agent's name, unique among agents ignoring case and spaces. The mesh shows it as the agent's display name."},
			"InstanceName": {Doc: `InstanceName is the name the link agent goes by on the agent mesh: "agent_" followed by id with
its dashes replaced by underscores. The agent sends it in the Sam-Agent header when it connects.`},
			"Description":  {Doc: "Description says what the link agent is for. Null when none was given."},
			"EntrypointID": {Doc: "EntrypointID is the UUID of the agent_link entrypoint the agent connects through."},
			"ConnectURL": {Doc: `ConnectURL is the WebSocket address the agent connects to. Null when Agent Mesh does not know the
entrypoint's public address. Then join the address the web UI is served from, with wss in place
of https, to connectPath.`},
			"ConnectPath": {Doc: "ConnectPath is the path of the entrypoint's connect endpoint on the address the web UI is served from."},
			"TokenPrefix": {Doc: "TokenPrefix is the first 8 characters of the token after its samk_ marker, kept so two tokens can be told apart. The prefix is not the token. Null when the token's digest was sent as tokenSha256, because Agent Mesh never held that token."},
			"Enabled":     {Doc: "Enabled is false when the agent is refused at the entrypoint and is not woken."},
			"Owner":       {Doc: "Owner says who answers for the agent, stamped on its card. Null when none was given."},
			"Team":        {Doc: "Team names the team that answers for the agent, stamped on its card. Null when none was given."},
			"Peers":       {Doc: "Peers are the mesh names of the agents this agent may call. Empty when it may call none."},
			"ShareUserProfile": {Doc: `ShareUserProfile is true when the user's profile from the configured profile provider is sent to the agent
with each task.`},
			"Wake": {Doc: `Wake holds the settings of the request-driven runtime that Agent Mesh calls when work arrives for
an agent that is not connected. Null when the agent has none.`},
			"DeploymentStatus": {Doc: `DeploymentStatus is the state the latest deployment left: deployed (a successful deploy or
update), deploy_failed (a failed deploy or update), or not_deployed (an undeploy, or no
deployment yet). An undeploy refused before it starts, for example while the entrypoint's saved
config cannot be read, records nothing and leaves it deployed.`},
			"SyncStatus": {Doc: `SyncStatus compares the agent's current settings, its token included, with its latest successful
deploy: in_sync when they match, out_of_sync when they have changed since. Null when the agent
is not deployed or never deployed successfully.`},
			"RuntimeStatus": {Doc: `RuntimeStatus is the link agent's state as last seen on the agent mesh: running while the agent is
connected or listed for wake, degraded when it reports problems, disconnected when it is
deployed and not connected, and stopped when it is not deployed.`},
			"Deployed": {Doc: `Deployed holds the settings the agent was last deployed with, without secrets. Null when the
agent is not deployed.`},
			"CreatedBy":   {Doc: "CreatedBy is the user who created the link agent."},
			"UpdatedBy":   {Doc: "UpdatedBy is the user who last changed the link agent."},
			"CreatedTime": {Doc: "CreatedTime is when the link agent was created."},
			"UpdatedTime": {Doc: "UpdatedTime is when the link agent was last changed."},
		},
	}
}

func (LinkAgentWithToken) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `LinkAgentWithToken is a link agent with its token, returned when the agent is
created and when its token is replaced. No other response carries the token,
and it cannot be read again. After a token is replaced on a deployed agent, it
also carries the deployment that put the new token into effect. A link agent
created with tokenSha256 has no token to return.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the link agent's UUID."},
			"Type": {Doc: "Type is always \"linkAgent\"."},
			"Name": {Doc: "Name is the link agent's name, unique among agents ignoring case and spaces. The mesh shows it as the agent's display name."},
			"InstanceName": {Doc: `InstanceName is the name the link agent goes by on the agent mesh: "agent_" followed by id with
its dashes replaced by underscores. The agent sends it in the Sam-Agent header when it connects.`},
			"Description":  {Doc: "Description says what the link agent is for. Null when none was given."},
			"EntrypointID": {Doc: "EntrypointID is the UUID of the agent_link entrypoint the agent connects through."},
			"ConnectURL": {Doc: `ConnectURL is the WebSocket address the agent connects to. Null when Agent Mesh does not know the
entrypoint's public address. Then join the address the web UI is served from, with wss in place
of https, to connectPath.`},
			"ConnectPath": {Doc: "ConnectPath is the path of the entrypoint's connect endpoint on the address the web UI is served from."},
			"Token": {Doc: `Token is the secret the agent sends as its bearer token when it connects. Store it now: no other
response shows it, and it cannot be retrieved again. Null when the agent was created with
tokenSha256, because Agent Mesh never held that token.`},
			"Deployment": {Doc: `Deployment is the update deployment that replacing the token started, so that the previous token
stops working. Its status is failed when the entrypoint did not apply it. Then the previous
token keeps working until an update deployment succeeds. Null when the agent was created, when
the agent is not deployed, and when the deployment could not start.`},
			"DeploymentRefusal": {Doc: `DeploymentRefusal says why the update deployment could not start after the token of a deployed
agent was replaced. The new token is stored and the previous one keeps working until an update
deployment succeeds. Null otherwise.`},
			"TokenPrefix": {Doc: "TokenPrefix is the first 8 characters of the token after its samk_ marker, kept so two tokens can be told apart. Null when the token's digest was sent as tokenSha256."},
			"Enabled":     {Doc: "Enabled is false when the agent is refused at the entrypoint and is not woken."},
			"Owner":       {Doc: "Owner says who answers for the agent, stamped on its card. Null when none was given."},
			"Team":        {Doc: "Team names the team that answers for the agent, stamped on its card. Null when none was given."},
			"Peers":       {Doc: "Peers are the mesh names of the agents this agent may call. Empty when it may call none."},
			"ShareUserProfile": {Doc: `ShareUserProfile is true when the user's profile from the configured profile provider is sent to the agent
with each task.`},
			"Wake": {Doc: `Wake holds the settings of the request-driven runtime that Agent Mesh calls when work arrives for
an agent that is not connected. Null when the agent has none.`},
			"DeploymentStatus": {Doc: `DeploymentStatus is the state the latest deployment left: deployed (a successful deploy or
update), deploy_failed (a failed deploy or update), or not_deployed (an undeploy, or no
deployment yet). An undeploy refused before it starts, for example while the entrypoint's saved
config cannot be read, records nothing and leaves it deployed.`},
			"SyncStatus": {Doc: `SyncStatus compares the agent's current settings, its token included, with its latest successful
deploy: in_sync when they match, out_of_sync when they have changed since. It is out_of_sync
after the token of a deployed agent is replaced and the deployment that followed did not
succeed. Null when the agent is not deployed or never deployed successfully.`},
			"RuntimeStatus": {Doc: `RuntimeStatus is the link agent's state as last seen on the agent mesh: running while the agent is
connected or listed for wake, degraded when it reports problems, disconnected when it is
deployed and not connected, and stopped when it is not deployed.`},
			"Deployed": {Doc: `Deployed holds the settings the agent was last deployed with, without secrets. Null when the
agent is not deployed.`},
			"CreatedBy":   {Doc: "CreatedBy is the user who created the link agent."},
			"UpdatedBy":   {Doc: "UpdatedBy is the user who last changed the link agent."},
			"CreatedTime": {Doc: "CreatedTime is when the link agent was created."},
			"UpdatedTime": {Doc: "UpdatedTime is when the link agent was last changed."},
		},
	}
}

func (LinkAgentWake) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `LinkAgentWake describes the request-driven runtime that Agent Mesh calls to
wake a link agent. The client secret and every header value come back as
"<REDACTED>".`,
		Fields: map[string]apidoc.Field{
			"URL":          {Doc: "URL is the address Agent Mesh calls to wake the agent."},
			"TokenURL":     {Doc: "TokenURL is the OAuth token endpoint Agent Mesh gets an access token from before a wake call. Null when the wake call is sent without one."},
			"ClientID":     {Doc: "ClientID is the OAuth client id sent to tokenUrl. Null when tokenUrl is not set."},
			"ClientSecret": {Doc: "ClientSecret is \"<REDACTED>\" when a client secret is stored. Null when none is stored."},
			"Scope":        {Doc: "Scope is the OAuth scope requested at tokenUrl. Null when none was given."},
			"Headers":      {Doc: "Headers are the extra HTTP headers sent with each wake call, keyed by header name. Empty when there are none. Every value is returned as \"<REDACTED>\"."},
			"TimeoutSeconds": {Doc: `TimeoutSeconds is how long one wake call may take, in seconds. It is 900 when none was
given.`},
			"OnStart": {Doc: "OnStart is true when the agent is also woken when its entrypoint starts, so it is listed on the mesh again."},
		},
	}
}

func (DeployedLinkAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DeployedLinkAgent is the link agent as it was when last deployed. Compare it
with the current settings to see what an update would change. It carries no
token and no secret.`,
		Fields: map[string]apidoc.Field{
			"Name":             {Doc: "Name is the link agent's name when it was deployed."},
			"Description":      {Doc: "Description is the link agent's description when it was deployed. Null when it had none."},
			"EntrypointID":     {Doc: "EntrypointID is the UUID of the agent_link entrypoint the agent was deployed on."},
			"TokenPrefix":      {Doc: "TokenPrefix is the prefix of the token the agent was deployed with. A different tokenPrefix on the link agent means its token was replaced since. Null when the digest of the deployed token was sent as tokenSha256. Then syncStatus is what shows that the token was replaced."},
			"Enabled":          {Doc: "Enabled is whether the agent was allowed to connect when it was deployed."},
			"Owner":            {Doc: "Owner is the deployed owner. Null when it had none."},
			"Team":             {Doc: "Team is the deployed team. Null when it had none."},
			"Peers":            {Doc: "Peers are the mesh names of the agents the deployed agent may call."},
			"ShareUserProfile": {Doc: "ShareUserProfile is whether the user's profile was shared with the agent when it was deployed."},
			"Wake":             {Doc: "Wake holds the deployed wake settings without their secrets. Null when the agent was deployed without them."},
			"DeployedTime":     {Doc: "DeployedTime is when the agent was last deployed successfully."},
			"DeployedBy":       {Doc: "DeployedBy is the user who made that deployment."},
		},
	}
}

func (DeployedLinkAgentWake) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DeployedLinkAgentWake is the wake settings a link agent was last deployed
with, without the client secret and without header values.`,
		Fields: map[string]apidoc.Field{
			"URL":            {Doc: "URL is the deployed wake address."},
			"TokenURL":       {Doc: "TokenURL is the deployed OAuth token endpoint. Null when it had none."},
			"ClientID":       {Doc: "ClientID is the deployed OAuth client id. Null when it had none."},
			"Scope":          {Doc: "Scope is the deployed OAuth scope. Null when it had none."},
			"HeaderNames":    {Doc: "HeaderNames are the names of the extra headers the deployed wake call sends, sorted. Their values are left out."},
			"TimeoutSeconds": {Doc: "TimeoutSeconds is how long one deployed wake call may take, in seconds."},
			"OnStart":        {Doc: "OnStart is whether the deployed agent is woken when its entrypoint starts."},
		},
	}
}

func (LinkAgentWakeRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `LinkAgentWakeRequest sets the request-driven runtime that Agent Mesh calls to
wake a link agent when work arrives and the agent is not connected.`,
		Fields: map[string]apidoc.Field{
			"URL": {Doc: `URL is the address Agent Mesh calls to wake the agent. Required. An absolute https URL, or http
on localhost or a loopback address only. A URL that carries a user name, a password or a query
parameter other than api-version fails the request with 422: send credentials in headers. The
api-version value must look like a version, such as 2025-05-01 or v1. The URL is stored and
returned as sent. So
does a literal IP address
for a cloud metadata endpoint, a link-local, unspecified or multicast address. The Entrypoint Executor
also refuses to call a private or loopback address, whether written as an address or reached
through a host name, unless its operator sets SAM_AGENT_LINK_ALLOW_PRIVATE_WAKE=true, so such a
URL is accepted here but its wake calls fail.`},
			"TokenURL": {Doc: `TokenURL is the OAuth token endpoint Agent Mesh gets an access token from before each wake call,
with the client credentials grant. The same rules as url apply, the Entrypoint Executor's
refusal of private and loopback addresses included, except that tokenUrl carries no query
string at all, api-version included. Leave it out, with clientId and
clientSecret, to send the wake call without a token.`},
			"ClientID": {Doc: "ClientID is the OAuth client id sent to tokenUrl. Required with tokenUrl."},
			"ClientSecret": {Doc: `ClientSecret is the OAuth client secret sent to tokenUrl. Required with tokenUrl when none is
stored. On a replace, leave it out, send "", or send "<REDACTED>" to keep the stored secret. If
tokenUrl or wake.url changes, the secret must be sent again, or the request fails with 422.`},
			"Scope":   {Doc: "Scope is the OAuth scope to request at tokenUrl."},
			"Headers": {Doc: "Headers are extra HTTP headers to send with each wake call, keyed by header name. Up to 32. Names are case-insensitive, so two names that differ only in case are refused, and so are Host, Content-Length, Transfer-Encoding and Content-Type, which the wake call sets itself, and Authorization when tokenUrl is set, because the access token is sent as the bearer. Any of these fails the request with 422. On a replace, send a value as \"<REDACTED>\" to keep the stored value of the header with that name. If wake.url changes, the value must be sent again, or the request fails with 422."},
			"TimeoutSeconds": {Doc: `TimeoutSeconds is how long one wake call may take, in seconds. 1 to 86,400. Defaults to
900.`},
			"OnStart": {Doc: "OnStart, when true, also wakes the agent when its entrypoint starts, so it is listed on the mesh again. Defaults to true."},
		},
	}
}

func (CreateLinkAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateLinkAgentRequest registers a third-party agent that joins the mesh
through an agent_link entrypoint. Agent Mesh generates the agent's token and
returns it once, unless tokenSha256 carries the digest of a token issued
elsewhere.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the link agent's name. 3 to 40 characters, with no control characters. It must be unique
among agents, ignoring case and spaces. If another agent has the name, the request fails with
409.`},
			"Description": {Doc: "Description says what the link agent is for."},
			"EntrypointID": {Doc: `EntrypointID is the UUID of the agent_link entrypoint the agent connects through. If it names no
entrypoint, or one of another type, the request fails with 422.`},
			"TokenSHA256": {Doc: `TokenSHA256 is the SHA-256 digest of a token issued outside Agent Mesh, as 64 lowercase
hexadecimal characters. The sam-link-sdk link-token command issues a token and prints this
digest. When it is sent, Agent Mesh stores it, generates no token, and returns token as null.
Leave it out to have Agent Mesh generate the token. A malformed digest, or the digest of a token
another link agent uses, fails with 422.`},
			"Enabled": {Doc: "Enabled, when false, refuses the agent at the entrypoint and stops it being woken. Defaults to true."},
			"Owner":   {Doc: "Owner says who answers for the agent, stamped on its card. Up to 256 bytes."},
			"Team":    {Doc: "Team names the team that answers for the agent, stamped on its card. Up to 256 bytes."},
			"Peers": {Doc: `Peers are the mesh names of the agents this agent may call, such as the instanceName of another
agent. Up to 64 names of letters, digits, "-" and "_", each starting with a letter or digit. A
name listed twice, or the agent's own instanceName, fails with 422. Leave it out to let the
agent call none.`},
			"ShareUserProfile": {Doc: `ShareUserProfile, when true, sends the user's profile from the configured profile provider to the agent with
each task. Defaults to false.`},
			"Wake": {Doc: "Wake sets the request-driven runtime that Agent Mesh calls when work arrives and the agent is not connected. Leave it out for an agent that stays connected."},
		},
	}
}

func (UpdateLinkAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateLinkAgentRequest replaces a link agent's settings. An optional field
left out is cleared or reset to its default. The token is kept when
tokenSha256 is left out, and so is a stored wake client secret that is left
out.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the link agent to replace."},
			"Name": {Doc: `Name is the link agent's new name. 3 to 40 characters, with no control characters. It must be
unique among agents, ignoring case and spaces. If another agent has the name, the request fails
with 409.`},
			"Description": {Doc: "Description replaces what the link agent is for. Leave it out to clear it."},
			"EntrypointID": {Doc: `EntrypointID is the UUID of the agent_link entrypoint the agent connects through. If it names no
entrypoint, or one of another type, the request fails with 422. If it differs from the stored
one while the agent is deployed, the request fails with 422: undeploy the agent first.`},
			"TokenSHA256": {Doc: `TokenSHA256 replaces the agent's token with the one this SHA-256 digest belongs to, as 64 lowercase
hexadecimal characters. Leave it out, or send the stored digest, to keep the token. A deployed
agent keeps accepting the token it was deployed with until an update deployment succeeds, and
syncStatus reads out_of_sync until then. A malformed digest, or the digest of a token another
link agent uses, fails with 422.`},
			"Enabled": {Doc: "Enabled, when false, refuses the agent at the entrypoint and stops it being woken. Defaults to true."},
			"Owner":   {Doc: "Owner replaces who answers for the agent. Up to 256 bytes. Leave it out to clear it."},
			"Team":    {Doc: "Team replaces the team that answers for the agent. Up to 256 bytes. Leave it out to clear it."},
			"Peers": {Doc: `Peers replace the mesh names of the agents this agent may call. Up to 64 names of letters, digits,
"-" and "_", each starting with a letter or digit. A name listed twice, or the agent's own
instanceName, fails with 422. Leave it out to let the agent call none.`},
			"ShareUserProfile": {Doc: `ShareUserProfile, when true, sends the user's profile from the configured profile provider to the agent with
each task. Defaults to false.`},
			"Wake": {Doc: "Wake replaces the wake settings. Leave it out to remove them, the stored client secret included."},
		},
	}
}

func (LinkAgentSecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "LinkAgentSecretsDiffRequest carries the token digest and the wake client secret to compare with a link agent's stored ones.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the UUID of the link agent whose stored values are compared. If the ID is unknown, the request
fails with 404.`},
			"Values": {Doc: "Values holds the token digest and the wake client secret to compare with the stored ones. Required."},
		},
	}
}

func (LinkAgentSecretsDiffForm) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `LinkAgentSecretsDiffForm is the token digest, the wake client secret and the
wake header values as a link agent's settings hold them. The result has one entry
for tokenSha256, one for wake.clientSecret and, when headers are sent, one for
wake.headers.`,
		Fields: map[string]apidoc.Field{
			"TokenSHA256": {Doc: `TokenSHA256 is the token digest to compare with the stored one. Left out or empty, it matches,
because a replace that leaves it out keeps the token.`},
			"Wake": {Doc: "Wake holds the wake client secret and header values to compare. Left out, the secret matches and the headers are not compared."},
		},
	}
}

func (LinkAgentWakeSecretsDiffForm) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "LinkAgentWakeSecretsDiffForm is the wake client secret and header values as a link agent's wake settings hold them.",
		Fields: map[string]apidoc.Field{
			"Headers": {Doc: `Headers is every wake header, name and value, to compare with the stored ones. The
entry matches when the same header names are sent and each value equals the stored one or
is "<REDACTED>". Left out, the headers are not compared and the result has no entry for them.`},
			"ClientSecret": {Doc: `ClientSecret is the wake client secret to compare with the stored one. Left out, empty, or sent as
"<REDACTED>", it matches, because a replace that sends it so keeps the stored secret.`},
		},
	}
}
