// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { evaluationsApi } from "@/api/evaluations";

import type { EvalRunResult } from "@/types/eval";

export function useEvalRunResultDetail(resultId: string | undefined) {
    const [result, setResult] = useState<EvalRunResult | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const fetchResult = useCallback(async () => {
        if (!resultId) {
            setLoading(false);
            return;
        }
        setLoading(true);
        setError(null);
        try {
            const data = await evaluationsApi.getRunResult(resultId);
            setResult(data);
        } catch (err) {
            setError(String(err));
        } finally {
            setLoading(false);
        }
    }, [resultId]);

    useEffect(() => {
        fetchResult();
    }, [fetchResult]);

    return { result, loading, error };
}
