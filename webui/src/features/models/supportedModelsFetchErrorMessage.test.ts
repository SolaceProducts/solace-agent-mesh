// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { supportedModelsFetchErrorMessage } from "./ModelConnectionFields";

describe("supportedModelsFetchErrorMessage", () => {
    const envelope = (message: string, errorId = "6f1b8c1e-2c7a-4d0e-9a53-1d7f4c2b9e10") => Object.assign(new Error(message), { errorId });

    it("falls back to the generic help text when there is no error", () => {
        expect(supportedModelsFetchErrorMessage("model", null)).toBe("Unable to find models. Verify that your model connection details above are correct.");
    });

    it("leads with the backend message (errorId present) instead of the generic, then the Verify hint", () => {
        const err = envelope("Unable to list models from the provider");
        expect(supportedModelsFetchErrorMessage("model", err)).toBe("Unable to list models from the provider. Verify that your model connection details above are correct.");
    });

    it("trims trailing punctuation on the backend message so the sentence reads naturally", () => {
        const err = envelope("Unable to list models from the provider.");
        expect(supportedModelsFetchErrorMessage("model", err)).toBe("Unable to list models from the provider. Verify that your model connection details above are correct.");
    });

    it("falls back to the generic for an envelope-less error (no errorId)", () => {
        const err = new Error("failed to list models from provider: http: no Host in request URL");
        expect(supportedModelsFetchErrorMessage("model", err)).toBe("Unable to find models. Verify that your model connection details above are correct.");
    });

    it("falls back to the generic on the client's Request failed: fallback", () => {
        const err = Object.assign(new Error("Request failed: 500 Internal Server Error"), { status: 500 });
        expect(supportedModelsFetchErrorMessage("model", err)).toBe("Unable to find models. Verify that your model connection details above are correct.");
    });

    it("respects the noun on the fallback (deployments vs models)", () => {
        const err = Object.assign(new Error("Request failed: 503 Service Unavailable"), { status: 503 });
        expect(supportedModelsFetchErrorMessage("deployment", err)).toBe("Unable to find deployments. Verify that your model connection details above are correct.");
    });
});
