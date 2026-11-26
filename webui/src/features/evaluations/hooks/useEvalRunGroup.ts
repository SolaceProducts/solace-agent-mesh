// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { evaluationsApi } from "@/api/evaluations";

import type { EvalRunGroup } from "@/types/eval";

export function useEvalRunGroup(groupId: string | undefined) {
    const [group, setGroup] = useState<EvalRunGroup | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const fetchGroup = useCallback(async () => {
        if (!groupId) {
            setLoading(false);
            return;
        }
        try {
            const data = await evaluationsApi.getRunGroup(groupId);
            setGroup(data);
        } catch (err) {
            setError(String(err));
        } finally {
            setLoading(false);
        }
    }, [groupId]);

    useEffect(() => {
        void fetchGroup();
    }, [fetchGroup]);

    const isActive = group?.status === "running" || group?.status === "pending";

    useEffect(() => {
        if (!isActive || !groupId) return;
        const interval = setInterval(async () => {
            try {
                const data = await evaluationsApi.getRunGroup(groupId);
                setGroup(data);
            } catch {}
        }, 5000);
        return () => clearInterval(interval);
    }, [isActive, groupId]);

    return { group, loading, error, fetchGroup, isActive };
}
