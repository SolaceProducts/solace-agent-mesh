// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";
import type { ModelConfig } from "@/api/models/types";
import { modelsApi } from "@/api/models/versioned";

import { accept, callsFreeModel, cancelSignIn, getActivation, getFreeModel, getModelsConfigured, getTerms, refreshFreeModel, setUpFreeModels, signIn, signInFreeModel } from "./service";

describe("activation service", () => {
    afterEach(() => vi.restoreAllMocks());

    test("getActivation GETs the status", async () => {
        const get = vi.spyOn(api.webui, "get").mockResolvedValue({} as never);
        await getActivation();
        expect(get).toHaveBeenCalledWith("/api/v1/activation");
    });

    test("signIn POSTs the signIn verb with an empty body", async () => {
        const post = vi.spyOn(api.webui, "post").mockResolvedValue({} as never);
        await signIn();
        expect(post).toHaveBeenCalledWith("/api/v1/activation:signIn", {});
    });

    test("cancelSignIn POSTs the cancelSignIn verb with an empty body", async () => {
        const post = vi.spyOn(api.webui, "post").mockResolvedValue({} as never);
        await cancelSignIn();
        expect(post).toHaveBeenCalledWith("/api/v1/activation:cancelSignIn", {});
    });

    test("accept POSTs the accept verb with the path chosen and the documents opened", async () => {
        const post = vi.spyOn(api.webui, "post").mockResolvedValue({} as never);
        await accept({ freeModel: true, openedDocumentIds: ["101"] });
        expect(post).toHaveBeenCalledWith("/api/v1/activation:accept", { freeModel: true, openedDocumentIds: ["101"] });
    });

    test("getTerms GETs the clickwrap's terms", async () => {
        const terms = { agreed: false, checkboxText: "", documents: [] };
        const get = vi.spyOn(api.webui, "get").mockResolvedValue(terms as never);
        await expect(getTerms()).resolves.toEqual(terms);
        expect(get).toHaveBeenCalledWith("/api/v1/activation/terms");
    });

    test("getFreeModel GETs the free model's state", async () => {
        const get = vi.spyOn(api.webui, "get").mockResolvedValue({ status: "available", remainingPercent: 40 } as never);
        await expect(getFreeModel()).resolves.toEqual({ status: "available", remainingPercent: 40 });
        expect(get).toHaveBeenCalledWith("/api/v1/freeModel");
    });

    test("signInFreeModel POSTs the free model's signIn verb with an empty body", async () => {
        const post = vi.spyOn(api.webui, "post").mockResolvedValue({} as never);
        await signInFreeModel();
        expect(post).toHaveBeenCalledWith("/api/v1/freeModel:signIn", {});
    });

    test("refreshFreeModel POSTs the free model's refresh verb with an empty body", async () => {
        const post = vi.spyOn(api.webui, "post").mockResolvedValue({ status: "available", remainingPercent: 80 } as never);
        await expect(refreshFreeModel()).resolves.toEqual({ status: "available", remainingPercent: 80 });
        expect(post).toHaveBeenCalledWith("/api/v1/freeModel:refresh", {});
    });
});

describe("getModelsConfigured", () => {
    afterEach(() => vi.restoreAllMocks());

    test("asks the v2 model status route", async () => {
        const get = vi.spyOn(api.platform, "get").mockResolvedValue(new Response(JSON.stringify({ data: { configured: true } })) as never);
        await expect(getModelsConfigured()).resolves.toBe(true);
        expect(get).toHaveBeenCalledWith("/api/v2/platform/modelStatus", { fullResponse: true });
    });
});

describe("setUpFreeModels", () => {
    afterEach(() => vi.restoreAllMocks());

    const freeModel = { grantId: "1c58a105-c275-4401-b9af-9b1af81a80af", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };
    const freeModelData = (alias: string) => ({
        alias,
        provider: "openai",
        modelName: alias === "planning" ? "free-planning" : "free",
        apiBase: "https://llm.example.test/v1",
        description: "",
        authType: "oauth2_delegated",
        authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId },
        modelParams: {},
        maxInputTokens: null,
        pricing: { input: null, output: null, cacheRead: null, cacheWrite: null },
    });
    const model = (id: string, alias: string, fields: Partial<ModelConfig> = {}) => ({ id, alias, provider: null, modelName: null, apiBase: null, authConfig: {}, ...fields }) as ModelConfig;
    const ownModel = (id: string, alias: string) => model(id, alias, { provider: "anthropic", modelName: "claude", authConfig: { type: "apikey" } });

    function stubModels(models: ModelConfig[]) {
        vi.spyOn(modelsApi, "getAll").mockResolvedValue(models);
        const update = vi.spyOn(modelsApi, "update").mockResolvedValue({} as ModelConfig);
        const create = vi.spyOn(modelsApi, "create").mockResolvedValue({} as ModelConfig);
        return { update, create };
    }

    test("sets the placeholder defaults to the free model and creates a missing one", async () => {
        const { update, create } = stubModels([model("general-id", "general")]);

        await setUpFreeModels(freeModel, false);

        expect(update).toHaveBeenCalledWith("general-id", freeModelData("general"));
        expect(create).toHaveBeenCalledWith(freeModelData("planning"));
    });

    test("replaces the person's own models once they confirmed it", async () => {
        const { update, create } = stubModels([ownModel("general-id", "general"), ownModel("planning-id", "planning")]);

        await setUpFreeModels(freeModel, true);

        expect(update).toHaveBeenCalledWith("general-id", freeModelData("general"));
        expect(update).toHaveBeenCalledWith("planning-id", freeModelData("planning"));
        expect(create).not.toHaveBeenCalled();
    });

    test("leaves the person's own models alone without confirmation", async () => {
        const { update, create } = stubModels([ownModel("general-id", "general"), ownModel("planning-id", "planning")]);

        await setUpFreeModels(freeModel, false);

        expect(update).not.toHaveBeenCalled();
        expect(create).not.toHaveBeenCalled();
    });

    test("changes nothing when the models already call the free model", async () => {
        const onFreeModel = (id: string, alias: string) =>
            model(id, alias, { provider: "openai", modelName: alias === "planning" ? "free-planning" : "free", apiBase: freeModel.apiBase, authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId } });
        const { update, create } = stubModels([onFreeModel("general-id", "general"), onFreeModel("planning-id", "planning")]);

        await setUpFreeModels(freeModel, true);

        expect(update).not.toHaveBeenCalled();
        expect(create).not.toHaveBeenCalled();
    });

    test("repoints a default that calls the free model under the other default's name", async () => {
        const swapped = model("planning-id", "planning", { provider: "openai", modelName: "free", apiBase: freeModel.apiBase, authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId } });
        const { update } = stubModels([model("general-id", "general"), swapped]);

        await setUpFreeModels(freeModel, true);

        expect(update).toHaveBeenCalledWith("planning-id", freeModelData("planning"));
    });
});

describe("callsFreeModel", () => {
    const freeModel = { grantId: "1c58a105-c275-4401-b9af-9b1af81a80af", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };
    const onGrant = (alias: string, modelName: string) => ({ id: alias, alias, provider: "openai", modelName, apiBase: freeModel.apiBase, authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId } }) as unknown as ModelConfig;

    test("matches each default by its own name", () => {
        expect(callsFreeModel(onGrant("general", "free"), freeModel)).toBe(true);
        expect(callsFreeModel(onGrant("planning", "free-planning"), freeModel)).toBe(true);
        expect(callsFreeModel(onGrant("planning", "free"), freeModel)).toBe(false);
    });

    test("matches any other alias by either name", () => {
        expect(callsFreeModel(onGrant("fast", "free-planning"), freeModel)).toBe(true);
        expect(callsFreeModel(onGrant("fast", "other"), freeModel)).toBe(false);
    });
});
