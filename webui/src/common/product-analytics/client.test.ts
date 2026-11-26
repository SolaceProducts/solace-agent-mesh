// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ProductAnalyticsClient } from "./client";

const BATCH_SIZE = 25;
const FLUSH_INTERVAL_MS = 5_000;
const MAX_CONSECUTIVE_FAILURES = 20;

function makeClient() {
    return new ProductAnalyticsClient();
}

describe("ProductAnalyticsClient — track and batch flush", () => {
    let client: ProductAnalyticsClient;
    let groupCollapsedSpy: ReturnType<typeof vi.spyOn>;
    let logSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        groupCollapsedSpy = vi.spyOn(console, "groupCollapsed").mockImplementation(() => {});
        logSpy = vi.spyOn(console, "log").mockImplementation(() => {});
        vi.spyOn(console, "groupEnd").mockImplementation(() => {});
        client = makeClient();
        client.setFeatureEnabled(true);
        client.setConsent(true);
    });

    afterEach(() => {
        client.destroy();
        vi.restoreAllMocks();
    });

    test("enriches every event with interface:ui and carries no time in properties", () => {
        for (let i = 0; i < BATCH_SIZE; i++) {
            client.track("page_view", { path: "/agent-mesh" });
        }
        expect(logSpy).toHaveBeenCalledWith("page_view", {
            interface: "ui",
            path: "/agent-mesh",
        });
    });

    test("flushes automatically when the queue reaches BATCH_SIZE", () => {
        for (let i = 0; i < BATCH_SIZE; i++) {
            client.track("page_view");
        }
        expect(groupCollapsedSpy).toHaveBeenCalledWith(`[Product Analytics] ${BATCH_SIZE} event(s)`);
    });

    test("does not flush before BATCH_SIZE is reached", () => {
        for (let i = 0; i < BATCH_SIZE - 1; i++) {
            client.track("page_view");
        }
        expect(groupCollapsedSpy).not.toHaveBeenCalled();
    });

    test("flushes only the queued events and leaves the queue empty afterwards", () => {
        for (let i = 0; i < BATCH_SIZE; i++) {
            client.track("page_view");
        }
        groupCollapsedSpy.mockClear();

        client.track("page_view");
        expect(groupCollapsedSpy).not.toHaveBeenCalled();
    });

    test("flushes on the periodic timer", () => {
        vi.useFakeTimers();
        const timerClient = makeClient();
        timerClient.setFeatureEnabled(true);
        timerClient.setConsent(true);

        timerClient.track("chat:first_response");
        expect(groupCollapsedSpy).not.toHaveBeenCalled();

        vi.advanceTimersByTime(FLUSH_INTERVAL_MS);
        expect(groupCollapsedSpy).toHaveBeenCalledWith("[Product Analytics] 1 event(s)");

        timerClient.destroy();
        vi.useRealTimers();
    });

    test("flushes when the tab becomes hidden", () => {
        client.track("page_view");
        expect(groupCollapsedSpy).not.toHaveBeenCalled();

        Object.defineProperty(document, "hidden", { value: true, configurable: true });
        document.dispatchEvent(new Event("visibilitychange"));
        Object.defineProperty(document, "hidden", { value: false, configurable: true });

        expect(groupCollapsedSpy).toHaveBeenCalledWith("[Product Analytics] 1 event(s)");
    });

    test("does not flush when the tab becomes visible", () => {
        client.track("page_view");

        Object.defineProperty(document, "hidden", { value: false, configurable: true });
        document.dispatchEvent(new Event("visibilitychange"));

        expect(groupCollapsedSpy).not.toHaveBeenCalled();
    });

    test("does not flush when consent is disabled, even when BATCH_SIZE is reached", () => {
        client.setConsent(false);
        for (let i = 0; i < BATCH_SIZE; i++) {
            client.track("page_view");
        }
        expect(groupCollapsedSpy).not.toHaveBeenCalled();
    });

    test("does not flush when the feature flag is disabled, even when BATCH_SIZE is reached", () => {
        client.setFeatureEnabled(false);
        for (let i = 0; i < BATCH_SIZE; i++) {
            client.track("page_view");
        }
        expect(groupCollapsedSpy).not.toHaveBeenCalled();
    });

    test("a fresh client drops events until BOTH feature and consent are enabled", () => {
        const fresh = makeClient();

        for (let i = 0; i < BATCH_SIZE; i++) fresh.track("page_view");
        expect(groupCollapsedSpy).not.toHaveBeenCalled();

        fresh.setFeatureEnabled(true);
        for (let i = 0; i < BATCH_SIZE; i++) fresh.track("page_view");
        expect(groupCollapsedSpy).not.toHaveBeenCalled();

        fresh.setConsent(true);
        for (let i = 0; i < BATCH_SIZE; i++) fresh.track("page_view");
        expect(groupCollapsedSpy).toHaveBeenCalledWith(`[Product Analytics] ${BATCH_SIZE} event(s)`);

        fresh.destroy();
    });
});

describe("ProductAnalyticsClient — lifecycle guarantees", () => {
    beforeEach(() => {
        vi.spyOn(console, "groupCollapsed").mockImplementation(() => {});
        vi.spyOn(console, "log").mockImplementation(() => {});
        vi.spyOn(console, "groupEnd").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("a flag-off client installs no document listeners", () => {
        const addSpy = vi.spyOn(document, "addEventListener");

        const idle = makeClient();

        expect(addSpy).not.toHaveBeenCalledWith("visibilitychange", expect.anything(), expect.anything());

        idle.destroy();
    });

    test("destroy resets the client so a later re-enable re-attaches listeners", () => {
        const groupCollapsedSpy = vi.spyOn(console, "groupCollapsed");
        const client = makeClient();
        client.setFeatureEnabled(true);
        client.setConsent(true);
        client.destroy();

        client.setFeatureEnabled(true);
        client.setConsent(true);
        client.track("page_view");

        Object.defineProperty(document, "hidden", { value: true, configurable: true });
        document.dispatchEvent(new Event("visibilitychange"));
        Object.defineProperty(document, "hidden", { value: false, configurable: true });

        expect(groupCollapsedSpy).toHaveBeenCalled();

        client.destroy();
    });

    test("warns once at the onset of an outage, naming the dropped event count", () => {
        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        vi.spyOn(console, "error").mockImplementation(() => {});
        const client = new ProductAnalyticsClient({
            transport: () => {
                throw new Error("transport down");
            },
        });
        client.setFeatureEnabled(true);
        client.setConsent(true);

        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining(`dropping up to ${BATCH_SIZE} event(s)`), expect.any(Error));

        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        expect(warnSpy).toHaveBeenCalledOnce();

        client.destroy();
    });

    test("warns again after a recovery, so a second outage is not silent", async () => {
        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        let shouldFail = true;
        const client = new ProductAnalyticsClient({
            transport: () => {
                if (shouldFail) throw new Error("transport down");
            },
        });
        client.setFeatureEnabled(true);
        client.setConsent(true);

        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        expect(warnSpy).toHaveBeenCalledOnce();

        shouldFail = false;
        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        await new Promise(resolve => setTimeout(resolve, 0));

        shouldFail = true;
        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        expect(warnSpy).toHaveBeenCalledTimes(2);

        client.destroy();
    });

    test("destroy resets a tripped circuit breaker so a re-enabled client emits again", () => {
        const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        let shouldFail = true;
        const sent: { event: string; properties: Record<string, unknown> }[][] = [];
        const client = new ProductAnalyticsClient({
            transport: events => {
                if (shouldFail) throw new Error("transport down");
                sent.push(events);
            },
        });
        client.setFeatureEnabled(true);
        client.setConsent(true);

        for (let f = 0; f < MAX_CONSECUTIVE_FAILURES; f++) {
            for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        }
        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        expect(sent).toHaveLength(0);
        expect(errorSpy).toHaveBeenCalledWith(expect.stringContaining("giving up after 20 consecutive flush failures"));
        expect(errorSpy).toHaveBeenCalledOnce();

        client.destroy();
        shouldFail = false;
        client.setFeatureEnabled(true);
        client.setConsent(true);
        for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        expect(sent).toHaveLength(1);

        client.destroy();
    });

    test("reports the give-up once when two in-flight sends both cross the threshold", async () => {
        const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        vi.spyOn(console, "warn").mockImplementation(() => {});
        const rejectInFlight: (() => void)[] = [];
        let syncFailuresLeft = MAX_CONSECUTIVE_FAILURES - 1;
        const client = new ProductAnalyticsClient({
            transport: () => {
                if (syncFailuresLeft > 0) {
                    syncFailuresLeft--;
                    throw new Error("transport down");
                }
                return new Promise<void>((_, reject) => rejectInFlight.push(() => reject(new Error("transport down"))));
            },
        });
        client.setFeatureEnabled(true);
        client.setConsent(true);

        for (let f = 0; f < MAX_CONSECUTIVE_FAILURES - 1; f++) {
            for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        }
        expect(errorSpy).not.toHaveBeenCalled();

        for (let f = 0; f < 2; f++) {
            for (let i = 0; i < BATCH_SIZE; i++) client.track("page_view");
        }
        expect(rejectInFlight).toHaveLength(2);

        rejectInFlight.forEach(reject => reject());
        await new Promise(resolve => setTimeout(resolve, 0));

        expect(errorSpy).toHaveBeenCalledOnce();

        client.destroy();
    });

    test("revoking consent purges queued events so they cannot flush after re-consent", () => {
        const groupCollapsedSpy = vi.spyOn(console, "groupCollapsed");
        const client = makeClient();
        client.setFeatureEnabled(true);
        client.setConsent(true);

        client.track("page_view");
        client.setConsent(false);
        client.setConsent(true);

        Object.defineProperty(document, "hidden", { value: true, configurable: true });
        document.dispatchEvent(new Event("visibilitychange"));
        Object.defineProperty(document, "hidden", { value: false, configurable: true });

        expect(groupCollapsedSpy).not.toHaveBeenCalled();

        client.destroy();
    });
});
