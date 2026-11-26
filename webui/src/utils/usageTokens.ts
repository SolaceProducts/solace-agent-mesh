// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageTokens } from "@/api/usage";

export const NO_TOKENS: UsageTokens = { uncachedInput: 0, output: 0, cacheRead: 0, cacheWrite: 0 };

export function sumTokens(counts: readonly UsageTokens[]): UsageTokens {
    return counts.reduce((sum, t) => ({ uncachedInput: sum.uncachedInput + t.uncachedInput, output: sum.output + t.output, cacheRead: sum.cacheRead + t.cacheRead, cacheWrite: sum.cacheWrite + t.cacheWrite }), NO_TOKENS);
}

export function totalTokens(tokens: UsageTokens): number {
    return tokens.uncachedInput + tokens.output + tokens.cacheRead + tokens.cacheWrite;
}
