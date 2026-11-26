// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ModelType, REDACTED_SENTINEL } from "@/api/models/types";

import { isHttpUrl } from "@/utils/url";

import { type PricingFormValues, readPricingFromForm } from "./modelPricing";

export const AUTH_CONFIG_TO_FORM_FIELD_MAP: Record<string, string> = {
    api_key: "apiKey",
    client_id: "clientId",
    client_secret: "clientSecret",
    tenant_id: "tenantId",
    aws_access_key_id: "awsAccessKeyId",
    aws_secret_access_key: "awsSecretAccessKey",
    aws_session_token: "awsSessionToken",
    vertex_credentials: "vertexCredentials",
    token_url: "tokenUrl",
    audience: "oauthAudience",
    ca_cert: "oauthCaCert",
    max_retries: "oauthMaxRetries",
    aws_region_name: "awsRegionName",
    vertex_project: "vertexProject",
    vertex_location: "vertexLocation",
    api_version: "apiVersion",
};

export interface ModelProvider {
    id: string;
    label: string;
}

export type FieldType = "text" | "password" | "number" | "textarea" | "select";
export type AuthType = "apikey" | "oauth2" | "none" | "aws_iam" | "gcp_service_account" | "azure_workload_identity" | "oauth2_delegated";

export const AUTH_TYPE_LABELS: Record<AuthType, string> = {
    apikey: "API Key",
    oauth2: "OAuth2",
    none: "None",
    aws_iam: "AWS IAM",
    gcp_service_account: "GCP Service Account",
    azure_workload_identity: "Azure Workload Identity",
    oauth2_delegated: "Sign-in",
};

export const AUTH_TYPE_DESCRIPTIONS: Record<AuthType, string> = {
    apikey: "Authenticate with a static API key.",
    oauth2: "Authenticate using the OAuth 2.0 client credentials flow.",
    none: "No authentication.",
    aws_iam: "Authenticate with AWS IAM credentials.",
    gcp_service_account: "Authenticate with a GCP service account key.",
    azure_workload_identity: "Sign in as an Azure managed identity or app registration that trusts the Agent Mesh Kubernetes service account. No key is stored.",
    oauth2_delegated: "Calls the provider with a sign-in made in the desktop app. No key is stored.",
};

export interface ProviderField {
    name: string;
    label: string;
    type: FieldType;
    required: boolean;
    placeholder?: string;
    helpText?: string;
    storageTarget: "model_params" | "auth";
    min?: number;
    max?: number;
    step?: number;
    options?: Array<{ value: string; label: string }>;
    pattern?: { value: RegExp; message: string };
}

const GUID_PATTERN = {
    value: /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/,
    message: "Invalid format. Enter a value using the format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
};

export interface SupportedModel {
    id: string;
    label: string;
}

export interface ModelFormData {
    alias: string;
    description: string;
    provider: string;
    modelName: string;
    modelPrefix?: string;
    apiBase?: string;
    authType: string;
    apiKey?: string;
    clientId?: string;
    clientSecret?: string;
    tenantId?: string;
    tokenUrl?: string;
    oauthScope?: string;
    oauthAudience?: string;
    oauthCaCert?: string;
    oauthTokenRefreshBufferSeconds?: string;
    oauthMaxRetries?: string;
    awsAccessKeyId?: string;
    awsSecretAccessKey?: string;
    awsSessionToken?: string;
    awsRegionName?: string;
    vertexCredentials?: string;
    vertexProject?: string;
    vertexLocation?: string;
    customParams?: Array<{ key: string; value: string }>;
    customHeaders?: Array<{ key: string; value: string }>;
    maxInputTokens?: string;
    pricing?: PricingFormValues;
    modelType?: ModelType;
    [key: string]: unknown;
}

export interface ProviderConfig {
    id: string;
    label: string;
    showApiBase: boolean;
    apiBaseRequired?: boolean;
    description?: string;
    fields: ProviderField[];
    modelNamePlaceholder: string;
    hideCommonParams?: boolean;
    allowedAuthTypes: AuthType[];
    apiBasePlaceholder?: string;
    apiBaseHelpText?: string;
    modelTypes?: ModelType[];
    typeOverrides?: Partial<Record<ModelType, ProviderTypeOverride>>;
    modelType?: ModelType;
    capabilityKey?: string;
}

type ProviderTypeOverride = Partial<Omit<ProviderConfig, "id" | "label" | "modelTypes" | "typeOverrides" | "modelType">>;

const AZURE_OPENAI_FIELDS: ProviderField[] = [
    {
        name: "apiVersion",
        label: "API Version",
        type: "text",
        required: false,
        storageTarget: "auth",
    },
];

export const AUTH_FIELDS: Record<AuthType, ProviderField[]> = {
    apikey: [
        {
            name: "apiKey",
            label: "API Key",
            type: "password",
            required: true,
            storageTarget: "auth",
        },
    ],
    oauth2: [
        {
            name: "tokenUrl",
            label: "Token URL",
            type: "text",
            required: true,
            storageTarget: "auth",
        },
        {
            name: "clientId",
            label: "Client ID",
            type: "text",
            required: true,
            storageTarget: "auth",
        },
        {
            name: "clientSecret",
            label: "Client Secret",
            type: "password",
            required: true,
            storageTarget: "auth",
        },
        {
            name: "oauthScope",
            label: "OAuth Scope",
            type: "text",
            required: false,
            storageTarget: "auth",
        },
        {
            name: "oauthAudience",
            label: "OAuth Audience",
            type: "text",
            required: false,
            storageTarget: "auth",
        },
        {
            name: "oauthCaCert",
            label: "CA Certificate Path",
            type: "text",
            required: false,
            storageTarget: "auth",
        },
        {
            name: "oauthTokenRefreshBufferSeconds",
            label: "Token Refresh Buffer (seconds)",
            type: "number",
            required: false,
            storageTarget: "auth",
            min: 0,
        },
        {
            name: "oauthMaxRetries",
            label: "Max Retries",
            type: "number",
            required: false,
            storageTarget: "auth",
            min: 0,
        },
    ],
    none: [],
    azure_workload_identity: [
        {
            name: "clientId",
            label: "Microsoft Client ID",
            type: "text",
            required: true,
            helpText: "The Client ID of the user-assigned managed identity, or the Application (client) ID of the app registration.",
            storageTarget: "auth",
            pattern: GUID_PATTERN,
        },
        {
            name: "tenantId",
            label: "Microsoft Tenant ID",
            type: "text",
            required: true,
            helpText: "The Tenant ID of the user-assigned managed identity, or the Directory (tenant) ID of the app registration.",
            storageTarget: "auth",
            pattern: GUID_PATTERN,
        },
    ],
    oauth2_delegated: [],
    aws_iam: [
        {
            name: "awsAccessKeyId",
            label: "AWS Access Key ID",
            type: "text",
            required: true,
            storageTarget: "auth",
        },
        {
            name: "awsSecretAccessKey",
            label: "AWS Secret Access Key",
            type: "password",
            required: true,
            storageTarget: "auth",
        },
        {
            name: "awsSessionToken",
            label: "AWS Session Token",
            type: "password",
            required: false,
            storageTarget: "auth",
        },
        {
            name: "awsRegionName",
            label: "AWS Region",
            type: "text",
            required: true,
            storageTarget: "auth",
        },
    ],
    gcp_service_account: [
        {
            name: "vertexCredentials",
            label: "Service Account JSON",
            type: "textarea",
            required: true,
            helpText: "Paste the full JSON key file contents",
            storageTarget: "auth",
        },
        {
            name: "vertexProject",
            label: "GCP Project ID",
            type: "text",
            required: true,
            storageTarget: "auth",
        },
        {
            name: "vertexLocation",
            label: "GCP Region",
            type: "text",
            required: true,
            storageTarget: "auth",
        },
    ],
};

export const COMMON_MODEL_PARAMS: ProviderField[] = [
    {
        name: "max_tokens",
        label: "Max Tokens",
        type: "number",
        required: false,
        helpText: "The maximum number of tokens to generate in the response. Sets an upper limit on output length.",
        storageTarget: "model_params",
        min: 1,
    },
    {
        name: "cache_strategy",
        label: "Prompt Caching Strategy",
        type: "select",
        required: false,
        helpText: "Controls how the model caches prompts for cost optimization",
        storageTarget: "model_params",
        options: [
            { value: "5m", label: "5 minutes (default)" },
            { value: "1h", label: "1 hour" },
            { value: "none", label: "Disabled" },
        ],
    },
];

export const DECISION_MODELS_CAPABILITY = "workflow_decision_node";

const PROVIDER_CONFIGS: Record<string, ProviderConfig> = {
    openai: {
        id: "openai",
        label: "OpenAI",
        showApiBase: false,
        apiBaseRequired: false,
        fields: [],
        modelNamePlaceholder: "gpt-4o-mini",
        allowedAuthTypes: ["apikey"],
        modelTypes: ["chat", "decision"],
        typeOverrides: {
            decision: {
                description: "OpenAI's Decisions API, for workflow decision nodes and decision evaluators. Agents cannot use these models.",
                showApiBase: true,
                apiBasePlaceholder: "https://api.openai.com/v1",
                apiBaseHelpText: "Leave blank for OpenAI's API, or enter another base that serves /decisions, such as a regional OpenAI endpoint.",
                modelNamePlaceholder: "gpt-6-luna",
                hideCommonParams: true,
                capabilityKey: DECISION_MODELS_CAPABILITY,
            },
        },
    },
    anthropic: {
        id: "anthropic",
        label: "Anthropic",
        showApiBase: false,
        apiBaseRequired: false,
        fields: [],
        modelNamePlaceholder: "claude-3-5-sonnet-20241022",
        allowedAuthTypes: ["apikey"],
    },
    azure_openai: {
        id: "azure_openai",
        label: "Microsoft Foundry",
        showApiBase: true,
        apiBaseRequired: true,
        fields: AZURE_OPENAI_FIELDS,
        modelNamePlaceholder: "azure/<deployment-name>",
        allowedAuthTypes: ["apikey", "oauth2", "azure_workload_identity"],
    },
    google_ai_studio: {
        id: "google_ai_studio",
        label: "Google AI Studio",
        showApiBase: false,
        apiBaseRequired: false,
        fields: [],
        modelNamePlaceholder: "gemini/gemini-1.5-pro",
        allowedAuthTypes: ["apikey"],
        modelTypes: ["chat", "decision"],
        typeOverrides: {
            decision: {
                description: "Google's Gemini Decisions API, for workflow decision nodes and decision evaluators. Agents cannot use these models.",
                showApiBase: true,
                apiBasePlaceholder: "https://generativelanguage.googleapis.com/v1beta",
                apiBaseHelpText: "Leave blank for Google's Gemini API, or enter another base that serves /v1beta/decisions.",
                modelNamePlaceholder: "gemini-decision-1",
                hideCommonParams: true,
                capabilityKey: DECISION_MODELS_CAPABILITY,
            },
        },
    },
    vertex_ai: {
        id: "vertex_ai",
        label: "Google Vertex AI",
        showApiBase: false,
        apiBaseRequired: false,
        fields: [],
        modelNamePlaceholder: "vertex_ai/gemini-1.5-pro",
        allowedAuthTypes: ["gcp_service_account"],
    },
    bedrock: {
        id: "bedrock",
        label: "Amazon Bedrock",
        showApiBase: false,
        apiBaseRequired: false,
        fields: [],
        modelNamePlaceholder: "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
        allowedAuthTypes: ["aws_iam"],
    },
    ollama: {
        id: "ollama",
        label: "Ollama",
        showApiBase: true,
        apiBaseRequired: true,
        fields: [],
        modelNamePlaceholder: "ollama/llama2",
        allowedAuthTypes: ["apikey", "none"],
    },
    custom: {
        id: "custom",
        label: "Custom",
        description: "Configure a provider that implements the OpenAI-compatible API protocol",
        showApiBase: true,
        apiBaseRequired: true,
        apiBasePlaceholder: "https://api.example.com",
        apiBaseHelpText: "Enter the provider's base URL and Agent Mesh adds /v1/chat/completions. If the provider does not use /v1, enter the full chat completions URL instead, ending in /chat/completions.",
        fields: [],
        modelNamePlaceholder: "my-model-id",
        allowedAuthTypes: ["apikey", "oauth2", "none"],
    },
    typesafe: {
        id: "typesafe",
        label: "TypeSafe",
        description: "Decision models for workflow decision nodes and decision evaluators. Agents cannot use them.",
        showApiBase: true,
        apiBaseRequired: false,
        apiBasePlaceholder: "https://api.typesafe.ai",
        apiBaseHelpText: "Leave blank for TypeSafe's API, or enter a pass-through that serves /v1/systemone, such as https://openrouter.ai/api.",
        fields: [],
        modelNamePlaceholder: "jev-1.13.0",
        hideCommonParams: true,
        allowedAuthTypes: ["apikey"],
        modelTypes: ["decision"],
        capabilityKey: DECISION_MODELS_CAPABILITY,
    },
};

function servedModelTypes(config: ProviderConfig): ModelType[] {
    return config.modelTypes ?? ["chat"];
}

export function getProviderConfig(providerId: string | null | undefined, modelType?: ModelType): ProviderConfig {
    const base = (providerId && PROVIDER_CONFIGS[providerId]) || PROVIDER_CONFIGS.custom;
    const types = servedModelTypes(base);
    const resolved = modelType && types.includes(modelType) ? modelType : types[0];
    return { ...base, ...base.typeOverrides?.[resolved], modelType: resolved };
}

const OPAQUE_ORIGIN = "null";
const CHAT_COMPLETIONS_SUFFIX = "/chat/completions";
const OPENAI_COMPATIBLE_PROVIDERS = new Set(["openai", "custom"]);

function trimTrailingSlashes(value: string): string {
    let end = value.length;
    while (end > 0 && value[end - 1] === "/") end--;
    return value.slice(0, end);
}

function fullChatCompletionsEndpoint(apiBase: string): string | undefined {
    let parsed: URL;
    try {
        parsed = new URL(apiBase);
    } catch {
        return undefined;
    }
    if (parsed.origin === OPAQUE_ORIGIN) return undefined;
    const path = trimTrailingSlashes(parsed.pathname);
    return path.endsWith(CHAT_COMPLETIONS_SUFFIX) ? `${parsed.origin}${path}${parsed.search}` : undefined;
}

function baseWithoutTrailingV1(apiBase: string): string {
    const base = trimTrailingSlashes(apiBase);
    return trimTrailingSlashes(base.endsWith("/v1") ? base.slice(0, -"/v1".length) : apiBase);
}

export function resolveChatRequestUrl(provider: string | null | undefined, apiBase: string | null | undefined, modelPrefix?: string, modelName?: string): string | undefined {
    const trimmed = apiBase?.trim();
    if (!provider || !OPENAI_COMPATIBLE_PROVIDERS.has(provider) || !trimmed) return undefined;
    const api = hasModelPrefixField(provider) ? customModelApi(modelPrefix, modelName) : OPENAI_MODEL_PREFIX;
    if (api === ANTHROPIC_MODEL_PREFIX) return `${baseWithoutTrailingV1(trimmed)}/v1/messages`;
    if (api !== OPENAI_MODEL_PREFIX) return undefined;
    return fullChatCompletionsEndpoint(trimmed) ?? `${baseWithoutTrailingV1(trimmed)}/v1/chat/completions`;
}

const ANTHROPIC_API_BASE_HELP_TEXT = "Enter the provider's base URL and Agent Mesh adds /v1/messages.";

export function resolveApiBaseHelpText(configuredHelpText: string | undefined, provider: string | null | undefined, modelPrefix?: string, modelName?: string): string | undefined {
    const callsAnthropicApi = hasModelPrefixField(provider) && customModelApi(modelPrefix, modelName) === ANTHROPIC_MODEL_PREFIX;
    return callsAnthropicApi ? ANTHROPIC_API_BASE_HELP_TEXT : configuredHelpText;
}

export const CONNECTION_FIELD_NAMES: readonly string[] = (() => {
    const names = new Set<string>(["apiBase"]);
    for (const fields of Object.values(AUTH_FIELDS)) {
        for (const f of fields) names.add(f.name);
    }
    for (const cfg of Object.values(PROVIDER_CONFIGS)) {
        for (const f of cfg.fields) {
            if (f.storageTarget === "auth") names.add(f.name);
        }
    }
    return Array.from(names);
})();

export const MODEL_FORM_FIELD_NAMES: readonly string[] = (() => {
    const names = new Set<string>(["alias", "authType", "customHeaders", "customParams", "description", "maxInputTokens", "modelName", "modelPrefix", "provider"]);
    for (const name of CONNECTION_FIELD_NAMES) names.add(name);
    for (const field of COMMON_MODEL_PARAMS) names.add(field.name);
    for (const cfg of Object.values(PROVIDER_CONFIGS)) {
        for (const field of cfg.fields) names.add(field.name);
    }
    return Array.from(names).sort((a, b) => a.localeCompare(b));
})();

export const AZURE_PROVIDER_ID = "azure_openai";

export function modelNameFieldLabel(providerId: string | null | undefined, fallback: string): string {
    return providerId === AZURE_PROVIDER_ID ? "Deployment Name" : fallback;
}

export function modelNameNoun(providerId: string | null | undefined): string {
    return providerId === AZURE_PROVIDER_ID ? "deployment" : "model";
}

export const CUSTOM_PROVIDER_ID = "custom";
const OPENAI_MODEL_PREFIX = "openai";
const ANTHROPIC_MODEL_PREFIX = "anthropic";

const RUNTIME_MODEL_PREFIXES: ReadonlySet<string> = new Set([
    "openai",
    "anthropic",
    "gemini",
    "google",
    "vertex_ai",
    "vertex",
    "bedrock",
    "azure",
    "ollama",
    "cerebras",
    "cohere",
    "groq",
    "huggingface",
    "mistral",
    "nebius",
    "openrouter",
    "perplexity",
    "replicate",
    "xai",
]);

function isRuntimeModelPrefix(prefix: string): boolean {
    return RUNTIME_MODEL_PREFIXES.has(prefix.toLowerCase());
}

export function normalizeModelPrefix(value: string): string {
    return trimTrailingSlashes(value.trim());
}

export function modelPrefixFieldValue(prefix: string | undefined): string {
    const normalized = normalizeModelPrefix(prefix ?? "");
    return normalized ? `${normalized}/` : "";
}

const MODEL_PREFIX_FORMAT = /^[^\s/]+$/;

export function customModelPrefixError(value: string | undefined): string | undefined {
    const prefix = normalizeModelPrefix(value ?? "");
    if (!prefix || MODEL_PREFIX_FORMAT.test(prefix)) return undefined;
    return "Enter one word followed by /, such as openai/.";
}

export const DEFAULT_CUSTOM_MODEL_PREFIX = OPENAI_MODEL_PREFIX;

function nameRoutedModelPrefix(modelName: string): string {
    const lowercase = modelName.toLowerCase();
    if (lowercase.includes("claude") || lowercase.includes("anthropic")) return ANTHROPIC_MODEL_PREFIX;
    if (lowercase.includes("gemini")) return "gemini";
    return OPENAI_MODEL_PREFIX;
}

function customModelApi(modelPrefix: string | undefined, modelName: string | undefined): string {
    const prefix = normalizeModelPrefix(modelPrefix ?? "") || DEFAULT_CUSTOM_MODEL_PREFIX;
    return isRuntimeModelPrefix(prefix) ? prefix.toLowerCase() : nameRoutedModelPrefix(`${prefix}/${modelName ?? ""}`);
}

const MODEL_PREFIX_PATTERN = /^([^\s/]+)\/(.*)$/;

export interface ModelNameParts {
    prefix?: string;
    name: string;
}

export function splitModelPrefix(modelName: string): ModelNameParts {
    const match = MODEL_PREFIX_PATTERN.exec(modelName);
    return match && isRuntimeModelPrefix(match[1]) ? { prefix: match[1], name: match[2] } : { name: modelName };
}

export function hasModelPrefixField(providerId: string | null | undefined): boolean {
    return providerId === CUSTOM_PROVIDER_ID;
}

export function customModelPrefix(providerId: string | null | undefined, storedName: string): string | undefined {
    if (!hasModelPrefixField(providerId)) return undefined;
    if (!storedName.trim()) return DEFAULT_CUSTOM_MODEL_PREFIX;
    return splitModelPrefix(storedName).prefix ?? nameRoutedModelPrefix(storedName);
}

export function modelNameFormValues(providerId: string | null | undefined, storedName: string): { modelName: string; modelPrefix?: string } {
    if (!hasModelPrefixField(providerId)) return { modelName: storedName };
    return { modelName: splitModelPrefix(storedName).name, modelPrefix: modelPrefixFieldValue(customModelPrefix(providerId, storedName)) };
}

function composeModelName(data: Pick<ModelFormData, "provider" | "modelName" | "modelPrefix">): string {
    const name = data.modelName.trim();
    if (!name || !hasModelPrefixField(data.provider)) return name;
    const prefix = normalizeModelPrefix(data.modelPrefix ?? "") || DEFAULT_CUSTOM_MODEL_PREFIX;
    return `${prefix}/${name}`;
}

export const PROVIDER_DISPLAY_NAMES: Record<string, string> = Object.fromEntries(Object.values(PROVIDER_CONFIGS).map(c => [c.id, c.label]));

export function formModelType(data: Pick<ModelFormData, "provider" | "modelType">): ModelType {
    return getProviderConfig(data.provider, data.modelType).modelType ?? "chat";
}

function providersOfType(modelType: ModelType): ModelProvider[] {
    return Object.values(PROVIDER_CONFIGS)
        .filter(config => servedModelTypes(config).includes(modelType))
        .map(({ id, label }) => ({ id, label }))
        .sort((a, b) => {
            if (a.id === "custom") return 1;
            if (b.id === "custom") return -1;
            return a.label.localeCompare(b.label);
        });
}

export const CHAT_PROVIDERS: ModelProvider[] = providersOfType("chat");

export const DECISION_PROVIDERS: ModelProvider[] = providersOfType("decision");

export const MODEL_TYPE_LABELS: Record<ModelType, string> = {
    chat: "Chat (LLM) model",
    decision: "Decision model",
};

export function defaultProviderForType(modelType: ModelType): string {
    const providers = providersForModelType(modelType);
    return providers.length === 1 ? providers[0].id : "";
}

export function providersForModelType(modelType: ModelType): ModelProvider[] {
    return modelType === "decision" ? DECISION_PROVIDERS : CHAT_PROVIDERS;
}

function parseMaxInputTokens(raw: string | undefined): number | null {
    if (raw == null || raw === "") return null;
    const n = Number(raw);
    if (!Number.isFinite(n) || n < 1) return null;
    return Math.floor(n);
}

export type CustomParamValue = string | number | boolean | null;

export function coerceCustomParamValue(input: string): CustomParamValue {
    try {
        const parsed = JSON.parse(input);
        if (parsed === null || typeof parsed === "string" || typeof parsed === "number" || typeof parsed === "boolean") {
            return parsed;
        }
        return input;
    } catch {
        return input;
    }
}

export function formatCustomParamValue(value: CustomParamValue): string {
    if (typeof value === "string") {
        try {
            const parsed = JSON.parse(value);
            const parsedIsScalar = parsed === null || typeof parsed === "string" || typeof parsed === "number" || typeof parsed === "boolean";
            if (parsedIsScalar) {
                return JSON.stringify(value);
            }
        } catch {}
        return value;
    }
    return JSON.stringify(value);
}

function buildExtraHeaders(customHeaders: ModelFormData["customHeaders"]): Record<string, string> | undefined {
    if (!Array.isArray(customHeaders)) return undefined;
    const headers: Record<string, string> = {};
    for (const header of customHeaders) {
        if (header.key.trim() && header.value.trim()) {
            headers[header.key.trim()] = header.value;
        }
    }
    return Object.keys(headers).length > 0 ? headers : undefined;
}

interface AuthFieldSpec {
    form: string;
    key: string;
    credential?: boolean;
    numeric?: boolean;
}

const AUTH_CONFIG_FIELDS: Record<string, AuthFieldSpec[]> = {
    apikey: [{ form: "apiKey", key: "api_key", credential: true }],
    oauth2: [
        { form: "clientId", key: "client_id", credential: true },
        { form: "clientSecret", key: "client_secret", credential: true },
        { form: "tokenUrl", key: "token_url" },
        { form: "oauthScope", key: "scope" },
        { form: "oauthAudience", key: "audience" },
        { form: "oauthCaCert", key: "ca_cert" },
        { form: "oauthTokenRefreshBufferSeconds", key: "token_refresh_buffer_seconds", numeric: true },
        { form: "oauthMaxRetries", key: "max_retries", numeric: true },
    ],
    aws_iam: [
        { form: "awsAccessKeyId", key: "aws_access_key_id", credential: true },
        { form: "awsSecretAccessKey", key: "aws_secret_access_key", credential: true },
        { form: "awsSessionToken", key: "aws_session_token", credential: true },
        { form: "awsRegionName", key: "aws_region_name" },
    ],
    gcp_service_account: [
        { form: "vertexCredentials", key: "vertex_credentials", credential: true },
        { form: "vertexProject", key: "vertex_project" },
        { form: "vertexLocation", key: "vertex_location" },
    ],
    azure_workload_identity: [
        { form: "clientId", key: "client_id" },
        { form: "tenantId", key: "tenant_id" },
    ],
    oauth2_delegated: [],
    none: [],
};

function shouldIncludeCredential(dirtyFields: Partial<Record<string, boolean>> | undefined, fieldName: string, value: unknown): boolean {
    if (value === REDACTED_SENTINEL) return false;
    if (dirtyFields) return !!dirtyFields[fieldName];
    return !!value;
}

function buildAuthConfig(data: ModelFormData, dirtyFields?: Partial<Record<string, boolean>>): Record<string, unknown> {
    const type = AUTH_CONFIG_FIELDS[data.authType] ? data.authType : "none";
    const authConfig: Record<string, unknown> = { type };
    for (const spec of AUTH_CONFIG_FIELDS[type]) {
        const value = data[spec.form];
        const include = spec.credential ? shouldIncludeCredential(dirtyFields, spec.form, value) : !!value;
        if (include) {
            authConfig[spec.key] = spec.numeric ? Number(value) : value;
        }
    }
    return authConfig;
}

function collectProviderFields(data: ModelFormData, fields: ProviderField[], modelParams: Record<string, unknown>, providerAuthFields: Record<string, unknown>) {
    for (const field of fields) {
        const raw = data[field.name];
        if (raw == null || raw === "") continue;
        const value = field.type === "number" ? Number(raw) : raw;
        if (field.storageTarget === "auth") {
            providerAuthFields[field.name.replaceAll(/[A-Z]/g, letter => `_${letter.toLowerCase()}`)] = value;
        } else {
            modelParams[field.name] = value;
        }
    }
}

function collectCommonParams(data: ModelFormData, modelParams: Record<string, unknown>) {
    for (const field of COMMON_MODEL_PARAMS) {
        const value = data[field.name];
        if (value == null || value === "") continue;
        modelParams[field.name] = field.type === "number" ? Number(value) : value;
    }
}

function collectCustomParams(data: ModelFormData, modelParams: Record<string, unknown>) {
    if (!Array.isArray(data.customParams)) return;
    for (const param of data.customParams) {
        if (param.key.trim()) {
            modelParams[param.key] = coerceCustomParamValue(param.value);
        }
    }
}

export function buildModelPayload(data: ModelFormData, dirtyFields?: Partial<Record<string, boolean>>) {
    const providerConfig = getProviderConfig(data.provider, data.modelType);

    const modelParams: Record<string, unknown> = {};
    const providerAuthFields: Record<string, unknown> = {};
    collectProviderFields(data, providerConfig.fields, modelParams, providerAuthFields);
    if (!providerConfig.hideCommonParams) {
        collectCommonParams(data, modelParams);
    }
    if (providerConfig.modelType !== "decision") {
        collectCustomParams(data, modelParams);
        const extraHeaders = buildExtraHeaders(data.customHeaders);
        if (extraHeaders) {
            modelParams.extra_headers = extraHeaders;
        }
    }

    const authConfig = buildAuthConfig(data, dirtyFields);
    Object.assign(authConfig, providerAuthFields);

    const modelName = composeModelName(data);
    const pricing = providerConfig.modelType === "decision" ? withoutCacheRates(data.pricing) : data.pricing;

    return {
        alias: data.alias.trim(),
        provider: data.provider,
        modelName,
        apiBase: providerConfig.showApiBase || isHttpUrl(data.apiBase) ? data.apiBase || "" : "",
        description: data.description || null,
        authType: data.authType,
        authConfig,
        modelParams,
        maxInputTokens: parseMaxInputTokens(data.maxInputTokens),
        ...(pricing ? { pricing: readPricingFromForm(pricing) } : {}),
    };
}

function withoutCacheRates(pricing: PricingFormValues | undefined): PricingFormValues | undefined {
    return pricing && { ...pricing, breakOutCacheRates: false };
}

export function buildTestPayload(savePayload: ReturnType<typeof buildModelPayload>, modelId?: string, modelType?: ModelType) {
    return {
        provider: savePayload.provider,
        modelName: savePayload.modelName,
        apiBase: savePayload.apiBase || undefined,
        authConfig: savePayload.authConfig,
        modelParams: savePayload.modelParams,
        ...(modelType ? { modelType } : {}),
        ...(modelId ? { modelId } : {}),
    };
}
