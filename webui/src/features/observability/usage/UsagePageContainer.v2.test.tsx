// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { usageAgentRows, usageModelRows, usageSummary, usageUserRows, usageWorkflowRows } from "@/testing/data/usage";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { OBSERVABILITY_ROUTES } from "../routes";

import { UsagePageContainer } from "./UsagePageContainer";

const ROWS: Record<string, { type: string; rows: object[] }> = {
    agents: { type: "agentUsage", rows: usageAgentRows },
    workflows: { type: "workflowUsage", rows: usageWorkflowRows },
    models: { type: "modelUsage", rows: usageModelRows },
    users: { type: "userUsage", rows: usageUserRows.map(({ bookedName: _bookedName, ...row }) => row) },
};

const pathOf = (input: unknown) => {
    const url = new URL(String(input), "http://localhost");
    return url.pathname + url.search;
};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

describe("UsagePageContainer on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const paths = () => fetchMock.mock.calls.map(([url]) => pathOf(url));

    beforeEach(() => {
        fetchMock.mockImplementation(async input => {
            const path = pathOf(input);
            if (path.startsWith("/api/v2/platform/usage/summary?")) return json({ data: usageSummary });
            const list = /^\/api\/v2\/platform\/usage\/(\w+)\?/.exec(path)?.[1];
            if (list && ROWS[list]) {
                const data = ROWS[list].rows.map(row => ({ ...row, type: ROWS[list].type }));
                return json({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 25, nextPage: null, totalPages: 1 } } });
            }
            return json({ message: "Not found", errorId: "x" }, 404);
        });
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    test("reads the summary and the users list from /api/v2 only", async () => {
        renderWithProviders(<UsagePageContainer />, {
            initialPath: `${OBSERVABILITY_ROUTES.usage}?type=users`,
        });

        expect((await screen.findAllByText("Parminder Procurement")).length).toBeGreaterThan(0);
        await waitFor(() => expect(paths().some(path => path.startsWith("/api/v2/platform/usage/summary?"))).toBe(true));
        expect(paths().some(path => path.startsWith("/api/v2/platform/usage/users?"))).toBe(true);
        expect(paths().filter(path => !path.startsWith("/api/v2/"))).toEqual([]);
    });
});
