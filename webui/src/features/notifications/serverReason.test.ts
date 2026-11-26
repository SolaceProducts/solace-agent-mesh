// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { serverReason } from "./serverReason";

const fromService = (message: string, status: number) => Object.assign(new Error(message), { status, errorId: "err-1" });
const fromElsewhere = (message: string, status: number) => Object.assign(new Error(message), { status });

describe("serverReason", () => {
    it("reads the service's own words", () => {
        expect(serverReason(fromService("no Slack account matches your login email", 400))).toBe("no Slack account matches your login email");
    });

    it.each([
        ["a proxy's plain-text error", fromElsewhere("no healthy upstream", 503)],
        ["a router's not found", fromElsewhere("404 page not found\n", 404)],
        ["the client's own sign-in text", fromElsewhere("Not authenticated", 401)],
        ["the client's generic fallback", fromElsewhere("Request failed: Bad Gateway", 502)],
        ["a proxy's html page", fromElsewhere("<html><body>502</body></html>", 502)],
        ["a whitespace-only body", fromElsewhere("\n", 502)],
        ["the browser's text for a request that got no answer", new TypeError("Failed to fetch")],
        ["a timeout", new DOMException("signal timed out", "TimeoutError")],
    ])("reads nothing from %s", (_name, error) => {
        expect(serverReason(error)).toBeUndefined();
    });

    it("reads nothing from the service's envelope with no message", () => {
        expect(serverReason(fromService("  ", 500))).toBeUndefined();
        expect(serverReason(fromService("Request failed: Internal Server Error", 500))).toBeUndefined();
    });

    it("reads nothing from a value that is not an error", () => {
        expect(serverReason(null)).toBeUndefined();
        expect(serverReason(undefined)).toBeUndefined();
    });
});
