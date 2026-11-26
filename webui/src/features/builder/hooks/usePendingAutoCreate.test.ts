// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

const KEY = "sam_builder_pending_autocreate";
const ACTIVE_SESSION_KEY = "sam_builder_active_session";

type Hook = typeof import("./usePendingAutoCreate").usePendingAutoCreate;

interface PageLoad {
    hash?: string;
    activeSession?: string | null;
}

async function loadPage({ hash = "#/builder", activeSession }: PageLoad = {}): Promise<Hook> {
    window.location.hash = hash;
    try {
        const active = activeSession === undefined ? sessionStorage.getItem(KEY) : activeSession;
        if (active === null) sessionStorage.removeItem(ACTIVE_SESSION_KEY);
        else sessionStorage.setItem(ACTIVE_SESSION_KEY, active);
    } catch {}
    vi.resetModules();
    return (await import("./usePendingAutoCreate")).usePendingAutoCreate;
}

function mount(useHook: Hook, initialSessionId: string) {
    return renderHook(({ sessionId }) => useHook(sessionId), { initialProps: { sessionId: initialSessionId } });
}

describe("usePendingAutoCreate", () => {
    beforeEach(() => {
        sessionStorage.clear();
        window.location.hash = "";
    });
    afterEach(() => {
        vi.restoreAllMocks();
        window.location.hash = "";
    });

    test("starts false for a session with nothing remembered", async () => {
        const { result } = mount(await loadPage(), "s1");
        expect(result.current[0]).toBe(false);
    });

    test("approving remembers the approved session, and only it", async () => {
        const { result } = mount(await loadPage(), "s1");

        act(() => result.current[1](true));

        expect(result.current[0]).toBe(true);
        expect(sessionStorage.getItem(KEY)).toBe("s1");
    });

    describe("a reload on the Builder", () => {
        test("hands the approval back to the same session", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));

            const reloaded = mount(await loadPage(), "s1");

            expect(reloaded.result.current[0]).toBe(true);
        });

        test("hands it back when the session is restored after the page starts empty", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));

            const reloaded = mount(await loadPage(), "");
            expect(reloaded.result.current[0]).toBe(false);
            reloaded.rerender({ sessionId: "s1" });

            expect(reloaded.result.current[0]).toBe(true);
        });

        test("does not hand it to another session", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));

            const reloaded = mount(await loadPage(), "s2");
            expect(reloaded.result.current[0]).toBe(false);
            reloaded.rerender({ sessionId: "s1" });

            expect(reloaded.result.current[0]).toBe(false);
            expect(sessionStorage.getItem(KEY)).toBeNull();
        });

        test("does not resume an attempt that already finished", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));
            act(() => first.result.current[1](false));

            const reloaded = mount(await loadPage(), "s1");

            expect(reloaded.result.current[0]).toBe(false);
            expect(sessionStorage.getItem(KEY)).toBeNull();
        });

        test("resumes once: finishing the resumed attempt forgets it for the next reload", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));
            const reloaded = mount(await loadPage(), "s1");
            act(() => reloaded.result.current[1](false));

            const reloadedAgain = mount(await loadPage(), "s1");

            expect(reloadedAgain.result.current[0]).toBe(false);
        });
    });

    describe("a page load that does not reopen the approved session never resumes it, and drops it", () => {
        test.each([
            { name: "it starts on another route", load: { hash: "#/chat" } },
            { name: "the Builder reopens a different session", load: { activeSession: "s2" } },
            { name: "no session is remembered for the restore", load: { activeSession: null } },
        ])("$name", async ({ load }) => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));

            const later = mount(await loadPage(load), "");
            later.rerender({ sessionId: "s1" });

            expect(later.result.current[0]).toBe(false);
            expect(sessionStorage.getItem(KEY)).toBeNull();
        });

        test("the restore does not land: another session is shown first, so the approval is dropped and opening the approved one later resumes nothing", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));

            const later = mount(await loadPage(), "");
            later.rerender({ sessionId: "new-session" });
            expect(sessionStorage.getItem(KEY)).toBeNull();
            later.rerender({ sessionId: "s1" });

            expect(later.result.current[0]).toBe(false);
        });

        test("accepted limit: when the restore never lands and nothing else is shown, opening the approved session later resumes it", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));

            const later = mount(await loadPage(), "");
            later.rerender({ sessionId: "s1" });

            expect(later.result.current[0]).toBe(true);
        });

        test("an explicit target (a sidebar click or redirect) wins over the restore", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));
            sessionStorage.setItem("sam_agent_session_redirect", "clicked-session");

            const later = mount(await loadPage(), "s1");

            expect(later.result.current[0]).toBe(false);
            expect(sessionStorage.getItem(KEY)).toBeNull();
        });
    });

    describe("leaving the session inside the page (the approval never outlives the page load that made it)", () => {
        test("another session starts without it", async () => {
            const { result, rerender } = mount(await loadPage(), "s1");
            act(() => result.current[1](true));

            rerender({ sessionId: "s2" });

            expect(result.current[0]).toBe(false);
        });

        test("coming back does not resume it, so a save still running in this page is not started twice", async () => {
            const { result, rerender } = mount(await loadPage(), "s1");
            act(() => result.current[1](true));

            rerender({ sessionId: "s2" });
            rerender({ sessionId: "s1" });

            expect(result.current[0]).toBe(false);
        });

        test("a resumed approval is forgotten the same way", async () => {
            const first = mount(await loadPage(), "s1");
            act(() => first.result.current[1](true));
            const reloaded = mount(await loadPage(), "s1");

            reloaded.rerender({ sessionId: "s2" });
            reloaded.rerender({ sessionId: "s1" });

            expect(reloaded.result.current[0]).toBe(false);
        });

        test("a reload after leaving finds nothing to resume", async () => {
            const { result, rerender } = mount(await loadPage(), "s1");
            act(() => result.current[1](true));
            rerender({ sessionId: "s2" });

            const reloaded = mount(await loadPage({ activeSession: "s1" }), "s1");

            expect(reloaded.result.current[0]).toBe(false);
            expect(sessionStorage.getItem(KEY)).toBeNull();
        });

        test("leaving the Builder page entirely forgets it", async () => {
            const { result, unmount } = mount(await loadPage(), "s1");
            act(() => result.current[1](true));

            unmount();

            expect(sessionStorage.getItem(KEY)).toBeNull();
        });

        test("an attempt finishing after the user moved on does not touch the session on screen", async () => {
            const { result, rerender } = mount(await loadPage(), "s1");
            act(() => result.current[1](true));
            const settleS1 = result.current[1];
            rerender({ sessionId: "s2" });
            act(() => result.current[1](true));

            act(() => settleS1(false));

            expect(result.current[0]).toBe(true);
            expect(sessionStorage.getItem(KEY)).toBe("s2");
        });
    });

    test("an approval made in this page load is never resumed by a later mount, even if storage still holds it", async () => {
        const useHook = await loadPage();
        const approving = mount(useHook, "s1");
        act(() => approving.result.current[1](true));

        const another = mount(useHook, "s1");

        expect(another.result.current[0]).toBe(false);
    });

    test("approving another session in this page load drops the inherited approval, so it cannot resume later", async () => {
        const first = mount(await loadPage(), "s1");
        act(() => first.result.current[1](true));
        const reloaded = mount(await loadPage(), "");
        reloaded.rerender({ sessionId: "s2" });
        act(() => reloaded.result.current[1](true));

        reloaded.rerender({ sessionId: "s1" });

        expect(reloaded.result.current[0]).toBe(false);
    });

    test("without a session id nothing is remembered", async () => {
        const { result } = mount(await loadPage(), "");

        act(() => result.current[1](true));

        expect(sessionStorage.getItem(KEY)).toBeNull();
    });

    describe("with storage unavailable", () => {
        const realStorage = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage")!;
        afterEach(() => Object.defineProperty(globalThis, "sessionStorage", realStorage));

        const denied = () => {
            throw new Error("denied");
        };
        const useStorage = (storage: Partial<Storage>) => Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: { getItem: () => null, ...storage } });

        test("nothing can be remembered: it works for the life of the page, and the one warning says what a reload will not do", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            useStorage({ getItem: denied, setItem: denied, removeItem: denied });
            const { result } = mount(await loadPage({ activeSession: null }), "s1");
            expect(result.current[0]).toBe(false);
            expect(warn).not.toHaveBeenCalled();

            act(() => result.current[1](true));
            expect(result.current[0]).toBe(true);
            act(() => result.current[1](false));

            expect(result.current[0]).toBe(false);
            expect(warn).toHaveBeenCalledTimes(1);
            expect(warn).toHaveBeenCalledWith(expect.stringContaining("could not remember"), expect.any(Error));
        });

        test("an approval this page load cannot resume stays stored when it cannot be cleared, says so, and is cleared by the next attempt", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            const removeItem = vi.fn((_key: string) => denied());
            useStorage({ getItem: (key: string) => (key === KEY ? "s1" : null), setItem: () => {}, removeItem });

            const { result } = mount(await loadPage({ activeSession: null }), "s1");

            expect(result.current[0]).toBe(false);
            expect(warn).toHaveBeenCalledWith(expect.stringContaining("could not clear"), expect.any(Error));
            act(() => result.current[1](false));
            expect(removeItem.mock.calls.filter(([key]) => key === KEY)).toHaveLength(2);
        });

        test("only clearing fails: it says so", async () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            useStorage({ setItem: () => {}, removeItem: denied });
            const { result } = mount(await loadPage({ activeSession: null }), "s1");
            act(() => result.current[1](true));

            act(() => result.current[1](false));

            expect(warn).toHaveBeenCalledTimes(1);
            expect(warn).toHaveBeenCalledWith(expect.stringContaining("could not clear"), expect.any(Error));
        });
    });
});
