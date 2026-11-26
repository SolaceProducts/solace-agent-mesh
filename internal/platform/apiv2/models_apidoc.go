// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Model) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Model is a large language model (LLM) configuration that agents and workflows run on.",
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: "ID is the model's UUID."},
			"Type":      {Doc: "Type is always \"model\"."},
			"Alias":     {Doc: "Alias is the name agents and workflows refer to the model by. Unique across models."},
			"Provider":  {Doc: "Provider is the large language model (LLM) provider, such as openai or anthropic. Null until the model is configured."},
			"ModelName": {Doc: "ModelName is the provider's own name for the model. Null until the model is configured."},
			"APIBase": {Doc: `APIBase is the provider endpoint to call. Create and update store the provider's default when
none is given, so this is null only for a model seeded without one.`},
			"AuthType": {Doc: `AuthType is how Agent Mesh authenticates to the provider: none, apikey, oauth2, bearer, basic,
aws_iam, gcp_service_account, azure_workload_identity, or oauth2_delegated (an OAuth grant the
user gave by signing in from SAM Desktop, which authConfig names by grant_id). Set to none
when the model was created with neither authType nor authConfig. A model stored earlier with another type still reads
back, with every credential field sent as <REDACTED> because Agent Mesh cannot tell which are
secret.`},
			"AuthConfig": {Doc: "AuthConfig holds the credential fields the auth type defines. Secret values are always sent as <REDACTED>."},
			"ModelParams": {Doc: `ModelParams are the generation parameters forwarded to the provider, such as temperature.
Empty when none are set.`},
			"ModelType":   {Doc: "ModelType is the kind of model: chat or decision."},
			"Description": {Doc: "Description says what the model is for. Null when none was given."},
			"MaxInputTokens": {Doc: `MaxInputTokens caps the prompt sent to the model, in tokens. Null when the provider's own
limit applies.`},
			"Pricing": {Doc: `Pricing is the model's token rates, used for spend reporting. Null when the model has no
price or the caller does not hold model_config:*:read. The response does not say which.`},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the model: their email, or their user ID when no
email is known. Set to "system" for a model Agent Mesh seeded. Null when no author was recorded.`},
			"UpdatedBy":   {Doc: "UpdatedBy identifies the user who last changed the model, in the same form as createdBy."},
			"CreatedTime": {Doc: "CreatedTime is when the model was created."},
			"UpdatedTime": {Doc: "UpdatedTime is when the model was last changed."},
		},
	}
}

func (Pricing) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Pricing is a model's token rates, in US dollars per million tokens. Each rate
is a decimal string with up to 4 decimal places, not a JSON number, so the
stored figure is exactly the one typed.`,
		Fields: map[string]apidoc.Field{
			"Input": {Doc: `Input is the cost of a million input tokens, in US dollars. Set together with output, or not
at all.`},
			"Output": {Doc: `Output is the cost of a million output tokens, in US dollars. Set together with input, or not
at all.`},
			"CacheRead": {Doc: `CacheRead is the cost of a million cached input tokens, in US dollars. Null when cached input
bills at the input rate. Set together with cacheWrite, and only when input and output are set.`},
			"CacheWrite": {Doc: `CacheWrite is the cost of a million tokens written to the cache, in US dollars. Null when cache
writes bill at the input rate. Set together with cacheRead, and only when input and output are set.`},
		},
	}
}

func (ModelStatus) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ModelStatus says whether the models Agent Mesh itself runs on are configured.",
		Fields: map[string]apidoc.Field{
			"Configured": {Doc: `Configured is true when both the general and planning aliases hold a real configuration
rather than the placeholder Agent Mesh seeds a new installation with.`},
		},
	}
}

func (ModelDependent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ModelDependent is an agent or workflow that runs on a model.",
		Fields: map[string]apidoc.Field{
			"Type": {Doc: "Type is always \"modelDependent\"."},
			"Key": {Doc: `Key is the dependent agent's or workflow's UUID. The dependentType field says which of the two
it is.`},
			"Name":          {Doc: "Name is the dependent's name, as it is listed under agents or workflows."},
			"DependentType": {Doc: "DependentType is what runs on the model: agent or workflow."},
			"DeploymentStatus": {Doc: `DeploymentStatus is the dependent's deployment state: deployed, not_deployed, or deploy_failed.
A failed undeploy also reads as not_deployed, so this is the last recorded state, not proof
that nothing is running.`},
			"MayStillRun": {Doc: `MayStillRun is true when the dependent might still be running. False only when it was never
deployed or its latest deployment was a successful undeploy. A deploy in progress counts as
running. After a failed model delete, read this field rather than deploymentStatus, which
shows a failed undeploy as not_deployed.`},
		},
	}
}

func (CreateModelRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateModelRequest is the body that creates a model.",
		Fields: map[string]apidoc.Field{
			"Alias": {Doc: `Alias is the name agents refer to the model by. If another model already has it, the request
fails with 409.`},
			"Provider":  {Doc: "Provider is the large language model (LLM) provider, such as openai or anthropic."},
			"ModelName": {Doc: "ModelName is the provider's own name for the model."},
			"APIBase":   {Doc: "APIBase is the provider endpoint. Leave it out to use the provider's default."},
			"AuthType": {Doc: `AuthType is how Agent Mesh authenticates to the provider: none, apikey, oauth2, bearer, basic,
aws_iam, gcp_service_account, azure_workload_identity, or oauth2_delegated. Any other type
fails with 422, with the error on authConfig.type. Matching is exact, so APIKey fails. Optional, and read only when
authConfig carries no type.`},
			"AuthConfig": {Doc: `AuthConfig holds the credential fields the auth type defines. A value of <REDACTED> fails with
422, because reads send it in place of a stored secret.`},
			"ModelParams":    {Doc: "ModelParams are the generation parameters forwarded to the provider."},
			"ModelType":      {Doc: "ModelType is the kind of model: chat or decision. Defaults to chat."},
			"Description":    {Doc: "Description says what the model is for."},
			"MaxInputTokens": {Doc: "MaxInputTokens caps the prompt, in tokens. Leave it out to use the provider's own limit."},
			"Pricing":        {Doc: "Pricing is the model's token rates. Leave it out for an unpriced model."},
			"Surface": {Doc: `Surface is the X-Sam-Client-Surface header, which says what kind of client is calling: ui, cli,
or api. Any other value, or none, counts as api. It only feeds usage statistics.`},
		},
	}
}

func (UpdateModelRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateModelRequest changes only the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the model to update."},
			"Alias": {Doc: `Alias is the name agents refer to the model by. Leave it out to keep it. Null or an empty
string fails with 422.`},
			"Provider": {Doc: "Provider is the large language model (LLM) provider. Leave it out to keep it. Null or an empty string fails with 422."},
			"ModelName": {Doc: `ModelName is the provider's own name for the model. Leave it out to keep it. Null or an empty
string fails with 422.`},
			"APIBase": {Doc: `APIBase is the provider endpoint. Leave it out to keep it, or send an empty string to use the
provider's default. Null fails with 422.`},
			"AuthType": {Doc: `AuthType is how Agent Mesh authenticates to the provider. Leave it out to keep it. Null or an
empty string fails with 422. When authConfig carries a type, that type wins. Changing the type
drops the old type's stored credentials, so send the new type's credentials in the same
request. The supported types are none, apikey, oauth2, bearer, basic, aws_iam,
gcp_service_account, azure_workload_identity, and oauth2_delegated, matched exactly. Any other
type fails with 422, with the error on authConfig.type, unless it is the type the model already
holds.`},
			"AuthConfig": {Doc: `AuthConfig merges into the stored credentials. Leave it out to keep them. Null fails with 422.
A field sent as <REDACTED>, the value reads return for a secret, keeps its stored value. When the
request also changes provider, authConfig replaces the stored credentials instead and a secret
sent as <REDACTED> is dropped, so send the new provider's credentials in the same request. A type,
when sent and not null, must be a string naming a supported auth type. Anything else fails with
422, with the error on authConfig.type.`},
			"ModelParams": {Doc: "ModelParams replaces every generation parameter. Leave it out to keep them. Null fails with 422."},
			"Description": {Doc: "Description says what the model is for. Leave it out to keep it, or send null to clear it."},
			"MaxInputTokens": {Doc: `MaxInputTokens caps the prompt, in tokens. Leave it out to keep it, or send null to use the
provider's limit.`},
			"Pricing": {Doc: `Pricing replaces the model's token rates: an object of optional decimal strings in US dollars
per million tokens, named input, output, cacheRead, and cacheWrite. Any other key fails with
422. Leave it out to keep the stored price. Send null to clear the price. An object replaces
the price whole, so a rate it leaves out or sets to null is cleared. Send input and output
together, and the cache rates only alongside them.`},
			"ModelType": {Doc: "ModelType is fixed when the model is created. Including it fails with 422."},
			"Surface": {Doc: `Surface is the X-Sam-Client-Surface header, which says what kind of client is calling: ui, cli,
or api. Any other value, or none, counts as api. It only feeds usage statistics.`},
		},
	}
}

func (listModelDependentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listModelDependentsRequest selects a page of one model's dependents.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the UUID of the model whose dependents to list."},
			"Page": {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: `Sort orders the list by name, as name:asc or name:desc; leave it out for agents before
workflows, each by name.`},
		},
	}
}
