// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type FreeModelErrorKind = "terms" | "sign_in" | "allowance" | "paused";

const PATTERNS: ReadonlyArray<readonly [FreeModelErrorKind, RegExp]> = [
    ["terms", /accept the free model's terms/i],
    ["sign_in", /the sign-in this model uses has expired/i],
    ["paused", /the free model is paused/i],
    ["allowance", /model's allowance is used up/i],
];

export function freeModelErrorKind(message: string): FreeModelErrorKind | null {
    return PATTERNS.find(([, pattern]) => pattern.test(message))?.[0] ?? null;
}
