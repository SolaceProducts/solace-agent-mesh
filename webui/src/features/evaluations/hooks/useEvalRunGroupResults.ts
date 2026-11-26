// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { evaluationsApi } from "@/api/evaluations";

import type { EvalRunGroupResults } from "@/types/eval";

export function useEvalRunGroupResults(groupId: string | undefined, isActive: boolean) {
    const [results, setResults] = useState<EvalRunGroupResults | null>(null);
    const [loading, setLoading] = useState(true);
    const pollInFlight = useRef(false);

    const fetchResults = useCallback(async () => {
        if (!groupId) {
            setLoading(false);
            return;
        }
        try {
            const data = await evaluationsApi.getRunGroupResults(groupId);
            setResults(data);
        } finally {
            setLoading(false);
        }
    }, [groupId]);

    useEffect(() => {
        void fetchResults();
    }, [fetchResults]);

    useEffect(() => {
        if (!isActive || !groupId) return;
        const interval = setInterval(async () => {
            if (pollInFlight.current) return;
            pollInFlight.current = true;
            try {
                const data = await evaluationsApi.getRunGroupResults(groupId);
                setResults(data);
            } catch {
            } finally {
                pollInFlight.current = false;
            }
        }, 5000);
        return () => clearInterval(interval);
    }, [isActive, groupId]);

    return { results, loading, fetchResults };
}
