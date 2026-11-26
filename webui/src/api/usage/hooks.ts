// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { keepPreviousData, type QueryKey, skipToken, useQuery, useQueryClient } from "@tanstack/react-query";

import { usageKeys } from "./keys";
import type { UsageKind, UsageListParams, UsageSubjectRef, UsageWindowParams } from "./types";
import { usageApi } from "./versioned";

type RetryOption = boolean | number | ((failureCount: number, error: Error) => boolean);

const DEFAULT_RETRIES = 3;
const TRANSIENT_CLIENT_ERRORS = new Set([408, 425, 429]);
export const USAGE_SLOW_READ_STATUS = 503;
const USAGE_STALE_TIME_MS = 0;

function retryUnlessClientErrorOrSlowRead(fallback: RetryOption | undefined): (failureCount: number, error: Error) => boolean {
    return (failureCount, error) => {
        const status = (error as Error & { status?: number }).status;
        if (status === USAGE_SLOW_READ_STATUS) return false;
        if (status !== undefined && status >= 400 && status < 500 && !TRANSIENT_CLIENT_ERRORS.has(status)) return false;
        if (typeof fallback === "function") return fallback(failureCount, error);
        if (typeof fallback === "number") return failureCount < fallback;
        return fallback ?? failureCount < DEFAULT_RETRIES;
    };
}

function useRetryFor(): (queryKey: QueryKey) => (failureCount: number, error: Error) => boolean {
    const client = useQueryClient();
    return useCallback((queryKey: QueryKey) => retryUnlessClientErrorOrSlowRead((client.getQueryDefaults(queryKey).retry ?? client.getDefaultOptions().queries?.retry) as RetryOption | undefined), [client]);
}

export function useUsageSummary(windowParams: UsageWindowParams, { enabled = true }: { enabled?: boolean } = {}) {
    const retryFor = useRetryFor();
    const queryKey = usageKeys.summary(windowParams);
    return useQuery({
        queryKey,
        queryFn: () => usageApi.getSummary(windowParams),
        placeholderData: keepPreviousData,
        retry: retryFor(queryKey),
        staleTime: USAGE_STALE_TIME_MS,
        enabled,
    });
}

export function useUsageList<K extends UsageKind>(kind: K, params: UsageListParams<K>, { enabled = true }: { enabled?: boolean } = {}) {
    const retryFor = useRetryFor();
    const queryKey = usageKeys.list(kind, params);
    return useQuery({
        queryKey,
        queryFn: () => usageApi.list(kind, params),
        placeholderData: keepPreviousData,
        retry: retryFor(queryKey),
        staleTime: USAGE_STALE_TIME_MS,
        enabled,
    });
}

export function useUsageDetail<K extends UsageKind>(kind: K, ref: UsageSubjectRef | null, windowParams: UsageWindowParams) {
    const retryFor = useRetryFor();
    const queryKey = usageKeys.detail(kind, ref, windowParams);
    return useQuery({
        queryKey,
        queryFn: ref ? () => usageApi.getDetail(kind, ref, windowParams) : skipToken,
        retry: retryFor(queryKey),
        staleTime: USAGE_STALE_TIME_MS,
    });
}

export function useSelfUsage(windowParams: UsageWindowParams, { enabled = true }: { enabled?: boolean } = {}) {
    const retryFor = useRetryFor();
    const queryKey = usageKeys.self(windowParams);
    return useQuery({
        queryKey,
        queryFn: () => usageApi.getSelf(windowParams),
        retry: retryFor(queryKey),
        staleTime: USAGE_STALE_TIME_MS,
        enabled,
    });
}

export function useUsageSavings(windowParams: UsageWindowParams) {
    const retryFor = useRetryFor();
    const queryKey = usageKeys.savings(windowParams);
    return useQuery({
        queryKey,
        queryFn: () => usageApi.getSavings(windowParams),
        placeholderData: keepPreviousData,
        retry: retryFor(queryKey),
        staleTime: USAGE_STALE_TIME_MS,
    });
}

export function useSelfUsageSavings(windowParams: UsageWindowParams) {
    const retryFor = useRetryFor();
    const queryKey = usageKeys.selfSavings(windowParams);
    return useQuery({
        queryKey,
        queryFn: () => usageApi.getSelfSavings(windowParams),
        retry: retryFor(queryKey),
        staleTime: USAGE_STALE_TIME_MS,
    });
}
