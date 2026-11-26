// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { PaginatedResponse } from "@/api/common";
import { evaluationsApi } from "@/api/evaluations";

import type { EvalRunGroupSummary, EvalTargetType } from "@/types/eval";

import { useEvalReportsList } from "./useEvalReports";

const emptyPage: PaginatedResponse<EvalRunGroupSummary> = { data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 20, nextPage: null, totalPages: 1 } } };

function wrapper({ children }: { readonly children: ReactNode }) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useEvalReportsList", () => {
    afterEach(() => vi.restoreAllMocks());

    test("sends the target type, and a new type is a new query", async () => {
        const list = vi.spyOn(evaluationsApi, "listRunGroupsPaginated").mockResolvedValue(emptyPage);

        const { rerender } = renderHook(({ targetType }: { targetType?: EvalTargetType }) => useEvalReportsList({ pageNumber: 1, pageSize: 20, targetType }), { wrapper, initialProps: { targetType: "workflow" } });
        await waitFor(() => expect(list).toHaveBeenCalledTimes(1));
        expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ targetType: "workflow" }));

        rerender({ targetType: "agent" });
        await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
        expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ targetType: "agent" }));
    });
});
