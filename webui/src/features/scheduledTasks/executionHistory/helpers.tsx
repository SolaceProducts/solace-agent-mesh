// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { formatEpochTimestampShort } from "@/utils/format";

import type { TaskExecution } from "@/types/scheduled-tasks";

export const PAGE_SIZE = 10;

export const hasChatOutput = (ex: TaskExecution): boolean => ex.status !== "quiet" || Boolean(ex.sessionId);

export function executionDisplayName(ex: TaskExecution): string {
    if (ex.scheduledFor) return formatEpochTimestampShort(ex.scheduledFor);
    return ex.id.slice(0, 8);
}

export const formatExecutionLabel = (ex: TaskExecution): string => {
    const ts = ex.completedAt ?? ex.startedAt ?? ex.scheduledFor;
    return ts ? formatEpochTimestampShort(ts) : "—";
};

export function resolveSelectedExecution(selectedId: string | null, fromQuery: TaskExecution | undefined, executions: TaskExecution[], latest: TaskExecution | null): TaskExecution | null {
    if (!selectedId) return null;
    return fromQuery ?? executions.find(e => e.id === selectedId) ?? (latest?.id === selectedId ? latest : null) ?? null;
}

export function findExecutionForChat(executionId: string, fromQuery: TaskExecution | undefined, executions: TaskExecution[], latest: TaskExecution | null): TaskExecution | null {
    const matchingQuery = fromQuery?.id === executionId ? fromQuery : null;
    return matchingQuery ?? executions.find(e => e.id === executionId) ?? (latest?.id === executionId ? latest : null);
}

export function startOfDayIso(date: string): string | null {
    return date ? new Date(`${date}T00:00:00`).toISOString() : null;
}

export function endOfDayIso(date: string): string | null {
    return date ? new Date(`${date}T23:59:59.999`).toISOString() : null;
}

export interface Breadcrumb {
    label: string;
    onClick?: () => void;
}

export function buildExecutionBreadcrumbs(taskName: string, selectedExecution: TaskExecution | null, onBack: () => void, onClearSelection: () => void): Breadcrumb[] {
    const root = { label: "Scheduled Tasks", onClick: onBack };
    if (!selectedExecution) return [root, { label: taskName }];
    return [root, { label: taskName, onClick: onClearSelection }, { label: formatExecutionLabel(selectedExecution) }];
}

export function formatDurationVerbose(ms: number): string {
    if (ms < 0 || !isFinite(ms)) return "—";
    if (ms < 1000) return `${Math.round(ms)} ms`;
    const totalSeconds = Math.round(ms / 1000);
    const days = Math.floor(totalSeconds / 86400);
    const hours = Math.floor((totalSeconds % 86400) / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    const parts: string[] = [];
    const push = (n: number, unit: string) => {
        if (n > 0) parts.push(`${n} ${unit}${n === 1 ? "" : "s"}`);
    };
    push(days, "day");
    push(hours, "hour");
    push(minutes, "minute");
    push(seconds, "second");
    if (parts.length === 0) return "0 seconds";
    return parts.slice(0, 2).join(" ");
}

export const Metric: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => (
    <div>
        <div className="mb-1 text-xs text-(--secondary-text-wMain)">{label}</div>
        <div className="text-sm">{children}</div>
    </div>
);
