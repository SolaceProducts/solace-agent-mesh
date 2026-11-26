// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { PaginatedResponse } from "@/api/common";
import { evaluationsApi } from "@/api/evaluations";

import type { EvalDataset, EvalExample } from "@/types/eval";

import { useEvalDatasetsPaginated } from "./useEvalDatasetsPaginated";
import { useEvalExamples, useEvalExamplesPaginated } from "./useEvalExamples";

const pageOf = <T,>(data: T[]): PaginatedResponse<T> => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 20, nextPage: null, totalPages: 1 } } });

describe("evaluation example and dataset hooks", () => {
    afterEach(() => vi.restoreAllMocks());

    test("examples are listed, created and deleted", async () => {
        const list = vi.spyOn(evaluationsApi, "getExamples").mockResolvedValue([{ id: "ex-1" } as EvalExample]);
        const create = vi.spyOn(evaluationsApi, "createExample").mockResolvedValue({ id: "ex-2" } as EvalExample);
        const remove = vi.spyOn(evaluationsApi, "deleteExample").mockResolvedValue();

        const { result } = renderHook(() => useEvalExamples("ds-1"));
        await waitFor(() => expect(result.current.examples).toHaveLength(1));
        await act(() => result.current.createExample({ prompt: "new" }));
        await act(() => result.current.deleteExample("ex-1"));

        expect(list).toHaveBeenCalledWith("ds-1");
        expect(create).toHaveBeenCalledWith("ds-1", { prompt: "new" });
        expect(remove).toHaveBeenCalledWith("ex-1");
        expect(result.current.examples.map(e => e.id)).toEqual(["ex-2"]);
    });

    test("pages of examples and datasets are read", async () => {
        const examples = vi.spyOn(evaluationsApi, "getExamplesPaginated").mockResolvedValue(pageOf([{ id: "ex-1" } as EvalExample]));
        const datasets = vi.spyOn(evaluationsApi, "getDatasetsPaginated").mockResolvedValue(pageOf([{ id: "ds-1" } as EvalDataset]));

        const { result } = renderHook(() => ({ examples: useEvalExamplesPaginated("ds-1"), datasets: useEvalDatasetsPaginated() }));

        await waitFor(() => expect(result.current.examples.examples).toHaveLength(1));
        await waitFor(() => expect(result.current.datasets.datasets).toHaveLength(1));
        expect(examples.mock.calls[0][0]).toBe("ds-1");
        expect(datasets).toHaveBeenCalledWith({ pageNumber: 1, pageSize: 20, name: undefined, sort: "name:asc" });
    });
});
