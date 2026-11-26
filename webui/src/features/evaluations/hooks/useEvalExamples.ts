// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { type PaginatedResponse } from "@/api/common";
import { evaluationsApi } from "@/api/evaluations";

import type { CreateExampleRequest, EvalExample, UpdateExampleRequest } from "@/types/eval";

export function useEvalExamples(datasetId: string | undefined) {
    const [examples, setExamples] = useState<EvalExample[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const fetchExamples = useCallback(async () => {
        if (!datasetId) {
            setLoading(false);
            return;
        }
        setLoading(true);
        setError(null);
        try {
            const data = await evaluationsApi.getExamples(datasetId);
            setExamples(data);
        } catch (err) {
            setError(String(err));
        } finally {
            setLoading(false);
        }
    }, [datasetId]);

    useEffect(() => {
        fetchExamples();
    }, [fetchExamples]);

    const createExample = useCallback(
        async (data: CreateExampleRequest): Promise<EvalExample> => {
            if (!datasetId) throw new Error("No dataset ID");
            const newEx = await evaluationsApi.createExample(datasetId, data);
            setExamples(prev => [...prev, newEx]);
            return newEx;
        },
        [datasetId]
    );

    const updateExample = useCallback(
        async (exampleId: string, data: UpdateExampleRequest): Promise<EvalExample> => {
            if (!datasetId) throw new Error("No dataset ID");
            const updated = await evaluationsApi.updateExample(exampleId, data);
            setExamples(prev => prev.map(e => (e.id === exampleId ? updated : e)));
            return updated;
        },
        [datasetId]
    );

    const deleteExample = useCallback(
        async (exampleId: string): Promise<void> => {
            if (!datasetId) throw new Error("No dataset ID");
            await evaluationsApi.deleteExample(exampleId);
            setExamples(prev => prev.filter(e => e.id !== exampleId));
        },
        [datasetId]
    );

    return { examples, loading, error, fetchExamples, createExample, updateExample, deleteExample };
}

export function useEvalExamplesPaginated(datasetId: string | undefined, options?: { pageSize?: number }) {
    const [data, setData] = useState<PaginatedResponse<EvalExample> | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [pageNumber, setPageNumber] = useState(1);
    const pageSize = options?.pageSize ?? 20;
    const [contentFilter, setContentFilter] = useState("");

    const [debouncedFilter, setDebouncedFilter] = useState("");
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const setContentFilterDebounced = useCallback((value: string) => {
        setContentFilter(value);
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

    const fetchExamples = useCallback(
        async (silent = false) => {
            if (!datasetId) {
                setLoading(false);
                return;
            }
            if (!silent) setLoading(true);
            setError(null);
            try {
                const res = await evaluationsApi.getExamplesPaginated(datasetId, {
                    pageNumber,
                    pageSize,
                    content: debouncedFilter || undefined,
                });
                setData(res);
            } catch (err) {
                setError(String(err));
            } finally {
                setLoading(false);
            }
        },
        [datasetId, pageNumber, pageSize, debouncedFilter]
    );

    useEffect(() => {
        fetchExamples();
    }, [fetchExamples]);

    useEffect(() => {
        setPageNumber(1);
        setContentFilter("");
        setDebouncedFilter("");
    }, [datasetId]);

    const examples = data?.data ?? [];
    const totalPages = data?.meta?.pagination?.totalPages ?? 1;
    const totalCount = data?.meta?.pagination?.count ?? 0;

    return {
        examples,
        loading,
        error,
        pageNumber,
        setPageNumber,
        totalPages,
        totalCount,
        contentFilter,
        setContentFilter: setContentFilterDebounced,
        fetchExamples,
    };
}
