// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Gateway, GatewayDeployment, GatewayNetworkingDetails } from "@/types";

import { mockGateways } from "./gateways";
import { toV2Instant } from "./platformWireV2";

export function toEntrypointV2(gateway: Gateway & { publicUrl?: string }) {
    const { runtimeStatus, deployed } = gateway;
    return {
        id: gateway.id ?? "",
        type: "entrypoint",
        entrypointType: gateway.type,
        slug: gateway.slug ?? null,
        name: gateway.name,
        description: gateway.description,
        publicUrl: gateway.publicUrl ?? null,
        values: gateway.values,
        deploymentStatus: gateway.deploymentStatus ?? "not_deployed",
        syncStatus: gateway.syncStatus ?? null,
        deployed: deployed ? { name: deployed.name, description: deployed.description, entrypointType: deployed.type, values: deployed.values, deployedTime: toV2Instant(deployed.deployedAt), deployedBy: deployed.deployedBy } : null,
        runtimeStatus: {
            status: runtimeStatus?.status ?? "stopped",
            problems: runtimeStatus?.status === "degraded" ? runtimeStatus.problems.map(p => ({ key: p.id, severity: p.severity, detail: p.detail, sinceTime: toV2Instant(p.since) })) : [],
        },
        createdBy: gateway.createdBy ?? "",
        updatedBy: gateway.updatedBy ?? "",
        createdTime: toV2Instant(gateway.createdAt),
        updatedTime: toV2Instant(gateway.updatedAt),
    };
}

export function mockEntrypointV2(id: string, overrides: Partial<Gateway> & { publicUrl?: string } = {}) {
    const gateway = mockGateways.find(g => g.id === id);
    if (!gateway) throw new Error(`no mock gateway ${id}`);
    return toEntrypointV2({ ...gateway, ...overrides });
}

export function savedEntrypointV2(id: string, body: unknown, stored: Gateway | undefined = mockGateways.find(g => g.id === id)) {
    const sent = typeof body === "object" && body !== null ? body : {};
    return { ...toEntrypointV2({ name: "", description: "", type: "", values: {}, ...stored, id }), ...sent };
}

export function toEntrypointDeploymentV2(deployment: GatewayDeployment) {
    return {
        id: deployment.id,
        type: "entrypointDeployment",
        entrypointId: deployment.gatewayId,
        status: deployment.status,
        action: deployment.action,
        failure: deployment.errorMessage ? { reason: "runtime_error", message: deployment.errorMessage, errorId: null } : null,
        createdBy: deployment.createdBy ?? "",
        createdTime: toV2Instant(deployment.createdAt),
        updatedTime: toV2Instant(deployment.updatedAt),
    };
}

export function succeededDeploymentV2(body: unknown) {
    const { entrypointId = "", action = "deploy" } = (typeof body === "object" && body !== null ? body : {}) as Partial<Pick<GatewayDeployment, "action">> & { entrypointId?: string };
    const now = new Date().toISOString();
    return toEntrypointDeploymentV2({ id: `deployment-${entrypointId}`, gatewayId: entrypointId, action, status: "success", createdBy: "test@example.com", updatedBy: "test@example.com", createdAt: now, updatedAt: now });
}

export function toNetworkingDetailsV2(details: Partial<GatewayNetworkingDetails> = {}) {
    return {
        dnsName: details.dnsName ?? null,
        port: details.port ?? null,
        url: details.url ?? null,
        webhookUrl: details.webhookUrl ?? null,
        apiPaths: details.apiPaths ?? [],
        publicUrl: details.publicUrl ?? null,
    };
}
