// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ApiV2Error, fetchAllPages, instantToIso, pageQuery, PLATFORM_V2_PREFIX, platformV2 } from "../common";

import type { AzureWorkloadIdentitySubject, CreateModelData, ModelData, TestConnectionRequest, TestConnectionResponse } from "./service";
import type { ModelConfig, ModelConfigStatus, ModelDependent, ModelPricing, ModelType } from "./types";

interface ModelV2 {
    id: string;
    type: string;
    alias: string;
    provider: string | null;
    modelName: string | null;
    apiBase: string | null;
    authType: string;
    authConfig: Record<string, unknown>;
    modelParams: Record<string, unknown>;
    modelType: ModelType;
    description: string | null;
    maxInputTokens: number | null;
    pricing: ModelPricing | null;
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
}

interface ModelStatusV2 {
    configured: boolean;
}

interface AzureWorkloadIdentitySubjectV2 extends AzureWorkloadIdentitySubject {
    type: string;
}

interface ModelDependentV2 {
    type: string;
    key: string;
    name: string;
    dependentType: string;
    deploymentStatus: string;
    mayStillRun: boolean;
}

interface SupportedModelV2 {
    type: string;
    key: string;
    label: string;
    provider: string;
}

interface ProviderParamV2 {
    type: string;
    key: string;
}

interface ModelTestConnectionResultV2 {
    success: boolean;
    message: string | null;
    errorClass: ModelProbeErrorClass | null;
}

export type ModelProbeErrorClass =
    "auth_credentials_invalid" | "auth_permission_denied" | "model_not_found" | "request_rejected" | "rate_limited" | "timeout" | "network_unreachable" | "provider_error" | "config_incomplete" | "no_response" | "invalid_response" | "unknown";

export type ProbedConnection = TestConnectionResponse & { errorClass?: ModelProbeErrorClass };

export class ModelKeptError extends ApiV2Error {
    readonly modelId: string;

    constructor(error: ApiV2Error, modelId: string) {
        super(error.status, error.message, { errorId: error.errorId, validationDetails: error.validationDetails, location: error.location });
        this.name = "ModelKeptError";
        this.modelId = modelId;
    }
}

const modelPath = (id: string) => `/models/${encodeURIComponent(id)}`;

function toModelConfig(m: ModelV2): ModelConfig {
    return {
        id: m.id,
        alias: m.alias,
        modelType: m.modelType,
        provider: m.provider,
        modelName: m.modelName,
        apiBase: m.apiBase,
        authType: m.authType,
        authConfig: m.authConfig,
        modelParams: m.modelParams,
        maxInputTokens: m.maxInputTokens,
        description: m.description,
        pricing: m.pricing,
        createdBy: m.createdBy ?? "",
        updatedBy: m.updatedBy ?? "",
        createdAt: instantToIso(m.createdTime),
        updatedAt: instantToIso(m.updatedTime),
    };
}

const toModelDependent = (d: ModelDependentV2): ModelDependent => ({ id: d.key, name: d.name, type: d.dependentType, deploymentStatus: d.deploymentStatus, mayStillRun: d.mayStillRun });

const toSupportedModel = (m: SupportedModelV2) => ({ id: m.key, label: m.label });

export async function fetchModelConfigsV2(): Promise<ModelConfig[]> {
    const all = await fetchAllPages(params => platformV2.list<ModelV2>(`/models?${pageQuery(params)}`));
    return all.map(toModelConfig);
}

export async function fetchModelByIdV2(id: string): Promise<ModelConfig> {
    return toModelConfig(await platformV2.get<ModelV2>(modelPath(id)));
}

export async function fetchModelConfigStatusV2(): Promise<ModelConfigStatus> {
    const { configured } = await platformV2.get<ModelStatusV2>("/modelStatus");
    return { configured };
}

export async function fetchAzureWorkloadIdentityV2(): Promise<AzureWorkloadIdentitySubject> {
    const { issuer, namespace, serviceAccount, audiences } = await platformV2.get<AzureWorkloadIdentitySubjectV2>("/models/azureWorkloadIdentity");
    return { issuer, namespace, serviceAccount, audiences };
}

export async function fetchModelDependentsV2(id: string): Promise<ModelDependent[]> {
    const all = await fetchAllPages(params => platformV2.list<ModelDependentV2>(`${modelPath(id)}/dependents?${pageQuery(params)}`));
    return all.map(toModelDependent);
}

export async function createModelConfigV2(data: CreateModelData): Promise<ModelConfig> {
    return toModelConfig(await platformV2.post<ModelV2>("/models", toCreateBody(data)));
}

export async function updateModelConfigV2(id: string, data: ModelData): Promise<ModelConfig> {
    return toModelConfig(await platformV2.patch<ModelV2>(modelPath(id), toUpdateBody(data)));
}

export async function deleteModelV2(id: string): Promise<void> {
    try {
        await platformV2.delete(modelPath(id));
    } catch (error) {
        if (error instanceof ApiV2Error && error.location === `${PLATFORM_V2_PREFIX}/models/${id}`) {
            throw new ModelKeptError(error, id);
        }
        throw error;
    }
}

export async function testModelConnectionV2(data: TestConnectionRequest): Promise<ProbedConnection> {
    const { success, message, errorClass } = await platformV2.put<ModelTestConnectionResultV2>("/models/testConnection", data);
    return { success, message: message ?? "", ...(errorClass ? { errorClass } : {}) };
}

export async function fetchSupportedModelsByProviderV2(
    provider: string,
    modelId?: string,
    options?: {
        apiBase?: string;
        authConfig?: Record<string, unknown>;
        modelParams?: Record<string, unknown>;
        modelType?: ModelType;
    }
): Promise<Array<{ id: string; label: string }>> {
    const body: Record<string, unknown> = {};
    if (modelId) body.modelId = modelId;
    if (options?.authConfig) body.authConfig = options.authConfig;
    if (options?.apiBase != null) body.apiBase = options.apiBase;
    if (options?.modelType) body.modelType = options.modelType;

    const all = await fetchAllPages(params => platformV2.listPut<SupportedModelV2>(`/providers/${encodeURIComponent(provider)}/models?${pageQuery(params)}`, body));
    return all.map(toSupportedModel);
}

export async function fetchSupportedParamsV2(provider: string, _modelName: string): Promise<string[]> {
    const all = await fetchAllPages(params => platformV2.listPut<ProviderParamV2>(`/providers/${encodeURIComponent(provider)}/params?${pageQuery(params)}`, {}));
    return all.map(p => p.key);
}

function toCreateBody(data: CreateModelData): Record<string, unknown> {
    const body: Record<string, unknown> = {
        alias: data.alias,
        provider: data.provider,
        modelName: data.modelName,
        apiBase: data.apiBase ?? "",
        description: data.description ?? "",
        authType: data.authType,
        authConfig: data.authConfig,
        modelParams: data.modelParams,
        maxInputTokens: data.maxInputTokens,
    };
    if (data.modelType) body.modelType = data.modelType;
    if (data.pricing !== undefined) body.pricing = data.pricing;
    return body;
}

function toUpdateBody(data: ModelData): Record<string, unknown> {
    const body: Record<string, unknown> = {
        alias: data.alias,
        provider: data.provider,
        modelName: data.modelName,
        apiBase: data.apiBase ?? "",
        description: data.description,
        authType: data.authType,
        authConfig: data.authConfig,
        modelParams: data.modelParams,
        maxInputTokens: data.maxInputTokens,
    };
    if (data.pricing !== undefined) body.pricing = data.pricing;
    return body;
}
