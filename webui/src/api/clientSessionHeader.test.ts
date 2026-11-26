// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { ProductAnalyticsClient, productAnalyticsClient } from "@/common/product-analytics/client";

const SESSION_HEADER = "X-Sam-Client-Session";
const SURFACE_HEADER = "X-Sam-Client-Surface";
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

let fetchSpy: ReturnType<typeof vi.spyOn>;

function okResponse() {
    return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
}

function hideTab() {
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    Object.defineProperty(document, "hidden", { value: false, configurable: true });
}

function sentHeaders(): Record<string, string> {
    expect(fetchSpy, "no request was made").toHaveBeenCalled();
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    return (init.headers ?? {}) as Record<string, string>;
}

beforeEach(() => {
    sessionStorage.clear();
    localStorage.clear();
    fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(() => Promise.resolve(okResponse()));
});

afterEach(() => {
    vi.restoreAllMocks();
    productAnalyticsClient.setConsent(false);
    productAnalyticsClient.setFeatureEnabled(false);
    productAnalyticsClient.destroy();
    sessionStorage.clear();
    localStorage.clear();
});

describe("the client-session header", () => {
    test("declares the tab's session once the feature and consent are on", async () => {
        productAnalyticsClient.setFeatureEnabled(true);
        productAnalyticsClient.setConsent(true);

        await api.webui.get("/api/v1/agents");

        const headers = sentHeaders();
        expect(headers[SESSION_HEADER]).toMatch(UUID_RE);
        expect(headers[SESSION_HEADER]).toBe(productAnalyticsClient.sessionId());
    });

    test("declares the browser's exact session id, since two well-formed ids never join", () => {
        const sent: Array<{ sessionId: string }> = [];
        const capturing = new ProductAnalyticsClient({
            transport: events => {
                sent.push(...(events as Array<{ sessionId: string }>));
            },
        });
        capturing.setFeatureEnabled(true);
        capturing.setConsent(true);

        capturing.track("page_view");
        hideTab();

        expect(sent.length, "the capturing client delivered nothing to assert against").toBe(1);
        expect(capturing.sessionId()).toMatch(UUID_RE);
        expect(sent[0].sessionId).toBe(capturing.sessionId());
        capturing.destroy();
    });

    test("declares nothing while consent is withheld, and mints no session id for the header", async () => {
        productAnalyticsClient.setFeatureEnabled(true);
        productAnalyticsClient.setConsent(false);

        await api.webui.get("/api/v1/agents");

        const headers = sentHeaders();
        expect(headers).not.toHaveProperty(SESSION_HEADER);
        expect(headers[SURFACE_HEADER]).toBe("ui");
        expect(sessionStorage.getItem("sam.productAnalytics.sessionId")).toBeNull();
    });

    test("declares nothing while the feature is off", async () => {
        productAnalyticsClient.setConsent(true);

        await api.webui.get("/api/v1/agents");

        const headers = sentHeaders();
        expect(headers).not.toHaveProperty(SESSION_HEADER);
        expect(headers[SURFACE_HEADER]).toBe("ui");
    });

    test("stops declaring a session the moment consent is withdrawn", async () => {
        productAnalyticsClient.setFeatureEnabled(true);
        productAnalyticsClient.setConsent(true);
        await api.webui.get("/api/v1/agents");
        const before = sentHeaders()[SESSION_HEADER];
        expect(before).toMatch(UUID_RE);

        fetchSpy.mockClear();
        productAnalyticsClient.setConsent(false);
        await api.webui.get("/api/v1/agents");

        expect(sentHeaders()).not.toHaveProperty(SESSION_HEADER);
    });

    test("carries the session header on the retry a token refresh triggers, so the run stays attributed", async () => {
        localStorage.setItem("access_token", "stale");
        localStorage.setItem("refresh_token", "refresh");
        productAnalyticsClient.setFeatureEnabled(true);
        productAnalyticsClient.setConsent(true);

        fetchSpy
            .mockResolvedValueOnce(new Response("{}", { status: 401 }))
            .mockResolvedValueOnce(new Response(JSON.stringify({ access_token: "fresh", sam_access_token: "", refresh_token: "refresh" }), { status: 200 }))
            .mockImplementation(() => Promise.resolve(okResponse()));

        await api.webui.get("/api/v1/agents");

        const retry = fetchSpy.mock.calls.at(-1) as [string, RequestInit];
        const headers = (retry[1].headers ?? {}) as Record<string, string>;
        expect(headers[SESSION_HEADER]).toBe(productAnalyticsClient.sessionId());
    });
});
