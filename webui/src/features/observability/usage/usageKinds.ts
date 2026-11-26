// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageAnySortField, UsageKind, UsageRow, UsageServedModel, UsageSortDir, UsageUser } from "@/api/usage";

export const USAGE_KINDS: readonly UsageKind[] = ["agents", "workflows", "users", "models"];
export const USAGE_KINDS_WITHOUT_USERS: readonly UsageKind[] = USAGE_KINDS.filter(kind => kind !== "users");

interface KindConfig {
    tab: string;
    column: string;
    plural: string;
    defaultSort: { key: UsageAnySortField; dir: UsageSortDir };
    rowsOverlap: boolean;
    outside?: OutsideSpendConfig;
}

export interface OutsideSpendConfig {
    leftOut: (cost: string) => string;
}

export const KIND_CONFIG: Record<UsageKind, KindConfig> = {
    workflows: {
        tab: "Workflows",
        column: "Workflow",
        plural: "Workflows",
        defaultSort: { key: "cost", dir: "desc" },
        rowsOverlap: true,
        outside: {
            leftOut: cost =>
                `This breakdown covers only the cost within workflows. It doesn't include ${cost} outside workflows, such as agents used on their own and AI usage no agent made, like AI-generated chat titles. The Users and Models tabs cover all spend.`,
        },
    },
    agents: {
        tab: "Agents",
        column: "Agent",
        plural: "Agents",
        defaultSort: { key: "cost", dir: "desc" },
        rowsOverlap: false,
        outside: {
            leftOut: cost =>
                `This breakdown covers only the cost of agents. It doesn't include ${cost} of AI usage no agent made, such as workflow decision steps, AI-generated chat titles, creating agents from a description, scoring in Evaluations, and model connection tests.`,
        },
    },
    users: { tab: "Users", column: "User", plural: "Users", defaultSort: { key: "budgetUsage", dir: "desc" }, rowsOverlap: false },
    models: { tab: "Models", column: "Name", plural: "Models", defaultSort: { key: "cost", dir: "desc" }, rowsOverlap: false },
};

export const TOP_PAGE_SIZE = 25;
export const SPENDERS_SHOWN = 5;
export const SPENDERS_CARD_MIN_HEIGHT = 275;
export const TABLE_PAGE_SIZE = TOP_PAGE_SIZE;

export const WORKFLOW_OVERLAP_HELP = "A call in a nested workflow counts in its own workflow's row and in the top-level workflow's row, so the rows can add up to more than the total.";

export const UNKNOWN_USER_LABEL = "Unknown user";
export const UNKNOWN_USER_HELP = "Cost under a sign-in that matches no current user in this deployment. There are no user details.";
const MISSING_NAME = "—";

export function isUsageKind(value: string | null): value is UsageKind {
    return value !== null && (USAGE_KINDS as readonly string[]).includes(value);
}

export function isUnknownUser(kind: UsageKind, row: Pick<UsageRow, "id" | "name"> & { user?: UsageUser | null }): boolean {
    return kind === "users" && (row.id === null || (row.name === null && !row.user));
}

export function displayName(kind: UsageKind, row: Pick<UsageRow, "id" | "name"> & { user?: UsageUser | null }): string {
    if (isUnknownUser(kind, row)) return UNKNOWN_USER_LABEL;
    return row.name ?? MISSING_NAME;
}

export function servedModelKey(model: Pick<UsageServedModel, "name" | "provider">): string {
    return `${model.provider ?? ""}|${model.name ?? ""}`;
}

export function subjectKey(row: Pick<UsageRow, "id" | "bookedName" | "name">): string | null {
    return row.id ?? row.bookedName ?? row.name;
}

export function rowKey(row: Pick<UsageRow, "id" | "bookedName" | "name">, index: number): string {
    return subjectKey(row) ?? `#${index}`;
}
