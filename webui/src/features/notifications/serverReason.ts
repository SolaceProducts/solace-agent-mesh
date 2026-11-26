// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const GENERIC_ERROR_PREFIX = "Request failed";

export function serverReason(error: unknown): string | undefined {
    const { message, errorId } = (error ?? {}) as { message?: string; errorId?: string };
    return errorId && message?.trim() && !message.startsWith(GENERIC_ERROR_PREFIX) ? message : undefined;
}
