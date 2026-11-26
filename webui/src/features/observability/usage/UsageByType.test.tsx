// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { type UsageKind, type UsageRow, useUsageList } from "@/api/usage";

import { usageAgentRows, usageModelRows, usageRow, usageSummary, usageWorkflowRows } from "@/testing/data/usage";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { UsageByType } from "./UsageByType";
import { WORKFLOW_OVERLAP_HELP } from "./usageKinds";

vi.mock("@/api/usage", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/usage")>()), useUsageList: vi.fn() }));

const KIND_COST = "1000";
const TOTAL_COST = "1240.5800000000";

type ListResult = ReturnType<typeof useUsageList>;

function loaded(rows: readonly UsageRow[]): ListResult {
    const page = { data: rows, meta: { pagination: { pageNumber: 1, pageSize: 25, count: rows.length, totalPages: 1 } } };
    return { data: page, error: null, isLoading: false, isPlaceholderData: false, isRefetching: false } as unknown as ListResult;
}

const loading = { data: undefined, error: null, isLoading: true, isPlaceholderData: false, isRefetching: false } as unknown as ListResult;

interface TabOptions {
    top?: ListResult;
    bottom?: ListResult;
    kindCost?: string;
    totalCost?: string;
    totalIsStale?: boolean;
}

function renderTab(kind: UsageKind, rows: readonly UsageRow[], { top = loaded(rows), bottom = loaded([...rows].reverse().slice(0, 5)), kindCost = KIND_COST, totalCost = TOTAL_COST, totalIsStale = false }: TabOptions = {}) {
    vi.mocked(useUsageList).mockImplementation((_kind, params) => (params.sortDir === "asc" ? bottom : top));
    renderWithProviders(
        <UsageByType
            kind={kind}
            onKindChange={vi.fn()}
            nameFilter=""
            onNameFilterChange={vi.fn()}
            windowParams={{ period: "this_month" }}
            costByKind={{ ...usageSummary.costByKind, [kind]: kindCost }}
            totalCost={totalCost}
            totalIsStale={totalIsStale}
            today="2026-10-27"
            isThisMonth
            openDetail={null}
            onOpenDetail={vi.fn()}
        />
    );
}

const region = (name: string) => within(screen.getByRole("region", { name }));
const breakdownButton = () => screen.queryByRole("button", { name: "About this table" });

async function openBreakdownTooltip() {
    await userEvent.hover(screen.getByRole("button", { name: "About this table" }));
}

beforeEach(() => {
    vi.mocked(useUsageList).mockReset();
});

describe("UsageByType", () => {
    test("agents' top spenders show what the listed agents leave of the agents' cost as Other", () => {
        renderTab("agents", usageAgentRows);
        expect(region("Top Spenders").getByText("Other")).toBeInTheDocument();
    });

    test("workflows' top spenders show no Other, since their rows overlap", () => {
        renderTab("workflows", usageWorkflowRows);
        expect(region("Top Spenders").getByText("Invoice Reconciliation")).toBeInTheDocument();
        expect(region("Top Spenders").queryByText("Other")).not.toBeInTheDocument();
    });

    describe("Lowest Spenders' Other", () => {
        const agents = ["100", "80", "10", "5", "3", "1", "1"].map((cost, i) => usageRow({ name: `Agent ${i + 1}`, cost, activities: 1 }));
        const lowest = loaded([...agents].reverse().slice(0, 5));

        test("leaves out Top Spenders' rows, so with every agent on one card or the other there is none", () => {
            renderTab("agents", agents, { bottom: lowest, kindCost: "200" });
            expect(region("Top Spenders").getByText("Other")).toBeInTheDocument();
            expect(region("Lowest Spenders").getByText("Agent 3")).toBeInTheDocument();
            expect(region("Lowest Spenders").queryByText("Other")).not.toBeInTheDocument();
        });

        test("is not shown while Top Spenders' rows have not loaded, rather than counting them again", () => {
            renderTab("agents", agents, { top: loading, bottom: lowest, kindCost: "200" });
            expect(region("Lowest Spenders").getByText("Agent 3")).toBeInTheDocument();
            expect(region("Lowest Spenders").queryByText("Other")).not.toBeInTheDocument();
        });
    });

    describe("Cost Breakdown tooltip", () => {
        test("on Agents names the spend no agent made", async () => {
            renderTab("agents", usageAgentRows, { kindCost: "767.3400000000" });
            await openBreakdownTooltip();
            expect(await screen.findByText(/It doesn't include \$473\.24 of AI usage no agent made/)).toBeInTheDocument();
        });

        test("on Workflows names the spend outside workflows and why the rows overlap", async () => {
            renderTab("workflows", usageWorkflowRows, { kindCost: "877.4000000000" });
            await openBreakdownTooltip();
            expect(await screen.findByText(/It doesn't include \$363\.18 outside workflows/)).toBeInTheDocument();
            expect(screen.getByText(WORKFLOW_OVERLAP_HELP)).toBeInTheDocument();
        });

        test("says the breakdown is the whole total when nothing is left out, and keeps the overlap note on Workflows", async () => {
            renderTab("workflows", usageWorkflowRows, { kindCost: TOTAL_COST });
            await openBreakdownTooltip();
            expect(await screen.findByText("This breakdown covers all of Agent Mesh Total Cost.")).toBeInTheDocument();
            expect(screen.getByText(WORKFLOW_OVERLAP_HELP)).toBeInTheDocument();
        });

        test("shows an amount under a cent as under a cent, not as $0.00", async () => {
            renderTab("agents", usageAgentRows, { kindCost: "1240.5790000000" });
            await openBreakdownTooltip();
            expect(await screen.findByText(/It doesn't include <\$0\.01 of AI usage/)).toBeInTheDocument();
        });

        test.each<[UsageKind, readonly UsageRow[]]>([
            ["users", usageAgentRows],
            ["models", usageModelRows],
        ])("is not offered on %s, whose list covers all spend", (kind, rows) => {
            renderTab(kind, rows);
            expect(breakdownButton()).not.toBeInTheDocument();
        });

        test("is not offered when an amount can't be read", () => {
            renderTab("agents", usageAgentRows, { kindCost: "NaN" });
            expect(breakdownButton()).not.toBeInTheDocument();
        });

        test("is not offered while the total still belongs to the previous period", () => {
            renderTab("agents", usageAgentRows, { kindCost: "767.3400000000", totalIsStale: true });
            expect(breakdownButton()).not.toBeInTheDocument();
        });
    });
});
