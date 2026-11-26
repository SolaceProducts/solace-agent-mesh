// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateLinkAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateLinkAgentRequest is config apply's copy of
apiv2.CreateLinkAgentRequest.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the link agent's name, 3 to 40 characters. It must be unique among all agents, link
agents included, ignoring case and spaces. The mesh shows it as the agent's display name.`},
			"Description": {Doc: "Description says what the link agent is for."},
			"EntrypointID": {Doc: `EntrypointID names the agent_link entrypoint the agent connects through. In config-apply
manifests this is the entrypoint's name, written as entrypoint and resolved to an ID at apply
time. It cannot change while the link agent is deployed.`},
			"TokenSHA256": {Doc: `TokenSHA256 is the SHA-256 digest of the agent's token, as 64 lowercase hexadecimal characters.
Required to create a link agent from a config-apply file, because a file cannot receive the
token Agent Mesh would otherwise generate and show once. The sam-link-sdk link-token command
issues a token and prints this digest. Leave it out for a link agent that exists to keep its
token. Agent Mesh never returns the digest, so sam config pull leaves it out. Changing it on a
deployed link agent takes effect at the deployment that apply runs.`},
			"Enabled": {Doc: "Enabled, when false, refuses the agent at the entrypoint and stops it being woken. Defaults to true."},
			"Owner":   {Doc: "Owner says who answers for the agent, stamped on its card. Up to 256 bytes."},
			"Team":    {Doc: "Team names the team that answers for the agent, stamped on its card. Up to 256 bytes."},
			"Peers": {Doc: `Peers are the mesh names of the agents this agent may call: the instanceName of an agent the
platform deploys (agent_ followed by its id with underscores), or the configured name of an
agent that runs from a runtime configuration file. Up to 64 names of letters, digits, "-" and "_", each starting with a
letter or digit. A peer does not have to be on the mesh yet. Leave it out to let the agent call
none.`},
			"ShareUserProfile": {Doc: `ShareUserProfile, when true, sends the user's profile from the configured profile provider to the agent
with each task. Defaults to false.`},
			"Wake": {Doc: `Wake sets the request-driven runtime that Agent Mesh calls when work arrives and the agent is not
connected: url (required), tokenUrl, clientId, clientSecret, scope, headers, timeoutSeconds
(1 to 86400, default 900) and onStart (default true). tokenUrl, clientId and clientSecret go
together. Write clientSecret as a ${VAR} placeholder. Leave wake out for an agent that stays
connected.`},
		},
	}
}

func (UpdateLinkAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateLinkAgentRequest is config apply's copy of
apiv2.UpdateLinkAgentRequest, a PUT body that replaces every setting. The link
agent's ID goes in the path, not the body.`,
	}
}

func (LinkAgentResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "LinkAgentResponse is config apply's copy of apiv2.LinkAgent.",
	}
}
