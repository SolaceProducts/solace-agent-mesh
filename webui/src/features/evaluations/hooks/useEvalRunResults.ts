// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { evaluationsApi } from "@/api/evaluations";

import type { EvalRunResult } from "@/types/eval";

export function useEvalRunResults(runId: string | undefined) {
    const [results, setResults] = useState<EvalRunResult[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const fetchResults = useCallback(async () => {
        if (!runId) {
            setLoading(false);
            return;
        }
        setLoading(true);
        setError(null);
        try {
            const data = await evaluationsApi.getRunResults(runId);
            setResults(data);
        } catch (err) {
            setError(String(err));
        } finally {
            setLoading(false);
        }
    }, [runId]);

    useEffect(() => {
        fetchResults();
    }, [fetchResults]);

    return { results, loading, error, fetchResults };
}
