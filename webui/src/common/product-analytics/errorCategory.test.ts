// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { classifyErrorCategory } from "./errorCategory";

describe("classifyErrorCategory", () => {
    test.each([
        ["auth_failure", "Request failed with status 401"],
        ["auth_failure", "Incorrect API key provided"],
        ["auth_failure", "You are not authorized to perform this action"],
        ["rate_limit", "429 Too Many Requests"],
        ["rate_limit", "Rate limit reached for gpt-4o"],
        ["rate_limit", "That model is currently overloaded"],
        ["context_overflow", "This model's maximum context length is 8192 tokens"],
        ["context_overflow", "prompt is too long: 300000 tokens"],
        ["content_filtered", "The response was flagged by the content filter"],
        ["content_filtered", "Blocked by our safety policy"],
        ["timeout", "The request timed out"],
        ["timeout", "504 Gateway Timeout"],
        ["budget_exhausted", "This request was stopped because this deployment's usage budget is used up until 1 November 2099 00:00 UTC."],
        ["budget_exhausted", "Sorry, I can't take new requests right now: this deployment's usage budget is used up until 1 November 2099 00:00 UTC."],
        ["free_llm_terms_required", "Accept the free model's terms to keep using it."],
        ["free_llm_paused", "The free model is paused. Add your own model to keep going."],
        ["allowance_exhausted", "This model's allowance is used up. Choose another model to keep going."],
        ["auth_failure", "The sign-in this model uses has expired. Sign in again to keep using it."],
        ["unknown", "This model's sign-in service can't be reached right now. Try again in a few minutes."],
    ])("classifies %s", (expected, message) => {
        expect(classifyErrorCategory(message)).toBe(expected);
    });

    test("falls back to unknown rather than inventing a bucket", () => {
        expect(classifyErrorCategory("Agent Deletion Failed")).toBe("unknown");
        expect(classifyErrorCategory("")).toBe("unknown");
    });

    test("an auth failure wins over other words in the same message", () => {
        expect(classifyErrorCategory("401 Unauthorized after the request timed out")).toBe("auth_failure");
    });
});
