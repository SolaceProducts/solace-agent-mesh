// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api, type ApiError } from "@/api/client";

import type { BudgetStatus } from "./types";

export async function getBudgetStatus(): Promise<BudgetStatus> {
    return api.webui.get("/api/v1/budget/status");
}

export const RPC_CODE_BUDGET_EXHAUSTED = -32029;

export const RPC_CODE_BUDGET_UNCONFIRMED = -32030;

export function isBudgetRefusal(error: unknown): boolean {
    return (error as ApiError | undefined)?.rpcErrorCode === RPC_CODE_BUDGET_EXHAUSTED;
}

export function isBudgetSubmitRefusal(error: unknown): boolean {
    const code = (error as ApiError | undefined)?.rpcErrorCode;
    return code === RPC_CODE_BUDGET_EXHAUSTED || code === RPC_CODE_BUDGET_UNCONFIRMED;
}
