A model captures a provider-specific LLM endpoint plus credentials.
The alias, the platform-side identity agents reference, is set as the
top-level `name:`; use kebab-case for portability. Don't write `alias`
under `spec:` — apply overwrites it with `name`. `authConfig` is a
free-form map whose required keys depend on `provider`; secret-shaped
keys (`apiKey`, `token`, `password`) are redacted in plan output and
rewritten as `${VAR}` placeholders by `sam config pull`.

`modelType` is `chat` (the default, and every agent's model) or `decision`
*(Early Access)*. A decision model is a structured model that workflow
`decision` nodes reference with `decision: {model_alias: <alias>}` and
`decision` evaluators reference with their `model` field. Its
provider is `typesafe`, `openai` or `google_ai_studio`. An `openai` decision
model uses `modelName: gpt-6-luna`, and a `google_ai_studio` one (Google's
Gemini Decisions API) uses `modelName: gemini-decision-1`. Every provider takes
`authConfig: {type: apikey, api_key: ...}`. State `apiBase` explicitly:
the platform fills in an omitted one with the provider's default, which may
not be the endpoint you want. For TypeSafe, use `https://api.typesafe.ai`, or
`https://openrouter.ai/api` with `modelName: typesafe/jev-1.13`. Through
OpenRouter, `modelName: openai/gpt-6-luna-decisions` also works, and it
reads images. Jev reads images on either `apiBase`; every other TypeSafe
model reads only text.
For OpenAI,
use `https://api.openai.com/v1`; for Google AI Studio,
`https://generativelanguage.googleapis.com/v1beta`. OpenAI and Gemini decision
models read images too. The
`api_key` is required: `sam config plan` fails on a new decision model
without one, and on an update that switches its `authConfig` to another
type; an update that omits the key keeps the stored one. `modelType` cannot
change after creation, so `sam config plan` fails on a file that changes
it, and on a `provider: typesafe` model that omits `modelType: decision`. A
`provider: openai` or `provider: google_ai_studio` model that omits it is a
chat model. A decision model's `provider` cannot change after creation
either, so define a new model to move to another provider.
An agent cannot use a decision model. A model that a workflow's
`decision.model_alias` names cannot be renamed while any workflow references
it, whether in its saved version or in the version still deployed; deleting
it from the platform undeploys the deployed workflows that name it.

A chat model's `modelName` may start with a model prefix that selects the API
it is called with, such as `anthropic/claude-sonnet-4-6`; the prefix is any
provider name the runtime routes by (`openai`, `anthropic`, `gemini`,
`vertex_ai`, `bedrock`, `azure`, `ollama`, `groq` and others). Without one,
`azure_openai`, `google_ai_studio`, `vertex_ai`, `bedrock` and `ollama` add
their own (`azure/`, `gemini/`, `vertex_ai/`, `bedrock/`, `ollama/`) to a name
with no `/`. Any other name is routed by its text: one containing `claude` or
`anthropic` uses the Anthropic Messages API, one containing `gemini` the Gemini
API, and the rest the OpenAI Chat Completions API. So under
`provider: custom`, a Claude model behind an OpenAI-compatible gateway needs
`openai/` (`openai/claude-sonnet-4-6`), and an endpoint that serves the
Anthropic Messages API takes `anthropic/` (`<apiBase>/v1/messages`, one
trailing `/v1` dropped first, `x-api-key` header). The web UI saves every
`custom` model with its prefix and starts a new one on `openai/`. Outside
`provider: custom`, use no prefix but the provider's own: another provider's
prefix sends that provider's credentials to the wrong API. An identifier whose
first segment is a provider name, such as `openai/gpt-oss-120b` on a vLLM
server, reads as that prefix, so write `openai/openai/gpt-oss-120b`. A
namespace that is not a provider name, such as `meta-llama/Llama-3-70b`, is
part of the identifier and gets no provider default, so write the provider's
prefix in front of it. Decision models take no prefix.

For a chat model with `provider: custom` (any OpenAI-compatible endpoint) or
`provider: openai`, `apiBase` takes one of two forms. A base URL, such as
`https://llm.example.com`: Agent Mesh appends `/v1/chat/completions`, dropping
one trailing `/v1` from the base first, and lists models at `<base>/v1/models`.
Or the provider's complete chat-completions URL, a URL whose path ends in
`/chat/completions` (a trailing `/` is fine and a query string is kept): Agent
Mesh calls it exactly as written and lists models at the `/models` URL beside
it. Use the complete form for a provider that does not serve its API under
`/v1`, such as `apiBase: https://ark.cn-beijing.volces.com/api/v3/chat/completions`
for Volcengine Ark. Other providers and decision models always treat `apiBase`
as a base URL. A `modelName` that selects another API does too: one with a
recognized prefix other than `openai/`, such as `anthropic/` or `groq/`, or one
without a recognized prefix that contains `claude`, `anthropic` or `gemini`.
Models that accept a request only
through the OpenAI Responses API do not work with the complete form. These
include the OpenAI `-pro` models, and some GPT-5 models when the request
includes tools.
Earlier Agent Mesh releases append `/v1/chat/completions` to a complete URL, so
upgrade every Agent Mesh component before you use the complete form.

`sam config apply --prune` deletes models after it applies agents and
workflows (and, unless `--no-deploy`, after it redeploys them), so
re-pointing an agent or a workflow at a new model and dropping the old one
converges in one apply. The apply never undeploys a workflow to delete a
model: a model that any workflow on the platform still names (saved, or
still deployed on it under `--no-deploy`) is kept, and its delete is
reported as failed with the workflow names. A later apply skips a workflow
whose file is unchanged, so redeploy it on another model before applying
again. The apply fails
before any change when an agent's `modelProvider`, a workflow's
`decision.model_alias` or an evaluator's `model` in the manifest still names
a model it would delete; `sam config plan` shows this as a warning.

**Pricing.** `spec.pricing` sets what the model's tokens cost, in US dollars
per 1,000,000 tokens, for the spend and budgets Agent Mesh records. Write each
rate as a quoted decimal string with at most four decimal places; a bare YAML
number fails the plan. Set
`input` and `output` together: if either is set, both must be. `cacheRead` and
`cacheWrite` are optional: set both or neither, and only with `input` and
`output`. Without them, cached tokens bill at the input rate.

```yaml
spec:
  # provider, modelName, authConfig, ...
  pricing:
    input: "3"
    output: "15"
    cacheRead: "0.30"
    cacheWrite: "3.75"
```

The price is replaced as a whole, so a rate left out of `pricing` is cleared.
Omitting `pricing` leaves the price to the UI and the plan never compares it;
`pricing: {}` clears it.

### Azure OpenAI without a stored key (`azure_workload_identity`)

Use `authConfig.type: azure_workload_identity` (UI label "Azure Workload
Identity") when Agent Mesh runs on Kubernetes and should reach Azure OpenAI
without an API key or client secret. Agent Mesh signs in as the Microsoft
Entra identity named on the model (a managed identity or app registration)
by exchanging its pods' Kubernetes service account token; the model stores
no secret.

```yaml
kind: model
name: azure-gpt
spec:
  provider: azure_openai
  modelName: "<deployment-name>"
  apiBase: "https://<resource>.openai.azure.com/"
  authConfig:
    type: azure_workload_identity
    client_id: "<identity-client-id>"   # GUID
    tenant_id: "<entra-tenant-id>"      # GUID
```

- `client_id` and `tenant_id` are required GUIDs. Neither is a secret.
  Models can name different identities.
- The identity needs a federated credential that trusts the cluster's
  issuer and `system:serviceaccount:<namespace>:<service-account>` with
  audience `api://AzureADTokenExchange`, and a role on the resource:
  "Cognitive Services OpenAI User" for Azure OpenAI models, or "Cognitive
  Services User" for other Microsoft Foundry models.
- The deployment must allow it: the Agent Mesh pods need
  `SAM_AZURE_WORKLOAD_IDENTITY_ENABLED=true` and a projected service account
  token with that audience, with `AZURE_FEDERATED_TOKEN_FILE` pointing at
  it. Without both, the Models page hides this type and the API rejects it.
- `provider` must be `azure_openai`. The Azure endpoint is `apiBase`, or
  `modelParams.endpoint` when set, since it overrides `apiBase`; it receives
  the Microsoft Entra access token. Give the resource address itself,
  without a path such as `/anthropic`: Agent Mesh adds the route.
- Agent Mesh signs in for `https://ai.azure.com/.default` on a
  `*.services.ai.azure.com` endpoint, and for
  `https://cognitiveservices.azure.com/.default` on the others. Claude on
  Microsoft Foundry accepts only the first, so use the
  `*.services.ai.azure.com` address for it.
- `api_key`, `client_secret`, `token_url` and OAuth settings are rejected
  for this type, and so are `client_id`/`tenant_id`/`api_key` in
  `modelParams`, the `_file` form of any of these, and
  `modelParams.azure_chat_route: deployment`.
- Changing `authConfig.type` to or from `azure_workload_identity` replaces
  the stored `authConfig`; nothing from the previous type carries over.

### Models that use a sign-in (`oauth2_delegated`)

A model whose `authConfig.type` is `oauth2_delegated` calls the provider with a sign-in
a person made in the desktop app; its `authConfig` holds only the
`grant_id` that names the sign-in. Config cannot make a sign-in, so:

- `sam config pull` leaves `oauth2_delegated` models out.
- `sam config plan` and `sam config apply` fail on a file that declares
  `authConfig.type: oauth2_delegated`.
- Apply never deletes a `oauth2_delegated` model that the files leave out.
- A file that declares the same name with another `authConfig.type` updates
  the model to that type, which replaces its stored `authConfig`.
