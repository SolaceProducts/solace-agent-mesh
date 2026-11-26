// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api, type ApiError, cancelProactiveRefresh, configureSessionIdleTimeout, refreshToken, scheduleProactiveRefresh, SESSION_IDLE_TIMEOUT_EVENT } from "@/api/client";

function makeJwt(exp: number): string {
    const header = btoa(JSON.stringify({ alg: "HS256", typ: "JWT" }));
    const payload = btoa(JSON.stringify({ exp, sub: "test-user" }));
    return `${header}.${payload}.fake-signature`;
}

function makeBase64UrlJwt(exp: number): string {
    const header = btoa(JSON.stringify({ alg: "HS256", typ: "JWT" }))
        .replace(/\+/g, "-")
        .replace(/\//g, "_")
        .replace(/=+$/, "");
    const payload = btoa(JSON.stringify({ exp, sub: "test+user/special" }))
        .replace(/\+/g, "-")
        .replace(/\//g, "_")
        .replace(/=+$/, "");
    return `${header}.${payload}.fake-signature`;
}

describe("Proactive Token Refresh", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        localStorage.clear();
        sessionStorage.clear();
        cancelProactiveRefresh();
    });

    afterEach(() => {
        cancelProactiveRefresh();
        vi.useRealTimers();
        vi.restoreAllMocks();
        localStorage.clear();
        sessionStorage.clear();
    });

    describe("scheduleProactiveRefresh", () => {
        test("does nothing when no tokens are in localStorage", () => {
            const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
            scheduleProactiveRefresh();
            const relevantCalls = setTimeoutSpy.mock.calls.filter(([fn]) => typeof fn === "function");
            expect(relevantCalls.length).toBe(0);
        });

        test("schedules refresh ~5 minutes before token expiry", () => {
            const now = Date.now();
            const expInSeconds = Math.floor((now + 60 * 60 * 1000) / 1000);
            const token = makeJwt(expInSeconds);
            localStorage.setItem("sam_access_token", token);

            const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
            scheduleProactiveRefresh();

            const timerCalls = setTimeoutSpy.mock.calls.filter(([fn]) => typeof fn === "function");
            expect(timerCalls.length).toBeGreaterThanOrEqual(1);

            const lastCall = timerCalls[timerCalls.length - 1];
            const delay = lastCall[1] as number;
            expect(delay).toBeGreaterThan(50 * 60 * 1000);
            expect(delay).toBeLessThan(56 * 60 * 1000);
        });

        test("uses minimum delay floor for near-expiry tokens", () => {
            const now = Date.now();
            const expInSeconds = Math.floor((now + 2 * 60 * 1000) / 1000);
            const token = makeJwt(expInSeconds);
            localStorage.setItem("sam_access_token", token);

            const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
            scheduleProactiveRefresh();

            const timerCalls = setTimeoutSpy.mock.calls.filter(([fn]) => typeof fn === "function");
            expect(timerCalls.length).toBeGreaterThanOrEqual(1);
            const lastCall = timerCalls[timerCalls.length - 1];
            const delay = lastCall[1] as number;
            expect(delay).toBe(30 * 1000);
        });

        test("uses minimum delay floor for already-expired tokens", () => {
            const now = Date.now();
            const expInSeconds = Math.floor((now - 10 * 60 * 1000) / 1000);
            const token = makeJwt(expInSeconds);
            localStorage.setItem("access_token", token);

            const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
            scheduleProactiveRefresh();

            const timerCalls = setTimeoutSpy.mock.calls.filter(([fn]) => typeof fn === "function");
            expect(timerCalls.length).toBeGreaterThanOrEqual(1);
            const lastCall = timerCalls[timerCalls.length - 1];
            const delay = lastCall[1] as number;
            expect(delay).toBe(30 * 1000);
        });

        test("picks the earliest expiring token when both are present", () => {
            const now = Date.now();
            const samExp = Math.floor((now + 30 * 60 * 1000) / 1000);
            const accessExp = Math.floor((now + 60 * 60 * 1000) / 1000);

            localStorage.setItem("sam_access_token", makeJwt(samExp));
            localStorage.setItem("access_token", makeJwt(accessExp));

            const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
            scheduleProactiveRefresh();

            const timerCalls = setTimeoutSpy.mock.calls.filter(([fn]) => typeof fn === "function");
            expect(timerCalls.length).toBeGreaterThanOrEqual(1);
            const lastCall = timerCalls[timerCalls.length - 1];
            const delay = lastCall[1] as number;
            expect(delay).toBeGreaterThan(20 * 60 * 1000);
            expect(delay).toBeLessThan(26 * 60 * 1000);
        });

        test("handles base64url-encoded JWT tokens", () => {
            const now = Date.now();
            const expInSeconds = Math.floor((now + 60 * 60 * 1000) / 1000);
            const token = makeBase64UrlJwt(expInSeconds);
            localStorage.setItem("sam_access_token", token);

            const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
            scheduleProactiveRefresh();

            const timerCalls = setTimeoutSpy.mock.calls.filter(([fn]) => typeof fn === "function");
            expect(timerCalls.length).toBeGreaterThanOrEqual(1);
        });

        test("handles non-JWT tokens gracefully", () => {
            localStorage.setItem("access_token", "not-a-jwt-token");

            expect(() => scheduleProactiveRefresh()).not.toThrow();
        });

        test("handles malformed JWT payload gracefully", () => {
            localStorage.setItem("access_token", "header.!!!invalid-base64!!!.signature");

            expect(() => scheduleProactiveRefresh()).not.toThrow();
        });

        test("clears previous timer when called again", () => {
            const clearTimeoutSpy = vi.spyOn(globalThis, "clearTimeout");
            const now = Date.now();
            const expInSeconds = Math.floor((now + 60 * 60 * 1000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(expInSeconds));

            scheduleProactiveRefresh();
            scheduleProactiveRefresh();

            expect(clearTimeoutSpy).toHaveBeenCalled();
        });
    });

    describe("cancelProactiveRefresh", () => {
        test("cancels a scheduled timer", () => {
            const now = Date.now();
            const expInSeconds = Math.floor((now + 60 * 60 * 1000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(expInSeconds));

            scheduleProactiveRefresh();
            const clearTimeoutSpy = vi.spyOn(globalThis, "clearTimeout");
            cancelProactiveRefresh();

            expect(clearTimeoutSpy).toHaveBeenCalled();
        });

        test("is safe to call when no timer is scheduled", () => {
            expect(() => cancelProactiveRefresh()).not.toThrow();
        });

        test("is idempotent", () => {
            const now = Date.now();
            const expInSeconds = Math.floor((now + 60 * 60 * 1000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(expInSeconds));

            scheduleProactiveRefresh();
            expect(() => {
                cancelProactiveRefresh();
                cancelProactiveRefresh();
            }).not.toThrow();
        });
    });

    describe("refreshToken", () => {
        test("returns null when logout_in_progress is set", async () => {
            sessionStorage.setItem("logout_in_progress", "true");
            localStorage.setItem("refresh_token", "some-token");

            const result = await refreshToken();
            expect(result).toBeNull();
        });

        test("returns null when no refresh_token exists", async () => {
            const result = await refreshToken();
            expect(result).toBeNull();
        });

        test("deduplicates concurrent refresh calls", async () => {
            localStorage.setItem("refresh_token", "test-refresh-token");

            const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
                new Response(
                    JSON.stringify({
                        access_token: "new-access",
                        sam_access_token: "",
                        refresh_token: "new-refresh",
                    }),
                    { status: 200 }
                )
            );

            const [result1, result2] = await Promise.all([refreshToken(), refreshToken()]);

            expect(fetchSpy).toHaveBeenCalledTimes(1);
            expect(result1).toBe(result2);
        });

        test("clears tokens and redirects on refresh failure", async () => {
            localStorage.setItem("refresh_token", "test-refresh-token");
            localStorage.setItem("access_token", "old-access");

            vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("Unauthorized", { status: 401 }));

            const locationSpy = vi.spyOn(globalThis, "location", "get").mockReturnValue({
                ...globalThis.location,
                href: "",
            } as Location);

            Object.defineProperty(globalThis, "location", {
                value: { href: "" },
                writable: true,
                configurable: true,
            });

            await refreshToken();

            expect(localStorage.getItem("access_token")).toBeNull();
            expect(localStorage.getItem("refresh_token")).toBeNull();

            locationSpy?.mockRestore();
        });

        test("breaks out of a same-origin frame when the refresh is rejected", async () => {
            localStorage.setItem("refresh_token", "test-refresh-token");

            vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("Unauthorized", { status: 401 }));

            const originalTop = globalThis.top;
            let topHref = "";
            Object.defineProperty(globalThis, "location", {
                value: { href: "http://localhost:3000/", origin: "http://localhost:3000" },
                writable: true,
                configurable: true,
            });
            Object.defineProperty(globalThis, "top", {
                value: {
                    location: {
                        origin: "http://localhost:3000",
                        set href(v: string) {
                            topHref = v;
                        },
                    },
                },
                writable: true,
                configurable: true,
            });

            try {
                await refreshToken();
                expect(topHref).toContain("/api/v1/auth/login");
            } finally {
                Object.defineProperty(globalThis, "top", { value: originalTop, writable: true, configurable: true });
            }
        });
    });
});

describe("authenticatedFetch — no-token path (401 storm prevention)", () => {
    beforeEach(() => {
        localStorage.clear();
        sessionStorage.clear();
        cancelProactiveRefresh();
        api.configure("", "");
        Object.defineProperty(globalThis, "location", {
            value: { href: "" },
            writable: true,
            configurable: true,
        });
    });

    afterEach(() => {
        cancelProactiveRefresh();
        vi.restoreAllMocks();
        localStorage.clear();
        sessionStorage.clear();
    });

    test("attempts token refresh when no bearer token is in localStorage", async () => {
        localStorage.setItem("refresh_token", "valid-refresh-token");

        const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
            new Response(
                JSON.stringify({
                    access_token: "new-access-token",
                    sam_access_token: "",
                    refresh_token: "new-refresh-token",
                }),
                { status: 200 }
            )
        );

        await api.webui.get("/api/v1/user").catch(() => {});

        const calls = fetchSpy.mock.calls.map(([url]) => url as string);
        expect(calls.some(url => url.includes("/auth/refresh"))).toBe(true);
        expect(calls.some(url => url.includes("/api/v1/user"))).toBe(true);
    });

    test("falls through to unauthenticated fetch when no bearer token and no refresh token", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ id: "user-1" }), { status: 200 }));

        let caughtError: Error | null = null;
        let result: unknown = null;
        try {
            result = await api.webui.get("/api/v1/user");
        } catch (e) {
            caughtError = e as Error;
        }

        expect(caughtError).toBeNull();
        expect(result).toEqual({ id: "user-1" });

        const fetchSpy = vi.mocked(globalThis.fetch);
        const apiCalls = fetchSpy.mock.calls.filter(([url]) => (url as string).includes("/api/v1/user"));
        expect(apiCalls).toHaveLength(1);

        const refreshCalls = fetchSpy.mock.calls.filter(([url]) => (url as string).includes("/auth/refresh"));
        expect(refreshCalls).toHaveLength(0);
    });

    test("deduplicates concurrent no-token requests into a single refresh call", async () => {
        localStorage.setItem("refresh_token", "valid-refresh-token");

        let refreshCallCount = 0;
        const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async url => {
            if ((url as string).includes("/auth/refresh")) {
                refreshCallCount++;
                return new Response(
                    JSON.stringify({
                        access_token: "new-access-token",
                        sam_access_token: "",
                        refresh_token: "new-refresh-token",
                    }),
                    { status: 200 }
                );
            }
            return new Response(JSON.stringify({ id: "user-1" }), { status: 200 });
        });

        await Promise.all([
            api.webui.get("/api/v1/user").catch(() => {}),
            api.webui.get("/api/v1/user").catch(() => {}),
            api.webui.get("/api/v1/user").catch(() => {}),
            api.webui.get("/api/v1/user").catch(() => {}),
            api.webui.get("/api/v1/user").catch(() => {}),
        ]);

        expect(refreshCallCount).toBe(1);

        fetchSpy.mockRestore();
    });
});

describe("fetchWithError — validationDetails", () => {
    beforeEach(() => {
        localStorage.clear();
        api.configure("", "");
    });

    afterEach(() => {
        vi.restoreAllMocks();
        localStorage.clear();
    });

    test("keeps the flattened message and attaches the per-field errors", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ message: "Validation failed", validationDetails: { question: ["question is required"], model: ["model is required", "bad"] } }), { status: 422 }));

        const err = (await api.webui.post("/api/v1/eval/evaluators", {}).catch(e => e)) as ApiError;

        expect(err.status).toBe(422);
        expect(err.message).toBe("Validation failed: question: question is required; model: model is required, bad");
        expect(err.validationDetails).toEqual({ question: ["question is required"], model: ["model is required", "bad"] });
    });

    test("copies the service's errorId onto the error, and only that", async () => {
        vi.spyOn(globalThis, "fetch")
            .mockResolvedValueOnce(new Response(JSON.stringify({ message: "no such channel", errorId: "01-abc" }), { status: 400 }))
            .mockResolvedValueOnce(new Response(JSON.stringify({ message: "no errorId here" }), { status: 400 }))
            .mockResolvedValueOnce(new Response(JSON.stringify({ message: "bad id", errorId: 7 }), { status: 400 }))
            .mockResolvedValueOnce(new Response("no healthy upstream", { status: 503 }));

        const own = (await api.webui.get("/api/v1/a").catch(e => e)) as ApiError;
        const bare = (await api.webui.get("/api/v1/b").catch(e => e)) as ApiError;
        const numeric = (await api.webui.get("/api/v1/c").catch(e => e)) as ApiError;
        const proxy = (await api.webui.get("/api/v1/d").catch(e => e)) as ApiError;

        expect(own.errorId).toBe("01-abc");
        expect(own.message).toBe("no such channel");
        expect(bare.errorId).toBeUndefined();
        expect(numeric.errorId).toBeUndefined();
        expect(proxy.errorId).toBeUndefined();
        expect(proxy.message).toBe("no healthy upstream");
    });

    test.each([400, 403, 500, 502, 503])("copies the errorId of the service's envelope for a %i, with or without per-field errors", async status => {
        const envelope = { message: "the service's words", errorId: "e-1" };
        vi.spyOn(globalThis, "fetch")
            .mockResolvedValueOnce(new Response(JSON.stringify(envelope), { status }))
            .mockResolvedValueOnce(new Response(JSON.stringify({ ...envelope, validationDetails: { field: ["is bad"] } }), { status }));

        const plain = (await api.webui.get("/api/v1/a").catch(e => e)) as ApiError;
        const withDetails = (await api.webui.get("/api/v1/b").catch(e => e)) as ApiError;

        expect(plain.status).toBe(status);
        expect(plain.errorId).toBe("e-1");
        expect(withDetails.status).toBe(status);
        expect(withDetails.errorId).toBe("e-1");
        expect(withDetails.validationDetails).toEqual({ field: ["is bad"] });
    });

    test("gives the client's own sign-in error no errorId", async () => {
        localStorage.setItem("refresh_token", "stale-refresh-token");
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ message: "refresh refused" }), { status: 401 }));

        const err = (await api.webui.get("/api/v1/x").catch(e => e)) as ApiError;

        expect(err.status).toBe(401);
        expect(err.message).toBe("Not authenticated");
        expect(err.errorId).toBeUndefined();
    });

    test("leaves validationDetails unset for a body without them, or one that is not JSON", async () => {
        vi.spyOn(globalThis, "fetch")
            .mockResolvedValueOnce(new Response(JSON.stringify({ message: "Not found" }), { status: 404 }))
            .mockResolvedValueOnce(new Response("upstream exploded", { status: 502 }));

        const notFound = (await api.webui.get("/api/v1/x").catch(e => e)) as ApiError;
        expect(notFound.message).toBe("Not found");
        expect(notFound.validationDetails).toBeUndefined();

        const plain = (await api.webui.get("/api/v1/y").catch(e => e)) as ApiError;
        expect(plain.message).toBe("upstream exploded");
        expect(plain.validationDetails).toBeUndefined();
    });
});

describe("fetchWithError — unusual error bodies", () => {
    beforeEach(() => {
        localStorage.clear();
        api.configure("", "");
    });

    afterEach(() => {
        vi.restoreAllMocks();
        localStorage.clear();
    });

    test("tolerates a validationDetails value that is a bare string", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ message: "x", validationDetails: { field: "must be set" } }), { status: 400 }));

        const err = (await api.webui.post("/api/v1/x", {}).catch(e => e)) as ApiError;

        expect(err.status).toBe(400);
        expect(err.message).toBe("x: field: must be set");
        expect(err.validationDetails).toEqual({ field: ["must be set"] });
    });

    test("surfaces a bare JSON string body as its raw text", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify("boom"), { status: 500 }));

        const err = (await api.webui.get("/api/v1/x").catch(e => e)) as ApiError;

        expect(err.status).toBe(500);
        expect(err.message).toBe('"boom"');
    });

    test("surfaces a bare JSON number body as its raw text", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("404", { status: 404 }));

        const err = (await api.webui.get("/api/v1/x").catch(e => e)) as ApiError;

        expect(err.status).toBe(404);
        expect(err.message).toBe("404");
    });
});

describe("fetchWithError — rpcErrorCode", () => {
    beforeEach(() => {
        localStorage.clear();
        api.configure("", "");
    });

    afterEach(() => {
        vi.restoreAllMocks();
        localStorage.clear();
    });

    test("attaches the JSON-RPC error code and keeps its message", async () => {
        vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ jsonrpc: "2.0", id: "req-1", error: { code: -32029, message: "usage budget is used up" } }), { status: 429 }));

        const err = (await api.webui.post("/api/v1/message:stream", {}).catch(e => e)) as ApiError;

        expect(err.status).toBe(429);
        expect(err.message).toBe("usage budget is used up");
        expect(err.rpcErrorCode).toBe(-32029);
    });

    test("leaves rpcErrorCode unset for a REST envelope, a codeless error, or a non-JSON body", async () => {
        vi.spyOn(globalThis, "fetch")
            .mockResolvedValueOnce(new Response(JSON.stringify({ message: "Not found" }), { status: 404 }))
            .mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: "busy" } }), { status: 429 }))
            .mockResolvedValueOnce(new Response("upstream exploded", { status: 502 }));

        for (const path of ["/api/v1/x", "/api/v1/y", "/api/v1/z"]) {
            const err = (await api.webui.get(path).catch(e => e)) as ApiError;
            expect(err.rpcErrorCode).toBeUndefined();
        }
    });
});

describe("Idle session timeout", () => {
    const IDLE_SECONDS = 1800;
    const freshJwt = () => makeJwt(Math.floor((Date.now() + 3600 * 1000) / 1000));

    const signInWithIdleWindow = () => {
        localStorage.setItem("sam_access_token", freshJwt());
        localStorage.setItem("access_token", freshJwt());
        localStorage.setItem("refresh_token", "rt");
        configureSessionIdleTimeout(IDLE_SECONDS);
        scheduleProactiveRefresh();
    };

    beforeEach(() => {
        vi.useFakeTimers();
        localStorage.clear();
        sessionStorage.clear();
        cancelProactiveRefresh();
    });

    afterEach(() => {
        configureSessionIdleTimeout(0);
        cancelProactiveRefresh();
        vi.useRealTimers();
        vi.restoreAllMocks();
        localStorage.clear();
        sessionStorage.clear();
    });

    test("signs out exactly one window after the last interaction and raises the event", () => {
        const onTimeout = vi.fn();
        globalThis.addEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
        signInWithIdleWindow();

        vi.advanceTimersByTime(IDLE_SECONDS * 1000 - 1000);
        expect(localStorage.getItem("sam_access_token")).not.toBeNull();
        expect(onTimeout).not.toHaveBeenCalled();

        vi.advanceTimersByTime(1000);
        expect(localStorage.getItem("sam_access_token")).toBeNull();
        expect(localStorage.getItem("refresh_token")).toBeNull();
        expect(onTimeout).toHaveBeenCalledTimes(1);
        globalThis.removeEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
    });

    test("user activity pushes the sign-out back without re-arming a timer per event", () => {
        signInWithIdleWindow();

        vi.advanceTimersByTime(IDLE_SECONDS * 1000 - 60_000);
        globalThis.dispatchEvent(new Event("keydown"));

        vi.advanceTimersByTime(60_000);
        expect(localStorage.getItem("sam_access_token")).not.toBeNull();

        vi.advanceTimersByTime(IDLE_SECONDS * 1000 - 60_000);
        expect(localStorage.getItem("sam_access_token")).toBeNull();
    });

    test("does nothing when the gateway reports no idle window", () => {
        localStorage.setItem("sam_access_token", freshJwt());
        localStorage.setItem("refresh_token", "rt");
        configureSessionIdleTimeout(0);
        scheduleProactiveRefresh();

        vi.advanceTimersByTime(24 * 3600 * 1000);
        expect(localStorage.getItem("sam_access_token")).not.toBeNull();
    });

    test("a tab returning to view past the window signs out immediately", () => {
        signInWithIdleWindow();
        vi.setSystemTime(Date.now() + (IDLE_SECONDS + 60) * 1000);

        vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
        document.dispatchEvent(new Event("visibilitychange"));

        expect(localStorage.getItem("sam_access_token")).toBeNull();
    });

    test("the idle sign-out clears the gateway's session cookie with a best-effort logout", () => {
        const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 200 }));
        signInWithIdleWindow();

        vi.advanceTimersByTime(IDLE_SECONDS * 1000);

        const logoutCall = fetchSpy.mock.calls.find(([url]) => String(url).includes("/api/v1/auth/logout"));
        expect(logoutCall).toBeDefined();
        const [, init] = logoutCall as [string, RequestInit];
        expect(init.method).toBe("POST");
        expect((init.headers as Record<string, string>).Authorization).toMatch(/^Bearer /);
        expect(localStorage.getItem("sam_access_token")).toBeNull();
    });

    test("a window longer than setTimeout's limit is armed in hops, not fired at once", () => {
        const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
        localStorage.setItem("sam_access_token", freshJwt());
        localStorage.setItem("refresh_token", "rt");
        configureSessionIdleTimeout(40 * 24 * 3600);

        const delays = setTimeoutSpy.mock.calls.map(([, d]) => Number(d));
        expect(Math.max(...delays)).toBeLessThanOrEqual(2 ** 31 - 1);
        vi.advanceTimersByTime(2 ** 31);
        expect(localStorage.getItem("sam_access_token")).not.toBeNull();
    });

    test("activity recorded by another tab keeps this tab signed in", () => {
        signInWithIdleWindow();

        vi.advanceTimersByTime(IDLE_SECONDS * 1000 - 1000);
        localStorage.setItem("sam_last_activity_at", String(Date.now()));

        vi.advanceTimersByTime(1000);
        expect(localStorage.getItem("sam_access_token")).not.toBeNull();

        vi.advanceTimersByTime(IDLE_SECONDS * 1000);
        expect(localStorage.getItem("sam_access_token")).toBeNull();
    });

    test("this tab's activity is published for the other tabs", () => {
        signInWithIdleWindow();
        localStorage.removeItem("sam_last_activity_at");

        vi.advanceTimersByTime(20_000);
        globalThis.dispatchEvent(new Event("pointerdown"));

        expect(Number(localStorage.getItem("sam_last_activity_at"))).toBe(Date.now());
    });

    test("an idle sign-out in another tab raises the event here", () => {
        const onTimeout = vi.fn();
        globalThis.addEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
        signInWithIdleWindow();

        globalThis.dispatchEvent(new StorageEvent("storage", { key: "sam_idle_signed_out_at", newValue: String(Date.now()) }));

        expect(onTimeout).toHaveBeenCalledTimes(1);
        globalThis.removeEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
    });

    test("refresh presents the current SAM token as the bearer", async () => {
        localStorage.setItem("sam_access_token", "current-sam");
        localStorage.setItem("refresh_token", "rt");
        const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ access_token: "a", sam_access_token: "b", refresh_token: "c" }), { status: 200 }));

        await refreshToken();

        const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
        expect((init.headers as Record<string, string>).Authorization).toBe("Bearer current-sam");
    });

    test("a refresh that completes after the idle sign-out does not resurrect the session", async () => {
        signInWithIdleWindow();
        let resolveFetch: (r: Response) => void = () => {};
        vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise<Response>(r => (resolveFetch = r)));

        const pending = refreshToken();
        vi.advanceTimersByTime(IDLE_SECONDS * 1000);
        expect(localStorage.getItem("refresh_token")).toBeNull();

        resolveFetch(new Response(JSON.stringify({ access_token: "a", sam_access_token: "b", refresh_token: "c" }), { status: 200 }));
        expect(await pending).toBeNull();
        expect(localStorage.getItem("sam_access_token")).toBeNull();
        expect(localStorage.getItem("refresh_token")).toBeNull();
    });

    const sessionExpiredRefusal = () => new Response(JSON.stringify({ message: "Session expired due to inactivity", errorType: "session_expired" }), { status: 401 });

    test("a refresh refused as session_expired after this tab's window lapsed while its watchdog never fired raises the event instead of redirecting", async () => {
        const onTimeout = vi.fn();
        globalThis.addEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
        signInWithIdleWindow();
        vi.setSystemTime(Date.now() + IDLE_SECONDS * 1000);
        vi.spyOn(globalThis, "fetch").mockImplementation(() => Promise.resolve(sessionExpiredRefusal()));
        Object.defineProperty(globalThis, "location", { value: { href: "http://localhost:3000/", origin: "http://localhost:3000" }, writable: true, configurable: true });

        await refreshToken();

        expect(onTimeout).toHaveBeenCalledTimes(1);
        expect(localStorage.getItem("sam_idle_signed_out_at")).not.toBeNull();
        expect(localStorage.getItem("refresh_token")).toBeNull();
        expect(globalThis.location.href).toBe("http://localhost:3000/");
        globalThis.removeEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
    });

    test("a refresh refused as session_expired on a fresh tab ends the session without the inactivity notice", async () => {
        const onTimeout = vi.fn();
        globalThis.addEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
        signInWithIdleWindow();
        const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(() => Promise.resolve(sessionExpiredRefusal()));
        Object.defineProperty(globalThis, "location", { value: { href: "http://localhost:3000/", origin: "http://localhost:3000" }, writable: true, configurable: true });

        expect(await refreshToken()).toBeNull();

        expect(onTimeout).not.toHaveBeenCalled();
        expect(localStorage.getItem("sam_idle_signed_out_at")).toBeNull();
        expect(localStorage.getItem("sam_access_token")).toBeNull();
        expect(localStorage.getItem("access_token")).toBeNull();
        expect(localStorage.getItem("refresh_token")).toBeNull();
        expect(globalThis.location.href).toBe("http://localhost:3000/");
        const calledUrls = fetchSpy.mock.calls.map(([url]) => String(url));
        expect(calledUrls.some(url => url.endsWith("/api/v1/auth/logout"))).toBe(true);
        globalThis.removeEventListener(SESSION_IDLE_TIMEOUT_EVENT, onTimeout);
    });
});
