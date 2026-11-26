// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ModelTestConnectionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ModelTestConnectionRequest probes a model's provider, from values or from a
saved model.`,
		Fields: map[string]apidoc.Field{
			"ModelID": {Doc: `ModelID is the UUID of a saved model whose configuration is probed, with the other fields laid
over it. Send modelId, a complete configuration, or both. When provider names another provider
than the saved model's, the saved endpoint and credentials are not used: they belong to the
saved provider.`},
			"Provider":  {Doc: "Provider is the LLM provider to probe, such as openai. Required unless modelId is sent."},
			"ModelName": {Doc: "ModelName is the provider's own name for the model. Required unless modelId is sent."},
			"APIBase": {Doc: `APIBase is the provider endpoint to call. Leave it out to use the saved model's endpoint, or the
provider's default when there is no saved model or provider names another provider.`},
			"AuthType": {Doc: `AuthType is how Agent Mesh authenticates to the provider. Read only when authConfig carries no
type, because authConfig.type wins when both are sent.`},
			"AuthConfig": {Doc: `AuthConfig holds the credential fields the auth type defines. With modelId they override the
stored credentials, and a field sent as <REDACTED>, the value every read returns for a secret,
probes with the stored one. When provider names another provider, only the fields sent are used
and a secret sent as <REDACTED> is dropped, as saving the model does. The auth type sets the
keys, so the schema lists none.`},
			"ModelParams": {Doc: `ModelParams are the generation parameters to forward to the provider. The provider sets the
keys. With modelId they replace the stored parameters, as saving does; leave it out to probe
with the stored ones.`},
			"ModelType": {Doc: `ModelType is the kind of model: chat or decision. Defaults to chat. Taken from the stored model
when modelId is sent.`},
			"Surface": {Doc: `Surface is the X-Sam-Client-Surface header, which says what kind of client is calling: ui, cli,
or api. The probe's token spend is booked to it.`},
		},
	}
}

func (ModelTestConnectionResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ModelTestConnectionResult is the outcome of a probe that ran. A probe that
fails still returns this result rather than an error.`,
		Fields: map[string]apidoc.Field{
			"Success": {Doc: "Success says whether Agent Mesh reached the model and got an answer it could use."},
			"Message": {Doc: `Message says why the probe failed. Null when it passed. Agent Mesh writes it and never includes
the provider's own error text, which goes to the platform log. Each errorClass has one fixed
sentence, except that config_incomplete carries the validator's reason when the configuration
failed validation, such as Azure workload identity not being enabled on this deployment.`},
			"ErrorClass": {Doc: `ErrorClass names the kind of failure. Null when the probe passed. One of
auth_credentials_invalid (the provider rejected the credentials), auth_permission_denied (the
credentials are real, but the account cannot use the model with them, for want of either a
permission or credit), model_not_found (the endpoint serves no model by that name),
request_rejected (the provider refused the request Agent Mesh sent), rate_limited, timeout,
network_unreachable (the endpoint could not be reached), provider_error (the provider reported
a fault of its own), config_incomplete (Agent Mesh could not build a complete request or a
provider client from the configuration, or the configuration failed validation, in which case
message says what failed), no_response (the model answered with neither content nor a tool
call), invalid_response (a decision model answered, but not in the form it was asked for), or
unknown.

A decision model and a chat model get the same class for the same upstream HTTP status. Three
cases keep the class of the decision model's kind of failure instead: a failure that carries no
HTTP status, such as an unreachable endpoint or an unusable configuration; a status the chat
classification names no class for, such as 409 or 413; and an OpenAI 429 that reports the
account out of credit, which reads as auth_permission_denied rather than rate_limited.`},
		},
	}
}

func (SupportedModel) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SupportedModel is a model a provider reports it serves.",
		Fields: map[string]apidoc.Field{
			"Type":     {Doc: "Type is always \"supportedModel\"."},
			"Key":      {Doc: "Key is the provider's own name for the model, which is what a model's modelName takes."},
			"Label":    {Doc: "Label is the name to show for the model."},
			"Provider": {Doc: "Provider is the provider that serves the model."},
		},
	}
}

func (ProviderParam) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ProviderParam is a generation parameter a provider's models accept.",
		Fields: map[string]apidoc.Field{
			"Type": {Doc: "Type is always \"providerParam\"."},
			"Key":  {Doc: "Key is the parameter's name, as it is written in a model's modelParams."},
		},
	}
}

func (SupportedModelsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SupportedModelsRequest asks a provider which models it serves, with the
credentials sent.`,
		Fields: map[string]apidoc.Field{
			"Provider": {Doc: "Provider is the LLM provider to ask, such as openai or anthropic."},
			"ModelID": {Doc: `ModelID is the UUID of a saved model whose provider endpoint and model type narrow the list.
Leave it out when listing for a model that is not saved yet.`},
			"APIBase": {Doc: `APIBase is the provider endpoint to ask. Leave it out to use the provider's default, or the
saved model's when modelId is sent.`},
			"AuthConfig": {Doc: `AuthConfig holds the credential fields the auth type defines, with the auth type in its type
field. The provider is asked with exactly these credentials, never the stored ones, even when
modelId is sent. A field sent as <REDACTED> goes to the provider as that text, and the provider
refuses it. To list with a saved model's credentials, use PUT /providers/{provider}/models. The
auth type sets the keys, so the schema lists none.`},
			"ModelType": {Doc: `ModelType is the kind of model, chat or decision, and narrows the list for a provider that
serves both. Taken from the saved model when modelId is sent.`},
			"Page": {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}

func (ProviderModelsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ProviderModelsRequest asks one provider which models it serves, either with
the credentials of a saved model or with credentials sent in the request.`,
		Fields: map[string]apidoc.Field{
			"Provider": {Doc: "Provider is the LLM provider to ask, such as openai or anthropic."},
			"ModelID": {Doc: `ModelID is the UUID of a saved model whose stored credentials are used, with authConfig laid
over them. Send modelId, or an authConfig with a type.`},
			"APIBase": {Doc: `APIBase is the provider endpoint to ask. Leave it out to use the provider's default, or the
saved model's when modelId is sent.`},
			"AuthConfig": {Doc: `AuthConfig holds the credential fields the auth type defines, with the auth type in its type
field. With modelId they override the stored credentials. A field sent as <REDACTED>, the value
every read returns for a secret, uses the stored one. The auth type sets the keys, so the schema
lists none.`},
			"ModelType": {Doc: `ModelType is the kind of model, chat or decision, and narrows the list for a provider that
serves both. Taken from the saved model when modelId is sent.`},
			"Page": {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}

func (ProviderParamsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ProviderParamsRequest asks which generation parameters a provider's models accept.",
		Fields: map[string]apidoc.Field{
			"Provider": {Doc: "Provider is the LLM provider to ask, such as openai or anthropic."},
			"Page":     {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}

func (ModelSecretsDiffRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ModelSecretsDiffRequest carries the credential values to compare with the
model's stored secrets.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the model whose stored secrets are compared."},
			"AuthConfig": {Doc: `AuthConfig holds the credential fields to compare. Only secret fields are compared. The model's
auth type sets the keys.`},
		},
	}
}

func (ModelSecretsDiffResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ModelSecretsDiffResult says, for each secret credential field, whether saving
the values sent would keep the stored secret.`,
		Fields: map[string]apidoc.Field{
			"Secrets": {Doc: `Secrets has one entry for each secret credential field of the model's auth type, keyed by
field name. Empty when Agent Mesh does not know the model's auth type.`},
		},
	}
}

func (AzureWorkloadIdentity) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `AzureWorkloadIdentity is who this deployment signs in to Azure as: the values
a federated credential on your Entra identity must match.`,
		Fields: map[string]apidoc.Field{
			"Type": {Doc: "Type is always \"azureWorkloadIdentity\"."},
			"Issuer": {Doc: `Issuer is the OIDC issuer URL of this deployment's cluster. Enter it as the issuer of the
federated credential. It is not a secret.`},
			"Namespace": {Doc: `Namespace is the Kubernetes namespace Agent Mesh runs in. The federated credential's subject
must name it.`},
			"ServiceAccount": {Doc: `ServiceAccount is the Kubernetes service account Agent Mesh runs as. The federated
credential's subject must name it.`},
			"Audiences": {Doc: "Audiences are the token audiences Agent Mesh requests. The federated credential must list them."},
		},
	}
}
