// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { report } from "@/common/product-analytics/report";

import type { BuildManifest } from "./hooks/useBuilderManifest";

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

const STORAGE_PREFIX = "sam.builder.agentCreationStarted.";

const TYPES_THAT_EMIT_AGENT_CREATED = new Set(["agent", "a2a_agent"]);

function agentsToBeCreated(components: ManifestComponent[] | undefined): ManifestComponent[] {
    return (components ?? []).filter(comp => TYPES_THAT_EMIT_AGENT_CREATED.has(comp.type ?? "") && comp.origin !== "platform" && comp.status !== "deployed");
}

function claimReport(sessionId: string, componentName: string): boolean {
    const key = `${STORAGE_PREFIX}${sessionId}.${componentName}`;
    try {
        if (sessionStorage.getItem(key) !== null) return false;
        sessionStorage.setItem(key, "1");
    } catch {}
    return true;
}

export function reportBuilderAgentCreationStarted(components: ManifestComponent[] | undefined, sessionId: string | null | undefined): void {
    for (const component of agentsToBeCreated(components)) {
        if (!sessionId || !component.name || claimReport(sessionId, component.name)) {
            report("agent:creation_started");
        }
    }
}
