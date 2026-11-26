// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type {
    DeploymentStatus,
    Gateway,
    GatewayApiPath,
    GatewayConnectionTestRequest,
    GatewayConnectionTestResponse,
    GatewayDeployment,
    GatewayFieldSchema,
    GatewayNetworkingDetails,
    GatewaySchema,
    SyncStatus,
    WhatsAppMessageTemplate,
    WhatsAppMessageTemplatesRequest,
    WhatsAppMessageTemplatesResponse,
} from "@/types";

import { ApiV2Error, fetchAllPages, instantToIso, pageQuery, PLATFORM_V2_PREFIX, platformV2, type RuntimeStatusV2, toRuntimeStatus } from "../common";

import type { CreateGatewayRequest, GatewayDeploymentRequest } from "./service";

interface DeployedEntrypointV2 {
    name: string;
    description: string;
    entrypointType: string;
    values: Record<string, unknown>;
    deployedTime: string | null;
    deployedBy: string;
}

interface EntrypointV2 {
    id: string;
    entrypointType: string;
    slug: string | null;
    name: string;
    description: string;
    publicUrl: string | null;
    values: Record<string, unknown>;
    deploymentStatus: DeploymentStatus;
    syncStatus: SyncStatus | null;
    deployed: DeployedEntrypointV2 | null;
    runtimeStatus: RuntimeStatusV2;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

interface EntrypointDeploymentV2 {
    id: string;
    entrypointId: string;
    status: GatewayDeployment["status"];
    action: GatewayDeployment["action"];
    failure: { reason: string; message: string; errorId: string | null } | null;
    createdBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

interface EntrypointSchemaV2 {
    id: string;
    type: "entrypointSchema";
    entrypointType: string;
    name: string;
    description: string;
    warning: string | null;
    schema: Record<string, GatewayFieldSchema>;
    requiredEnvVars: string[];
}

interface NetworkingDetailsV2 {
    dnsName: string | null;
    port: number | null;
    url: string | null;
    webhookUrl: string | null;
    apiPaths: GatewayApiPath[];
    publicUrl: string | null;
}

interface TestConnectionResultV2 {
    success: boolean;
    message: string | null;
}

type UpdateGatewayRequest = Pick<Gateway, "name" | "description" | "values"> & { publicUrl?: string };

type SavedGateway = Gateway & { id: string; publicUrl?: string; deployError?: string };

function toGateway(e: EntrypointV2): SavedGateway {
    return {
        id: e.id,
        slug: e.slug ?? undefined,
        name: e.name,
        description: e.description,
        type: e.entrypointType,
        publicUrl: e.publicUrl ?? undefined,
        values: e.values,
        deploymentStatus: e.deploymentStatus,
        syncStatus: e.syncStatus ?? undefined,
        runtimeStatus: toRuntimeStatus(e.runtimeStatus),
        deployed: e.deployed
            ? { name: e.deployed.name, description: e.deployed.description, type: e.deployed.entrypointType, values: e.deployed.values, deployedAt: instantToIso(e.deployed.deployedTime), deployedBy: e.deployed.deployedBy }
            : undefined,
        createdBy: e.createdBy,
        updatedBy: e.updatedBy,
        createdAt: instantToIso(e.createdTime),
        updatedAt: instantToIso(e.updatedTime),
    };
}

function toGatewayDeployment(d: EntrypointDeploymentV2): GatewayDeployment {
    return {
        id: d.id,
        gatewayId: d.entrypointId,
        action: d.action,
        status: d.status,
        errorMessage: d.failure?.message,
        createdBy: d.createdBy,
        updatedBy: d.createdBy,
        createdAt: instantToIso(d.createdTime),
        updatedAt: instantToIso(d.updatedTime),
    };
}

export async function getAllGatewaysV2(): Promise<Gateway[]> {
    const all = await fetchAllPages(params => platformV2.list<EntrypointV2>(`/entrypoints?${pageQuery(params)}`));
    return all.map(toGateway).sort((a, b) => a.name.localeCompare(b.name));
}

export async function getGatewayV2(id: string): Promise<SavedGateway> {
    return toGateway(await platformV2.get<EntrypointV2>(`/entrypoints/${id}`));
}

export async function createGatewayV2({ name, description, type, slug, publicUrl, values }: CreateGatewayRequest): Promise<SavedGateway> {
    const body: Record<string, unknown> = { name, description, entrypointType: type, values };
    if (publicUrl) body.publicUrl = publicUrl;
    if (slug) body.slug = slug;
    return toGateway(await platformV2.post<EntrypointV2>("/entrypoints", body));
}

async function updateGatewayV2(id: string, { name, description, publicUrl, values }: UpdateGatewayRequest): Promise<SavedGateway> {
    return toGateway(await platformV2.patch<EntrypointV2>(`/entrypoints/${id}`, { name, description, publicUrl, values }));
}

export async function deleteGatewayV2(id: string): Promise<void> {
    await platformV2.delete(`/entrypoints/${id}`);
}

export async function deployGatewayV2({ gatewayId, action }: GatewayDeploymentRequest): Promise<GatewayDeployment> {
    try {
        return toGatewayDeployment(await platformV2.post<EntrypointDeploymentV2>("/entrypointDeployments", { entrypointId: gatewayId, action }));
    } catch (error) {
        if (!(error instanceof ApiV2Error) || !error.location?.startsWith(PLATFORM_V2_PREFIX)) throw error;
        const record = await platformV2.get<EntrypointDeploymentV2>(error.location.slice(PLATFORM_V2_PREFIX.length)).catch(() => {
            throw error;
        });
        throw new ApiV2Error(error.status, record.failure?.message ?? error.message, { errorId: error.errorId, validationDetails: error.validationDetails, location: error.location });
    }
}

export async function saveGatewayV2(gatewayId: string | undefined, fields: CreateGatewayRequest): Promise<SavedGateway> {
    const saved = gatewayId ? await updateGatewayV2(gatewayId, fields) : await createGatewayV2(fields);
    if (!fields.deploy) return saved;
    const action = gatewayId && saved.deploymentStatus === "deployed" ? "update" : "deploy";
    try {
        await deployGatewayV2({ gatewayId: saved.id, action });
        return { ...saved, deploymentStatus: "deployed" };
    } catch (error) {
        return { ...saved, deploymentStatus: "deploy_failed", deployError: error instanceof Error ? error.message : undefined };
    }
}

export async function getGatewayDeploymentsV2(gatewayId: string): Promise<GatewayDeployment[]> {
    const all = await fetchAllPages(params => platformV2.list<EntrypointDeploymentV2>(`/entrypointDeployments?entrypointId=${encodeURIComponent(gatewayId)}&${pageQuery(params)}`));
    return all.map(toGatewayDeployment);
}

export async function getGatewayNetworkingDetailsV2(gatewayId: string): Promise<GatewayNetworkingDetails | null> {
    const details = await platformV2.get<NetworkingDetailsV2>(`/entrypoints/${gatewayId}/networkingDetails`);
    if (details.url === null && details.apiPaths.length === 0) return null;
    return {
        dnsName: details.dnsName ?? undefined,
        port: details.port ?? undefined,
        url: details.url ?? "",
        webhookUrl: details.webhookUrl ?? undefined,
        publicUrl: details.publicUrl,
        apiPaths: details.apiPaths,
    };
}

export async function getGatewaySchemasV2(): Promise<GatewaySchema[]> {
    const all = await fetchAllPages(params => platformV2.list<EntrypointSchemaV2>(`/entrypointSchemas?${pageQuery(params)}`));
    return all.map(({ id, type: _type, entrypointType: _entrypointType, warning, ...schema }) => ({ ...schema, type: id, warning: warning ?? undefined }));
}

export async function testGatewayConnectionV2({ type, values, gatewayId }: GatewayConnectionTestRequest): Promise<GatewayConnectionTestResponse> {
    const { success, message } = await platformV2.put<TestConnectionResultV2>("/entrypoints/testConnection", { entrypointType: type, values, entrypointId: gatewayId });
    return { success, message: message ?? "" };
}

export async function getWhatsAppMessageTemplatesV2({ businessAccountId, values, gatewayId }: WhatsAppMessageTemplatesRequest): Promise<WhatsAppMessageTemplatesResponse> {
    const lookup = await platformV2.put<{ templates: WhatsAppMessageTemplate[]; hasMore: boolean }>("/entrypoints/whatsapp/messageTemplates", { businessAccountId, values, entrypointId: gatewayId });
    return { data: lookup.templates.map(template => ({ ...template, status: template.status.toUpperCase() })), hasMore: lookup.hasMore };
}
