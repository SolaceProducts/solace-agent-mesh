// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const REDACTED_SENTINEL = "<REDACTED>";

export type ModelType = "chat" | "decision";

export interface ModelPricing {
    input: string | null;
    output: string | null;
    cacheRead: string | null;
    cacheWrite: string | null;
}

export interface ModelConfig {
    id: string;
    alias: string;
    modelType?: ModelType;
    provider: string | null;
    modelName: string | null;
    apiBase: string | null;
    authType: string | null;
    authConfig: Record<string, unknown>;
    modelParams: Record<string, unknown>;
    maxInputTokens: number | null;
    description: string | null;
    pricing?: ModelPricing | null;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export function isDecisionModel(model: Pick<ModelConfig, "modelType">): boolean {
    return model.modelType === "decision";
}

export function chatModelsOnly<T extends Pick<ModelConfig, "modelType">>(models: readonly T[]): T[] {
    return models.filter(model => !isDecisionModel(model));
}

export function decisionModelsOnly<T extends Pick<ModelConfig, "modelType">>(models: readonly T[]): T[] {
    return models.filter(isDecisionModel);
}

export interface ModelConfigurationListResponse {
    data: ModelConfig[];
}

export interface ModelConfigStatus {
    configured: boolean;
}

export interface ModelDependent {
    id: string;
    name: string;
    type: string;
    deploymentStatus: string;
    mayStillRun?: boolean;
}
