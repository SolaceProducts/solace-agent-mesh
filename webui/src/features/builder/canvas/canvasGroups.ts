// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

export interface ComponentGroup {
    id: string;
    label: string;
    type: "workflow" | "agent" | "gateway" | "connector" | "a2a_agent" | "skill";
    members: Set<string>;
    anchor: string;
}

function labelFor(name: string, manifest: BuildManifest, componentData: Map<string, ComponentCanvasData> | undefined): string {
    const comp = manifest.components?.find(c => c.name === name);
    if (!comp) return name;
    const cd = componentData?.get(name);
    const cfgDisplay = cd?.workflowConfig && (cd.workflowConfig as { display_name?: string }).display_name;
    if (typeof cfgDisplay === "string" && cfgDisplay.trim() !== "") return cfgDisplay;
    return name;
}

function pushStandaloneGroups(
    comps: NonNullable<BuildManifest["components"]>,
    kind: ComponentGroup["type"],
    idPrefix: string,
    claimed: Set<string>,
    groups: ComponentGroup[],
    manifest: BuildManifest,
    componentData: Map<string, ComponentCanvasData> | undefined
) {
    for (const comp of comps) {
        if (comp.type !== kind || comp.origin === "platform" || claimed.has(comp.name)) continue;
        groups.push({
            id: `${idPrefix}:${comp.name}`,
            label: labelFor(comp.name, manifest, componentData),
            type: kind,
            members: new Set<string>([comp.name]),
            anchor: comp.name,
        });
        claimed.add(comp.name);
    }
}

export function groupComponents(manifest: BuildManifest | null, componentData?: Map<string, ComponentCanvasData>): ComponentGroup[] {
    if (!manifest?.components) return [];

    const comps = manifest.components;
    const byName = new Map(comps.map(c => [c.name, c]));
    const claimed = new Set<string>();
    const groups: ComponentGroup[] = [];

    const addConnectorsOf = (compName: string, members: Set<string>) => {
        const m = byName.get(compName);
        if (!m) return;
        for (const dep of [...(m.depends_on ?? []), ...(m.connectors ?? [])]) {
            const depComp = byName.get(dep);
            if (depComp?.type === "connector" && depComp.origin !== "platform") {
                if (!members.has(dep)) members.add(dep);
                claimed.add(dep);
            }
        }
    };

    const addSkillsOf = (compName: string, members: Set<string>) => {
        const m = byName.get(compName);
        if (!m) return;
        for (const dep of m.depends_on ?? []) {
            const depComp = byName.get(dep);
            if (depComp?.type === "skill" && depComp.origin !== "platform") {
                if (!members.has(dep)) members.add(dep);
                claimed.add(dep);
            }
        }
    };

    for (const comp of comps) {
        if (comp.type !== "workflow" || comp.origin === "platform") continue;

        const members = new Set<string>([comp.name]);
        claimed.add(comp.name);

        for (const node of comp.nodes ?? []) {
            if (node.type === "agent" && node.agent_name) {
                members.add(node.agent_name);
                claimed.add(node.agent_name);
            }
        }
        for (const dep of comp.depends_on ?? []) {
            const depComp = byName.get(dep);
            if (depComp?.type === "agent") {
                members.add(dep);
                claimed.add(dep);
            }
        }
        for (const member of [...members]) {
            addConnectorsOf(member, members);
            addSkillsOf(member, members);
        }

        for (const gw of comps) {
            if (gw.type !== "gateway" || gw.origin === "platform") continue;
            if (gw.depends_on?.includes(comp.name)) {
                members.add(gw.name);
                claimed.add(gw.name);
            }
        }
        for (const dep of comp.depends_on ?? []) {
            const depComp = byName.get(dep);
            if (depComp?.type === "gateway" && depComp.origin !== "platform") {
                members.add(dep);
                claimed.add(dep);
            }
        }

        groups.push({
            id: `wf:${comp.name}`,
            label: labelFor(comp.name, manifest, componentData),
            type: "workflow",
            members,
            anchor: comp.name,
        });
    }

    for (const comp of comps) {
        if (comp.type !== "agent" || comp.origin === "platform" || claimed.has(comp.name)) continue;
        const members = new Set<string>([comp.name]);
        claimed.add(comp.name);
        addConnectorsOf(comp.name, members);
        addSkillsOf(comp.name, members);
        groups.push({
            id: `ag:${comp.name}`,
            label: labelFor(comp.name, manifest, componentData),
            type: "agent",
            members,
            anchor: comp.name,
        });
    }

    pushStandaloneGroups(comps, "gateway", "gw", claimed, groups, manifest, componentData);
    pushStandaloneGroups(comps, "connector", "cn", claimed, groups, manifest, componentData);
    pushStandaloneGroups(comps, "a2a_agent", "a2a", claimed, groups, manifest, componentData);
    pushStandaloneGroups(comps, "skill", "sk", claimed, groups, manifest, componentData);

    return groups;
}

export function filterManifestToGroup(manifest: BuildManifest, members: Set<string>): BuildManifest {
    return {
        ...manifest,
        components: manifest.components?.filter(c => members.has(c.name) || c.origin === "platform"),
    };
}

export type GroupStatus = "ready" | "creating" | "queued" | "paused" | "draft" | "waiting" | "error";

export function pausedWhenInterrupted(status: GroupStatus, buildInterrupted: boolean): GroupStatus {
    return buildInterrupted && status === "draft" ? "paused" : status;
}

export function deriveGroupStatus(group: ComponentGroup, manifest: BuildManifest | null, componentData: Map<string, ComponentCanvasData>): GroupStatus {
    if (!manifest?.components) return "queued";
    const groupComps = manifest.components.filter(c => group.members.has(c.name) && c.origin !== "platform");
    if (groupComps.length === 0) return "ready";

    let anyGenerating = false;
    let anyQueued = false;
    let anyWaiting = false;
    let anyError = false;
    let allDone = true;
    for (const c of groupComps) {
        const cd = componentData.get(c.name);
        if (cd?.isWaitingForInput) anyWaiting = true;
        if (cd?.isGenerating || cd?.isBuildFinalizing) anyGenerating = true;
        if (c.status === "error") anyError = true;
        const isDone = c.status === "validated" || c.status === "deployed";
        if (!isDone) {
            allDone = false;
            if (cd?.isQueued) anyQueued = true;
        }
    }

    if (anyGenerating) return "creating";
    if (anyWaiting) return "waiting";
    if (anyError) return "error";
    if (allDone) return "ready";
    if (anyQueued) return "queued";
    return "draft";
}
