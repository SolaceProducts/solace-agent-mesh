// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { fetchTransport, ProductAnalyticsClient } from "./client";
import type { ClientPlatform, PlatformSource } from "./platform";

const INGEST_ENDPOINT = "/api/v1/productAnalytics/ingest";
const BATCH_SIZE = 25;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

interface Envelope {
    eventId: string;
    sessionId: string;
    occurredAt: string;
    event: string;
    properties: Record<string, unknown>;
}

let client: ProductAnalyticsClient;
let postSpy: ReturnType<typeof vi.spyOn>;

function makeProdClient() {
    const c = new ProductAnalyticsClient({ transport: fetchTransport });
    c.setFeatureEnabled(true);
    c.setConsent(true);
    return c;
}

function hideTab() {
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    Object.defineProperty(document, "hidden", { value: false, configurable: true });
}

beforeEach(() => {
    postSpy = vi.spyOn(api.webui, "post").mockResolvedValue(undefined as never);
    client = makeProdClient();
});

afterEach(() => {
    client.destroy();
    vi.restoreAllMocks();
});

describe("ProductAnalyticsClient — PROD send path (api client)", () => {
    test("routes size-triggered flushes through api.webui.post to the ingest endpoint", async () => {
        for (let i = 0; i < 25; i++) client.track("page_view", { path: "/agent-mesh" });

        expect(postSpy).toHaveBeenCalledOnce();
        const [endpoint, body] = postSpy.mock.calls[0] as [string, { events: unknown[] }];
        expect(endpoint).toBe(INGEST_ENDPOINT);
        expect(Array.isArray(body.events)).toBe(true);
        expect(body.events.length).toBe(25);
    });

    test("size- and timer-triggered flushes do not use keepalive", async () => {
        for (let i = 0; i < 25; i++) client.track("page_view");

        const options = postSpy.mock.calls[0][2] as { keepalive?: boolean } | undefined;
        expect(options?.keepalive).not.toBe(true);
    });

    test("the tab-hide flush uses keepalive so an in-flight batch survives unload", async () => {
        client.track("chat:first_response");
        hideTab();

        expect(postSpy).toHaveBeenCalledOnce();
        const options = postSpy.mock.calls[0][2] as { keepalive?: boolean };
        expect(options.keepalive).toBe(true);
    });

    function sessionIdOfNextFlush(): string {
        client.track("page_view");
        hideTab();
        const [, body] = postSpy.mock.calls.at(-1) as [string, { events: { sessionId: string }[] }];
        return body.events[0].sessionId;
    }

    test("withdrawing consent forgets the session so a re-grant cannot be joined to it", () => {
        const before = sessionIdOfNextFlush();

        client.setConsent(false);
        client.setConsent(true);

        expect(sessionIdOfNextFlush()).not.toBe(before);
    });

    test("the session id is otherwise stable across flushes within one tab", () => {
        expect(sessionIdOfNextFlush()).toBe(sessionIdOfNextFlush());
    });

    test("a consent read that fails forgets the session, because a withdrawal elsewhere looks exactly like it", () => {
        const before = sessionIdOfNextFlush();

        client.setConsent(false);
        client.setConsent(true);

        expect(sessionIdOfNextFlush()).not.toBe(before);
    });

    function sessionIdAfterAPageLoad(): string {
        const loaded = new ProductAnalyticsClient({ transport: fetchTransport });
        loaded.setFeatureEnabled(true);
        loaded.setConsent(false);
        loaded.setConsent(true);
        loaded.track("page_view");
        hideTab();
        const [, body] = postSpy.mock.calls.at(-1) as [string, { events: { sessionId: string }[] }];
        loaded.destroy();
        return body.events[0].sessionId;
    }

    test("the session survives a page load, where the gate mirrors consent as false first", () => {
        expect(sessionIdAfterAPageLoad()).toBe(sessionIdAfterAPageLoad());
    });

    test("each event carries interface:ui and its envelope alongside the properties", async () => {
        client.track("error:displayed", { errorCategory: "timeout" });
        for (let i = 1; i < 25; i++) client.track("page_view");

        const [, body] = postSpy.mock.calls[0] as [string, { events: Envelope[] }];
        const created = body.events.find(e => e.event === "error:displayed");

        expect(created).toBeDefined();
        expect(created!.properties["interface"]).toBe("ui");
        expect(created!.properties["errorCategory"]).toBe("timeout");
        expect(created!.properties["timestamp"]).toBeUndefined();
        expect(created!.occurredAt).toMatch(/^\d{4}-\d{2}-\d{2}T/);
        expect(created!.eventId).toMatch(UUID_RE);
        expect(created!.sessionId).toMatch(UUID_RE);
    });

    test("sends exactly the envelope keys the ingest DTO declares", async () => {
        for (let i = 0; i < 25; i++) client.track("page_view");

        const [, body] = postSpy.mock.calls[0] as [string, { events: Envelope[] }];
        expect(Object.keys(body.events[0]).sort()).toEqual(["event", "eventId", "occurredAt", "properties", "sessionId"]);
    });

    test("mints a fresh eventId per event but keeps one sessionId for the batch", async () => {
        for (let i = 0; i < 25; i++) client.track("page_view");

        const [, body] = postSpy.mock.calls[0] as [string, { events: Envelope[] }];
        expect(new Set(body.events.map(e => e.eventId)).size).toBe(25);
        expect(new Set(body.events.map(e => e.sessionId)).size).toBe(1);
    });

    test("reuses the stored sessionId across client instances in the same tab", async () => {
        client.track("page_view");
        hideTab();
        const first = (postSpy.mock.calls[0][1] as { events: Envelope[] }).events[0].sessionId;

        client.destroy();
        client = makeProdClient();
        client.track("page_view");
        hideTab();
        const second = (postSpy.mock.calls[1][1] as { events: Envelope[] }).events[0].sessionId;

        expect(second).toBe(first);
    });

    test("falls back to a minted sessionId when sessionStorage is unavailable", async () => {
        client.destroy();
        vi.stubGlobal("sessionStorage", {
            getItem: () => {
                throw new Error("storage disabled");
            },
            setItem: () => {
                throw new Error("storage disabled");
            },
        });

        client = makeProdClient();
        client.track("page_view");
        hideTab();

        const [, body] = postSpy.mock.calls[0] as [string, { events: Envelope[] }];
        expect(body.events[0].sessionId).toMatch(UUID_RE);
        vi.unstubAllGlobals();
    });

    test("a rejected send does not throw and gives up after repeated failures", async () => {
        const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        client.destroy();
        vi.useFakeTimers();
        postSpy.mockRejectedValue(new TypeError("network failure"));

        const failing = makeProdClient();
        for (let attempt = 0; attempt < 20; attempt++) {
            failing.track("page_view");
            await vi.advanceTimersByTimeAsync(5_000);
        }

        const callsAtGiveUp = postSpy.mock.calls.length;
        failing.track("page_view");
        await vi.advanceTimersByTimeAsync(5_000);

        expect(postSpy.mock.calls.length).toBe(callsAtGiveUp);
        expect(errorSpy).toHaveBeenCalledWith(expect.stringContaining("giving up after 20 consecutive flush failures"));

        failing.destroy();
        vi.useRealTimers();
    });
});

describe("ProductAnalyticsClient — the reported host platform", () => {
    function clientWith(source: PlatformSource) {
        const c = new ProductAnalyticsClient({ transport: fetchTransport, platformSource: source });
        c.setFeatureEnabled(true);
        c.setConsent(true);
        return c;
    }

    function bodyOfNextFlush(c: ProductAnalyticsClient): { platform: ClientPlatform; events: unknown[] } {
        c.track("page_view");
        hideTab();
        const [, body] = postSpy.mock.calls.at(-1) as [string, { platform: ClientPlatform; events: unknown[] }];
        return body;
    }

    const settle = () => new Promise(resolve => setTimeout(resolve, 0));

    test("sends exactly the top-level keys the ingest DTO declares", () => {
        client.destroy();
        client = clientWith({ navigatorPlatform: "Linux x86_64" });
        expect(Object.keys(bodyOfNextFlush(client)).sort()).toEqual(["events", "platform"]);
    });

    test("reports the OS and architecture the browser mapped for itself", async () => {
        client.destroy();
        client = clientWith({ userAgentData: { platform: "Windows", getHighEntropyValues: () => Promise.resolve({ architecture: "x86", bitness: "64" }) } });
        await settle();

        expect(bodyOfNextFlush(client).platform).toEqual({ os: "windows", arch: "amd64" });
    });

    test("reports the desktop host's own platform inside the WebView", async () => {
        client.destroy();
        client = clientWith({ navigatorPlatform: "MacIntel", hostEnvironment: () => Promise.resolve({ platform: "darwin", arch: "arm64" }) });
        await settle();

        expect(bodyOfNextFlush(client).platform).toEqual({ os: "darwin", arch: "arm64" });
    });

    test("sends an empty platform when the browser cannot report one", async () => {
        client.destroy();
        client = clientWith({});
        await settle();

        const body = bodyOfNextFlush(client);
        expect(body.platform.os).toBeUndefined();
        expect(body.platform.arch).toBeUndefined();
        expect(JSON.parse(JSON.stringify(body)).platform).toEqual({});
    });

    test("claims no appVersion, because a page cannot know the deployment's and the route refuses a wrong one", async () => {
        client.destroy();
        client = clientWith({ navigatorPlatform: "Linux x86_64", hostEnvironment: () => Promise.resolve({ platform: "linux", arch: "amd64" }) });
        await settle();

        expect(bodyOfNextFlush(client).platform).not.toHaveProperty("appVersion");
    });

    test("reports the synchronous reading before the refinement settles", () => {
        client.destroy();
        client = clientWith({ navigatorPlatform: "Linux aarch64", hostEnvironment: () => new Promise(() => {}) });

        expect(bodyOfNextFlush(client).platform).toEqual({ os: "linux", arch: "arm64" });
    });
});

describe("ProductAnalyticsClient — keepalive byte-cap chunking", () => {
    const bulky = "x".repeat(25_000);

    function eventsFrom(calls: unknown[][]): Envelope[] {
        return calls.flatMap(([, body]) => (body as { events: Envelope[] }).events);
    }

    function queueOversizedBatch(count = 5) {
        for (let i = 0; i < count; i++) client.track("page_view", { i, bulky });
    }

    const settle = () => new Promise(resolve => setTimeout(resolve, 0));

    test("splits a keepalive flush that exceeds the cap across multiple sequential requests", async () => {
        queueOversizedBatch();
        hideTab();
        await settle();

        expect(postSpy.mock.calls.length).toBeGreaterThan(1);
        expect(postSpy.mock.calls.every((call: unknown[]) => (call[2] as { keepalive?: boolean }).keepalive === true)).toBe(true);
    });

    test("ships every event exactly once across the sub-batches", async () => {
        queueOversizedBatch();
        hideTab();
        await settle();

        const sent = eventsFrom(postSpy.mock.calls);
        expect(sent).toHaveLength(5);
        expect(sent.map(e => e.properties["i"]).sort((a, b) => Number(a) - Number(b))).toEqual([0, 1, 2, 3, 4]);
    });

    test("a single keepalive event larger than the cap is still sent as its own request", async () => {
        client.track("page_view", { huge: "y".repeat(70_000) });
        hideTab();
        await settle();

        expect(postSpy).toHaveBeenCalledOnce();
        expect(eventsFrom(postSpy.mock.calls)).toHaveLength(1);
    });

    test("a non-keepalive flush is sent as a single request regardless of size", async () => {
        queueOversizedBatch(BATCH_SIZE);
        await settle();

        expect(postSpy).toHaveBeenCalledOnce();
        expect(eventsFrom(postSpy.mock.calls)).toHaveLength(BATCH_SIZE);
    });

    test("stops sending the chunks behind the one in flight when consent is withdrawn mid-flush", async () => {
        postSpy.mockImplementation(async () => {
            client.setConsent(false);
        });

        queueOversizedBatch();
        hideTab();
        await settle();

        expect(postSpy).toHaveBeenCalledOnce();
    });
});
