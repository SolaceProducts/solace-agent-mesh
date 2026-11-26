// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

const initialize = vi.fn();
const authenticate = vi.fn();
const notifySuccess = vi.fn();
const notifyFailure = vi.fn();

const loadApp = vi.fn(() => ({ initialize }));

vi.mock("@microsoft/teams-js", () => ({
    get app() {
        return loadApp();
    },
    authentication: {
        get authenticate() {
            return authenticate;
        },
        get notifySuccess() {
            return notifySuccess;
        },
        get notifyFailure() {
            return notifyFailure;
        },
    },
}));

const LOGIN_URL = "/api/v1/auth/login";
const ABSOLUTE_LOGIN_URL = "http://test.invalid/api/v1/auth/login";
const TOKENS = { accessToken: "a1", samAccessToken: "s1", refreshToken: "r1" };

async function freshModule() {
    vi.resetModules();
    return import("@/utils/teamsAuth");
}

const inTeams = () => initialize.mockResolvedValue(undefined);
const inTeamsAfter = (ms: number) => initialize.mockImplementation(() => new Promise<void>(resolve => setTimeout(resolve, ms)));
const notInTeams = () => initialize.mockReturnValue(new Promise(() => {}));

let warnSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    vi.useFakeTimers();
    warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    initialize.mockReset();
    loadApp.mockReset();
    loadApp.mockImplementation(() => ({ initialize }));
    authenticate.mockReset();
    notifySuccess.mockReset();
    notifyFailure.mockReset();
});

afterEach(() => {
    vi.useRealTimers();
    warnSpy.mockRestore();
});

describe("signInThroughTeams", () => {
    test("returns the session Teams hands back", async () => {
        inTeams();
        authenticate.mockResolvedValue(JSON.stringify(TOKENS));
        const { signInThroughTeams } = await freshModule();

        await expect(signInThroughTeams(LOGIN_URL)).resolves.toEqual(TOKENS);
        expect(authenticate).toHaveBeenCalledWith(expect.objectContaining({ url: ABSOLUTE_LOGIN_URL }));
    });

    test("gives up on the handshake outside Teams instead of hanging", async () => {
        notInTeams();
        const { signInThroughTeams } = await freshModule();

        const pending = signInThroughTeams(LOGIN_URL);
        await vi.advanceTimersByTimeAsync(2_000);

        await expect(pending).resolves.toBeNull();
        expect(authenticate).not.toHaveBeenCalled();
    });

    test("reports no session when the user closes the Teams window", async () => {
        inTeams();
        authenticate.mockRejectedValue(new Error("CancelledByUser"));
        const { signInThroughTeams } = await freshModule();

        await expect(signInThroughTeams(LOGIN_URL)).resolves.toBeNull();
    });

    test.each([
        ["not json at all", "{{{"],
        ["json without an access token", JSON.stringify({ samAccessToken: "s1" })],
        ["an empty access token", JSON.stringify({ accessToken: "" })],
        ["an empty payload", ""],
    ])("refuses %s", async (_label, payload) => {
        inTeams();
        authenticate.mockResolvedValue(payload);
        const { signInThroughTeams } = await freshModule();

        await expect(signInThroughTeams(LOGIN_URL)).resolves.toBeNull();
    });

    test("keeps only the optional tokens that are really strings", async () => {
        inTeams();
        authenticate.mockResolvedValue(JSON.stringify({ accessToken: "a1", samAccessToken: 42, refreshToken: { value: "r1" } }));
        const { signInThroughTeams } = await freshModule();

        await expect(signInThroughTeams(LOGIN_URL)).resolves.toEqual({ accessToken: "a1", samAccessToken: null, refreshToken: null });
    });
});

describe("knownToBeInTeams", () => {
    test("stays false until the host has actually answered, because a sign-in click cannot await it", async () => {
        inTeams();
        const { isInTeams, knownToBeInTeams } = await freshModule();

        expect(knownToBeInTeams()).toBe(false);

        void isInTeams();
        expect(knownToBeInTeams()).toBe(false);

        await vi.advanceTimersByTimeAsync(0);
        expect(knownToBeInTeams()).toBe(true);
    });

    test("stays false when nothing answers", async () => {
        notInTeams();
        const { isInTeams, knownToBeInTeams } = await freshModule();

        void isInTeams();
        await vi.advanceTimersByTimeAsync(2_000);

        expect(knownToBeInTeams()).toBe(false);
    });

    test("waits long enough for a Teams client that is slow to answer", async () => {
        inTeamsAfter(1_500);
        const { isInTeams, knownToBeInTeams } = await freshModule();

        const answer = isInTeams();
        await vi.advanceTimersByTimeAsync(1_600);

        await expect(answer).resolves.toBe(true);
        expect(knownToBeInTeams()).toBe(true);
    });

    test("counts an answer that arrives after the deadline instead of caching the deadline forever", async () => {
        inTeamsAfter(3_000);
        authenticate.mockResolvedValue(JSON.stringify(TOKENS));
        const { isInTeams, knownToBeInTeams, signInThroughTeams } = await freshModule();

        void isInTeams();
        await vi.advanceTimersByTimeAsync(2_500);
        expect(knownToBeInTeams()).toBe(false);

        await vi.advanceTimersByTimeAsync(1_000);

        expect(knownToBeInTeams()).toBe(true);
        await expect(isInTeams()).resolves.toBe(true);
        await expect(signInThroughTeams(LOGIN_URL)).resolves.toEqual(TOKENS);
        expect(authenticate).toHaveBeenCalledWith(expect.objectContaining({ url: ABSOLUTE_LOGIN_URL }));
    });

    test("stays false forever once the host has definitively refused", async () => {
        initialize.mockRejectedValue(new Error("no parent window found"));
        const { isInTeams, knownToBeInTeams, signInThroughTeams } = await freshModule();

        void isInTeams();
        await vi.advanceTimersByTimeAsync(10_000);

        expect(knownToBeInTeams()).toBe(false);
        await expect(isInTeams()).resolves.toBe(false);
        await expect(signInThroughTeams(LOGIN_URL)).resolves.toBeNull();
        expect(authenticate).not.toHaveBeenCalled();
        expect(initialize).toHaveBeenCalledTimes(1);
    });

    test("asks again after the SDK fails to load, rather than caching the failure", async () => {
        loadApp.mockImplementationOnce(() => {
            throw new Error("failed to fetch dynamically imported module");
        });
        initialize.mockResolvedValue(undefined);
        const { isInTeams, knownToBeInTeams } = await freshModule();

        await expect(isInTeams()).resolves.toBe(false);
        expect(knownToBeInTeams()).toBe(false);

        await expect(isInTeams()).resolves.toBe(true);
        expect(knownToBeInTeams()).toBe(true);
    });

    test("bounds the retry after a failed load with its own budget, not the first attempt's", async () => {
        loadApp.mockImplementationOnce(() => {
            throw new Error("failed to fetch dynamically imported module");
        });
        initialize.mockReturnValue(new Promise(() => {}));
        const { isInTeams } = await freshModule();

        const first = isInTeams();
        await vi.advanceTimersByTimeAsync(2_000);
        await expect(first).resolves.toBe(false);

        const second = isInTeams();
        await vi.advanceTimersByTimeAsync(2_000);
        await expect(second).resolves.toBe(false);
        expect(initialize).toHaveBeenCalledTimes(1);
    });
});

describe("the host predicates", () => {
    const defineOn = (prop: string, value: unknown) => {
        Object.defineProperty(globalThis, prop, { value, writable: true, configurable: true });
    };

    let originalTop: Window | null;
    let originalOpener: unknown;
    let hadNativeInterface: boolean;

    beforeEach(() => {
        originalTop = globalThis.top;
        originalOpener = globalThis.opener;
        hadNativeInterface = "nativeInterface" in globalThis;
        defineOn("top", globalThis.self);
        defineOn("opener", null);
    });

    afterEach(() => {
        defineOn("top", originalTop);
        defineOn("opener", originalOpener);
        if (!hadNativeInterface) delete (globalThis as unknown as Record<string, unknown>).nativeInterface;
    });

    test("a framed document could be either", async () => {
        defineOn("top", {});
        const { couldBeTeamsTab, couldBeTeamsAuthWindow } = await freshModule();

        expect(couldBeTeamsTab()).toBe(true);
        expect(couldBeTeamsAuthWindow()).toBe(true);
    });

    test("a frameless native window could be either", async () => {
        defineOn("nativeInterface", {});
        const { couldBeTeamsTab, couldBeTeamsAuthWindow } = await freshModule();

        expect(couldBeTeamsTab()).toBe(true);
        expect(couldBeTeamsAuthWindow()).toBe(true);
    });

    test("an opener alone could only be the auth window", async () => {
        defineOn("opener", {});
        const { couldBeTeamsTab, couldBeTeamsAuthWindow } = await freshModule();

        expect(couldBeTeamsTab()).toBe(false);
        expect(couldBeTeamsAuthWindow()).toBe(true);
    });

    test("an ordinary top-level page is neither", async () => {
        const { couldBeTeamsTab, couldBeTeamsAuthWindow } = await freshModule();

        expect(couldBeTeamsTab()).toBe(false);
        expect(couldBeTeamsAuthWindow()).toBe(false);
    });

    describe("startTeamsHandshakeIfPossible", () => {
        test("asks nothing on a page that could not be a Teams tab", async () => {
            inTeams();
            const { startTeamsHandshakeIfPossible, knownToBeInTeams } = await freshModule();

            startTeamsHandshakeIfPossible();
            await vi.advanceTimersByTimeAsync(0);

            expect(initialize).not.toHaveBeenCalled();
            expect(knownToBeInTeams()).toBe(false);
        });

        test("asks nothing in a window the app itself opened", async () => {
            inTeams();
            defineOn("opener", {});
            const { startTeamsHandshakeIfPossible } = await freshModule();

            startTeamsHandshakeIfPossible();
            await vi.advanceTimersByTimeAsync(0);

            expect(initialize).not.toHaveBeenCalled();
        });

        test("asks the host when this document could be the tab", async () => {
            inTeams();
            defineOn("top", {});
            const { startTeamsHandshakeIfPossible, knownToBeInTeams } = await freshModule();

            startTeamsHandshakeIfPossible();
            await vi.advanceTimersByTimeAsync(0);

            expect(initialize).toHaveBeenCalled();
            expect(knownToBeInTeams()).toBe(true);
        });
    });
});

describe("completeTeamsSignIn", () => {
    test("hands the session back through Teams", async () => {
        inTeams();
        const { completeTeamsSignIn } = await freshModule();

        await expect(completeTeamsSignIn(TOKENS)).resolves.toBe(true);
        expect(JSON.parse(notifySuccess.mock.calls[0][0] as string)).toEqual(TOKENS);
    });

    test("declines outside Teams so the window completes on its own", async () => {
        notInTeams();
        const { completeTeamsSignIn } = await freshModule();

        const pending = completeTeamsSignIn(TOKENS);
        await vi.advanceTimersByTimeAsync(2_000);

        await expect(pending).resolves.toBe(false);
        expect(notifySuccess).not.toHaveBeenCalled();
    });

    test("declines when Teams refuses the handover rather than claiming success", async () => {
        inTeams();
        notifySuccess.mockImplementation(() => {
            throw new Error("not in an auth window");
        });
        const { completeTeamsSignIn } = await freshModule();

        await expect(completeTeamsSignIn(TOKENS)).resolves.toBe(false);
    });
});

describe("failTeamsSignIn", () => {
    test("tells Teams so the tab stops waiting", async () => {
        inTeams();
        const { failTeamsSignIn } = await freshModule();

        await failTeamsSignIn("no token");

        expect(notifyFailure).toHaveBeenCalledWith("no token");
    });

    test("says nothing outside Teams", async () => {
        notInTeams();
        const { failTeamsSignIn } = await freshModule();

        const pending = failTeamsSignIn("no token");
        await vi.advanceTimersByTimeAsync(2_000);
        await pending;

        expect(notifyFailure).not.toHaveBeenCalled();
    });
});
