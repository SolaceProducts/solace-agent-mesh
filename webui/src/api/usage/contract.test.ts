// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { usageGoldens } from "@/testing/data/usage";

import type { ListPagination } from "../common";

import {
    USAGE_BUDGET_SOURCES,
    USAGE_BUDGET_STATUSES,
    USAGE_NODE_KINDS,
    USAGE_ROOT_KINDS,
    USAGE_SCOPES,
    type UsageBudget,
    type UsageBudgetWindow,
    type UsageComparison,
    type UsageCostByBucket,
    type UsageCostByKind,
    type UsageDay,
    type UsageModelDetail,
    type UsageModelRow,
    type UsageMoney,
    type UsageMonth,
    type UsageRoot,
    type UsageRow,
    type UsageSavings,
    type UsageSelf,
    type UsageServedModel,
    type UsageSubjectDetail,
    type UsageSubjectTotals,
    type UsageSummary,
    type UsageTimestamp,
    type UsageTokens,
    type UsageTopUp,
    type UsageTotals,
    type UsageUser,
    type UsageUserDetail,
    type UsageUserRow,
    type UsageWindow,
    type UsageWorkflowDetail,
    type UsageWorkflowNode,
} from "./types";

type NonNullField = "money" | "timestamp" | "day" | "month" | "string" | "number" | "boolean" | { oneOf: readonly string[]; nullable?: false } | { object: Spec; nullable?: false } | { array: Spec };
type NullableField = "nullableMoney" | "nullableTimestamp" | "nullableDay" | "nullableString" | "nullableNumber" | { oneOf: readonly string[]; nullable: true } | { object: Spec; nullable: true } | { array: Spec; nullable: true };
type Field = NonNullField | NullableField;
type Spec = Record<string, Field>;

type NonNullFieldFor<V> = [V] extends [readonly (infer Item)[]]
    ? { array: SpecFor<Item> }
    : [V] extends [boolean]
      ? "boolean"
      : [V] extends [number]
        ? "number"
        : [V] extends [UsageMoney]
          ? "money"
          : [V] extends [UsageTimestamp]
            ? "timestamp"
            : [V] extends [string]
              ? string extends V
                  ? "string" | "day" | "month"
                  : { oneOf: readonly V[]; nullable?: false }
              : { object: SpecFor<V>; nullable?: false };
type NullableFieldFor<V> = [V] extends [readonly (infer Item)[]]
    ? { array: SpecFor<Item>; nullable: true }
    : [V] extends [number]
      ? "nullableNumber"
      : [V] extends [UsageMoney]
        ? "nullableMoney"
        : [V] extends [UsageTimestamp]
          ? "nullableTimestamp"
          : [V] extends [string]
            ? string extends V
                ? "nullableString" | "nullableDay"
                : { oneOf: readonly V[]; nullable: true }
            : { object: SpecFor<V>; nullable: true };
type SpecFor<T> = { [K in keyof T]-?: null extends T[K] ? NullableFieldFor<NonNullable<T[K]>> : NonNullFieldFor<T[K]> };

const windowSpec = { startTime: "timestamp", endTime: "timestamp" } satisfies SpecFor<UsageWindow>;
const bucketsSpec = { uncachedInput: "money", output: "money", cacheRead: "money", cacheWrite: "money" } satisfies SpecFor<UsageCostByBucket>;
const tokensSpec = { uncachedInput: "number", output: "number", cacheRead: "number", cacheWrite: "number" } satisfies SpecFor<UsageTokens>;
const costByKindSpec = { users: "money", agents: "money", workflows: "money", models: "money" } satisfies SpecFor<UsageCostByKind>;
const daySpec = { date: "day", cost: "money", tokens: { object: tokensSpec }, activities: "number" } satisfies SpecFor<UsageDay>;
const topUpSpec = { amount: "money", expiresTime: "timestamp" } satisfies SpecFor<UsageTopUp>;
const budgetSpec = {
    id: "string",
    name: "string",
    source: { oneOf: USAGE_BUDGET_SOURCES },
    amount: "nullableMoney",
    warningPercentage: "nullableNumber",
    topUp: { object: topUpSpec, nullable: true },
    effectiveAmount: "nullableMoney",
    periodStart: "timestamp",
    periodEnd: "timestamp",
    periodSpend: "money",
    usagePercent: "nullableNumber",
    status: { oneOf: USAGE_BUDGET_STATUSES },
} satisfies SpecFor<UsageBudget>;
const userSpec = { email: "nullableString", isSystemUser: "boolean" } satisfies SpecFor<UsageUser>;
const totalsSpec = {
    cost: "money",
    costByBucket: { object: bucketsSpec },
    tokens: { object: tokensSpec },
    calls: "number",
    unpricedCalls: "number",
    activities: "number",
} satisfies SpecFor<UsageTotals>;
const subjectTotalsSpec = {
    cost: "money",
    activities: "number",
    costPerActivity: "nullableMoney",
    shareOfSpend: "nullableNumber",
    costByBucket: { object: bucketsSpec },
    tokens: { object: tokensSpec },
    calls: "number",
    unpricedCalls: "number",
} satisfies SpecFor<UsageSubjectTotals>;
const comparisonSpec = { cost: "money", changePercent: "nullableNumber" } satisfies SpecFor<UsageComparison>;
const headerSpec = { scope: { oneOf: USAGE_SCOPES }, dataAsOf: "nullableTimestamp", historyComplete: "boolean" } as const;

const summarySpec = {
    ...headerSpec,
    window: { object: windowSpec },
    comparisonWindow: { object: windowSpec, nullable: true },
    currency: "string",
    totals: { object: totalsSpec },
    costByKind: { object: costByKindSpec },
    comparison: { object: comparisonSpec, nullable: true },
    meshBudget: { object: budgetSpec, nullable: true },
    daily: { array: daySpec },
} satisfies SpecFor<UsageSummary>;

const savingsSpec = {
    ...headerSpec,
    window: { object: windowSpec },
    currency: "string",
    avoidedInputTokens: "nullableNumber",
    avoidedCost: "nullableMoney",
    calls: "number",
    measuredCalls: "number",
    unpricedCalls: "number",
} satisfies SpecFor<UsageSavings>;

const rowSpec = {
    id: "nullableString",
    name: "nullableString",
    bookedName: "nullableString",
    cost: "money",
    activities: "number",
    costPerActivity: "nullableMoney",
    shareOfSpend: "nullableNumber",
    costByBucket: { object: bucketsSpec },
    tokens: { object: tokensSpec },
    calls: "number",
    unpricedCalls: "number",
    daily: { array: daySpec, nullable: true },
} satisfies SpecFor<UsageRow>;
const servedModelSpec = {
    name: "nullableString",
    provider: "nullableString",
    cost: "money",
    activities: "number",
    costByBucket: { object: bucketsSpec },
    tokens: { object: tokensSpec },
    calls: "number",
    unpricedCalls: "number",
} satisfies SpecFor<UsageServedModel>;
const modelRowSpec = { ...rowSpec, models: { array: servedModelSpec } } satisfies SpecFor<UsageModelRow>;
const budgetWindowSpec = { windowBudget: "money", usagePercent: "number" } satisfies SpecFor<UsageBudgetWindow>;
const userRowSpec = {
    ...rowSpec,
    user: { object: userSpec, nullable: true },
    budget: { object: budgetSpec, nullable: true },
    budgetUsage: { object: budgetWindowSpec, nullable: true },
} satisfies SpecFor<UsageUserRow>;

const subjectDetailSpec = {
    ...headerSpec,
    id: "nullableString",
    name: "string",
    bookedName: "nullableString",
    window: { object: windowSpec },
    totals: { object: subjectTotalsSpec },
    daily: { array: daySpec },
} satisfies SpecFor<UsageSubjectDetail>;
const modelDetailSpec = { ...subjectDetailSpec, models: { array: servedModelSpec } } satisfies SpecFor<UsageModelDetail>;
const nodeSpec = {
    nodeId: "string",
    name: "string",
    kind: { oneOf: USAGE_NODE_KINDS, nullable: true },
    cost: "money",
    shareOfWorkflow: "number",
    changePercent: "nullableNumber",
} satisfies SpecFor<UsageWorkflowNode>;
const workflowDetailSpec = {
    ...subjectDetailSpec,
    comparisonWindow: { object: windowSpec, nullable: true },
    nodes: { array: nodeSpec },
} satisfies SpecFor<UsageWorkflowDetail>;
const rootSpec = { kind: { oneOf: USAGE_ROOT_KINDS }, id: "nullableString", name: "string", bookedName: "nullableString", cost: "money", shareOfUser: "number" } satisfies SpecFor<UsageRoot>;
const userDetailSpec = {
    ...headerSpec,
    id: "nullableString",
    name: "nullableString",
    user: { object: userSpec, nullable: true },
    window: { object: windowSpec },
    totals: { object: subjectTotalsSpec },
    daily: { array: daySpec },
    budget: { object: budgetSpec, nullable: true },
    topRoots: { array: rootSpec },
    otherCost: "money",
    otherShareOfUser: "number",
} satisfies SpecFor<UsageUserDetail>;
const monthSpec = { month: "month", cost: "money", budgetAmount: "nullableMoney", partialFromDate: "nullableDay" } satisfies SpecFor<UsageMonth>;
const selfSpec = { ...userDetailSpec, months: { array: monthSpec } } satisfies SpecFor<UsageSelf>;

const paginationSpec = { pageNumber: "number", count: "number", pageSize: "number", nextPage: "nullableNumber", totalPages: "number" } satisfies SpecFor<ListPagination>;
function listSpec<Row>(rows: SpecFor<Row>): Spec {
    return { data: { array: rows as Spec }, meta: { object: { pagination: { object: paginationSpec } } } };
}

const MONEY = /^-?\d+\.\d{10}$/;
const TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;
const DAY = /^\d{4}-\d{2}-\d{2}$/;
const MONTH = /^\d{4}-\d{2}$/;

const PRIMITIVE: Record<string, { type: "string" | "number" | "boolean"; nullable: boolean; pattern?: RegExp; describe?: string }> = {
    string: { type: "string", nullable: false },
    nullableString: { type: "string", nullable: true },
    number: { type: "number", nullable: false },
    nullableNumber: { type: "number", nullable: true },
    boolean: { type: "boolean", nullable: false },
    money: { type: "string", nullable: false, pattern: MONEY, describe: "a ten-decimal money string" },
    nullableMoney: { type: "string", nullable: true, pattern: MONEY, describe: "a ten-decimal money string" },
    timestamp: { type: "string", nullable: false, pattern: TIMESTAMP, describe: "a …sss Z timestamp" },
    nullableTimestamp: { type: "string", nullable: true, pattern: TIMESTAMP, describe: "a …sss Z timestamp" },
    day: { type: "string", nullable: false, pattern: DAY, describe: "a YYYY-MM-DD day" },
    nullableDay: { type: "string", nullable: true, pattern: DAY, describe: "a YYYY-MM-DD day" },
    month: { type: "string", nullable: false, pattern: MONTH, describe: "a YYYY-MM month" },
};

const FORMATTED = [
    { pattern: MONEY, kind: "money" },
    { pattern: TIMESTAMP, kind: "timestamp" },
];

function fieldViolation(value: unknown, field: Field, at: string): string[] {
    if (typeof field === "string") {
        const rule = PRIMITIVE[field];
        if (value === null) return rule.nullable ? [] : [`${at}: null but the type says it never is`];
        if (typeof value !== rule.type || (rule.pattern && !rule.pattern.test(value as string))) return [`${at}: ${JSON.stringify(value)} is not ${rule.describe ?? `a ${rule.type}`}`];
        const formatted = !rule.pattern && typeof value === "string" ? FORMATTED.find(({ pattern }) => pattern.test(value)) : undefined;
        if (formatted) return [`${at}: ${JSON.stringify(value)} is ${formatted.kind}-shaped but specced as a plain string`];
        return [];
    }
    if (value === null) return "nullable" in field && field.nullable ? [] : [`${at}: null but the type says it never is`];
    if ("oneOf" in field) return field.oneOf.includes(value as string) ? [] : [`${at}: ${JSON.stringify(value)} is not one of ${JSON.stringify(field.oneOf)}`];
    if ("array" in field) return Array.isArray(value) ? value.flatMap((item, i) => violations(item, field.array, `${at}[${i}]`)) : [`${at}: expected an array`];
    return violations(value, field.object, at);
}

const V1_ONLY_FIELD = /\.topUp\.id$/;

function violations(value: unknown, spec: Spec, path: string): string[] {
    if (typeof value !== "object" || value === null || Array.isArray(value)) return [`${path}: expected an object`];
    const record = value as Record<string, unknown>;
    const extra = Object.keys(record)
        .filter(key => !(key in spec) && !V1_ONLY_FIELD.test(`${path}.${key}`))
        .map(key => `${path}.${key}: on the wire but not in the TS type`);
    const fields = Object.entries(spec).flatMap(([key, field]) => (key in record ? fieldViolation(record[key], field, `${path}.${key}`) : [`${path}.${key}: absent (every field is always present, null when missing)`]));
    return [...extra, ...fields];
}

describe("usage wire types match the backend goldens", () => {
    test.each([
        ["summary.json", usageGoldens.summary, summarySpec],
        ["summary_building.json", usageGoldens.summaryBuilding, summarySpec],
        ["agents_list.json", usageGoldens.agentsList, listSpec<UsageRow>(rowSpec)],
        ["users_list.json", usageGoldens.usersList, listSpec<UsageUserRow>(userRowSpec)],
        ["models_list_daily.json", usageGoldens.modelsListDaily, listSpec<UsageModelRow>(modelRowSpec)],
        ["agent_detail.json", usageGoldens.agentDetail, subjectDetailSpec],
        ["model_detail.json", usageGoldens.modelDetail, modelDetailSpec],
        ["workflow_detail.json", usageGoldens.workflowDetail, workflowDetailSpec],
        ["workflow_detail_by_name.json", usageGoldens.workflowDetailByName, workflowDetailSpec],
        ["user_detail.json", usageGoldens.userDetail, userDetailSpec],
        ["self.json", usageGoldens.self, selfSpec],
        ["self_unattributed.json", usageGoldens.selfUnattributed, selfSpec],
        ["savings.json", usageGoldens.savings, savingsSpec],
        ["savings_unpriced.json", usageGoldens.savingsUnpriced, savingsSpec],
        ["savings_building.json", usageGoldens.savingsBuilding, savingsSpec],
        ["self_savings.json", usageGoldens.selfSavings, savingsSpec],
    ] as const)("%s", (_name, golden, spec) => {
        expect(violations(golden, spec as Spec, "$")).toEqual([]);
    });

    test("every golden file in the fixtures folder is checked above", () => {
        const files = Object.keys(import.meta.glob("@/testing/data/usage/*.json")).map(path => path.split("/").pop());
        expect(files.sort()).toEqual([
            "agent_detail.json",
            "agents_list.json",
            "model_detail.json",
            "models_list_daily.json",
            "savings.json",
            "savings_building.json",
            "savings_unpriced.json",
            "self.json",
            "self_savings.json",
            "self_unattributed.json",
            "summary.json",
            "summary_building.json",
            "user_detail.json",
            "users_list.json",
            "workflow_detail.json",
            "workflow_detail_by_name.json",
        ]);
    });
});

describe("the contract check itself", () => {
    test("catches an unknown key, an absent key, a wrong JS type, a null the type forbids, bad money, an old timestamp and an unknown enum value", () => {
        const { currency: _absent, ...withoutCurrency } = usageGoldens.summary;
        const drifted = {
            ...withoutCurrency,
            extra: 1,
            scope: "everyone",
            window: { startTime: "2026-10-01T00:00:00.000000+00:00", endTime: usageGoldens.summary.window.endTime },
            totals: { ...usageGoldens.summary.totals, cost: 1240.58, calls: "182345", activities: null },
        };
        expect(violations(drifted, summarySpec as Spec, "$")).toEqual([
            "$.extra: on the wire but not in the TS type",
            '$.scope: "everyone" is not one of ["tenant","self"]',
            '$.window.startTime: "2026-10-01T00:00:00.000000+00:00" is not a …sss Z timestamp',
            "$.currency: absent (every field is always present, null when missing)",
            "$.totals.cost: 1240.58 is not a ten-decimal money string",
            '$.totals.calls: "182345" is not a number',
            "$.totals.activities: null but the type says it never is",
        ]);
    });

    test("catches a malformed day or month, and a money value specced as a plain string", () => {
        expect(violations({ date: "2026-10-1", cost: "1.0000000000", tokens: { uncachedInput: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, activities: 1 }, daySpec as Spec, "$")).toEqual(['$.date: "2026-10-1" is not a YYYY-MM-DD day']);
        expect(violations({ month: "2026-10-01", cost: "1.0000000000", budgetAmount: null, partialFromDate: "Aug 12" }, monthSpec as Spec, "$")).toEqual([
            '$.month: "2026-10-01" is not a YYYY-MM month',
            '$.partialFromDate: "Aug 12" is not a YYYY-MM-DD day',
        ]);
        expect(violations({ currency: "1.0000000000" }, { currency: "string" }, "$")).toEqual(['$.currency: "1.0000000000" is money-shaped but specced as a plain string']);
    });
});
