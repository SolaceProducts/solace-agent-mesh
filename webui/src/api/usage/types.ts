// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PaginatedResponse } from "../common";

export type UsageMoney = `${number}.${number}`;

export type UsageTimestamp = `${number}-${number}-${number}T${number}:${number}:${number}.${number}Z`;

export const USAGE_SCOPES = ["tenant", "self"] as const;
export type UsageScope = (typeof USAGE_SCOPES)[number];

export const USAGE_PERIODS = ["this_month", "last_month", "last_7_days", "last_30_days", "last_90_days"] as const;
export type UsagePeriod = (typeof USAGE_PERIODS)[number];

export type UsageWindowParams = { period: UsagePeriod } | { startDate: string; endDate: string };

export type UsageKind = "workflows" | "agents" | "users" | "models";
export type UsageDetailByNameKind = Exclude<UsageKind, "users">;

export type UsagePopulation = "all" | "spenders";

export type UsageSortField = "cost" | "activities" | "costPerActivity" | "shareOfSpend" | "name" | "uncachedInputCost" | "outputCost" | "cacheReadCost" | "cacheWriteCost";
export type UsageUserSortField = UsageSortField | "budget" | "budgetUsage";
export type UsageModelSortField = UsageSortField | "model";
export type UsageAnySortField = UsageUserSortField | UsageModelSortField;
export type UsageSortFieldFor<K extends UsageKind> = K extends "users" ? UsageUserSortField : K extends "models" ? UsageModelSortField : UsageSortField;
export type UsageSortDir = "asc" | "desc";

export interface UsageListParams<K extends UsageKind = UsageKind> {
    window: UsageWindowParams;
    pageNumber: number;
    pageSize: number;
    sortBy: UsageSortFieldFor<K>;
    sortDir: UsageSortDir;
    expandDaily?: boolean;
    name?: string;
    population?: UsagePopulation;
    budgetId?: K extends "users" ? string : never;
}

export type UsageSubjectRef = { id: string } | { bookedName: string };

export interface UsageWindow {
    startTime: UsageTimestamp;
    endTime: UsageTimestamp;
}

export interface UsageCostByBucket {
    uncachedInput: UsageMoney;
    output: UsageMoney;
    cacheRead: UsageMoney;
    cacheWrite: UsageMoney;
}

export interface UsageTokens {
    uncachedInput: number;
    output: number;
    cacheRead: number;
    cacheWrite: number;
}

export interface UsageCostByKind {
    users: UsageMoney;
    agents: UsageMoney;
    workflows: UsageMoney;
    models: UsageMoney;
}

export interface UsageTotals {
    cost: UsageMoney;
    costByBucket: UsageCostByBucket;
    tokens: UsageTokens;
    calls: number;
    unpricedCalls: number;
    activities: number;
}

export interface UsageSubjectTotals {
    cost: UsageMoney;
    activities: number;
    costPerActivity: UsageMoney | null;
    shareOfSpend: number | null;
    costByBucket: UsageCostByBucket;
    tokens: UsageTokens;
    calls: number;
    unpricedCalls: number;
}

export interface UsageDay {
    date: string;
    cost: UsageMoney;
    tokens: UsageTokens;
    activities: number;
}

export interface UsageComparison {
    cost: UsageMoney;
    changePercent: number | null;
}

export interface UsageTopUp {
    amount: UsageMoney;
    expiresTime: UsageTimestamp;
}

export const USAGE_BUDGET_STATUSES = ["ok", "approaching", "exceeded", "unlimited"] as const;
export type UsageBudgetStatus = (typeof USAGE_BUDGET_STATUSES)[number];
export const USAGE_BUDGET_SOURCES = ["assigned", "default"] as const;
export type UsageBudgetSource = (typeof USAGE_BUDGET_SOURCES)[number];

export interface UsageBudget {
    id: string;
    name: string;
    source: UsageBudgetSource;
    amount: UsageMoney | null;
    warningPercentage: number | null;
    topUp: UsageTopUp | null;
    effectiveAmount: UsageMoney | null;
    periodStart: UsageTimestamp;
    periodEnd: UsageTimestamp;
    periodSpend: UsageMoney;
    usagePercent: number | null;
    status: UsageBudgetStatus;
}

export interface UsageBudgetWindow {
    windowBudget: UsageMoney;
    usagePercent: number;
}

export interface UsageUser {
    email: string | null;
    isSystemUser: boolean;
}

interface UsageReportHeader {
    scope: UsageScope;
    dataAsOf: UsageTimestamp | null;
    historyComplete: boolean;
}

export interface UsageSavings extends UsageReportHeader {
    window: UsageWindow;
    currency: string;
    avoidedInputTokens: number | null;
    avoidedCost: UsageMoney | null;
    calls: number;
    measuredCalls: number;
    unpricedCalls: number;
}

export interface UsageSummary extends UsageReportHeader {
    window: UsageWindow;
    comparisonWindow: UsageWindow | null;
    currency: string;
    totals: UsageTotals;
    costByKind: UsageCostByKind;
    comparison: UsageComparison | null;
    meshBudget: UsageBudget | null;
    daily: UsageDay[];
}

export interface UsageRow {
    id: string | null;
    name: string | null;
    bookedName: string | null;
    cost: UsageMoney;
    activities: number;
    costPerActivity: UsageMoney | null;
    shareOfSpend: number | null;
    costByBucket: UsageCostByBucket;
    tokens: UsageTokens;
    calls: number;
    unpricedCalls: number;
    daily: UsageDay[] | null;
}

export interface UsageServedModel {
    name: string | null;
    provider: string | null;
    cost: UsageMoney;
    activities: number;
    costByBucket: UsageCostByBucket;
    tokens: UsageTokens;
    calls: number;
    unpricedCalls: number;
}

export interface UsageModelRow extends UsageRow {
    models: UsageServedModel[];
}

export interface UsageUserRow extends UsageRow {
    user: UsageUser | null;
    budget: UsageBudget | null;
    budgetUsage: UsageBudgetWindow | null;
}

export type UsageRowFor<K extends UsageKind> = K extends "users" ? UsageUserRow : K extends "models" ? UsageModelRow : UsageRow;
export type UsageListResponse<K extends UsageKind> = PaginatedResponse<UsageRowFor<K>>;

export interface UsageSubjectDetail extends UsageReportHeader {
    id: string | null;
    name: string;
    bookedName: string | null;
    window: UsageWindow;
    totals: UsageSubjectTotals;
    daily: UsageDay[];
}

export interface UsageModelDetail extends UsageSubjectDetail {
    models: UsageServedModel[];
}

export const USAGE_NODE_KINDS = ["agent", "workflow", "tool", "switch", "loop", "map", "decision"] as const;
export type UsageNodeKind = (typeof USAGE_NODE_KINDS)[number];

export interface UsageWorkflowNode {
    nodeId: string;
    name: string;
    kind: UsageNodeKind | null;
    cost: UsageMoney;
    shareOfWorkflow: number;
    changePercent: number | null;
}

export interface UsageWorkflowDetail extends UsageSubjectDetail {
    comparisonWindow: UsageWindow | null;
    nodes: UsageWorkflowNode[];
}

export const USAGE_ROOT_KINDS = ["agent", "workflow"] as const;
export type UsageRootKind = (typeof USAGE_ROOT_KINDS)[number];

export interface UsageRoot {
    kind: UsageRootKind;
    id: string | null;
    name: string;
    bookedName: string | null;
    cost: UsageMoney;
    shareOfUser: number;
}

export interface UsageUserDetail extends UsageReportHeader {
    id: string | null;
    name: string | null;
    user: UsageUser | null;
    window: UsageWindow;
    totals: UsageSubjectTotals;
    daily: UsageDay[];
    budget: UsageBudget | null;
    topRoots: UsageRoot[];
    otherCost: UsageMoney;
    otherShareOfUser: number;
}

export interface UsageMonth {
    month: string;
    cost: UsageMoney;
    budgetAmount: UsageMoney | null;
    partialFromDate: string | null;
}

export interface UsageSelf extends UsageUserDetail {
    months: UsageMonth[];
}

export type UsageDetailFor<K extends UsageKind> = K extends "workflows" ? UsageWorkflowDetail : K extends "users" ? UsageUserDetail : K extends "models" ? UsageModelDetail : UsageSubjectDetail;
