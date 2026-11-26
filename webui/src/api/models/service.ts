// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ModelPricing, ModelType } from "./types";

export interface ModelData {
    alias: string;
    provider: string;
    modelName: string;
    apiBase: string | null;
    description: string | null;
    authType: string;
    authConfig: Record<string, unknown>;
    modelParams: Record<string, unknown>;
    maxInputTokens: number | null;
    pricing?: ModelPricing;
}

export interface CreateModelData extends ModelData {
    modelType?: ModelType;
}

export interface TestConnectionRequest {
    modelId?: string;
    provider?: string;
    modelName?: string;
    apiBase?: string;
    authConfig: Record<string, unknown>;
    modelParams: Record<string, unknown>;
    modelType?: ModelType;
}

export interface TestConnectionResponse {
    success: boolean;
    message: string;
}

export interface AzureWorkloadIdentitySubject {
    issuer: string;
    namespace: string;
    serviceAccount: string;
    audiences: string[];
}
