// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { chatModelsOnly, type ModelConfig } from "@/api/models";

import {
    buildModelPayload,
    buildTestPayload,
    CHAT_PROVIDERS,
    coerceCustomParamValue,
    customModelPrefix,
    customModelPrefixError,
    type CustomParamValue,
    DECISION_PROVIDERS,
    defaultProviderForType,
    formatCustomParamValue,
    formModelType,
    getProviderConfig,
    type ModelFormData,
    modelNameFieldLabel,
    modelNameFormValues,
    modelNameNoun,
    modelPrefixFieldValue,
    normalizeModelPrefix,
    providersForModelType,
    resolveApiBaseHelpText,
    resolveChatRequestUrl,
    splitModelPrefix,
} from "./modelProviderUtils";

const formData = (overrides: Partial<ModelFormData>): ModelFormData => ({
    alias: "test",
    description: "",
    provider: "custom",
    modelName: "",
    authType: "apikey",
    ...overrides,
});

describe("buildModelPayload model-name prefix", () => {
    it("defaults a custom model to openai/ whatever its name says", () => {
        expect(buildModelPayload(formData({ modelName: "us.anthropic.claude-opus-4-8" })).modelName).toBe("openai/us.anthropic.claude-opus-4-8");
        expect(buildModelPayload(formData({ modelName: "claude-sonnet-4-6" })).modelName).toBe("openai/claude-sonnet-4-6");
        expect(buildModelPayload(formData({ modelName: "gpt-4o-mini" })).modelName).toBe("openai/gpt-4o-mini");
    });

    it("sends a custom model with the prefix chosen for it", () => {
        expect(buildModelPayload(formData({ modelName: "claude-sonnet-4-6", modelPrefix: "anthropic" })).modelName).toBe("anthropic/claude-sonnet-4-6");
        expect(buildModelPayload(formData({ modelName: "llama-3.1-70b", modelPrefix: "groq" })).modelName).toBe("groq/llama-3.1-70b");
    });

    it("keeps a gateway alias that has its own slash behind the chosen prefix", () => {
        expect(buildModelPayload(formData({ modelName: "anthropic/claude-3-5-sonnet", modelPrefix: "openai" })).modelName).toBe("openai/anthropic/claude-3-5-sonnet");
        expect(buildModelPayload(formData({ modelName: "openai/gpt-oss-120b", modelPrefix: "openai" })).modelName).toBe("openai/openai/gpt-oss-120b");
    });

    it("trims surrounding whitespace before adding the prefix", () => {
        expect(buildModelPayload(formData({ modelName: "  gpt-4o-mini " })).modelName).toBe("openai/gpt-4o-mini");
        expect(buildModelPayload(formData({ provider: "anthropic", modelName: " claude-sonnet-4-6 " })).modelName).toBe("claude-sonnet-4-6");
        expect(buildModelPayload(formData({ provider: "anthropic", modelName: "\tclaude-sonnet-4-6\u00a0" })).modelName).toBe("claude-sonnet-4-6");
    });

    it("leaves an empty custom model name empty", () => {
        expect(buildModelPayload(formData({ modelName: "  " })).modelName).toBe("");
    });

    it("sends other providers' names without the prefix the backend adds", () => {
        expect(buildModelPayload(formData({ provider: "anthropic", modelName: "claude-sonnet-4-6", modelPrefix: "openai" })).modelName).toBe("claude-sonnet-4-6");
        expect(buildModelPayload(formData({ provider: "azure_openai", modelName: "my-deployment" })).modelName).toBe("my-deployment");
    });
});

describe("splitModelPrefix", () => {
    it("splits off a prefix the runtime routes by", () => {
        expect(splitModelPrefix("anthropic/claude-sonnet-4-6")).toEqual({ prefix: "anthropic", name: "claude-sonnet-4-6" });
        expect(splitModelPrefix("vertex_ai/gemini-1.5-pro")).toEqual({ prefix: "vertex_ai", name: "gemini-1.5-pro" });
        expect(splitModelPrefix("openrouter/anthropic/claude-3.5-sonnet")).toEqual({ prefix: "openrouter", name: "anthropic/claude-3.5-sonnet" });
    });

    it("keeps a first segment the runtime does not route by in the name, as part of the model's own id", () => {
        expect(splitModelPrefix("hosted_vllm/llama")).toEqual({ name: "hosted_vllm/llama" });
        expect(splitModelPrefix("meta-llama/Llama-3-70b")).toEqual({ name: "meta-llama/Llama-3-70b" });
        expect(splitModelPrefix("hf.co/org/model")).toEqual({ name: "hf.co/org/model" });
        expect(splitModelPrefix("litellm_proxy/claude-sonnet-4-6")).toEqual({ name: "litellm_proxy/claude-sonnet-4-6" });
    });

    it("splits only the first segment", () => {
        expect(splitModelPrefix("openai/anthropic/claude-3-5-sonnet")).toEqual({ prefix: "openai", name: "anthropic/claude-3-5-sonnet" });
        expect(splitModelPrefix("openai/openai/gpt-oss-120b")).toEqual({ prefix: "openai", name: "openai/gpt-oss-120b" });
    });

    it("matches the prefix in any case and keeps its case as stored", () => {
        expect(splitModelPrefix("Anthropic/claude-sonnet-4-6")).toEqual({ prefix: "Anthropic", name: "claude-sonnet-4-6" });
        expect(splitModelPrefix("OpenAI/gpt-4o")).toEqual({ prefix: "OpenAI", name: "gpt-4o" });
    });

    it("leaves a name without a slash whole, and gives a routed prefix with nothing after it an empty name", () => {
        expect(splitModelPrefix("gpt-4o")).toEqual({ name: "gpt-4o" });
        expect(splitModelPrefix("openai/")).toEqual({ prefix: "openai", name: "" });
    });
});

describe("modelNameFormValues", () => {
    it("splits a custom model's stored prefix into its own field when the runtime routes by it", () => {
        expect(modelNameFormValues("custom", "anthropic/claude-sonnet-4-6")).toEqual({ modelName: "claude-sonnet-4-6", modelPrefix: "anthropic/" });
        expect(modelNameFormValues("custom", "groq/llama-3.1-70b")).toEqual({ modelName: "llama-3.1-70b", modelPrefix: "groq/" });
        expect(modelNameFormValues("custom", "openai/openai/gpt-oss-120b")).toEqual({ modelName: "openai/gpt-oss-120b", modelPrefix: "openai/" });
    });

    it("shows a custom model whose first segment is not a prefix with its whole name, under the prefix the runtime picks for it", () => {
        expect(modelNameFormValues("custom", "meta-llama/Llama-3.3-70B-Instruct")).toEqual({ modelName: "meta-llama/Llama-3.3-70B-Instruct", modelPrefix: "openai/" });
        expect(modelNameFormValues("custom", "hosted_vllm/llama")).toEqual({ modelName: "hosted_vllm/llama", modelPrefix: "openai/" });
        expect(modelNameFormValues("custom", "litellm_proxy/claude-sonnet-4-6")).toEqual({ modelName: "litellm_proxy/claude-sonnet-4-6", modelPrefix: "anthropic/" });
    });

    it("shows a custom model stored without a prefix under the prefix the runtime picks from its name", () => {
        expect(modelNameFormValues("custom", "us.anthropic.claude-opus-4-8")).toEqual({ modelName: "us.anthropic.claude-opus-4-8", modelPrefix: "anthropic/" });
        expect(modelNameFormValues("custom", "gemini-2.5-pro")).toEqual({ modelName: "gemini-2.5-pro", modelPrefix: "gemini/" });
    });

    it("shows a stored routed prefix with nothing after it as an empty name, which the form asks for", () => {
        expect(modelNameFormValues("custom", "openai/")).toEqual({ modelName: "", modelPrefix: "openai/" });
    });

    it("shows another provider's name as stored", () => {
        expect(modelNameFormValues("azure_openai", "azure/my-deployment")).toEqual({ modelName: "azure/my-deployment" });
        expect(modelNameFormValues("anthropic", "claude-sonnet-4-6")).toEqual({ modelName: "claude-sonnet-4-6" });
    });

    it("keeps a prefix another provider does not add, since it overrides the default", () => {
        expect(modelNameFormValues("vertex_ai", "anthropic/claude-3-haiku@20240307")).toEqual({ modelName: "anthropic/claude-3-haiku@20240307" });
    });

    it("keeps a decision model's name as stored", () => {
        expect(modelNameFormValues("openai", "openai/gpt-6-luna")).toEqual({ modelName: "openai/gpt-6-luna" });
    });

    it.each([
        ["custom", "us.anthropic.claude-opus-4-8", "anthropic/us.anthropic.claude-opus-4-8"],
        ["custom", "gemini-2.5-pro", "gemini/gemini-2.5-pro"],
        ["custom", "gpt-oss-120b", "openai/gpt-oss-120b"],
        ["custom", "meta-llama/Llama-3-70b", "openai/meta-llama/Llama-3-70b"],
        ["custom", "hf.co/org/model", "openai/hf.co/org/model"],
        ["custom", "anthropic/claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"],
        ["custom", "openai/google/gemma-2-9b-it", "openai/google/gemma-2-9b-it"],
        ["custom", "openai/openai/gpt-oss-120b", "openai/openai/gpt-oss-120b"],
        ["custom", "hosted_vllm/llama", "openai/hosted_vllm/llama"],
        ["custom", "litellm_proxy/claude-sonnet-4-6", "anthropic/litellm_proxy/claude-sonnet-4-6"],
        ["custom", "OpenAI/gpt-4o", "OpenAI/gpt-4o"],
        ["azure_openai", "azure/my-deployment", "azure/my-deployment"],
        ["openai", "claude-sonnet-4-6", "claude-sonnet-4-6"],
        ["vertex_ai", "anthropic/claude-3-haiku@20240307", "anthropic/claude-3-haiku@20240307"],
    ])("saves a %s model stored as %s unedited as %s", (provider, stored, saved) => {
        expect(buildModelPayload(formData({ provider, ...modelNameFormValues(provider, stored) })).modelName).toBe(saved);
    });
});

describe("customModelPrefix", () => {
    it("is a custom model's stored prefix when the runtime routes by it", () => {
        expect(customModelPrefix("custom", "anthropic/claude-sonnet-4-6")).toBe("anthropic");
        expect(customModelPrefix("custom", "groq/llama-3.1-70b")).toBe("groq");
    });

    it("is the one the runtime picks from a custom model's name when it has none it routes by", () => {
        expect(customModelPrefix("custom", "claude-sonnet-4-6")).toBe("anthropic");
        expect(customModelPrefix("custom", "gemini-2.5-pro")).toBe("gemini");
        expect(customModelPrefix("custom", "gpt-oss-120b")).toBe("openai");
        expect(customModelPrefix("custom", "hosted_vllm/llama")).toBe("openai");
        expect(customModelPrefix("custom", "meta-llama/Llama-3.3-70B-Instruct")).toBe("openai");
        expect(customModelPrefix("custom", "litellm_proxy/claude-sonnet-4-6")).toBe("anthropic");
    });

    it("is openai before a custom model's name is entered", () => {
        expect(customModelPrefix("custom", "")).toBe("openai");
    });

    it("is undefined for any other provider, which selects the API itself", () => {
        expect(customModelPrefix("bedrock", "anthropic.claude-3-5-sonnet-20241022-v2:0")).toBeUndefined();
        expect(customModelPrefix("anthropic", "claude-sonnet-4-6")).toBeUndefined();
        expect(customModelPrefix("vertex_ai", "anthropic/claude-3-haiku@20240307")).toBeUndefined();
        expect(customModelPrefix("typesafe", "openai/gpt-6-luna-decisions")).toBeUndefined();
        expect(customModelPrefix("anthropic", "")).toBeUndefined();
    });
});

describe("custom model prefixes", () => {
    it("shows a prefix in the field with its trailing slash", () => {
        expect(modelPrefixFieldValue("openai")).toBe("openai/");
        expect(modelPrefixFieldValue(" Groq ")).toBe("Groq/");
        expect(modelPrefixFieldValue("anthropic//")).toBe("anthropic/");
        expect(modelPrefixFieldValue("")).toBe("");
        expect(modelPrefixFieldValue(undefined)).toBe("");
    });

    it("drops surrounding whitespace and the trailing slash, keeping the case as typed", () => {
        expect(normalizeModelPrefix(" Groq/ ")).toBe("Groq");
        expect(normalizeModelPrefix("anthropic")).toBe("anthropic");
        expect(normalizeModelPrefix("Qwen/")).toBe("Qwen");
        expect(normalizeModelPrefix("")).toBe("");
    });

    it("accepts any one-word prefix, and an empty one for the openai/ default", () => {
        expect(customModelPrefixError("groq")).toBeUndefined();
        expect(customModelPrefixError("XAI/")).toBeUndefined();
        expect(customModelPrefixError("together_ai")).toBeUndefined();
        expect(customModelPrefixError("meta-llama/")).toBeUndefined();
        expect(customModelPrefixError("")).toBeUndefined();
        expect(customModelPrefixError(undefined)).toBeUndefined();
    });

    it("rejects a prefix with a space or a slash inside it", () => {
        expect(customModelPrefixError("my prefix")).toBe("Enter one word followed by /, such as openai/.");
        expect(customModelPrefixError("openai/v1")).toBe("Enter one word followed by /, such as openai/.");
        expect(customModelPrefixError("/openai")).toBe("Enter one word followed by /, such as openai/.");
    });

    it("saves a typed prefix as typed", () => {
        expect(buildModelPayload(formData({ modelName: "llama-3.1-70b", modelPrefix: "Groq/" })).modelName).toBe("Groq/llama-3.1-70b");
        expect(buildModelPayload(formData({ modelName: "my-model", modelPrefix: "" })).modelName).toBe("openai/my-model");
    });

    it("saves a prefix the runtime does not route by as typed, with no other prefix added", () => {
        expect(buildModelPayload(formData({ modelName: "my-model", modelPrefix: "Qwen/" })).modelName).toBe("Qwen/my-model");
        expect(buildModelPayload(formData({ modelName: "claude-sonnet-4-6", modelPrefix: "litellm_proxy" })).modelName).toBe("litellm_proxy/claude-sonnet-4-6");
    });

    it.each(["anthropic/claude-sonnet-4-6", "vertex_ai/gemini-1.5-pro", "OpenAI/gpt-4o", "openai/", "openrouter/anthropic/claude-3.5-sonnet", "huggingface/org/model"])("can save the prefix a stored %s splits into", stored => {
        expect(customModelPrefixError(splitModelPrefix(stored).prefix)).toBeUndefined();
    });
});

describe("buildModelPayload auth-config routing", () => {
    it("apikey: routes the api key into authConfig.api_key", () => {
        const payload = buildModelPayload(formData({ provider: "anthropic", authType: "apikey", apiKey: "sk-secret" }));
        expect(payload.authConfig).toEqual({ type: "apikey", api_key: "sk-secret" });
    });

    it("oauth2: routes structural fields as snake_case and includes credentials the user supplied", () => {
        const payload = buildModelPayload(
            formData({
                provider: "azure_openai",
                authType: "oauth2",
                clientId: "cid",
                clientSecret: "csecret",
                tokenUrl: "https://auth.example.com/token",
                oauthScope: "openai.read",
                oauthAudience: "aud-xyz",
                oauthTokenRefreshBufferSeconds: "30",
                oauthMaxRetries: "3",
            })
        );
        expect(payload.authConfig).toMatchObject({
            type: "oauth2",
            client_id: "cid",
            client_secret: "csecret",
            token_url: "https://auth.example.com/token",
            scope: "openai.read",
            audience: "aud-xyz",
            token_refresh_buffer_seconds: 30,
            max_retries: 3,
        });
    });

    it("aws_iam: routes AWS creds + region into authConfig with snake_case keys", () => {
        const payload = buildModelPayload(
            formData({
                provider: "bedrock",
                authType: "aws_iam",
                awsAccessKeyId: "AKIA",
                awsSecretAccessKey: "secret",
                awsSessionToken: "token",
                awsRegionName: "us-east-1",
            })
        );
        expect(payload.authConfig).toEqual({
            type: "aws_iam",
            aws_access_key_id: "AKIA",
            aws_secret_access_key: "secret",
            aws_session_token: "token",
            aws_region_name: "us-east-1",
        });
    });

    it("gcp_service_account: routes vertex credentials + project + location into authConfig", () => {
        const payload = buildModelPayload(
            formData({
                provider: "vertex_ai",
                authType: "gcp_service_account",
                vertexCredentials: '{"type":"service_account"}',
                vertexProject: "my-project",
                vertexLocation: "us-central1",
            })
        );
        expect(payload.authConfig).toEqual({
            type: "gcp_service_account",
            vertex_credentials: '{"type":"service_account"}',
            vertex_project: "my-project",
            vertex_location: "us-central1",
        });
    });

    it("oauth2: routes oauthCaCert into authConfig.ca_cert", () => {
        const payload = buildModelPayload(formData({ provider: "azure_openai", authType: "oauth2", oauthCaCert: "/path/to/ca.pem" }));
        expect(payload.authConfig).toMatchObject({ type: "oauth2", ca_cert: "/path/to/ca.pem" });
    });

    it("azure_openai apikey: apiVersion is routed into authConfig, not modelParams", () => {
        const payload = buildModelPayload(
            formData({
                provider: "azure_openai",
                authType: "apikey",
                apiKey: "key",
                modelName: "gpt-4",
                apiVersion: "2024-02-15-preview",
            })
        );
        expect(payload.authConfig).toMatchObject({
            type: "apikey",
            api_key: "key",
            api_version: "2024-02-15-preview",
        });
        expect(payload.modelParams).not.toHaveProperty("api_version");
        expect(payload.modelParams).not.toHaveProperty("apiVersion");
    });

    it("azure_workload_identity: sends client and tenant IDs, keeps apiVersion, drops a lingering secret", () => {
        const payload = buildModelPayload(
            formData({
                provider: "azure_openai",
                authType: "azure_workload_identity",
                apiKey: "should-be-dropped",
                clientSecret: "should-be-dropped",
                clientId: "11111111-2222-3333-4444-555555555555",
                tenantId: "66666666-7777-8888-9999-000000000000",
                modelName: "gpt-4.1-mini",
                apiVersion: "2024-10-21",
            })
        );
        expect(payload.authConfig).toEqual({
            type: "azure_workload_identity",
            client_id: "11111111-2222-3333-4444-555555555555",
            tenant_id: "66666666-7777-8888-9999-000000000000",
            api_version: "2024-10-21",
        });
    });

    it("authType=none clears any lingering credentials from prior auth choices", () => {
        const payload = buildModelPayload(
            formData({
                provider: "ollama",
                authType: "none",
                apiKey: "should-be-dropped",
                clientId: "should-be-dropped",
                awsAccessKeyId: "should-be-dropped",
            })
        );
        expect(payload.authConfig).toEqual({ type: "none" });
    });

    it("dirtyFields gate: unchanged credentials are omitted, dirty ones flow through", () => {
        const payload = buildModelPayload(formData({ provider: "anthropic", authType: "apikey", apiKey: "old-value" }), { apiKey: false });
        expect(payload.authConfig).toEqual({ type: "apikey" });
        expect(payload.authConfig).not.toHaveProperty("api_key");

        const dirtyPayload = buildModelPayload(formData({ provider: "anthropic", authType: "apikey", apiKey: "new-value" }), { apiKey: true });
        expect(dirtyPayload.authConfig).toEqual({ type: "apikey", api_key: "new-value" });
    });

    it("common model params land in modelParams with numeric coercion", () => {
        const payload = buildModelPayload(
            formData({
                provider: "openai",
                authType: "apikey",
                apiKey: "k",
                max_tokens: "2048",
                cache_strategy: "1h",
            } as Partial<ModelFormData>)
        );
        expect(payload.modelParams).toEqual({
            max_tokens: 2048,
            cache_strategy: "1h",
        });
    });

    it("temperature is not a common param: a stray form value never reaches modelParams", () => {
        const payload = buildModelPayload(
            formData({
                provider: "openai",
                authType: "apikey",
                apiKey: "k",
                temperature: "0.7",
            } as Partial<ModelFormData>)
        );
        expect(payload.modelParams).toEqual({});
    });
});

describe("buildModelPayload maxInputTokens", () => {
    const maxInputTokensOf = (raw: string | undefined) => buildModelPayload(formData({ provider: "openai", authType: "apikey", apiKey: "k", maxInputTokens: raw })).maxInputTokens;

    it("omitted or empty input becomes null", () => {
        expect(maxInputTokensOf(undefined)).toBeNull();
        expect(maxInputTokensOf("")).toBeNull();
    });

    it("non-numeric input becomes null", () => {
        expect(maxInputTokensOf("abc")).toBeNull();
        expect(maxInputTokensOf("NaN")).toBeNull();
    });

    it("values below 1 become null", () => {
        expect(maxInputTokensOf("0")).toBeNull();
        expect(maxInputTokensOf("-5")).toBeNull();
        expect(maxInputTokensOf("0.5")).toBeNull();
    });

    it("floats are floored to an integer", () => {
        expect(maxInputTokensOf("200000.9")).toBe(200000);
        expect(maxInputTokensOf("1.01")).toBe(1);
    });

    it("valid integers pass through unchanged", () => {
        expect(maxInputTokensOf("200000")).toBe(200000);
        expect(maxInputTokensOf("1")).toBe(1);
    });
});

describe("buildModelPayload custom HTTP headers", () => {
    it("maps the customHeaders array into modelParams.extra_headers as an object", () => {
        const payload = buildModelPayload(
            formData({
                provider: "openai",
                authType: "apikey",
                apiKey: "k",
                customHeaders: [
                    { key: "X-Correlation-ID", value: "abc-123" },
                    { key: "anthropic-beta", value: "prompt-caching-2024-07-31" },
                ],
            })
        );
        expect(payload.modelParams.extra_headers).toEqual({
            "X-Correlation-ID": "abc-123",
            "anthropic-beta": "prompt-caching-2024-07-31",
        });
    });

    it("trims header keys and drops rows with an empty key or value", () => {
        const payload = buildModelPayload(
            formData({
                provider: "openai",
                authType: "apikey",
                apiKey: "k",
                customHeaders: [
                    { key: "  X-Trim  ", value: "v" },
                    { key: "", value: "ignored" },
                    { key: "X-Empty", value: "   " },
                ],
            })
        );
        expect(payload.modelParams.extra_headers).toEqual({ "X-Trim": "v" });
    });

    it("omits extra_headers entirely when no headers are provided", () => {
        const payload = buildModelPayload(formData({ provider: "openai", authType: "apikey", apiKey: "k" }));
        expect(payload.modelParams).not.toHaveProperty("extra_headers");
    });
});

describe("modelNameFieldLabel", () => {
    it("labels the Azure field as a deployment", () => {
        expect(modelNameFieldLabel("azure_openai", "Model Name")).toBe("Deployment Name");
        expect(modelNameFieldLabel("azure_openai", "Default Model")).toBe("Deployment Name");
    });

    it("uses the fallback label for other providers", () => {
        expect(modelNameFieldLabel("openai", "Model Name")).toBe("Model Name");
        expect(modelNameFieldLabel("custom", "Default Model")).toBe("Default Model");
        expect(modelNameFieldLabel(null, "Model Name")).toBe("Model Name");
    });
});

describe("modelNameNoun", () => {
    it("calls the Azure identifier a deployment", () => {
        expect(modelNameNoun("azure_openai")).toBe("deployment");
    });

    it("calls other providers' identifier a model", () => {
        expect(modelNameNoun("anthropic")).toBe("model");
        expect(modelNameNoun(undefined)).toBe("model");
    });
});

describe("coerceCustomParamValue", () => {
    it("parses unquoted numbers as numbers", () => {
        expect(coerceCustomParamValue("500")).toBe(500);
        expect(coerceCustomParamValue("3.14")).toBe(3.14);
        expect(coerceCustomParamValue("-42")).toBe(-42);
        expect(coerceCustomParamValue("0")).toBe(0);
    });

    it("parses quoted numbers as strings", () => {
        expect(coerceCustomParamValue('"500"')).toBe("500");
        expect(coerceCustomParamValue('"3.14"')).toBe("3.14");
    });

    it("parses unquoted booleans as booleans", () => {
        expect(coerceCustomParamValue("true")).toBe(true);
        expect(coerceCustomParamValue("false")).toBe(false);
    });

    it("parses quoted booleans as strings", () => {
        expect(coerceCustomParamValue('"true"')).toBe("true");
        expect(coerceCustomParamValue('"false"')).toBe("false");
    });

    it("parses unquoted null as null", () => {
        expect(coerceCustomParamValue("null")).toBeNull();
    });

    it("parses quoted null as string", () => {
        expect(coerceCustomParamValue('"null"')).toBe("null");
    });

    it("treats plain unquoted text as a string", () => {
        expect(coerceCustomParamValue("hello")).toBe("hello");
        expect(coerceCustomParamValue("gpt-4o-mini")).toBe("gpt-4o-mini");
    });

    it("treats quoted text as a string", () => {
        expect(coerceCustomParamValue('"hello"')).toBe("hello");
    });

    it("treats invalid JSON as a plain string", () => {
        expect(coerceCustomParamValue("{broken")).toBe("{broken");
        expect(coerceCustomParamValue("TRUE")).toBe("TRUE");
    });

    it("does not coerce JSON objects or arrays — returns the original string", () => {
        expect(coerceCustomParamValue("[1,2,3]")).toBe("[1,2,3]");
        expect(coerceCustomParamValue('{"foo":"bar"}')).toBe('{"foo":"bar"}');
    });
});

describe("formatCustomParamValue", () => {
    it("formats numbers unquoted", () => {
        expect(formatCustomParamValue(500)).toBe("500");
        expect(formatCustomParamValue(3.14)).toBe("3.14");
        expect(formatCustomParamValue(0)).toBe("0");
    });

    it("formats booleans unquoted", () => {
        expect(formatCustomParamValue(true)).toBe("true");
        expect(formatCustomParamValue(false)).toBe("false");
    });

    it("formats null unquoted", () => {
        expect(formatCustomParamValue(null)).toBe("null");
    });

    it("formats plain strings without quotes", () => {
        expect(formatCustomParamValue("hello")).toBe("hello");
        expect(formatCustomParamValue("gpt-4o-mini")).toBe("gpt-4o-mini");
    });

    it("quotes strings that would otherwise parse as non-string JSON", () => {
        expect(formatCustomParamValue("500")).toBe('"500"');
        expect(formatCustomParamValue("true")).toBe('"true"');
        expect(formatCustomParamValue("false")).toBe('"false"');
        expect(formatCustomParamValue("null")).toBe('"null"');
        expect(formatCustomParamValue("3.14")).toBe('"3.14"');
    });

    it("leaves JSON-object/array strings unquoted — coerce treats them as strings anyway", () => {
        expect(formatCustomParamValue('["a","b"]')).toBe('["a","b"]');
        expect(formatCustomParamValue('{"a":"b"}')).toBe('{"a":"b"}');
    });
});

describe("round-trip between coerce and format", () => {
    const cases: Array<{ stored: CustomParamValue; displayed: string }> = [
        { stored: 500, displayed: "500" },
        { stored: 3.14, displayed: "3.14" },
        { stored: true, displayed: "true" },
        { stored: false, displayed: "false" },
        { stored: null, displayed: "null" },
        { stored: "hello", displayed: "hello" },
        { stored: "500", displayed: '"500"' },
        { stored: "true", displayed: '"true"' },
        { stored: '["a","b"]', displayed: '["a","b"]' },
        { stored: '{"a":"b"}', displayed: '{"a":"b"}' },
    ];

    it.each(cases)("stored $stored displays as $displayed and coerces back to the same value", ({ stored, displayed }) => {
        expect(formatCustomParamValue(stored)).toBe(displayed);
        expect(coerceCustomParamValue(displayed)).toStrictEqual(stored);
    });
});

describe("decision model providers", () => {
    const ids = (providers: { id: string }[]) => providers.map(p => p.id);

    it("keeps TypeSafe out of the chat providers, and lists OpenAI and Google AI Studio for both types", () => {
        expect(ids(CHAT_PROVIDERS)).not.toContain("typesafe");
        expect(ids(CHAT_PROVIDERS)).toContain("openai");
        expect(ids(CHAT_PROVIDERS)).toContain("google_ai_studio");
        expect(ids(DECISION_PROVIDERS)).toEqual(["google_ai_studio", "openai", "typesafe"]);
        expect(ids(CHAT_PROVIDERS).at(-1)).toBe("custom");
    });

    it("resolves a form's model type from its choice, else its provider's first, defaulting to chat", () => {
        expect(formModelType({ provider: "typesafe" })).toBe("decision");
        expect(formModelType({ provider: "anthropic" })).toBe("chat");
        expect(formModelType({ provider: "unknown-provider" })).toBe("chat");
        expect(formModelType({ provider: "" })).toBe("chat");
        expect(formModelType({ provider: "openai", modelType: "decision" })).toBe("decision");
        expect(formModelType({ provider: "openai" })).toBe("chat");
        expect(formModelType({ provider: "google_ai_studio", modelType: "decision" })).toBe("decision");
        expect(formModelType({ provider: "google_ai_studio" })).toBe("chat");
        expect(formModelType({ provider: "anthropic", modelType: "decision" })).toBe("chat");
    });

    it("offers a model only the providers of its type", () => {
        expect(ids(providersForModelType("decision"))).toEqual(["google_ai_studio", "openai", "typesafe"]);
        expect(defaultProviderForType("decision")).toBe("");
        const chat = ids(providersForModelType("chat"));
        expect(chat).toContain("anthropic");
        expect(chat).toContain("openai");
        expect(chat).not.toContain("typesafe");
    });

    it("applies a provider's per-type overrides", () => {
        const chat = getProviderConfig("openai");
        expect(chat.modelType).toBe("chat");
        expect(chat.showApiBase).toBe(false);
        expect(chat.hideCommonParams).toBeFalsy();
        const decision = getProviderConfig("openai", "decision");
        expect(decision).toMatchObject({
            modelType: "decision",
            showApiBase: true,
            apiBasePlaceholder: "https://api.openai.com/v1",
            modelNamePlaceholder: "gpt-6-luna",
            hideCommonParams: true,
            allowedAuthTypes: ["apikey"],
            capabilityKey: "workflow_decision_node",
        });
        expect(getProviderConfig("anthropic", "decision").modelType).toBe("chat");
    });

    it("keeps Google AI Studio's chat config and adds its Gemini decision overrides", () => {
        const chat = getProviderConfig("google_ai_studio");
        expect(chat).toMatchObject({ modelType: "chat", showApiBase: false, modelNamePlaceholder: "gemini/gemini-1.5-pro" });
        expect(chat.hideCommonParams).toBeFalsy();
        expect(chat.capabilityKey).toBeUndefined();
        expect(getProviderConfig("google_ai_studio", "decision")).toMatchObject({
            modelType: "decision",
            showApiBase: true,
            apiBasePlaceholder: "https://generativelanguage.googleapis.com/v1beta",
            modelNamePlaceholder: "gemini-decision-1",
            hideCommonParams: true,
            allowedAuthTypes: ["apikey"],
            capabilityKey: "workflow_decision_node",
        });
    });

    it("sends no chat-only parameters for an OpenAI decision model, and its type to the connection test", () => {
        const payload = buildModelPayload(formData({ provider: "openai", modelType: "decision", modelName: "gpt-6-luna", cache_strategy: "5m", customParams: [{ key: "foo", value: "1" }] }));
        expect(payload.modelParams).toEqual({});
        expect(buildTestPayload(payload, undefined, "decision")).toMatchObject({ provider: "openai", modelType: "decision" });
        expect(buildTestPayload(payload)).not.toHaveProperty("modelType");
    });

    it("sends no chat-only parameters for a decision provider", () => {
        const payload = buildModelPayload(formData({ provider: "typesafe", modelName: "jev-1.13.0", cache_strategy: "5m" }));
        expect(payload.modelParams).toEqual({});
        expect(payload.modelName).toBe("jev-1.13.0");
    });

    it("drops custom parameters and headers carried over from a chat provider", () => {
        const payload = buildModelPayload(
            formData({
                provider: "typesafe",
                modelName: "jev-1.13.0",
                customParams: [{ key: "foo", value: "1" }],
                customHeaders: [{ key: "X-Tenant", value: "acme" }],
            })
        );
        expect(payload.modelParams).toEqual({});
    });

    it("sends a decision model's input and output price and clears its cache rates", () => {
        const payload = buildModelPayload(
            formData({
                provider: "typesafe",
                modelName: "jev-1.13.0",
                pricing: { input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75", breakOutCacheRates: true },
            })
        );
        expect(payload.pricing).toEqual({ input: "3", output: "15", cacheRead: null, cacheWrite: null });
    });

    it("sends no price at all for a decision model whose form carries none", () => {
        const payload = buildModelPayload(formData({ provider: "typesafe", modelName: "jev-1.13.0" }));
        expect(payload).not.toHaveProperty("pricing");
    });

    it("still sends common parameters for a chat provider", () => {
        const payload = buildModelPayload(formData({ provider: "anthropic", modelName: "claude-sonnet-5", cache_strategy: "5m" }));
        expect(payload.modelParams).toMatchObject({ cache_strategy: "5m" });
    });

    it("filters decision models out of a chat model list", () => {
        const models = [{ modelType: "chat" }, { modelType: "decision" }, {}] as Pick<ModelConfig, "modelType">[];
        expect(chatModelsOnly(models)).toEqual([{ modelType: "chat" }, {}]);
    });
});

describe("buildModelPayload pricing", () => {
    it("omits pricing entirely when the form does not carry it", () => {
        const payload = buildModelPayload(formData({ modelName: "gpt-4" }));

        expect(payload).not.toHaveProperty("pricing");
    });
});

describe("resolveChatRequestUrl", () => {
    it.each([
        ["https://h", "https://h/v1/chat/completions"],
        ["https://h/", "https://h/v1/chat/completions"],
        ["https://h/v1", "https://h/v1/chat/completions"],
        ["https://h/v1/", "https://h/v1/chat/completions"],
        ["https://h/openai", "https://h/openai/v1/chat/completions"],
        ["https://h/api/v3", "https://h/api/v3/v1/chat/completions"],
        ["https://h/chat/completions/proxy", "https://h/chat/completions/proxy/v1/chat/completions"],
        ["  https://h/v1  ", "https://h/v1/chat/completions"],
    ])("appends /v1/chat/completions to %s", (apiBase, expected) => {
        expect(resolveChatRequestUrl("custom", apiBase)).toBe(expected);
        expect(resolveChatRequestUrl("openai", apiBase)).toBe(expected);
    });

    it("uses a base ending in /chat/completions as the full endpoint", () => {
        expect(resolveChatRequestUrl("custom", "https://ark.cn-beijing.volces.com/api/v3/chat/completions")).toBe("https://ark.cn-beijing.volces.com/api/v3/chat/completions");
    });

    it("drops trailing slashes from a full endpoint", () => {
        expect(resolveChatRequestUrl("custom", "https://ark.cn-beijing.volces.com/api/v3/chat/completions/")).toBe("https://ark.cn-beijing.volces.com/api/v3/chat/completions");
    });

    it("keeps the query string of a full endpoint", () => {
        expect(resolveChatRequestUrl("custom", "https://h/x/chat/completions?k=v")).toBe("https://h/x/chat/completions?k=v");
    });

    it("falls back to the legacy form for a string that is not a URL", () => {
        expect(resolveChatRequestUrl("custom", "not a url")).toBe("not a url/v1/chat/completions");
        expect(resolveChatRequestUrl("custom", "localhost:8080/chat/completions")).toBe("localhost:8080/chat/completions/v1/chat/completions");
    });

    it("returns undefined for providers other than openai and custom", () => {
        expect(resolveChatRequestUrl("anthropic", "https://h")).toBeUndefined();
        expect(resolveChatRequestUrl("", "https://h")).toBeUndefined();
        expect(resolveChatRequestUrl(undefined, "https://h")).toBeUndefined();
    });

    it("returns undefined for an empty API base", () => {
        expect(resolveChatRequestUrl("custom", "")).toBeUndefined();
        expect(resolveChatRequestUrl("custom", "   ")).toBeUndefined();
        expect(resolveChatRequestUrl("custom", undefined)).toBeUndefined();
    });

    it.each([
        ["https://h", "https://h/v1/messages"],
        ["https://h/", "https://h/v1/messages"],
        ["https://gateway.example.com/llms/v1", "https://gateway.example.com/llms/v1/messages"],
        ["https://gateway.example.com/llms/v1/", "https://gateway.example.com/llms/v1/messages"],
    ])("appends /v1/messages to %s for an anthropic/ custom model", (apiBase, expected) => {
        expect(resolveChatRequestUrl("custom", apiBase, "anthropic")).toBe(expected);
    });

    it("sends an openai/ custom model to the chat completions endpoint", () => {
        expect(resolveChatRequestUrl("custom", "https://gateway.example.com/llms/v1", "openai")).toBe("https://gateway.example.com/llms/v1/chat/completions");
    });

    it("returns undefined for a custom prefix whose request path it does not know", () => {
        expect(resolveChatRequestUrl("custom", "https://h", "groq")).toBeUndefined();
    });

    it("returns undefined for a prefix the runtime does not route by when the name selects the Gemini API", () => {
        expect(resolveChatRequestUrl("custom", "https://h", "hosted_vllm/", "gemini-2.5-pro")).toBeUndefined();
    });

    it("sends a custom model whose prefix the runtime does not route by to the API its name selects, as the runtime does", () => {
        expect(resolveChatRequestUrl("custom", "https://h", "anth")).toBe("https://h/v1/chat/completions");
        expect(resolveChatRequestUrl("custom", "https://h", "Qwen/", "my-model")).toBe("https://h/v1/chat/completions");
        expect(resolveChatRequestUrl("custom", "https://h", "litellm_proxy/", "claude-sonnet-4-6")).toBe("https://h/v1/messages");
    });

    it("matches a prefix the runtime routes by in any case", () => {
        expect(resolveChatRequestUrl("custom", "https://h", "Anthropic/", "claude-sonnet-4-6")).toBe("https://h/v1/messages");
    });

    it("selects the API from the prefix and the name together when the prefix is not one the runtime routes by", () => {
        expect(resolveChatRequestUrl("custom", "https://h", "my-anthropic-gw/", "llama-3")).toBe("https://h/v1/messages");
    });

    it("appends /v1/messages after a base ending in /chat/completions, which only the OpenAI API treats as a full endpoint", () => {
        expect(resolveChatRequestUrl("custom", "https://h/api/chat/completions", "anthropic")).toBe("https://h/api/chat/completions/v1/messages");
    });

    it("ignores the prefix for the openai provider", () => {
        expect(resolveChatRequestUrl("openai", "https://h", "anthropic")).toBe("https://h/v1/chat/completions");
    });
});

describe("resolveApiBaseHelpText", () => {
    const configured = "Enter the provider's base URL and Agent Mesh adds /v1/chat/completions.";

    it("keeps the provider's own hint for a custom model on the OpenAI API", () => {
        expect(resolveApiBaseHelpText(configured, "custom", "openai", "gpt-oss-120b")).toBe(configured);
        expect(resolveApiBaseHelpText(configured, "custom", undefined, "my-model")).toBe(configured);
    });

    it("names /v1/messages for a custom model the runtime calls on the Anthropic API", () => {
        const anthropicHelp = "Enter the provider's base URL and Agent Mesh adds /v1/messages.";
        expect(resolveApiBaseHelpText(configured, "custom", "anthropic/", "claude-sonnet-4-6")).toBe(anthropicHelp);
        expect(resolveApiBaseHelpText(configured, "custom", "litellm_proxy/", "claude-sonnet-4-6")).toBe(anthropicHelp);
    });

    it("keeps the provider's own hint for any other provider, whatever the prefix", () => {
        expect(resolveApiBaseHelpText(configured, "openai", "anthropic", "claude-sonnet-4-6")).toBe(configured);
        expect(resolveApiBaseHelpText(undefined, "anthropic", "anthropic", "claude-sonnet-4-6")).toBeUndefined();
    });
});

describe("buildModelPayload apiBase", () => {
    it("clears a malformed apiBase on a hidden-field provider (typo)", () => {
        const payload = buildModelPayload(formData({ provider: "anthropic", modelName: "claude-sonnet-4-6", apiBase: "https:stale.example.com" }));

        expect(payload.apiBase).toBe("");
    });

    it("preserves a well-formed custom apiBase on a hidden-field provider", () => {
        const payload = buildModelPayload(formData({ provider: "anthropic", modelName: "claude-sonnet-4-6", apiBase: "https://my-proxy.example.com" }));

        expect(payload.apiBase).toBe("https://my-proxy.example.com");
    });

    it("sends the user-entered value for providers that show the field", () => {
        const payload = buildModelPayload(formData({ provider: "custom", modelName: "openai/gpt-4", apiBase: "https://lite-llm.example.com" }));

        expect(payload.apiBase).toBe("https://lite-llm.example.com");
    });

    it("clears a malformed apiBase on every hidden-field provider", () => {
        for (const provider of ["openai", "anthropic", "bedrock", "google_ai_studio", "vertex_ai"] as const) {
            const payload = buildModelPayload(formData({ provider, modelName: "x", apiBase: "https:stale.example.com" }));
            expect(payload.apiBase, `provider=${provider}`).toBe("");
        }
    });

    it("preserves a well-formed apiBase on every hidden-field provider", () => {
        for (const provider of ["openai", "anthropic", "bedrock", "google_ai_studio", "vertex_ai"] as const) {
            const payload = buildModelPayload(formData({ provider, modelName: "x", apiBase: "https://my-proxy.example.com" }));
            expect(payload.apiBase, `provider=${provider}`).toBe("https://my-proxy.example.com");
        }
    });
});
