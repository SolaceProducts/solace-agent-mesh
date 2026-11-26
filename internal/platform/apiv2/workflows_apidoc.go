// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Workflow) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Workflow is a stored workflow definition. Its appConfig is the workflow's own
app configuration, which the caller writes and Agent Mesh stores as it is.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the workflow's UUID."},
			"Type":        {Doc: "Type is always \"workflow\"."},
			"Name":        {Doc: "Name is the workflow's unique name."},
			"Description": {Doc: "Description says what the workflow does."},
			"AppConfig": {Doc: `AppConfig is the workflow's app configuration, returned as it was last written. Its structure is
up to the caller.`},
			"DeploymentStatus": {Doc: `DeploymentStatus is the workflow's deployment state: deployed when the latest deploy or update
succeeded, deploy_failed when it failed, and not_deployed when the workflow was never deployed
or was last undeployed.`},
			"RuntimeStatus": {Doc: "RuntimeStatus is the deployed workflow's health as last reported."},
			"CreatedBy":     {Doc: "CreatedBy identifies the user who created the workflow: their email, or their user ID when no email is known."},
			"UpdatedBy":     {Doc: "UpdatedBy identifies the user who last changed the workflow, in the same form as createdBy."},
			"CreatedTime":   {Doc: "CreatedTime is when the workflow was created."},
			"UpdatedTime":   {Doc: "UpdatedTime is when the workflow was last changed."},
		},
	}
}

func (WorkflowConfiguration) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WorkflowConfiguration is the YAML a workflow deploys as.",
		Fields: map[string]apidoc.Field{
			"ConfigurationFile": {Doc: "ConfigurationFile is the workflow's app configuration as YAML, with connector references left unresolved."},
		},
	}
}

func (CreateWorkflowRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateWorkflowRequest creates a workflow. If the name is taken, the request fails with 409.",
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the workflow's unique name, 3 to 255 characters."},
			"Description": {Doc: "Description says what the workflow does, 10 to 2,000 characters."},
			"AppConfig":   {Doc: "AppConfig is the workflow's app configuration, stored as sent. Its structure is up to the caller."},
		},
	}
}

func (UpdateWorkflowRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateWorkflowRequest replaces a workflow's name, description, and app configuration.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the UUID of the workflow to replace."},
			"Name":        {Doc: "Name is the workflow's new name, 3 to 255 characters."},
			"Description": {Doc: "Description is the workflow's new description, 10 to 2,000 characters."},
			"AppConfig": {Doc: `AppConfig replaces the workflow's app configuration, stored as sent. Its structure is up to the
caller.`},
		},
	}
}
