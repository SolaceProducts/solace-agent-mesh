// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isBlockedSessionComponent, normalizeComponentName } from "./builderUtils";

interface BlockableComponent {
    name: string;
    type?: string;
    origin?: string;
    status?: string;
    blocked_reason?: unknown;
    depends_on?: string[];
    connectors?: string[];
    skills?: unknown;
    nodes?: unknown;
}

export interface BlockedComponent {
    name: string;
    type: string;
    reason: string;
    heldBack: string[];
}

export const BLOCKED_REASON_FALLBACK = "more information from you";

function namesIn(value: unknown): string[] {
    return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string" && entry !== "") : [];
}

function namesUsedBy(component: BlockableComponent): string[] {
    const nodes: unknown[] = Array.isArray(component.nodes) ? component.nodes : [];
    const nodeNames = nodes
        .flatMap(node => {
            const { agent_name, workflow_name, connector } = (node ?? {}) as Record<string, unknown>;
            return [agent_name, workflow_name, connector];
        })
        .filter((name): name is string => typeof name === "string" && name !== "");
    return [...namesIn(component.depends_on), ...namesIn(component.connectors), ...namesIn(component.skills), ...nodeNames];
}

function heldBackBy(rootName: string, components: readonly BlockableComponent[]): string[] {
    const held = new Set([normalizeComponentName(rootName)]);
    const found: string[] = [];
    let grew = true;
    while (grew) {
        grew = false;
        for (const component of components) {
            const key = normalizeComponentName(component.name);
            if (component.origin === "platform" || held.has(key)) continue;
            if (namesUsedBy(component).some(used => held.has(normalizeComponentName(used)))) {
                held.add(key);
                found.push(component.name);
                grew = true;
            }
        }
    }
    return found;
}

export function blockedReasonOf(component: Pick<BlockableComponent, "blocked_reason">): string {
    let reason = typeof component.blocked_reason === "string" ? component.blocked_reason.trim() : "";
    while (reason.endsWith(".")) reason = reason.slice(0, -1).trimEnd();
    return reason || BLOCKED_REASON_FALLBACK;
}

export function blockedSessionComponents(components: readonly BlockableComponent[] | undefined): BlockedComponent[] {
    const all = components ?? [];
    return all.filter(isBlockedSessionComponent).map(c => ({ name: c.name, type: c.type ?? "", reason: blockedReasonOf(c), heldBack: heldBackBy(c.name, all) }));
}
