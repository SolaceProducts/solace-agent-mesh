// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Activation, FreeModelStatus } from "@/api/activation";

export const mockFreeModel = { grantId: "grant-1", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };

export const mockOnFreeModel: Activation = { state: "active", signingIn: false, agreements: { eulaClickwrapId: "eula-1" }, freeModelAvailable: true, freeModel: mockFreeModel, freeModelChosen: true };

export function mockFreeCredits(remainingPercent: number): FreeModelStatus {
    return { status: "available", remainingPercent, models: [] };
}

export function mockFreeModelRowV2(alias: "general" | "planning") {
    return {
        id: `model-${alias}`,
        type: "model",
        alias,
        provider: mockFreeModel.provider,
        modelName: alias === "general" ? mockFreeModel.generalModelName : mockFreeModel.planningModelName,
        apiBase: mockFreeModel.apiBase,
        authType: "oauth2_delegated",
        authConfig: { type: "oauth2_delegated", grant_id: mockFreeModel.grantId },
        modelParams: {},
        modelType: "chat",
        description: null,
        maxInputTokens: null,
        pricing: null,
        createdBy: null,
        updatedBy: null,
        createdTime: null,
        updatedTime: null,
    };
}
