// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { api } from "@/api/client";

const TZ_HEADER = "X-Sam-User-Timezone";
const SURFACE_HEADER = "X-Sam-Client-Surface";

let fetchSpy: ReturnType<typeof vi.spyOn>;

function okResponse() {
    return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
}

function sentHeaders(): Record<string, string> {
    expect(fetchSpy, "no request was made").toHaveBeenCalled();
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    return (init.headers ?? {}) as Record<string, string>;
}

beforeEach(() => {
    localStorage.clear();
    fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(() => Promise.resolve(okResponse()));
});

afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
});

test("declares the browser's IANA zone", async () => {
    await api.webui.get("/api/v1/agents");

    const expected = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(expected, "the test environment reports no timezone").toBeTruthy();
    expect(sentHeaders()[TZ_HEADER]).toBe(expected);
});

test("rides the retry a token refresh triggers", async () => {
    localStorage.setItem("access_token", "stale");
    localStorage.setItem("refresh_token", "refresh");

    fetchSpy
        .mockResolvedValueOnce(new Response("{}", { status: 401 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ access_token: "fresh", sam_access_token: "", refresh_token: "refresh" }), { status: 200 }))
        .mockImplementation(() => Promise.resolve(okResponse()));

    await api.webui.get("/api/v1/agents");

    const retry = fetchSpy.mock.calls.at(-1) as [string, RequestInit];
    const headers = (retry[1].headers ?? {}) as Record<string, string>;
    expect(headers[TZ_HEADER]).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);
});

test.each([
    ["reports no zone", () => ({ resolvedOptions: () => ({ timeZone: "" }) })],
    [
        "throws",
        () => {
            throw new RangeError("Intl unavailable");
        },
    ],
])("declares nothing when the platform %s", async (_label, impl) => {
    vi.spyOn(Intl, "DateTimeFormat").mockImplementation(impl as unknown as typeof Intl.DateTimeFormat);

    await api.webui.get("/api/v1/agents");

    const headers = sentHeaders();
    expect(headers).not.toHaveProperty(TZ_HEADER);
    expect(headers[SURFACE_HEADER]).toBe("ui");
});
