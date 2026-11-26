// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

export function isComponentDone(component: { origin?: string; status?: string }): boolean {
    return component.origin === "platform" || component.status === "validated" || component.status === "deployed";
}

export function isWorkflowChildAgentDone(agentName: string, componentData: Map<string, ComponentCanvasData>, manifest?: BuildManifest | null): boolean {
    const cd = componentData.get(agentName);
    if (cd) return isComponentDone(cd);

    const comp = manifest?.components?.find(c => c.name === agentName);
    return !comp || isComponentDone(comp);
}

export function areWorkflowChildAgentsDone(agentNames: string[], componentData: Map<string, ComponentCanvasData>, manifest?: BuildManifest | null): boolean {
    return agentNames.every(name => isWorkflowChildAgentDone(name, componentData, manifest));
}

type BuildStateEntry = Pick<ComponentCanvasData, "origin" | "status" | "isBuilderResponding" | "isGenerating" | "isQueued" | "isWaitingForInput">;

export function isBuildStopped(entries: Iterable<BuildStateEntry>): boolean {
    let hasFinished = false;
    let hasUnfinished = false;
    for (const entry of entries) {
        if (entry.isWaitingForInput) return false;
        if (entry.origin === "platform") continue;
        if (entry.isBuilderResponding || entry.isGenerating || entry.isQueued) return false;
        if (isComponentDone(entry)) hasFinished = true;
        else hasUnfinished = true;
    }
    return hasFinished && hasUnfinished;
}

export function isBuildInterruptedOnCanvas(hostInterrupted: boolean, entries: Iterable<BuildStateEntry>): boolean {
    return hostInterrupted || isBuildStopped(entries);
}
