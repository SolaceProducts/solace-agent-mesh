// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";
import type { ModelData } from "@/api/models/service";
import type { ModelConfig } from "@/api/models/types";
import { modelsApi } from "@/api/models/versioned";

import type { AcceptActivationRequest, Activation, ActivationFreeModel, ClickwrapTerms, FreeModelStatus } from "./types";

const ACTIVATION_PATH = "/api/v1/activation";
const FREE_MODEL_PATH = "/api/v1/freeModel";

export async function getActivation(): Promise<Activation> {
    return api.webui.get(ACTIVATION_PATH);
}

export async function signIn(): Promise<Activation> {
    return api.webui.post(`${ACTIVATION_PATH}:signIn`, {});
}

export async function cancelSignIn(): Promise<Activation> {
    return api.webui.post(`${ACTIVATION_PATH}:cancelSignIn`, {});
}

export async function accept(request: AcceptActivationRequest): Promise<Activation> {
    return api.webui.post(`${ACTIVATION_PATH}:accept`, request);
}

export async function getTerms(): Promise<ClickwrapTerms> {
    return api.webui.get(`${ACTIVATION_PATH}/terms`);
}

export async function getFreeModel(): Promise<FreeModelStatus> {
    return api.webui.get(FREE_MODEL_PATH);
}

export async function signInFreeModel(): Promise<Activation> {
    return api.webui.post(`${FREE_MODEL_PATH}:signIn`, {});
}

export async function refreshFreeModel(): Promise<FreeModelStatus> {
    return api.webui.post(`${FREE_MODEL_PATH}:refresh`, {});
}

export async function getModelsConfigured(): Promise<boolean> {
    const { configured } = await modelsApi.getStatus();
    return configured;
}

export const FREE_MODEL_ALIASES: readonly string[] = ["general", "planning"];

const freeModelNamesFor = (alias: string | undefined, freeModel: ActivationFreeModel): string[] => {
    switch (alias?.toLowerCase()) {
        case "general":
            return [freeModel.generalModelName];
        case "planning":
            return [freeModel.planningModelName];
        default:
            return [freeModel.generalModelName, freeModel.planningModelName];
    }
};

const UNPRICED = { input: null, output: null, cacheRead: null, cacheWrite: null };

function freeModelData(alias: string, freeModel: ActivationFreeModel): ModelData {
    return {
        alias,
        provider: freeModel.provider,
        modelName: freeModelNamesFor(alias, freeModel)[0],
        apiBase: freeModel.apiBase,
        description: "",
        authType: "oauth2_delegated",
        authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId },
        modelParams: {},
        maxInputTokens: null,
        pricing: UNPRICED,
    };
}

const isConfigured = (model: ModelConfig) => !!model.provider && !!model.modelName?.trim();

export const callsFreeModel = (model: ModelConfig, freeModel: ActivationFreeModel) =>
    model.authConfig?.grant_id === freeModel.grantId && freeModelNamesFor(model.alias, freeModel).includes(model.modelName ?? "") && model.apiBase === freeModel.apiBase;

export async function setUpFreeModels(freeModel: ActivationFreeModel, replace: boolean): Promise<void> {
    const models = await modelsApi.getAll();
    for (const alias of FREE_MODEL_ALIASES) {
        const existing = models.find(model => model.alias?.toLowerCase() === alias);
        if (existing && (callsFreeModel(existing, freeModel) || (isConfigured(existing) && !replace))) continue;
        const data = freeModelData(alias, freeModel);
        if (existing) {
            await modelsApi.update(existing.id, data);
        } else {
            await modelsApi.create(data);
        }
    }
}
