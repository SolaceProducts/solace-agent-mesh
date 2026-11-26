// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Entrypoint) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Entrypoint is how a user reaches Agent Mesh: a chat interface, webhook, or
messaging channel.`,
		Fields: map[string]apidoc.Field{
			"ID":             {Doc: "ID is the entrypoint's UUID."},
			"Type":           {Doc: "Type is always \"entrypoint\"."},
			"EntrypointType": {Doc: "EntrypointType is the kind of entrypoint, such as slack, teams, webhook, or whatsapp."},
			"Slug": {Doc: `Slug is the readable identifier that stands in for the id in the entrypoint's URLs. Null when
none was set.`},
			"Name":        {Doc: "Name is the entrypoint's display name, unique among entrypoints."},
			"Description": {Doc: "Description says what the entrypoint is for."},
			"PublicURL":   {Doc: "PublicURL is the public address recorded for the entrypoint. Null when none is set."},
			"Values": {Doc: `Values holds the entrypoint's settings. A secret setting reads "<REDACTED>". Each entrypoint
type has its own keys and value types.`},
			"DeploymentStatus": {Doc: `DeploymentStatus is the deployment state: deployed when the latest deploy or update
succeeded, deploy_failed when it failed, and not_deployed when there was none or the latest
action was an undeploy.`},
			"SyncStatus": {Doc: `SyncStatus says whether the settings match the latest successful deploy: in_sync when they
do, out_of_sync when they have changed since. Null when deploymentStatus is not_deployed or
no deploy has succeeded.`},
			"Deployed": {Doc: `Deployed is the entrypoint as its latest successful deploy left it. Null when
deploymentStatus is not_deployed or no deploy has succeeded.`},
			"RuntimeStatus": {Doc: "RuntimeStatus is what the running entrypoint reports about itself."},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the entrypoint: their email, or their user ID when
no email is known.`},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the entrypoint, in the same form as createdBy."},
			"CreatedTime": {Doc: "CreatedTime is when the entrypoint was created."},
			"UpdatedTime": {Doc: "UpdatedTime is when the entrypoint was last changed."},
		},
	}
}

func (DeployedEntrypoint) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DeployedEntrypoint is the entrypoint as its latest successful deploy left it.",
		Fields: map[string]apidoc.Field{
			"Name":           {Doc: "Name is the display name the entrypoint was deployed with."},
			"Description":    {Doc: "Description is the description the entrypoint was deployed with."},
			"EntrypointType": {Doc: "EntrypointType is the kind of entrypoint that was deployed."},
			"Values": {Doc: `Values holds the settings the entrypoint was deployed with. A secret setting reads
"<REDACTED>". Each entrypoint type has its own keys and value types.`},
			"DeployedTime": {Doc: "DeployedTime is when the deploy ran."},
			"DeployedBy": {Doc: `DeployedBy identifies the user who ran the deploy, in the same form as the entrypoint's
createdBy.`},
		},
	}
}

func (CreateEntrypointRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateEntrypointRequest is a request to create an entrypoint. Deploying it is
a separate POST /entrypointDeployments.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the display name: 3 to 255 characters with no control characters, unique among
entrypoints.`},
			"Slug": {Doc: `Slug is a readable identifier that stands in for the id in the entrypoint's URLs. 3 to 63
lowercase letters, digits, and hyphens, starting with a letter and ending with a letter or
digit. Must be unique and not oauth, api, well-known, gw, or health. Leave it out to use the
id. It cannot be changed later.`},
			"Description": {Doc: `Description says what the entrypoint is for: 10 to 1,000 characters with no control
characters other than tabs and line breaks.`},
			"EntrypointType": {Doc: "EntrypointType is the kind of entrypoint, such as slack or webhook. It cannot be changed later."},
			"PublicURL":      {Doc: "PublicURL is an https URL to record as the entrypoint's public address. Leave it out for none."},
			"Values": {Doc: `Values holds the entrypoint's settings. Leave it out to store none, which the email, teams,
whatsapp, event_mesh, webhook, and google_chat types reject. Each entrypoint type has its own
keys and value types.`},
		},
	}
}

func (UpdateEntrypointRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateEntrypointRequest changes the fields it includes and keeps the rest. The
entrypointType and slug fields are fixed at create, so sending either fails
with 422.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the entrypoint to update."},
			"Name": {Doc: `Name is the display name, with the same rules as on create. Leave it out to keep it. Null or
"" fails with 422.`},
			"Description": {Doc: `Description says what the entrypoint is for, with the same rules as on create. Leave it out
to keep it. Null or "" fails with 422.`},
			"PublicURL": {Doc: `PublicURL is the public address, an https URL. Leave it out to keep it, or send null or "" to
clear it.`},
			"Values": {Doc: `Values changes the settings it includes and keeps the others. A secret setting sent as null or
"<REDACTED>" keeps its stored value, and "" clears an optional secret setting. Leave it out to
keep every setting. Null fails with 422. Each entrypoint type has its own keys and value types.`},
			"EntrypointType": {Doc: "EntrypointType is fixed at create. Including it fails with 422."},
			"Slug":           {Doc: "Slug is fixed at create. Including it fails with 422."},
		},
	}
}

func (EntrypointSchema) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "EntrypointSchema describes one entrypoint type and the values it takes.",
		Fields: map[string]apidoc.Field{
			"ID":             {Doc: "ID is the entrypoint type this schema describes, such as slack."},
			"Type":           {Doc: "Type is always \"entrypointSchema\"."},
			"EntrypointType": {Doc: "EntrypointType is the type this schema describes, the value to send as entrypointType."},
			"Name":           {Doc: "Name is the type's display name."},
			"Description":    {Doc: "Description says what the type connects to."},
			"Warning": {Doc: `Warning is a setup note for the type, such as an account it requires. Null when there is
none.`},
			"Schema": {Doc: `Schema maps each key of the type's values to its field definition, such as its label, input
type, whether it is required or secret, and its default. The shape differs for each
entrypoint type.`},
			"RequiredEnvVars": {Doc: `RequiredEnvVars names the environment variables the type expects at runtime. Empty when it
needs none.`},
		},
	}
}

func (NetworkingDetails) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `NetworkingDetails is where an ingress-backed entrypoint is reached. For a
type with no ingress, such as slack, every field is null and apiPaths is
empty.`,
		Fields: map[string]apidoc.Field{
			"DNSName": {Doc: `DNSName is the entrypoint's own host name. Null when entrypoints share the platform's
ingress.`},
			"Port": {Doc: `Port is the entrypoint's public port, 443. Null when entrypoints share the platform's
ingress.`},
			"URL": {Doc: `URL is the entrypoint's base address: https://<dnsName>, or, when entrypoints share the
platform's ingress, a path such as /gw/<slug or id> on the platform's own host.`},
			"WebhookURL": {Doc: `WebhookURL is the address to register with the provider: url followed by the first of
apiPaths. Null when apiPaths is empty.`},
			"APIPaths":  {Doc: "APIPaths are the paths the entrypoint serves, relative to url."},
			"PublicURL": {Doc: "PublicURL is the public address recorded for the entrypoint. Null when none is set."},
		},
	}
}

func (EntrypointAPIPath) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "EntrypointAPIPath is one path an entrypoint serves.",
		Fields: map[string]apidoc.Field{
			"Path":        {Doc: "Path is where the entrypoint serves this endpoint, relative to url."},
			"Description": {Doc: "Description says what the path is for."},
		},
	}
}

func (TestConnectionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `TestConnectionRequest is a request to probe an entrypoint's credentials, from
values or from a saved entrypoint.`,
		Fields: map[string]apidoc.Field{
			"EntrypointType": {Doc: `EntrypointType is the type to probe. Ignored when entrypointId is sent. Defaults to
event_mesh.`},
			"Values": {Doc: `Values holds the settings to probe. With entrypointId, they override the saved settings. A
secret sent as "<REDACTED>" uses the stored secret while the destination is unchanged. The
destination is broker_url and tls_skip_verify for event_mesh, and imap_host and imap_port for
email. If the destination changes and the saved entrypoint holds a secret, send the secret
again, or the request fails with 422. With the secret sent, no saved setting is used. If the
saved entrypoint holds no secret, a destination change uses only the values sent. Identity
fields, such as the mailbox or message VPN, can change while the destination stays. Each
entrypoint type has its own keys and value types, listed by GET /entrypointSchemas/{id}.`},
			"EntrypointID": {Doc: `EntrypointID is the UUID of a saved entrypoint whose settings and type are probed, with values
laid over them. Send entrypointId, values, or both.`},
		},
	}
}

func (TestConnectionResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `TestConnectionResult is the outcome of a probe that ran. A failed probe is a
result, not an error.`,
		Fields: map[string]apidoc.Field{
			"Success": {Doc: `Success says whether the probe passed. Connectors of type event_mesh, and entrypoints of type
event_mesh, email, whatsapp, and google_chat, are probed. An entrypoint of any other type
reports true. Probing a connector of any other type fails with 422.`},
			"Message": {Doc: `Message says why the probe failed or, when it passed, what it could not check. Null when
there is nothing to say.`},
			"ErrorClass": {Doc: `ErrorClass names the kind of failure for an email, whatsapp, or google_chat probe. Null
otherwise, and always null for a connector. Email: auth_credentials_invalid,
auth_credentials_expired, auth_permission_denied, auth_consent_required, network_unreachable,
tls_handshake_failed, starttls_unsupported, or unknown. WhatsApp: config_incomplete,
auth_credentials_invalid, auth_credentials_expired, auth_permission_denied,
phone_number_id_unknown, rate_limited, network_unreachable, or unknown. Google Chat:
config_incomplete, service_account_key_invalid, auth_credentials_invalid, chat_api_disabled,
chat_app_not_configured, auth_permission_denied, pubsub_api_disabled, subscription_not_found,
subscription_permission_denied, rate_limited, network_unreachable, or unknown.`},
			"Stage": {Doc: `Stage is the leg of the connection that failed: inbound for email and outbound for whatsapp.
For google_chat, outbound for the Chat API check and inbound for the Pub/Sub subscription
check. Null otherwise, and always null for a connector.`},
		},
	}
}

func (SecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SecretsDiffRequest holds the values to compare with the entrypoint's stored
secrets.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the entrypoint whose stored secrets are compared."},
			"Values": {Doc: `Values holds the settings to compare. Only secret settings are compared. Each entrypoint type
has its own keys and value types.`},
		},
	}
}

func (SecretsDiffResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SecretsDiffResult says, for each secret setting, whether saving the values
sent would keep the stored secret.`,
		Fields: map[string]apidoc.Field{
			"Secrets": {Doc: "Secrets has one entry for each secret setting compared, keyed by its name."},
		},
	}
}

func (SecretMatch) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SecretMatch is one secret field's comparison.",
		Fields: map[string]apidoc.Field{
			"Matches": {Doc: `Matches is true when saving the values would keep the stored secret: the setting is left
out, null, "<REDACTED>", "" on a required setting, or equal to the stored value. False means
saving would change it.`},
		},
	}
}

func (WhatsAppMessageTemplatesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `WhatsAppMessageTemplatesRequest is a lookup of a WhatsApp Business Account's
message templates.`,
		Fields: map[string]apidoc.Field{
			"BusinessAccountID": {Doc: `BusinessAccountID is the WhatsApp Business Account the templates belong to, not the phone
number id the entrypoint is configured with.`},
			"Values": {Doc: `Values holds the WhatsApp settings to look up with, including the access token. With
entrypointId, they override the saved settings. A secret sent as "<REDACTED>" uses the stored
one. GET /entrypointSchemas/whatsapp lists the keys.`},
			"EntrypointID": {Doc: `EntrypointID is the UUID of a saved WhatsApp entrypoint whose settings are used, with values
laid over them. Send entrypointId, values, or both.`},
		},
	}
}

func (MessageTemplateLookup) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MessageTemplateLookup is the templates one lookup found.",
		Fields: map[string]apidoc.Field{
			"Templates": {Doc: "Templates are the account's message templates. Up to 200."},
			"HasMore":   {Doc: "HasMore is true when the account holds more templates than one lookup returns."},
		},
	}
}

func (WhatsAppMessageTemplate) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `WhatsAppMessageTemplate is one of the account's templates. The usable field
says whether it can serve as the contact-request offer, and the blockers field
says why not.`,
		Fields: map[string]apidoc.Field{
			"Name":     {Doc: "Name is the template's name in WhatsApp Manager."},
			"Language": {Doc: "Language is the template's language code."},
			"Status": {Doc: `Status is the review status Meta gives the template, in lowercase: approved, pending,
rejected, in_appeal, paused, disabled, limit_exceeded, pending_deletion, deleted, archived, or
another status Meta adds, in the same form. Only an approved template can be sent.`},
			"Body": {Doc: "Body is the template's body text, truncated to 1,024 characters."},
			"Usable": {Doc: `Usable is true when the template can be the contact-request offer: approved, with a Share
Contact Info button and no parameters.`},
			"Blockers": {Doc: `Blockers lists every reason the template is not usable: no_contact_button, has_variables, or
not_approved. Empty when it is usable.`},
		},
	}
}

func (listEntrypointsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listEntrypointsRequest selects a page of entrypoints.",
		Fields: map[string]apidoc.Field{
			"Page":           {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"EntrypointType": {Doc: "EntrypointType limits the list to one entrypoint type. Leave it out to list every type."},
		},
	}
}

func (getEntrypointSchemaRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getEntrypointSchemaRequest addresses one entrypoint schema.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the entrypoint type whose schema to get, such as slack."},
		},
	}
}
