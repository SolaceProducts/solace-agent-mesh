// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AzureWorkloadIdentitySubject, CreateModelData, ModelData, TestConnectionRequest } from "./service";
import type { ModelConfig, ModelConfigStatus, ModelDependent, ModelType } from "./types";
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
    type ProbedConnection,
    testModelConnectionV2,
    updateModelConfigV2,
} from "./v2";

export interface SupportedModelsOptions {
    apiBase?: string;
    authConfig?: Record<string, unknown>;
    modelParams?: Record<string, unknown>;
    modelType?: ModelType;
}

export interface ModelsApi {
    getAll: () => Promise<ModelConfig[]>;
    get: (id: string) => Promise<ModelConfig>;
    create: (data: CreateModelData) => Promise<ModelConfig>;
    update: (id: string, data: ModelData) => Promise<ModelConfig>;
    delete: (id: string) => Promise<void>;
    getStatus: () => Promise<ModelConfigStatus>;
    getDependents: (id: string) => Promise<ModelDependent[]>;
    testConnection: (data: TestConnectionRequest) => Promise<ProbedConnection>;
    getSupportedModels: (provider: string, modelId?: string, options?: SupportedModelsOptions) => Promise<Array<{ id: string; label: string }>>;
    getSupportedParams: (provider: string, modelName: string) => Promise<string[]>;
    getAzureWorkloadIdentity: () => Promise<AzureWorkloadIdentitySubject>;
}

export const modelsApi: ModelsApi = {
    getAll: fetchModelConfigsV2,
    get: fetchModelByIdV2,
    create: createModelConfigV2,
    update: updateModelConfigV2,
    delete: deleteModelV2,
    getStatus: fetchModelConfigStatusV2,
    getDependents: fetchModelDependentsV2,
    testConnection: testModelConnectionV2,
    getSupportedModels: fetchSupportedModelsByProviderV2,
    getSupportedParams: fetchSupportedParamsV2,
    getAzureWorkloadIdentity: fetchAzureWorkloadIdentityV2,
};
