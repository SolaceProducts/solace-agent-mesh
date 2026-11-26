// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LlmErrorCategory } from "./types";

const CATEGORY_PATTERNS: ReadonlyArray<readonly [LlmErrorCategory, RegExp]> = [
    ["budget_exhausted", /usage budget is used up/i],
    ["free_llm_terms_required", /accept the free model's terms/i],
    ["free_llm_paused", /the free model is paused/i],
    ["allowance_exhausted", /model's allowance is used up/i],
    ["auth_failure", /the sign-in this model uses has expired/i],
    ["auth_failure", /\b(401|403)\b|authori[sz]|forbidden|authenticat|api[ -]?key|permission denied|credential/i],
    ["rate_limit", /\b429\b|rate[ -]?limit|too many requests|quota exceeded|overloaded/i],
    ["context_overflow", /context[ _-]?(length|window|overflow)|maximum context|token limit|too many tokens|prompt is too long/i],
    ["content_filtered", /content[ _-]?filter|content policy|safety (filter|policy)|flagged|moderation/i],
    ["timeout", /\b(408|504)\b|timed? ?out|timeout|deadline exceeded/i],
];

export function classifyErrorCategory(message: string): LlmErrorCategory {
    for (const [category, pattern] of CATEGORY_PATTERNS) {
        if (pattern.test(message)) return category;
    }
    return "unknown";
}
