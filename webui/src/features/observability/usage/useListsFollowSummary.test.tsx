// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { usageKeys } from "@/api/usage";

import { useListsFollowSummary } from "./useListsFollowSummary";

function follow(initial: string | null | undefined) {
    const client = new QueryClient();
    const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue();
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const hook = renderHook(({ dataAsOf }: { dataAsOf: string | null | undefined }) => useListsFollowSummary(dataAsOf), { wrapper, initialProps: { dataAsOf: initial } });
    return { invalidate, rerender: (dataAsOf: string | null | undefined) => hook.rerender({ dataAsOf }) };
}

describe("useListsFollowSummary", () => {
    test("refetches every usage list when the summary reports a newer rollup pass", () => {
        const { invalidate, rerender } = follow("2026-10-16T14:03:00.000Z");
        rerender("2026-10-16T14:08:00.000Z");
        expect(invalidate).toHaveBeenCalledOnce();
        expect(invalidate).toHaveBeenCalledWith({ queryKey: usageKeys.allLists });
    });

    test("leaves the lists alone for the summary's first value, and while it stays the same", () => {
        const { invalidate, rerender } = follow(undefined);
        rerender(null);
        rerender(null);
        expect(invalidate).not.toHaveBeenCalled();
    });

    test("a summary updated for the first time counts as newer", () => {
        const { invalidate, rerender } = follow(null);
        rerender("2026-10-16T14:03:00.000Z");
        expect(invalidate).toHaveBeenCalledOnce();
    });
});
