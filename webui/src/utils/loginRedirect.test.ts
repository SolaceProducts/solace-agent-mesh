// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { FramedLoginBlockedDetail } from "@/utils/loginRedirect";
import { FRAMED_LOGIN_BLOCKED_EVENT, LOGIN_POPUP_NAME, navigateToLogin } from "@/utils/loginRedirect";

const ORIGIN = "http://localhost:3000";
const CURRENT_URL = "http://localhost:3000/#/agent-mode/chat?agent=Foo";
const LOGIN_URL = "http://localhost:3000/api/v1/auth/login";

let originalLocation: Location;
let originalTop: Window | null;
let warnSpy: ReturnType<typeof vi.spyOn>;
let openSpy: ReturnType<typeof vi.spyOn>;
const listenerCleanups: Array<() => void> = [];

const captureBlockedSignal = () => {
    const listener = vi.fn();
    globalThis.addEventListener(FRAMED_LOGIN_BLOCKED_EVENT, listener);
    listenerCleanups.push(() => globalThis.removeEventListener(FRAMED_LOGIN_BLOCKED_EVENT, listener));
    return listener;
};

const blockedReason = (listener: ReturnType<typeof captureBlockedSignal>) => (listener.mock.calls[0][0] as CustomEvent<FramedLoginBlockedDetail>).detail.reason;

const defineOn = (prop: string, value: unknown) => {
    Object.defineProperty(globalThis, prop, { value, writable: true, configurable: true });
};

const setFrameElement = (el: HTMLElement | null) => defineOn("frameElement", el);

const sandboxedFrame = (tokens: string): HTMLElement => {
    const frame = document.createElement("iframe");
    frame.setAttribute("sandbox", tokens);
    return frame;
};

const sameOriginTop = () => {
    const nav = { href: "" };
    return {
        top: {
            location: {
                origin: ORIGIN,
                set href(v: string) {
                    nav.href = v;
                },
            },
        },
        nav,
    };
};

const crossOriginTop = () => {
    const attempt = { made: false };
    return {
        top: {
            location: {
                get origin(): string {
                    throw new Error("cross-origin: cannot read top.location.origin");
                },
                set href(_v: string) {
                    attempt.made = true;
                },
            },
        },
        attempt,
    };
};

beforeEach(() => {
    originalLocation = globalThis.location;
    originalTop = globalThis.top;
    warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    openSpy = vi.spyOn(globalThis, "open").mockReturnValue(null);
    defineOn("location", { href: CURRENT_URL, origin: ORIGIN });
    setFrameElement(null);
});

afterEach(() => {
    defineOn("location", originalLocation);
    defineOn("top", originalTop);
    setFrameElement(null);
    warnSpy.mockRestore();
    openSpy.mockRestore();
    listenerCleanups.forEach(remove => remove());
    listenerCleanups.length = 0;
});

describe("navigateToLogin", () => {
    test("navigates the current window when not framed, without warning", () => {
        defineOn("top", globalThis.self);

        navigateToLogin(LOGIN_URL);

        expect(globalThis.location.href).toBe(LOGIN_URL);
        expect(warnSpy).not.toHaveBeenCalled();
    });

    test("breaks out to a same-origin embedder and leaves the frame where it is", () => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);

        navigateToLogin(LOGIN_URL);

        expect(nav.href).toBe(LOGIN_URL);
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(warnSpy).not.toHaveBeenCalled();
    });

    test("never navigates a cross-origin frame to the IdP, and signals instead", () => {
        const { top, attempt } = crossOriginTop();
        defineOn("top", top);
        const blocked = captureBlockedSignal();

        navigateToLogin(LOGIN_URL);

        expect(attempt.made).toBe(false);
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(blocked).toHaveBeenCalledOnce();
        expect(warnSpy).toHaveBeenCalledOnce();
    });

    test("opens the IdP in a popup when a gesture is available inside a cross-origin frame", () => {
        const { top, attempt } = crossOriginTop();
        defineOn("top", top);
        openSpy.mockReturnValue({ focus: vi.fn() } as unknown as Window);
        const blocked = captureBlockedSignal();

        navigateToLogin(LOGIN_URL, { userInitiated: true });

        expect(openSpy).toHaveBeenCalledWith(LOGIN_URL, LOGIN_POPUP_NAME, expect.stringContaining("popup=yes"));
        expect(attempt.made).toBe(false);
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(blocked).not.toHaveBeenCalled();
    });

    test("hands the opened popup to the caller", () => {
        const { top } = crossOriginTop();
        defineOn("top", top);
        const popup = { focus: vi.fn() } as unknown as Window;
        openSpy.mockReturnValue(popup);
        const onPopupOpened = vi.fn();

        navigateToLogin(LOGIN_URL, { userInitiated: true, onPopupOpened });

        expect(onPopupOpened).toHaveBeenCalledWith(popup);
    });

    test("has no popup to hand over when the browser refused one", () => {
        const { top } = crossOriginTop();
        defineOn("top", top);
        openSpy.mockReturnValue(null);
        const onPopupOpened = vi.fn();

        navigateToLogin(LOGIN_URL, { userInitiated: true, onPopupOpened });

        expect(onPopupOpened).not.toHaveBeenCalled();
    });

    test("signals rather than navigating the frame when the popup is blocked", () => {
        const { top } = crossOriginTop();
        defineOn("top", top);
        openSpy.mockReturnValue(null);
        const blocked = captureBlockedSignal();

        navigateToLogin(LOGIN_URL, { userInitiated: true });

        expect(openSpy).toHaveBeenCalledOnce();
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(blocked).toHaveBeenCalledOnce();
        expect(blockedReason(blocked)).toBe("popup-refused");
    });

    test("reports whether the sign-in actually started", () => {
        defineOn("top", globalThis.self);
        expect(navigateToLogin(LOGIN_URL)).toBe(true);

        const { top } = crossOriginTop();
        defineOn("top", top);
        expect(navigateToLogin(LOGIN_URL)).toBe(false);

        openSpy.mockReturnValue({ focus: vi.fn() } as unknown as Window);
        expect(navigateToLogin(LOGIN_URL, { userInitiated: true })).toBe(true);

        openSpy.mockReturnValue(null);
        expect(navigateToLogin(LOGIN_URL, { userInitiated: true })).toBe(false);
    });

    test("does not attempt a popup without a gesture, and reports no-gesture rather than blaming popup settings", () => {
        const { top } = crossOriginTop();
        defineOn("top", top);
        const blocked = captureBlockedSignal();

        navigateToLogin(LOGIN_URL);

        expect(openSpy).not.toHaveBeenCalled();
        expect(blocked).toHaveBeenCalledOnce();
        expect(blockedReason(blocked)).toBe("no-gesture");
    });

    test("signals when a same-origin embedder sandboxes away top navigation, without navigating", () => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);
        setFrameElement(sandboxedFrame("allow-scripts allow-same-origin"));
        const blocked = captureBlockedSignal();

        navigateToLogin(LOGIN_URL);

        expect(nav.href).toBe("");
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(blocked).toHaveBeenCalledOnce();
        expect(warnSpy).toHaveBeenCalledOnce();
    });

    test.each([true, false])("breaks out when the embedder grants allow-top-navigation (userInitiated=%s)", userInitiated => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);
        setFrameElement(sandboxedFrame("allow-scripts allow-same-origin allow-top-navigation"));

        navigateToLogin(LOGIN_URL, { userInitiated });

        expect(nav.href).toBe(LOGIN_URL);
        expect(warnSpy).not.toHaveBeenCalled();
    });

    test("breaks out on the by-user-activation token only when the caller is inside a gesture", () => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);
        setFrameElement(sandboxedFrame("allow-scripts allow-same-origin allow-top-navigation-by-user-activation"));

        navigateToLogin(LOGIN_URL, { userInitiated: true });

        expect(nav.href).toBe(LOGIN_URL);
        expect(warnSpy).not.toHaveBeenCalled();
    });

    test("signals on the by-user-activation token when there is no gesture", () => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);
        setFrameElement(sandboxedFrame("allow-scripts allow-same-origin allow-top-navigation-by-user-activation"));
        const blocked = captureBlockedSignal();

        navigateToLogin(LOGIN_URL);

        expect(nav.href).toBe("");
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(blocked).toHaveBeenCalledOnce();
        expect(warnSpy).toHaveBeenCalledOnce();
    });

    test("defaults to no gesture when the option is omitted, since most callers have no click", () => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);
        setFrameElement(sandboxedFrame("allow-top-navigation-by-user-activation"));

        navigateToLogin(LOGIN_URL);

        expect(nav.href).toBe("");
        expect(globalThis.location.href).toBe(CURRENT_URL);
    });

    test("treats an empty sandbox attribute as blocking", () => {
        const { top, nav } = sameOriginTop();
        defineOn("top", top);
        setFrameElement(sandboxedFrame(""));

        navigateToLogin(LOGIN_URL);

        expect(nav.href).toBe("");
        expect(globalThis.location.href).toBe(CURRENT_URL);
    });

    test("signals when the top-window assignment throws, without navigating the frame", () => {
        let attempted = false;
        defineOn("top", {
            location: {
                origin: ORIGIN,
                set href(_v: string) {
                    attempted = true;
                    throw new Error("navigation blocked");
                },
            },
        });

        navigateToLogin(LOGIN_URL);

        expect(attempted).toBe(true);
        expect(globalThis.location.href).toBe(CURRENT_URL);
        expect(warnSpy).toHaveBeenCalledOnce();
    });
});
