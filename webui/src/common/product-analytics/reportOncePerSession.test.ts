// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "./client";
import { reportOncePerSession } from "./reportOncePerSession";

let trackSpy: ReturnType<typeof vi.spyOn>;
let sessionSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    sessionStorage.clear();
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    sessionSpy = vi.spyOn(productAnalyticsClient, "sessionId").mockReturnValue("session-one");
});

afterEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
});

function reportedNames(): string[] {
    return (trackSpy.mock.calls as unknown[][]).map(call => call[0] as string);
}

describe("reportOncePerSession", () => {
    test("reports the first call and drops the rest of the session", () => {
        reportOncePerSession("builder:opened");
        reportOncePerSession("builder:opened");
        reportOncePerSession("builder:opened");

        expect(reportedNames()).toEqual(["builder:opened"]);
    });

    test("dedupes per event, so a second event still reports", () => {
        reportOncePerSession("builder:opened");
        reportOncePerSession("builder:completed");

        expect(reportedNames()).toEqual(["builder:opened", "builder:completed"]);
    });

    test("records the claim in sessionStorage, keyed by session and event", () => {
        reportOncePerSession("builder:opened");

        const keys = Array.from({ length: sessionStorage.length }, (_, i) => sessionStorage.key(i));
        expect(keys).toEqual(["sam.productAnalytics.sessionOnce.session-one.builder:opened"]);
    });

    test("reports again for a new session", () => {
        reportOncePerSession("builder:opened");
        sessionSpy.mockReturnValue("session-two");

        reportOncePerSession("builder:opened");

        expect(reportedNames()).toEqual(["builder:opened", "builder:opened"]);
    });

    test("claims nothing while there is no session to report under", () => {
        sessionSpy.mockReturnValue(undefined);

        reportOncePerSession("builder:opened");

        expect(sessionStorage.length).toBe(0);
    });

    test("forwards the properties it was given", () => {
        reportOncePerSession("page_view", { path: "/builder" });

        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/builder" });
    });
});
