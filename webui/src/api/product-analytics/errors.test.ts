// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { isConsentWriteRefused } from "./errors";

function withStatus(status: number): Error {
    const error = new Error("boom") as Error & { status?: number };
    error.status = status;
    return error;
}

describe("isConsentWriteRefused", () => {
    test("treats 403 as a permission refusal", () => {
        expect(isConsentWriteRefused(withStatus(403))).toBe(true);
    });

    test.each([400, 401, 404, 405, 409, 408, 429, 500, 502, 503])("treats %i as something other than a refusal, so the UI keeps offering retry", status => {
        expect(isConsentWriteRefused(withStatus(status))).toBe(false);
    });

    test("treats an error with no status as retryable", () => {
        expect(isConsentWriteRefused(new Error("network down"))).toBe(false);
    });

    test("tolerates a non-error rejection", () => {
        expect(isConsentWriteRefused(null)).toBe(false);
        expect(isConsentWriteRefused(undefined)).toBe(false);
        expect(isConsentWriteRefused("nope")).toBe(false);
    });
});
