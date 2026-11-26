// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Connector) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Connector is an external system an agent or workflow can reach, such as an
OpenAPI service, a Model Context Protocol (MCP) server, or an event mesh.`,
		Fields: map[string]apidoc.Field{
			"ID":                 {Doc: "ID is the connector's UUID."},
			"Type":               {Doc: "Type is always \"connector\"."},
			"ConnectorType":      {Doc: "ConnectorType is the kind of system the connector reaches: api, document_db, email, event_mesh, graph_db, knowledge_base, mcp, search, slack, sql, or web_search. A plugin package can register other types at runtime. Fixed when the connector is created."},
			"ConnectorSubtype":   {Doc: "ConnectorSubtype narrows the kind, such as openapi for an api connector or postgres for a sql one. Fixed when the connector is created."},
			"Name":               {Doc: "Name is the connector's display name, unique across connectors ignoring case."},
			"Description":        {Doc: "Description says what the connector is for. Null when it has none."},
			"Values":             {Doc: "Values holds the settings the connector subtype's schema defines. Secret settings read back as \"<REDACTED>\". So does every setting of a connector type this server does not recognize."},
			"AgentCount":         {Doc: "AgentCount is how many agents are bound to this connector."},
			"HasResource":        {Doc: "HasResource is true when an OpenAPI specification has been uploaded for this connector."},
			"ContentHash":        {Doc: "ContentHash is the hex SHA-256 hash of the uploaded specification. Null when none has been uploaded. The sam config apply command compares it to skip re-uploading an unchanged file."},
			"Warnings":           {Doc: "Warnings lists messages that do not block the request. Create and update add reachability findings from a connection probe. An update of an mcp connector adds a note when the server's tool list could not be refreshed, saying whether the stored list was kept or the update's empty manifest cleared it, and names the tools left out because their names are over 64 characters. Create, update, and specification upload add approval gates that could not be checked against a specification, or that were removed because the connector no longer provides that tool. Getting a single connector adds a note when not every agent registration could be gathered. Empty when there are none."},
			"AgentRegistrations": {Doc: "AgentRegistrations reports, per running agent, whether this connector's tools registered. Empty on a list, and for agents that do not report."},
			"CreatedBy":          {Doc: "CreatedBy identifies the user who created the connector: their email, or their user ID when no email is known. Set to \"unknown\" when the call carried no identity. Null when no API call created the connector, such as a seeded one."},
			"UpdatedBy":          {Doc: "UpdatedBy identifies the user who last changed the connector, in the same form as createdBy."},
			"CreatedTime":        {Doc: "CreatedTime is when the connector was created."},
			"UpdatedTime":        {Doc: "UpdatedTime is when the connector was last changed."},
		},
	}
}

func (ConnectorAgentRegistration) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ConnectorAgentRegistration is one running agent's registration outcome for a
connector's tools. Read status, not error, to tell whether registration
succeeded: a ready agent can still report a partial loss.`,
		Fields: map[string]apidoc.Field{
			"Agent":     {Doc: "Agent identifies the agent the outcome belongs to."},
			"Status":    {Doc: "Status is the registration outcome: ready or failed."},
			"Error":     {Doc: "Error says what went wrong. Null when nothing did."},
			"ToolCount": {Doc: "ToolCount is how many of the connector's tools the agent registered."},
		},
	}
}

func (ConnectorAgentRef) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConnectorAgentRef names the agent a registration outcome belongs to.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the agent's UUID."},
			"Name": {Doc: "Name is the agent's display name."},
		},
	}
}

func (CreateConnectorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateConnectorRequest is the body that creates a connector.",
		Fields: map[string]apidoc.Field{
			"Name":             {Doc: "Name is the connector's display name, 3 to 255 characters with no control characters, and unique ignoring case."},
			"Description":      {Doc: "Description says what the connector is for, 10 to 1,000 characters."},
			"ConnectorType":    {Doc: "ConnectorType is the kind of system to reach: api, document_db, email, event_mesh, graph_db, knowledge_base, mcp, search, slack, sql, or web_search. A type that a plugin package registered at runtime is also accepted. Every setting of such a connector reads back as \"<REDACTED>\". A type this deployment has turned off fails with 422. Cannot be changed later."},
			"ConnectorSubtype": {Doc: "ConnectorSubtype narrows the kind, such as openapi for an api connector or postgres for a sql one. A declared type takes only its own declared subtypes. It cannot be changed later."},
			"Values":           {Doc: "Values holds the settings the subtype's schema defines. Leave it out to use the subtype's defaults."},
		},
	}
}

func (UpdateConnectorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateConnectorRequest changes only the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID":               {Doc: "ID is the UUID of the connector to update."},
			"Name":             {Doc: "Name is the display name. A new name follows the same rules as when the connector is created: 3 to 255 characters with no control characters, and unique ignoring case. Leave it out to keep it. Null, an empty string, or a name that breaks those rules fails with 422. A stored name that breaks the rules is kept as long as the name is not changed."},
			"Description":      {Doc: "Description says what the connector is for. Leave it out to keep it, or send null to clear it. An empty string fails with 422."},
			"Values":           {Doc: "Values replaces all of the connector's settings. A secret left out, sent as null, or sent as \"<REDACTED>\" keeps its stored value. A hidden field, such as hil, and the ssl_config of an MCP connector are kept when left out or sent as null, and are removed only by sending {}. Leave values out to keep every setting. Null fails with 422."},
			"ConnectorType":    {Doc: "ConnectorType is fixed when the connector is created. Including it fails with 422."},
			"ConnectorSubtype": {Doc: "ConnectorSubtype is fixed when the connector is created. Including it fails with 422."},
		},
	}
}

func (listConnectorsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listConnectorsRequest selects a page of connectors.",
		Fields: map[string]apidoc.Field{
			"Page":             {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"ConnectorType":    {Doc: "ConnectorType limits the list to one connector type. Leave it out to list every type."},
			"ConnectorSubtype": {Doc: "ConnectorSubtype limits the list to one subtype. Leave it out to list every subtype."},
			"Name":             {Doc: "Name limits the list to connectors whose name contains this text, ignoring case."},
			"Sort":             {Doc: "Sort orders the list by name or createdTime, as field:asc or field:desc; leave it out for newest first."},
		},
	}
}
