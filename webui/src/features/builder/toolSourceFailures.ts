// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Agent, RuntimeStatusValue } from "@/types";

import { resolveByIdentity } from "./builderUtils";

export interface ToolSourceFailure {
    agentName: string;
    connectorName?: string;
}

export interface SessionComponent {
    id?: string;
    name: string;
    type?: string;
    origin?: string;
}

const UNSETTLED_RUNTIME_STATUSES: ReadonlySet<RuntimeStatusValue> = new Set(["starting", "disconnected"]);

function isSessionAgent(component: SessionComponent): boolean {
    return component.type === "agent" && component.origin !== "platform";
}

export function hasSessionAgent(components: readonly SessionComponent[] | undefined): boolean {
    return (components ?? []).some(isSessionAgent);
}

function platformAgentFor(component: SessionComponent, agents: readonly Agent[] | undefined): Agent | undefined {
    return resolveByIdentity(component, agents ?? []);
}

export function toolSourceFailures(components: readonly SessionComponent[] | undefined, agents: readonly Agent[] | undefined, connectors: ReadonlyArray<{ id: string; name: string }> | undefined): ToolSourceFailure[] {
    const failures: ToolSourceFailure[] = [];
    for (const component of (components ?? []).filter(isSessionAgent)) {
        for (const reg of platformAgentFor(component, agents)?.toolRegistrations ?? []) {
            if (reg.status !== "failed") continue;
            failures.push({ agentName: component.name, connectorName: connectors?.find(c => c.id === reg.connectorId)?.name });
        }
    }
    return failures;
}

export function awaitingToolRegistrations(components: readonly SessionComponent[] | undefined, agents: readonly Agent[] | undefined): boolean {
    return (components ?? []).filter(isSessionAgent).some(component => {
        const status = platformAgentFor(component, agents)?.runtimeStatus?.status;
        return status !== undefined && UNSETTLED_RUNTIME_STATUSES.has(status);
    });
}

export function describeToolSourceFailures(failures: readonly ToolSourceFailure[]): string {
    const parts = failures.map(f => `“${f.agentName}” could not load tools from ${connectorPhrase(f.connectorName)}`);
    const manyAgents = new Set(failures.map(f => f.agentName)).size > 1;
    const manyConnectors = new Set(failures.map(f => f.connectorName)).size > 1;
    const consequence = `so ${manyAgents ? "they are" : "it is"} running without ${manyConnectors ? "those connectors’" : "that connector’s"} tools`;
    const remedy = manyConnectors ? "Check those connectors’ configuration" : "Check the connector’s configuration";
    return `${parts.join("; ")}, ${consequence}. ${remedy}, then redeploy.`;
}

function connectorPhrase(connectorName: string | undefined): string {
    return connectorName ? `the “${connectorName}” connector` : "a connector";
}
