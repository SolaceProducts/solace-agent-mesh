// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function isConsentWriteRefused(error: unknown): boolean {
    return (error as { status?: number } | null)?.status === 403;
}
