// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { DroppedDeployment, DroppedResource, RemovalResult, RemovalTarget } from "@/api/builder/applyTypes";

export interface DroppedView {
    status: "loading" | "ready" | "unavailable";
    resources: readonly DroppedResource[];
    checkFailed: boolean;
    failures: ReadonlyMap<string, string>;
}

export const NO_DROPPED: DroppedView = { status: "unavailable", resources: [], checkFailed: false, failures: new Map() };

const DEPLOYMENT_PHRASE: Record<DroppedDeployment, string> = {
    deployed: "still deployed and running",
    deploy_failed: "its last deploy failed, so it may still be running",
    undeploy_failed: "its undeploy failed, so it is still running",
    not_deployed: "saved, but not deployed",
};

export function deploymentPhrase(deployment: DroppedDeployment): string {
    return DEPLOYMENT_PHRASE[deployment];
}

export function removableResources(resources: readonly DroppedResource[]): DroppedResource[] {
    return resources.filter(r => r.createdBySession);
}

export function kindLabel(kind: DroppedResource["kind"]): string {
    return kind === "workflow" ? "workflow" : "agent";
}

export function pageLabel(kind: DroppedResource["kind"]): string {
    return kind === "workflow" ? "Workflows" : "Agents";
}

export function targetsOf(snapshot: readonly DroppedResource[]): RemovalTarget[] {
    return snapshot.map(({ kind, id }) => ({ kind, id }));
}

export function removalProblem(result: RemovalResult): string {
    const reason = result.error?.message ?? "the platform did not say why";
    return result.status === "refused" ? `Not removed: ${reason}` : `Could not remove it: ${reason}`;
}

export function problemsOf(results: readonly RemovalResult[]): Map<string, string> {
    const problems = new Map<string, string>();
    for (const result of results) {
        if (result.status === "failed" || result.status === "refused") problems.set(result.id, removalProblem(result));
    }
    return problems;
}
