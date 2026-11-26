// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type {
    UsageBudget,
    UsageCostByBucket,
    UsageDay,
    UsageKind,
    UsageModelDetail,
    UsageModelRow,
    UsageMoney,
    UsageRoot,
    UsageRow,
    UsageSavings,
    UsageServedModel,
    UsageSubjectDetail,
    UsageSubjectTotals,
    UsageSummary,
    UsageTimestamp,
    UsageTokens,
    UsageUserDetail,
    UsageUserRow,
    UsageWindow,
    UsageWorkflowDetail,
    UsageWorkflowNode,
} from "@/api/usage";

import { parseUsageMoney, sumUsageMoney, toUsageMoney } from "@/utils/usageMoney";

import agentDetailGolden from "./usage/agent_detail.json";
import agentsListGolden from "./usage/agents_list.json";
import modelDetailGolden from "./usage/model_detail.json";
import modelsListDailyGolden from "./usage/models_list_daily.json";
import savingsGolden from "./usage/savings.json";
import savingsBuildingGolden from "./usage/savings_building.json";
import savingsUnpricedGolden from "./usage/savings_unpriced.json";
import selfGolden from "./usage/self.json";
import selfSavingsGolden from "./usage/self_savings.json";
import selfUnattributedGolden from "./usage/self_unattributed.json";
import summaryGolden from "./usage/summary.json";
import summaryBuildingGolden from "./usage/summary_building.json";
import userDetailGolden from "./usage/user_detail.json";
import usersListGolden from "./usage/users_list.json";
import workflowDetailGolden from "./usage/workflow_detail.json";
import workflowDetailByNameGolden from "./usage/workflow_detail_by_name.json";

export const usageGoldens = {
    summary: summaryGolden as UsageSummary,
    summaryBuilding: summaryBuildingGolden as UsageSummary,
    agentsList: agentsListGolden,
    usersList: usersListGolden,
    modelsListDaily: modelsListDailyGolden,
    agentDetail: agentDetailGolden,
    modelDetail: modelDetailGolden,
    workflowDetail: workflowDetailGolden,
    workflowDetailByName: workflowDetailByNameGolden,
    userDetail: userDetailGolden,
    self: selfGolden,
    selfUnattributed: selfUnattributedGolden,
    savings: savingsGolden as UsageSavings,
    savingsUnpriced: savingsUnpricedGolden as UsageSavings,
    savingsBuilding: savingsBuildingGolden as UsageSavings,
    selfSavings: selfSavingsGolden as UsageSavings,
};

const WINDOW: UsageWindow = { startTime: "2026-10-01T00:00:00.000Z", endTime: "2026-10-16T14:05:00.000Z" };
const COMPARISON_WINDOW: UsageWindow = { startTime: "2026-09-01T00:00:00.000Z", endTime: "2026-09-16T14:05:00.000Z" };
const PERIOD: Pick<UsageBudget, "periodStart" | "periodEnd"> = { periodStart: "2026-10-01T00:00:00.000Z", periodEnd: "2026-11-01T00:00:00.000Z" };
const DATA_AS_OF: UsageTimestamp = "2026-10-16T14:03:00.000Z";
export const USAGE_TOTAL_COST: UsageMoney = "1240.5800000000";

const money = (dollars: string) => toUsageMoney(parseUsageMoney(dollars));

function shareOf(part: bigint, whole: bigint): number {
    return whole === 0n ? 0 : (Number(part) / Number(whole)) * 100;
}

function bucketsOf(cost: bigint): UsageCostByBucket {
    const uncachedInput = (cost * 49n) / 100n;
    const output = (cost * 43n) / 100n;
    const cacheRead = (cost * 3n) / 100n;
    return {
        uncachedInput: toUsageMoney(uncachedInput),
        output: toUsageMoney(output),
        cacheRead: toUsageMoney(cacheRead),
        cacheWrite: toUsageMoney(cost - uncachedInput - output - cacheRead),
    };
}

interface RowSeed {
    id?: string | null;
    name: string;
    bookedName?: string;
    cost: string;
    activities: number;
}

let seededId = 0;
const nextId = () => `01a10c9e-${String(++seededId).padStart(4, "0")}-7e77-be6a-3cdc8d0b9000`;

export function usageRow({ id, name, bookedName, cost, activities }: RowSeed): UsageRow {
    const scaled = parseUsageMoney(money(cost));
    const rowId = id === undefined ? nextId() : id;
    return {
        id: rowId,
        name,
        bookedName: rowId === null ? (bookedName ?? name) : null,
        cost: toUsageMoney(scaled),
        activities,
        costPerActivity: activities === 0 ? null : toUsageMoney(scaled / BigInt(activities)),
        shareOfSpend: shareOf(scaled, parseUsageMoney(USAGE_TOTAL_COST)),
        costByBucket: bucketsOf(scaled),
        tokens: { uncachedInput: activities * 4200, output: activities * 610, cacheRead: activities * 300, cacheWrite: activities * 12 },
        calls: activities * 7,
        unpricedCalls: 0,
        daily: null,
    };
}

interface UsageBudgetSeed {
    amount: string | null;
    topUp?: string;
    id?: string;
    name?: string;
}

export function usageBudget({ amount, spend, topUp, warningPercentage = 80, id, name }: UsageBudgetSeed & { spend: string; warningPercentage?: number | null }): UsageBudget {
    const periodSpend = parseUsageMoney(money(spend));
    if (amount === null) {
        return {
            id: id ?? nextId(),
            name: name ?? "Unlimited",
            source: "default",
            amount: null,
            warningPercentage: null,
            topUp: null,
            effectiveAmount: null,
            ...PERIOD,
            periodSpend: toUsageMoney(periodSpend),
            usagePercent: null,
            status: "unlimited",
        };
    }
    const base = parseUsageMoney(money(amount));
    const extra = topUp ? parseUsageMoney(money(topUp)) : 0n;
    const effective = base + extra;
    const usagePercent = effective === 0n ? 100 : shareOf(periodSpend, effective);
    let status: UsageBudget["status"] = "ok";
    if (periodSpend >= effective) status = "exceeded";
    else if (warningPercentage !== null && usagePercent >= warningPercentage) status = "approaching";
    return {
        id: id ?? nextId(),
        name: name ?? `$${amount} budget`,
        source: "assigned",
        amount: toUsageMoney(base),
        warningPercentage,
        topUp: topUp ? { amount: toUsageMoney(extra), expiresTime: PERIOD.periodEnd } : null,
        effectiveAmount: toUsageMoney(effective),
        ...PERIOD,
        periodSpend: toUsageMoney(periodSpend),
        usagePercent,
        status,
    };
}

const USER_DEFAULT_BUDGET = "100";

export const USAGE_BUDGETS = {
    engineering: { id: "0b0d6e70-0000-4000-8000-000000000150", name: "Engineering", amount: "150" },
    default: { id: "0b0d6e70-0000-4000-8000-000000000100", name: "Default user budget", amount: USER_DEFAULT_BUDGET },
} as const;

export function usageUserRow(seed: RowSeed & { email?: string; isSystemUser?: boolean; budget?: UsageBudgetSeed & { source?: UsageBudget["source"] } }): UsageUserRow {
    const row = usageRow(seed);
    const budgetUsageOf = (budget: UsageBudget | null) => (budget?.effectiveAmount ? { windowBudget: budget.effectiveAmount, usagePercent: budget.usagePercent ?? 0 } : null);
    if (row.id === null) {
        const budget = { ...usageBudget({ ...USAGE_BUDGETS.default, spend: seed.cost }), source: "default" as const };
        return { ...row, name: null, bookedName: null, user: null, budget, budgetUsage: budgetUsageOf(budget) };
    }
    const budget = seed.budget ? { ...usageBudget({ ...seed.budget, spend: seed.cost }), ...(seed.budget.source && { source: seed.budget.source }) } : null;
    const user = seed.isSystemUser ? { email: null, isSystemUser: true } : { email: seed.email ?? `${seed.name.split(" ")[0].toLowerCase()}@example.com`, isSystemUser: false };
    return { ...row, user, budget, budgetUsage: budgetUsageOf(budget) };
}

const TOKENS_PER_DOLLAR = { uncachedInput: 38_000n, output: 14_500n, cacheRead: 22_500n, cacheWrite: 2_600n };
const MONEY_SCALE = 10_000_000_000n;

function tokensFor(cost: bigint): UsageTokens {
    const at = (perDollar: bigint) => Number((cost * perDollar) / MONEY_SCALE);
    return { uncachedInput: at(TOKENS_PER_DOLLAR.uncachedInput), output: at(TOKENS_PER_DOLLAR.output), cacheRead: at(TOKENS_PER_DOLLAR.cacheRead), cacheWrite: at(TOKENS_PER_DOLLAR.cacheWrite) };
}

export function usageDaily(total: string, weights: readonly number[], activitiesTotal = weights.reduce((sum, w) => sum + w * 30, 0), startDay = 1): UsageDay[] {
    const scaled = parseUsageMoney(money(total));
    const weightSum = weights.reduce((sum, w) => sum + w, 0);
    let assigned = 0n;
    let assignedActivities = 0;
    return weights.map((weight, index) => {
        const isLast = index === weights.length - 1;
        const cost = isLast ? scaled - assigned : (scaled * BigInt(weight)) / BigInt(weightSum);
        const activities = isLast ? activitiesTotal - assignedActivities : Math.floor((activitiesTotal * weight) / weightSum);
        assigned += cost;
        assignedActivities += activities;
        return { date: `2026-10-${String(startDay + index).padStart(2, "0")}`, cost: toUsageMoney(cost), tokens: tokensFor(cost), activities };
    });
}

const DAILY_WEIGHTS = [55, 56, 58, 60, 37, 38, 65, 67, 69, 69, 68, 41, 40, 68, 67, 66];

export function usageRowDaily(row: Pick<UsageRow, "cost" | "activities">): UsageDay[] {
    return usageDaily(row.cost, DAILY_WEIGHTS, row.activities);
}

export const usageWorkflowRows: UsageRow[] = [
    usageRow({ name: "Invoice Reconciliation", cost: "260.85", activities: 1284 }),
    usageRow({ name: "Vendor Pricing Review", cost: "195.40", activities: 968 }),
    usageRow({ name: "Support Triage", cost: "162.30", activities: 2410 }),
    usageRow({ name: "Contract Summarization", cost: "110.75", activities: 540 }),
    usageRow({ name: "Onboarding Checklist", cost: "78.60", activities: 412 }),
    usageRow({ name: "Expense Audit", cost: "57.40", activities: 286 }),
    usageRow({ id: null, name: "nightly-report", cost: "12.10", activities: 31 }),
    usageRow({ name: "Quarterly Forecast", cost: "0", activities: 0 }),
];

export const usageAgentRows: UsageRow[] = [
    usageRow({ name: "Customer Support", cost: "301.22", activities: 3120 }),
    usageRow({ name: "Code Assistant", cost: "188.40", activities: 940 }),
    usageRow({ name: "Data Analyzer", cost: "142.05", activities: 1210 }),
    usageRow({ name: "Product Researcher", cost: "96.33", activities: 512 }),
    usageRow({ name: "QA Tester", cost: "20.44", activities: 206 }),
    usageRow({ id: null, name: "Orchestrator", bookedName: "OrchestratorAgent", cost: "18.90", activities: 401 }),
    usageRow({ name: "Archivist", cost: "0", activities: 0 }),
];

interface ServedSeed {
    name: string | null;
    provider: string | null;
    share: number;
}

export function usageModelRow(seed: RowSeed, served: readonly ServedSeed[]): UsageModelRow {
    const row = usageRow(seed);
    const costs = splitCost(
        row.cost,
        served.map(model => model.share)
    );
    let assignedActivities = 0;
    const models: UsageServedModel[] = served.map((model, index) => {
        const isLast = index === served.length - 1;
        const activities = isLast ? row.activities - assignedActivities : Math.floor((row.activities * model.share) / 100);
        assignedActivities += activities;
        return {
            name: model.name,
            provider: model.provider,
            cost: costs[index],
            activities,
            costByBucket: bucketsOf(parseUsageMoney(costs[index])),
            tokens: { uncachedInput: activities * 4200, output: activities * 610, cacheRead: activities * 300, cacheWrite: activities * 12 },
            calls: activities * 7,
            unpricedCalls: 0,
        };
    });
    return { ...row, models };
}

export const usageModelRows: UsageModelRow[] = [
    usageModelRow({ name: "General", cost: "452.18", activities: 9842 }, [
        { name: "claude-sonnet-4-6", provider: "anthropic", share: 71.5 },
        { name: "claude-sonnet-4-5", provider: "anthropic", share: 28.5 },
    ]),
    usageModelRow({ name: "Planning", cost: "380.02", activities: 4121 }, [
        { name: "o3", provider: "openai", share: 99.6 },
        { name: null, provider: null, share: 0.4 },
    ]),
    usageModelRow({ name: "Image Generation", cost: "120.75", activities: 233 }, [{ name: "gpt-image-1", provider: "openai", share: 100 }]),
    usageModelRow({ id: null, name: "gpt-4.1-mini", cost: "41.60", activities: 1802 }, [{ name: "gpt-4.1-mini", provider: "openai", share: 100 }]),
];

export const usageUserRows: UsageUserRow[] = [
    usageUserRow({ name: "Parminder Procurement", cost: "412.30", activities: 1842, budget: { amount: "450" } }),
    usageUserRow({ name: "Olive Operations", cost: "96.40", activities: 431, budget: { amount: "200", topUp: "50" } }),
    usageUserRow({ name: "Morgan Admin", cost: "58.20", activities: 260, budget: { amount: "250" } }),
    usageUserRow({ name: "Dana Finance", cost: "40.15", activities: 179, budget: USAGE_BUDGETS.engineering }),
    usageUserRow({ name: "Build Bot", cost: "31.70", activities: 142, budget: USAGE_BUDGETS.engineering, isSystemUser: true }),
    usageUserRow({ name: "Riley Legal", cost: "22.85", activities: 102, budget: { ...USAGE_BUDGETS.default, source: "default" } }),
    usageUserRow({ name: "Jordan Marketing", cost: "15.35", activities: 69, budget: { ...USAGE_BUDGETS.default, source: "default" } }),
    usageUserRow({ name: "Casey Unlimited", cost: "9.80", activities: 40, budget: { amount: null } }),
    { ...usageRow({ name: "", cost: "6.20", activities: 22 }), name: null, user: null, budget: null, budgetUsage: null },
    usageUserRow({ id: null, name: "google-oauth2|1234567890", cost: "3.10", activities: 4 }),
];

export const usageSummary: UsageSummary = {
    scope: "tenant",
    dataAsOf: DATA_AS_OF,
    historyComplete: true,
    window: WINDOW,
    comparisonWindow: COMPARISON_WINDOW,
    currency: "USD",
    totals: {
        cost: USAGE_TOTAL_COST,
        costByBucket: bucketsOf(parseUsageMoney(USAGE_TOTAL_COST)),
        tokens: { uncachedInput: 91234567, output: 12345678, cacheRead: 4567890, cacheWrite: 123456 },
        calls: 182345,
        unpricedCalls: 120,
        activities: 23011,
    },
    costByKind: { users: USAGE_TOTAL_COST, agents: sumUsageMoney(usageAgentRows.map(row => row.cost)), workflows: sumUsageMoney(usageWorkflowRows.map(row => row.cost)), models: USAGE_TOTAL_COST },
    comparison: { cost: "912.1900000000", changePercent: 36 },
    meshBudget: usageBudget({ amount: "2000", spend: "1240.58", topUp: "400" }),
    daily: usageDaily("1240.58", DAILY_WEIGHTS),
};

function splitCost(cost: string, shares: readonly number[]): UsageMoney[] {
    const scaled = parseUsageMoney(cost);
    let assigned = 0n;
    return shares.map((share, index) => {
        const part = index === shares.length - 1 ? scaled - assigned : (scaled * BigInt(Math.round(share * 100))) / 10_000n;
        assigned += part;
        return toUsageMoney(part);
    });
}

const share = (part: string, whole: string) => (parseUsageMoney(whole) === 0n ? 0 : (Number(parseUsageMoney(part)) / Number(parseUsageMoney(whole))) * 100);

function totalsOf(row: UsageRow): UsageSubjectTotals {
    const { cost, activities, costPerActivity, shareOfSpend, costByBucket, tokens, calls, unpricedCalls } = row;
    return { cost, activities, costPerActivity, shareOfSpend, costByBucket, tokens, calls, unpricedCalls };
}

const NODE_PLAN: ReadonlyArray<{ nodeId: string; name: string; kind: UsageWorkflowNode["kind"]; share: number; changePercent: number | null }> = [
    { nodeId: "code_assistant", name: "Code Assistant", kind: "agent", share: 57.4, changePercent: 6.2 },
    { nodeId: "data_analyzer", name: "Data Analyzer", kind: "agent", share: 24.8, changePercent: -2.4 },
    { nodeId: "qa_tester", name: "QA Tester", kind: "agent", share: 7.8, changePercent: 3.1 },
    { nodeId: "review_each", name: "Review Each", kind: "map", share: 5.6, changePercent: null },
    { nodeId: "summarize_sub", name: "Summarize Report", kind: "workflow", share: 2.4, changePercent: 4.4 },
    { nodeId: "old_step", name: "old_step", kind: null, share: 2, changePercent: null },
    { nodeId: "notify", name: "send_email", kind: "tool", share: 0, changePercent: null },
];

const ROOT_PLAN: ReadonlyArray<{ kind: UsageRoot["kind"]; name: string; share: number; hasId: boolean }> = [
    { kind: "agent", name: "Customer Support", share: 46.8, hasId: true },
    { kind: "agent", name: "Data Analyzer", share: 12.6, hasId: true },
    { kind: "agent", name: "Product Researcher", share: 8, hasId: true },
    { kind: "workflow", name: "Vendor Pricing Review", share: 6.8, hasId: true },
    { kind: "workflow", name: "nightly-report", share: 6.2, hasId: false },
];

export function usageDetailFor(kind: UsageKind, row: UsageRow, summary: UsageSummary = usageSummary): UsageSubjectDetail | UsageModelDetail | UsageWorkflowDetail | UsageUserDetail {
    const header = { scope: "tenant" as const, dataAsOf: summary.dataAsOf, historyComplete: summary.historyComplete, window: summary.window };
    const daily = row.daily ?? usageRowDaily(row);
    if (kind === "users") {
        const userRow = row as UsageUserRow;
        const rootCosts = splitCost(row.cost, [...ROOT_PLAN.map(root => root.share), 100 - ROOT_PLAN.reduce((sum, root) => sum + root.share, 0)]);
        const rootId = (root: (typeof ROOT_PLAN)[number]) => {
            if (!root.hasId) return null;
            const rows = root.kind === "agent" ? usageAgentRows : usageWorkflowRows;
            return rows.find(candidate => candidate.name === root.name)?.id ?? null;
        };
        const topRoots = ROOT_PLAN.map((root, index) => {
            const id = rootId(root);
            return { kind: root.kind, id, name: root.name, bookedName: id === null ? root.name : null, cost: rootCosts[index], shareOfUser: share(rootCosts[index], row.cost) };
        });
        const otherCost = rootCosts[ROOT_PLAN.length];
        return { ...header, id: row.id, name: row.name, user: userRow.user, totals: totalsOf(row), daily, budget: userRow.budget, topRoots, otherCost, otherShareOfUser: share(otherCost, row.cost) };
    }
    const base = { ...header, id: row.id, name: row.name ?? "", bookedName: row.bookedName, totals: totalsOf(row), daily };
    if (kind === "models") return { ...base, models: (row as UsageModelRow).models };
    if (kind !== "workflows") return base;
    const nodeCosts = splitCost(
        row.cost,
        NODE_PLAN.map(node => node.share)
    );
    const comparisonWindow = row.id === null || !summary.historyComplete ? null : summary.comparisonWindow;
    const nodes = NODE_PLAN.map((node, index) => ({ nodeId: node.nodeId, name: node.name, kind: node.kind, cost: nodeCosts[index], shareOfWorkflow: share(nodeCosts[index], row.cost), changePercent: comparisonWindow ? node.changePercent : null }));
    return { ...base, comparisonWindow, nodes };
}
