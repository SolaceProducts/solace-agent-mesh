// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MessageFE } from "@/types";

import type { BuildManifest } from "./hooks/useBuilderManifest";

const FINISHED_STATUSES: ReadonlySet<string | undefined> = new Set(["validated", "deployed", "error", "blocked"]);

function hasUnfinishedSessionComponent(manifest: BuildManifest): boolean {
    return (manifest.components ?? []).some(c => c.origin !== "platform" && !FINISHED_STATUSES.has(c.status));
}

export function isApprovedBuildInterrupted(args: {
    planApproved: boolean;
    isApplyingChanges: boolean;
    isResponding: boolean;
    isCancelling: boolean;
    turnEndedAbnormally: boolean;
    hasDeployFailure: boolean;
    isBuildActivating: boolean;
    manifest: BuildManifest | null;
}): boolean {
    if (!args.planApproved || args.isApplyingChanges || args.isResponding || args.isCancelling || !args.turnEndedAbnormally) return false;
    if (args.hasDeployFailure || args.isBuildActivating || !args.manifest) return false;
    return hasUnfinishedSessionComponent(args.manifest);
}

export type ResumeSurface = "plan-card" | "timeline" | "none";

export function resumeSurface(args: { hostInterrupted: boolean; isCancelling: boolean }): ResumeSurface {
    if (args.isCancelling) return "none";
    return args.hostInterrupted ? "plan-card" : "timeline";
}

export function borrowedStepsSource(messages: readonly MessageFE[], args: { isInterrupted: boolean; isTimelineCollapsed: boolean }): MessageFE | undefined {
    if (!args.isInterrupted || args.isTimelineCollapsed) return undefined;
    const last = messages.findLast(m => !m.isUser);
    if (!last || last.progressUpdates?.length || !last.taskId) return undefined;
    return messages.findLast(m => !m.isUser && m.taskId === last.taskId && !!m.progressUpdates?.length && !!m.metadata?.messageId);
}

export function activitySuppressionFor(base: string | undefined, borrowedFrom: MessageFE | undefined): string | undefined {
    return borrowedFrom?.metadata?.messageId ?? base;
}
