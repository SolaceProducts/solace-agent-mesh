// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { type PaginatedResponse } from "@/api/common";
import { evaluationsApi } from "@/api/evaluations";

import type { EvalDataset } from "@/types/eval";

export function useEvalDatasetsPaginated() {
    const [data, setData] = useState<PaginatedResponse<EvalDataset> | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [pageNumber, setPageNumber] = useState(1);
    const [pageSize] = useState(20);
    const [nameFilter, setNameFilter] = useState("");
    const [sortBy, setSortBy] = useState("name");
    const [sortOrder, setSortOrder] = useState("asc");

    const [debouncedFilter, setDebouncedFilter] = useState("");
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const setNameFilterDebounced = useCallback((value: string) => {
        setNameFilter(value);
        if (timerRef.current) clearTimeout(timerRef.current);
        timerRef.current = setTimeout(() => {
            setDebouncedFilter(value);
            setPageNumber(1);
        }, 300);
    }, []);

    useEffect(
        () => () => {
            if (timerRef.current) clearTimeout(timerRef.current);
        },
        []
    );

    const setSortByAndResetPage = useCallback((value: string) => {
        setSortBy(value);
        setPageNumber(1);
    }, []);

    const setSortOrderAndResetPage = useCallback((value: string) => {
        setSortOrder(value);
        setPageNumber(1);
    }, []);

    const fetchDatasets = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            const res = await evaluationsApi.getDatasetsPaginated({
                pageNumber,
                pageSize,
                name: debouncedFilter || undefined,
                sort: `${sortBy}:${sortOrder}`,
            });
            setData(res);
        } catch (err) {
            setError(String(err));
        } finally {
            setLoading(false);
        }
    }, [pageNumber, pageSize, debouncedFilter, sortBy, sortOrder]);

    useEffect(() => {
        fetchDatasets();
    }, [fetchDatasets]);

    const datasets = data?.data ?? [];
    const totalPages = data?.meta?.pagination?.totalPages ?? 1;
    const totalCount = data?.meta?.pagination?.count ?? 0;
    const currentPageSize = data?.meta?.pagination?.pageSize ?? pageSize;

    return {
        datasets,
        loading,
        error,
        pageNumber,
        setPageNumber,
        totalPages,
        totalCount,
        currentPageSize,
        nameFilter,
        setNameFilter: setNameFilterDebounced,
        sortBy,
        setSortBy: setSortByAndResetPage,
        sortOrder,
        setSortOrder: setSortOrderAndResetPage,
        fetchDatasets,
    };
}
