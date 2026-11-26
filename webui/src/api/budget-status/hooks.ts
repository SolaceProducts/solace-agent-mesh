// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import type { ApiError } from "@/api/client";

import { budgetStatusKeys } from "./keys";
import { getBudgetStatus } from "./service";

function isClientError(error: unknown): boolean {
    const status = (error as ApiError | null | undefined)?.status;
    return status !== undefined && status >= 400 && status < 500;
}

const MAX_TRANSIENT_RETRIES = 3;

function retryTransientFailures(failureCount: number, error: unknown): boolean {
    return !isClientError(error) && failureCount < MAX_TRANSIENT_RETRIES;
}

export function useBudgetStatus() {
    return useQuery({
        queryKey: budgetStatusKeys.all,
        queryFn: getBudgetStatus,
        refetchOnMount: "always",
        retry: retryTransientFailures,
    });
}
