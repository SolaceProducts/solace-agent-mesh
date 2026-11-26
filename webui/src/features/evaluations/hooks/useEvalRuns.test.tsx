// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { EvalRun } from "@/types/eval";

import { makeEvalRun } from "@/testing/helpers/evaluations";

function buildQueryWrapper() {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("useEvalRuns", () => {
    let useEvalRuns: typeof import("./useEvalRuns").useEvalRuns;
    type AnyAsyncFn = ((...args: unknown[]) => Promise<unknown>) & {
        mock: { calls: unknown[][] };
        mockResolvedValue: (v: unknown) => AnyAsyncFn;
        mockResolvedValueOnce: (v: unknown) => AnyAsyncFn;
        mockRejectedValue: (e: unknown) => AnyAsyncFn;
        mockRejectedValueOnce: (e: unknown) => AnyAsyncFn;
    };
    let getRunsMock: AnyAsyncFn;
    let triggerRunMock: AnyAsyncFn;
    let cancelRunMock: AnyAsyncFn;
    let deleteRunMock: AnyAsyncFn;

    beforeEach(async () => {
        vi.resetModules();
        getRunsMock = vi.fn() as unknown as AnyAsyncFn;
        triggerRunMock = vi.fn() as unknown as AnyAsyncFn;
        cancelRunMock = vi.fn() as unknown as AnyAsyncFn;
        deleteRunMock = vi.fn() as unknown as AnyAsyncFn;

        vi.doMock("@/api/evaluations/versioned", async () => {
            const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
            return {
                ...actual,
                evaluationsApi: {
                    ...actual.evaluationsApi,
                    getRuns: (...args: unknown[]) => getRunsMock(...args),
                    triggerRun: (...args: unknown[]) => triggerRunMock(...args),
                    cancelRun: (...args: unknown[]) => cancelRunMock(...args),
                    deleteRun: (...args: unknown[]) => deleteRunMock(...args),
                },
            };
        });

        const mod = await import("./useEvalRuns");
        useEvalRuns = mod.useEvalRuns;
    });

    test("loads runs for the experiment and reports hasActiveRuns when any is running", async () => {
        const runs: EvalRun[] = [makeEvalRun({ id: "r1", status: "completed" }), makeEvalRun({ id: "r2", status: "running" })];
        getRunsMock.mockResolvedValue(runs);

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(result.current.runs).toHaveLength(2));
        expect(getRunsMock).toHaveBeenCalledWith("exp-1");
        expect(result.current.hasActiveRuns).toBe(true);
        expect(result.current.error).toBeNull();
    });

    test("hasActiveRuns is false when all runs are terminal", async () => {
        getRunsMock.mockResolvedValue([makeEvalRun({ status: "completed" })]);

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(result.current.runs).toHaveLength(1));
        expect(result.current.hasActiveRuns).toBe(false);
    });

    test("skips the list query when enableListQuery is false", async () => {
        getRunsMock.mockResolvedValue([makeEvalRun()]);

        renderHook(() => useEvalRuns("exp-1", { enableListQuery: false }), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(getRunsMock).not.toHaveBeenCalled());
    });

    test("does not fetch when experimentId is undefined", async () => {
        renderHook(() => useEvalRuns(undefined), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(getRunsMock).not.toHaveBeenCalled());
    });

    test("triggerRun posts the run name and refetches the list", async () => {
        getRunsMock.mockResolvedValue([]);
        triggerRunMock.mockResolvedValue([makeEvalRun({ id: "new-run" })]);

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });
        await waitFor(() => expect(getRunsMock).toHaveBeenCalledTimes(1));

        await act(async () => {
            await result.current.triggerRun("Smoke test");
        });

        expect(triggerRunMock).toHaveBeenCalledWith("exp-1", { runName: "Smoke test" });
        await waitFor(() => expect(getRunsMock).toHaveBeenCalledTimes(2));
    });

    test("triggerRun rejects when experimentId is undefined", async () => {
        const { result } = renderHook(() => useEvalRuns(undefined), { wrapper: buildQueryWrapper() });

        await expect(result.current.triggerRun("name")).rejects.toThrow(/No experiment ID/);
        expect(triggerRunMock).not.toHaveBeenCalled();
    });

    test("cancelRun resolves on success and propagates rejection on failure", async () => {
        getRunsMock.mockResolvedValue([]);
        cancelRunMock.mockResolvedValueOnce(makeEvalRun({ id: "r1", status: "cancelled" })).mockRejectedValueOnce(new Error("boom"));

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });
        await waitFor(() => expect(getRunsMock).toHaveBeenCalled());

        await act(async () => {
            await expect(result.current.cancelRun("r1")).resolves.toBeUndefined();
        });
        expect(cancelRunMock).toHaveBeenNthCalledWith(1, "r1");

        await act(async () => {
            await expect(result.current.cancelRun("r2")).rejects.toThrow("boom");
        });
    });

    test("deleteRun delegates to the API and refetches", async () => {
        getRunsMock.mockResolvedValue([]);
        deleteRunMock.mockResolvedValue(undefined);

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });
        await waitFor(() => expect(getRunsMock).toHaveBeenCalledTimes(1));

        await act(async () => {
            await result.current.deleteRun("r1");
        });

        expect(deleteRunMock).toHaveBeenCalledWith("r1");
        await waitFor(() => expect(getRunsMock).toHaveBeenCalledTimes(2));
    });

    test("fetchRuns triggers a fresh API call", async () => {
        getRunsMock.mockResolvedValue([]);

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });
        await waitFor(() => expect(getRunsMock).toHaveBeenCalledTimes(1));

        await act(async () => {
            await result.current.fetchRuns();
        });

        expect(getRunsMock).toHaveBeenCalledTimes(2);
    });

    test("surfaces query errors as a stringified message", async () => {
        getRunsMock.mockRejectedValue(new Error("network down"));

        const { result } = renderHook(() => useEvalRuns("exp-1"), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(result.current.error).toBeTruthy());
        expect(result.current.error).toContain("network down");
    });
});
