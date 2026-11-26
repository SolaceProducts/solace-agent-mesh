// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { EvalDataset, EvalEvaluator, EvalExperiment } from "@/types/eval";

import { useCreateDataset, useDeleteEvaluator, useEvalDatasets, useEvalEvaluators, useEvalExperiments, useSampleExperiment, useSetEvalWatchlist, useUpdateEvaluator } from "./hooks";
import { evaluationsApi } from "./versioned";

function wrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("evaluation hooks", () => {
    afterEach(() => vi.restoreAllMocks());

    test("experiments are listed through the evaluations API", async () => {
        const list = vi.spyOn(evaluationsApi, "getExperiments").mockResolvedValue([{ id: "exp-1" } as EvalExperiment]);

        const { result } = renderHook(() => useEvalExperiments({ datasetId: "ds-1" }), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.data).toHaveLength(1));
        expect(list).toHaveBeenCalledWith({ datasetId: "ds-1" });
    });

    test("the sample experiment is read through the evaluations API", async () => {
        const read = vi.spyOn(evaluationsApi, "getSampleExperiment").mockResolvedValue(null);

        const { result } = renderHook(() => useSampleExperiment(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(read).toHaveBeenCalled();
    });

    test("the watchlist is saved through the evaluations API", async () => {
        const save = vi.spyOn(evaluationsApi, "setWatchlist").mockResolvedValue([]);

        const { result } = renderHook(() => useSetEvalWatchlist(), { wrapper: wrapper() });
        await act(() => result.current.mutateAsync(["A"]));

        expect(save).toHaveBeenCalledWith(["A"]);
    });

    test("datasets and evaluators are listed through the evaluations API", async () => {
        const datasets = vi.spyOn(evaluationsApi, "getDatasets").mockResolvedValue([{ id: "ds-1" } as EvalDataset]);
        const evaluators = vi.spyOn(evaluationsApi, "getEvaluators").mockResolvedValue([{ id: "ev-1" } as EvalEvaluator]);

        const { result } = renderHook(() => ({ datasets: useEvalDatasets(), evaluators: useEvalEvaluators() }), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.datasets.data).toHaveLength(1));
        await waitFor(() => expect(result.current.evaluators.data).toHaveLength(1));
        expect([datasets, evaluators].map(spy => spy.mock.calls.length)).toEqual([1, 1]);
    });

    test("datasets and evaluators are written through the evaluations API", async () => {
        const create = vi.spyOn(evaluationsApi, "createDataset").mockResolvedValue({ id: "ds-1" } as EvalDataset);
        const update = vi.spyOn(evaluationsApi, "updateEvaluator").mockResolvedValue({ id: "ev-1" } as EvalEvaluator);
        const remove = vi.spyOn(evaluationsApi, "deleteEvaluator").mockResolvedValue();

        const { result } = renderHook(() => ({ create: useCreateDataset(), update: useUpdateEvaluator(), remove: useDeleteEvaluator() }), { wrapper: wrapper() });
        await act(() => result.current.create.mutateAsync({ name: "Golden", description: "" }));
        await act(() => result.current.update.mutateAsync({ id: "ev-1", data: { description: "Kept" } }));
        await act(() => result.current.remove.mutateAsync("ev-2"));

        expect(create).toHaveBeenCalledWith({ name: "Golden", description: "" });
        expect(update).toHaveBeenCalledWith("ev-1", { description: "Kept" });
        expect(remove).toHaveBeenCalledWith("ev-2");
    });
});
