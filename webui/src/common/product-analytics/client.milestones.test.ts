// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ProductAnalyticsClient } from "./client";

let client: ProductAnalyticsClient;
const sent: string[] = [];

const PAST_ONE_FLUSH_MS = 6_000;

function openClient(...decisions: boolean[]): ProductAnalyticsClient {
    const next = new ProductAnalyticsClient({
        transport: events => {
            events.forEach(e => sent.push(e.event));
        },
    });
    next.setFeatureEnabled(true);
    next.setConsent(false);
    decisions.forEach(decision => next.setConsent(decision));
    return next;
}

beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
    sessionStorage.clear();
    sent.length = 0;
    client = openClient(true);
});

afterEach(() => {
    client.destroy();
    vi.useRealTimers();
    vi.restoreAllMocks();
});

function queued(): string[] {
    vi.advanceTimersByTime(PAST_ONE_FLUSH_MS);
    return sent;
}

describe("trackOnce", () => {
    test("emits the first time and never again for this page load", () => {
        client.trackOnce("app:launched");
        client.trackOnce("app:launched");
        client.trackOnce("app:launched");

        expect(queued()).toEqual(["app:launched"]);
    });

    test("does not consume the one chance while consent is still closed", () => {
        client.setConsent(false);
        client.trackOnce("app:launched");
        client.setConsent(true);
        client.trackOnce("app:launched");

        expect(queued()).toEqual(["app:launched"]);
    });

    test("leaves other events alone", () => {
        client.trackOnce("app:launched");
        client.track("page_view");
        client.track("page_view");

        expect(queued()).toEqual(["app:launched", "page_view", "page_view"]);
    });
});

describe("trackFirstEver", () => {
    test("emits once and records the milestone in localStorage", () => {
        client.trackFirstEver("chat:first_response");
        client.trackFirstEver("chat:first_response");

        expect(queued()).toEqual(["chat:first_response"]);
        expect(localStorage.getItem("sam.productAnalytics.milestone.chat:first_response")).not.toBeNull();
    });

    test("stays silent for a fresh client on the same install, unlike trackOnce", () => {
        client.trackFirstEver("chat:first_response");
        vi.advanceTimersByTime(PAST_ONE_FLUSH_MS);
        client.destroy();

        const reloaded = openClient(true);
        reloaded.trackFirstEver("chat:first_response");
        reloaded.trackOnce("app:launched");
        vi.advanceTimersByTime(PAST_ONE_FLUSH_MS);
        reloaded.destroy();

        expect(sent).toEqual(["chat:first_response", "app:launched"]);
    });

    test("forgets the milestone on a load that reads a withdrawal it never watched happen", () => {
        client.trackFirstEver("chat:first_response");
        vi.advanceTimersByTime(PAST_ONE_FLUSH_MS);
        client.destroy();
        sent.length = 0;

        const reloaded = openClient(false, true);
        reloaded.trackFirstEver("chat:first_response");
        vi.advanceTimersByTime(PAST_ONE_FLUSH_MS);
        reloaded.destroy();

        expect(sent).toEqual(["chat:first_response"]);
    });

    test("withdrawing consent forgets the milestone, so a later install can report it again", () => {
        client.trackFirstEver("chat:first_response");
        client.setConsent(false);

        expect(localStorage.getItem("sam.productAnalytics.milestone.chat:first_response")).toBeNull();

        client.setConsent(true);
        client.trackFirstEver("chat:first_response");

        expect(queued()).toEqual(["chat:first_response"]);
    });

    test("survives unavailable storage by reporting rather than losing the milestone", () => {
        vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
            throw new Error("storage disabled");
        });
        vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
            throw new Error("storage disabled");
        });

        expect(() => client.trackFirstEver("chat:first_response")).not.toThrow();
        expect(queued()).toEqual(["chat:first_response"]);
    });
});
