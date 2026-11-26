// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { GatewayNetworkingDetails } from "@/types/models";

import { DOC_LINKS } from "@/utils";

export function resolveGatewayBaseUrl(url: string): string {
    if (!url.startsWith("/")) {
        return url;
    }
    let origin = window.location.origin;
    if (import.meta.env.DEV) {
        const backendPort = typeof __SAM_BACKEND_PORT__ === "string" ? __SAM_BACKEND_PORT__ : "";
        if (backendPort) {
            try {
                const parsed = new URL(origin);
                if (parsed.port && parsed.port !== backendPort) {
                    parsed.port = backendPort;
                    origin = parsed.origin;
                }
            } catch {}
        }
    }
    return `${origin}${url}`;
}

export function formatTeamsWebhookUrl(baseUrl: string): string {
    const resolved = resolveGatewayBaseUrl(baseUrl).replace(/\/$/, "");
    return `${resolved}/api/messages`;
}

export function buildEndpointUrl(baseUrl: string, path: string): string {
    const cleanPath = path.startsWith("/") ? path : `/${path}`;
    const resolved = resolveGatewayBaseUrl(baseUrl).replace(/\/$/, "");
    return `${resolved}${cleanPath}`;
}

export function resolveEntrypointEndpointUrl(gatewayType: string, networkingDetails: GatewayNetworkingDetails): string {
    if (networkingDetails.webhookUrl) {
        return resolveGatewayBaseUrl(networkingDetails.webhookUrl);
    }
    if (gatewayType === "teams") {
        return formatTeamsWebhookUrl(networkingDetails.url);
    }
    return buildEndpointUrl(networkingDetails.url, networkingDetails.apiPaths[0]?.path ?? "");
}

export const TEAMS_CONFIG_DOCS_URL = DOC_LINKS.buildingEntrypointsTeams;

export const WEBHOOK_COPIED_MESSAGE = "Webhook URL copied to clipboard";

export const GATEWAY_ENDPOINT_COPIED_MESSAGE = "Entrypoint endpoint copied to clipboard";

export const DEPLOYMENT_TIME_MESSAGE = "Deployment takes approximately 5 minutes.";
