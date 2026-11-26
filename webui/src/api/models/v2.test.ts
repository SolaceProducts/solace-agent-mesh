// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { buildModelPayload } from "@/features/models/modelProviderUtils";

import { ApiV2Error } from "../common";

import type { CreateModelData, TestConnectionRequest } from "./service";
import { chatModelsOnly } from "./types";
import {
    createModelConfigV2,
    deleteModelV2,
    fetchAzureWorkloadIdentityV2,
    fetchModelByIdV2,
    fetchModelConfigStatusV2,
    fetchModelConfigsV2,
    fetchModelDependentsV2,
    fetchSupportedModelsByProviderV2,
    fetchSupportedParamsV2,
    ModelKeptError,
    testModelConnectionV2,
    updateModelConfigV2,
} from "./v2";

const model = (overrides: Record<string, unknown> = {}) => ({
    id: "m1",
    type: "model",
    alias: "support",
    provider: "openai",
    modelName: "gpt-4o",
    apiBase: null,
    authType: "apikey",
    authConfig: { type: "apikey", api_key: "<REDACTED>" },
    modelParams: {},
    modelType: "chat",
    description: null,
    maxInputTokens: null,
    pricing: null,
    createdBy: null,
    updatedBy: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: nextPage ? 2 : 1 } } });

const fields: ReturnType<typeof buildModelPayload> = {
    alias: "support",
    provider: "openai",
    modelName: "gpt-4o",
    apiBase: "",
    description: null,
    authType: "apikey",
    authConfig: { api_key: "sk-real" },
    modelParams: { temperature: 0.2 },
    maxInputTokens: 128000,
};

describe("models on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status, headers }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0], method: fetchMock.mock.calls[n][1]?.method, body: JSON.parse((fetchMock.mock.calls[n][1]?.body as string) ?? "null") });

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    describe("read", () => {
        test("maps a v2 model onto the ModelConfig the pages render", async () => {
            respond(200, {
                data: model({
                    apiBase: "https://api.openai.com/v1",
                    description: "fast",
                    maxInputTokens: 128000,
                    pricing: { input: "2.5", output: "10", cacheRead: null, cacheWrite: null },
                    createdBy: "jane@example.com",
                    updatedBy: "jane@example.com",
                    updatedTime: "2026-01-03T00:00:00.000Z",
                }),
            });

            const config = await fetchModelByIdV2("m1");

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/models/m1", method: undefined });
            expect(config).toEqual({
                id: "m1",
                alias: "support",
                modelType: "chat",
                provider: "openai",
                modelName: "gpt-4o",
                apiBase: "https://api.openai.com/v1",
                authType: "apikey",
                authConfig: { type: "apikey", api_key: "<REDACTED>" },
                modelParams: {},
                maxInputTokens: 128000,
                description: "fast",
                pricing: { input: "2.5", output: "10", cacheRead: null, cacheWrite: null },
                createdBy: "jane@example.com",
                updatedBy: "jane@example.com",
                createdAt: "2026-01-02T03:04:05.678Z",
                updatedAt: "2026-01-03T00:00:00.000Z",
            });
        });

        test("a decision model keeps the platform's modelType, so chatModelsOnly drops it", async () => {
            respond(200, page([model({ id: "m1", alias: "support" }), model({ id: "m2", alias: "router", modelType: "decision" })]));

            const configs = await fetchModelConfigsV2();

            expect(configs.map(c => c.modelType)).toEqual(["chat", "decision"]);
            expect(chatModelsOnly(configs).map(c => c.alias)).toEqual(["support"]);
        });

        test("an unknown author and an unknown instant map onto v1's empty strings, never the word null", async () => {
            respond(200, { data: model() });

            const config = await fetchModelByIdV2("m1");

            expect(config.createdBy).toBe("");
            expect(config.updatedBy).toBe("");
            expect(config.updatedAt).toBe("");
            expect(config.createdAt).toBe("2026-01-02T03:04:05.678Z");
        });

        test("a field v1 types as nullable stays null, never undefined", async () => {
            respond(200, { data: model({ provider: null, modelName: null }) });

            const config = await fetchModelByIdV2("m1");

            expect(config.provider).toBeNull();
            expect(config.modelName).toBeNull();
            expect(config.apiBase).toBeNull();
            expect(config.description).toBeNull();
            expect(config.pricing).toBeNull();
        });

        test("an instant that is not a v2 instant is rejected rather than rendered", async () => {
            respond(200, { data: model({ createdTime: "2026-01-02T03:04:05Z" }) });

            await expect(fetchModelByIdV2("m1")).rejects.toThrow(TypeError);
        });

        test("the list reads every page", async () => {
            respond(200, page([model({ id: "a", alias: "a" })], 2));
            respond(200, page([model({ id: "b", alias: "b" })]));

            const configs = await fetchModelConfigsV2();

            expect(configs.map(c => c.alias)).toEqual(["a", "b"]);
            expect(fetchMock).toHaveBeenCalledTimes(2);
            expect(call(0).url).toBe("/api/v2/platform/models?pageNumber=1&pageSize=100");
            expect(call(1).url).toBe("/api/v2/platform/models?pageNumber=2&pageSize=100");
        });

        test("status comes from its own resource, not from under /models", async () => {
            respond(200, { data: { configured: true } });

            await expect(fetchModelConfigStatusV2()).resolves.toEqual({ configured: true });
            expect(call(0).url).toBe("/api/v2/platform/modelStatus");
        });
    });

    describe("write", () => {
        test("create posts the model, defaulting the fields v2 types as plain strings", async () => {
            respond(201, { data: model() });

            await createModelConfigV2({ ...fields, modelType: "decision" } as CreateModelData);

            expect(call(0)).toEqual({
                url: "/api/v2/platform/models",
                method: "POST",
                body: {
                    alias: "support",
                    provider: "openai",
                    modelName: "gpt-4o",
                    apiBase: "",
                    description: "",
                    authType: "apikey",
                    authConfig: { api_key: "sk-real" },
                    modelParams: { temperature: 0.2 },
                    maxInputTokens: 128000,
                    modelType: "decision",
                },
            });
        });

        test("create sends pricing only when the form priced the model", async () => {
            respond(201, { data: model() });
            await createModelConfigV2(fields);
            expect(call(0).body).not.toHaveProperty("pricing");

            respond(201, { data: model() });
            await createModelConfigV2({ ...fields, pricing: { input: "2.5", output: "10", cacheRead: null, cacheWrite: null } });
            expect(call(1).body.pricing).toEqual({ input: "2.5", output: "10", cacheRead: null, cacheWrite: null });
        });

        test("update omits pricing entirely when the form did not address it", async () => {
            respond(200, { data: model() });

            await updateModelConfigV2("m1", fields);

            expect(call(0)).toEqual({
                url: "/api/v2/platform/models/m1",
                method: "PATCH",
                body: { alias: "support", provider: "openai", modelName: "gpt-4o", apiBase: "", description: null, authType: "apikey", authConfig: { api_key: "sk-real" }, modelParams: { temperature: 0.2 }, maxInputTokens: 128000 },
            });
            expect(Object.keys(call(0).body)).not.toContain("pricing");
        });

        test("update sends maxInputTokens as an explicit null to clear the limit", async () => {
            respond(200, { data: model() });

            await updateModelConfigV2("m1", { ...fields, maxInputTokens: null });

            expect(call(0).body).toHaveProperty("maxInputTokens", null);
        });

        test("update sends the rates whole when the form addressed them", async () => {
            respond(200, { data: model() });

            await updateModelConfigV2("m1", { ...fields, pricing: { input: null, output: null, cacheRead: null, cacheWrite: null } });

            expect(call(0).body.pricing).toEqual({ input: null, output: null, cacheRead: null, cacheWrite: null });
        });

        test("a 422 reaches the caller with the field it named", async () => {
            respond(422, { message: "Validation failed", errorId: "e1", validationDetails: { "authConfig.api_key": ["cannot be <REDACTED>, which a read sends in place of a stored secret"] } });

            await expect(createModelConfigV2(fields)).rejects.toMatchObject({
                status: 422,
                validationDetails: { "authConfig.api_key": ["cannot be <REDACTED>, which a read sends in place of a stored secret"] },
            });
        });

        test("a 422 folds its field errors into the message v1's client showed", async () => {
            respond(422, { message: "Validation failed", errorId: "e2", validationDetails: { alias: ["is required"] } });

            await expect(createModelConfigV2(fields)).rejects.toThrow("Validation failed: alias: is required");
        });
    });

    describe("delete", () => {
        test("a successful delete sends DELETE and resolves", async () => {
            respond(204);

            await expect(deleteModelV2("m1")).resolves.toBeUndefined();
            expect(call(0)).toMatchObject({ url: "/api/v2/platform/models/m1", method: "DELETE" });
        });

        test("a 502 pointing back at the model rejects as a kept model", async () => {
            const kept = "SAM could not undeploy every agent and workflow that uses this model, so the model was not deleted.";
            respond(502, { message: kept, errorId: "e3" }, { Location: "/api/v2/platform/models/m1" });

            const error = await deleteModelV2("m1").catch((e: unknown) => e);

            expect(error).toBeInstanceOf(ModelKeptError);
            expect(error).toMatchObject({ status: 502, message: kept, errorId: "e3", modelId: "m1", location: "/api/v2/platform/models/m1" });
        });

        test("a failure that points elsewhere stays an ordinary error", async () => {
            respond(502, { message: "Unable to delete model", errorId: "e4" }, { Location: "/api/v2/platform/models/other" });

            const error = await deleteModelV2("m1").catch((e: unknown) => e);

            expect(error).toBeInstanceOf(ApiV2Error);
            expect(error).not.toBeInstanceOf(ModelKeptError);
        });

        test("a refused delete with no Location stays an ordinary error", async () => {
            respond(409, { message: "the model aliases SAM itself runs on cannot be deleted", errorId: "e5" });

            await expect(deleteModelV2("m1")).rejects.not.toBeInstanceOf(ModelKeptError);
        });
    });

    describe("azure workload identity", () => {
        test("unwraps the envelope and returns only the declared subject fields", async () => {
            respond(200, { data: { type: "azureWorkloadIdentity", issuer: "https://oidc.example.com/id/abc", namespace: "sam", serviceAccount: "solace-agent-mesh", audiences: ["api://AzureADTokenExchange"] } });

            const subject = await fetchAzureWorkloadIdentityV2();

            expect(call(0)).toMatchObject({ url: "/api/v2/platform/models/azureWorkloadIdentity", method: undefined });
            expect(subject).toEqual({ issuer: "https://oidc.example.com/id/abc", namespace: "sam", serviceAccount: "solace-agent-mesh", audiences: ["api://AzureADTokenExchange"] });
        });
    });

    describe("dependents", () => {
        const dependent = (overrides: Record<string, unknown> = {}) => ({ type: "modelDependent", key: "d1", name: "Support agent", dependentType: "agent", deploymentStatus: "deployed", ...overrides });

        test("a dependent's key becomes the id, and dependentType the type the dialog splits on", async () => {
            respond(200, page([dependent(), dependent({ key: "d2", name: "Triage", dependentType: "workflow", deploymentStatus: "not_deployed" })]));

            await expect(fetchModelDependentsV2("m1")).resolves.toEqual([
                { id: "d1", name: "Support agent", type: "agent", deploymentStatus: "deployed" },
                { id: "d2", name: "Triage", type: "workflow", deploymentStatus: "not_deployed" },
            ]);
            expect(call(0).url).toBe("/api/v2/platform/models/m1/dependents?pageNumber=1&pageSize=100");
        });

        test("dependents read every page", async () => {
            respond(200, page([dependent()], 2));
            respond(200, page([dependent({ key: "d2" })]));

            await expect(fetchModelDependentsV2("m1")).resolves.toHaveLength(2);
            expect(fetchMock).toHaveBeenCalledTimes(2);
        });
    });

    describe("provider lookups", () => {
        test("supported models are a PUT, and each key becomes the id the picker uses", async () => {
            respond(200, page([{ type: "supportedModel", key: "gpt-4o", label: "gpt-4o", provider: "openai" }]));

            await expect(fetchSupportedModelsByProviderV2("openai", "m1", { apiBase: "https://x", authConfig: { type: "apikey" }, modelType: "chat" })).resolves.toEqual([{ id: "gpt-4o", label: "gpt-4o" }]);
            expect(call(0)).toMatchObject({
                url: "/api/v2/platform/providers/openai/models?pageNumber=1&pageSize=100",
                method: "PUT",
                body: { modelId: "m1", authConfig: { type: "apikey" }, apiBase: "https://x", modelType: "chat" },
            });
        });

        test("supported models leave out what the form did not supply", async () => {
            respond(200, page([]));

            await fetchSupportedModelsByProviderV2("openai");

            expect(call(0).body).toEqual({});
        });

        test("supported models never send modelParams", async () => {
            respond(200, page([]));

            await fetchSupportedModelsByProviderV2("openai", undefined, { modelParams: { temperature: 0.2 }, authConfig: { type: "apikey" } });

            expect(call(0).body).not.toHaveProperty("modelParams");
        });

        test("supported models read every page", async () => {
            respond(200, page([{ type: "supportedModel", key: "a", label: "a", provider: "openai" }], 2));
            respond(200, page([{ type: "supportedModel", key: "b", label: "b", provider: "openai" }]));

            await expect(fetchSupportedModelsByProviderV2("openai")).resolves.toEqual([
                { id: "a", label: "a" },
                { id: "b", label: "b" },
            ]);
            expect(fetchMock).toHaveBeenCalledTimes(2);
        });

        test("a provider name is escaped into the path", async () => {
            respond(200, page([]));

            await fetchSupportedParamsV2("open ai", "gpt-4o");

            expect(call(0).url).toContain("/api/v2/platform/providers/open%20ai/params");
        });

        test("supported params are a PUT that carries no model name, and come back as plain keys", async () => {
            respond(
                200,
                page([
                    { type: "providerParam", key: "temperature" },
                    { type: "providerParam", key: "top_p" },
                ])
            );

            await expect(fetchSupportedParamsV2("openai", "gpt-4o")).resolves.toEqual(["temperature", "top_p"]);
            expect(call(0)).toMatchObject({ url: "/api/v2/platform/providers/openai/params?pageNumber=1&pageSize=100", method: "PUT", body: {} });
        });
    });

    describe("connection probe", () => {
        const request: TestConnectionRequest = { provider: "openai", modelName: "gpt-4o", authConfig: { type: "apikey", api_key: "sk-real" }, modelParams: {} };

        test("a probe that passed reports no message, and the mapper surfaces that as an empty string", async () => {
            respond(200, { data: { success: true, message: null, errorClass: null } });

            await expect(testModelConnectionV2(request)).resolves.toEqual({ success: true, message: "" });
            expect(call(0)).toMatchObject({ url: "/api/v2/platform/models/testConnection", method: "PUT", body: request });
        });

        test("a probe that failed carries its class alongside the sentence", async () => {
            respond(200, { data: { success: false, message: "The provider rejected the credentials.", errorClass: "auth_credentials_invalid" } });

            await expect(testModelConnectionV2(request)).resolves.toEqual({ success: false, message: "The provider rejected the credentials.", errorClass: "auth_credentials_invalid" });
        });

        test("a probe against a saved model sends its id", async () => {
            respond(200, { data: { success: true, message: null, errorClass: null } });

            await testModelConnectionV2({ modelId: "m1", authConfig: {}, modelParams: {} });

            expect(call(0).body).toEqual({ modelId: "m1", authConfig: {}, modelParams: {} });
        });

        test("a 404 for a model that does not exist still rejects", async () => {
            respond(404, { message: "Unable to read model", errorId: "e6" });

            await expect(testModelConnectionV2({ modelId: "gone", authConfig: {}, modelParams: {} })).rejects.toBeInstanceOf(ApiV2Error);
        });
    });
});
