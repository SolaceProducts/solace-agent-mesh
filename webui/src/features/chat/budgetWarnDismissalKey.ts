// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const WARN_DISMISSAL_KEY_PREFIX = "budget-warn-banner-dismissed";

function usernameDigest(username: string): string {
    let hash = 0;
    for (let i = 0; i < username.length; i++) {
        hash = Math.imul(hash, 31) + (username.codePointAt(i) ?? 0);
    }
    return (hash >>> 0).toString(36);
}

export function budgetWarnDismissalKey(username: string): string {
    return `${WARN_DISMISSAL_KEY_PREFIX}:${usernameDigest(username)}`;
}
