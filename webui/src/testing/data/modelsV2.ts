// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ModelConfig, ModelDependent } from "@/api/models/types";

import { toV2Instant } from "./platformWireV2";

export function toModelV2(model: ModelConfig) {
    return {
        id: model.id,
        type: "model",
        alias: model.alias,
        provider: model.provider ?? null,
        modelName: model.modelName ?? null,
        apiBase: model.apiBase ?? null,
        authType: model.authType ?? "",
        authConfig: model.authConfig ?? {},
        modelParams: model.modelParams ?? {},
        modelType: model.modelType ?? "chat",
        description: model.description ?? null,
        maxInputTokens: model.maxInputTokens ?? null,
        pricing: model.pricing ?? null,
        createdBy: model.createdBy || null,
        updatedBy: model.updatedBy || null,
        createdTime: toV2Instant(model.createdAt),
        updatedTime: toV2Instant(model.updatedAt),
    };
}

export function savedModelV2(id: string, body: unknown, stored?: ModelConfig) {
    const sent = typeof body === "object" && body !== null ? body : {};
    const now = new Date().toISOString();
    const base: ModelConfig = stored ?? {
        id,
        alias: "",
        provider: null,
        modelName: null,
        apiBase: null,
        authType: null,
        authConfig: {},
        modelParams: {},
        maxInputTokens: null,
        description: null,
        createdBy: "test-user",
        updatedBy: "test-user",
        createdAt: now,
        updatedAt: now,
    };
    return { ...toModelV2({ ...base, id, updatedAt: now }), ...sent };
}

export const toModelDependentV2 = (dependent: ModelDependent) => ({
    type: "modelDependent",
    key: dependent.id,
    name: dependent.name,
    dependentType: dependent.type,
    deploymentStatus: dependent.deploymentStatus,
    mayStillRun: dependent.mayStillRun ?? false,
});

export const toSupportedModelV2 = (provider: string) => (model: { id: string; label: string }) => ({ type: "supportedModel", key: model.id, label: model.label, provider });
