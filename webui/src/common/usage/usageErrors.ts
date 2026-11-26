// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { USAGE_SLOW_READ_STATUS } from "@/api/usage";

import { getErrorMessage } from "@/utils/api";

export const SLOW_READ_HINT = "The figures took too long to read. Select Refresh in a moment, or choose a shorter period.";

export const TRY_AGAIN_SLOW_READ_HINT = "The figures took too long to read. Try again in a moment.";

export function usageErrorDetail(error: Error | null, slowReadHint = SLOW_READ_HINT): string {
    if (!error) return "";
    if ((error as Error & { status?: number }).status === USAGE_SLOW_READ_STATUS) return slowReadHint;
    return getErrorMessage(error, "");
}

export function usageLoadErrorMessage(lead: string, error: Error | null, slowReadHint?: string): string {
    return [lead, usageErrorDetail(error, slowReadHint)].filter(Boolean).join(" ");
}
