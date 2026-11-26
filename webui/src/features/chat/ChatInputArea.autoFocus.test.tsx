// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, onTestFinished, test, vi } from "vitest";

import { MOBILE_BREAKPOINT } from "@/hooks/useMobile";

import { ChatInputArea } from "@/features/chat/ChatInputArea";
import { useChatInputAutoFocus } from "@/features/chat/hooks/useChatInputAutoFocus";

import { StoryProvider } from "@/testing/providers/StoryProvider";

document.execCommand = vi.fn().mockReturnValue(true);

function renderComposer(autoFocus = true, chatContextValues = {}) {
    return render(
        <MemoryRouter initialEntries={["/chat"]}>
            <StoryProvider chatContextValues={chatContextValues}>
                <ChatInputArea agents={[]} autoFocus={autoFocus} />
            </StoryProvider>
        </MemoryRouter>
    );
}

const composer = () => screen.getByTestId("chat-input");
const isComposerFocused = () => document.activeElement?.getAttribute("contenteditable") === "true";

function setPointer({ coarse, fine = false, width = 1024 }: { coarse: boolean; fine?: boolean; width?: number }) {
    Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: width });
    Object.defineProperty(window, "matchMedia", {
        writable: true,
        configurable: true,
        value: (query: string) => ({
            matches: query === "(pointer: coarse)" ? coarse : query === "(any-pointer: fine)" ? fine : false,
            media: query,
            addEventListener: () => {},
            removeEventListener: () => {},
        }),
    });
}

function setVisibility(state: DocumentVisibilityState) {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
}

function openModalLayer(): HTMLElement {
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("data-state", "open");
    document.body.appendChild(dialog);
    return dialog;
}

function pressKey(init: KeyboardEventInit) {
    const event = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
    act(() => {
        document.dispatchEvent(event);
    });
    return event;
}

async function settle() {
    await act(async () => {
        await new Promise(resolve => requestAnimationFrame(() => resolve(null)));
    });
}

beforeEach(() => {
    vi.mocked(document.execCommand).mockClear().mockReturnValue(true);
    setPointer({ coarse: false });
    setVisibility("visible");
});

afterEach(() => {
    document.querySelectorAll('[role="dialog"], [role="menu"], [role="listbox"]').forEach(node => node.remove());
});

describe("landing", () => {
    test("focuses the composer on mount", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
    });

    test("does not focus when autoFocus is off, so Builder and Test Mode keep their focus", async () => {
        renderComposer(false);
        await settle();
        expect(isComposerFocused()).toBe(false);
    });

    test("does not focus on a touch-only device, so the soft keyboard stays down", async () => {
        setPointer({ coarse: true });
        renderComposer();
        await settle();
        expect(isComposerFocused()).toBe(false);
    });

    test("focuses on a hybrid device with both a touchscreen and a trackpad/mouse", async () => {
        setPointer({ coarse: true, fine: true });
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
    });

    test("does not focus on a narrow-viewport touch device with a paired mouse, so the soft keyboard stays down", async () => {
        setPointer({ coarse: true, fine: true, width: MOBILE_BREAKPOINT - 1 });
        renderComposer();
        await settle();
        expect(isComposerFocused()).toBe(false);
    });

    test("does not focus while a modal layer owns focus", async () => {
        openModalLayer();
        renderComposer();
        await settle();
        expect(isComposerFocused()).toBe(false);
    });
});

describe("returning to the window", () => {
    test("refocuses on window focus", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());
        expect(isComposerFocused()).toBe(false);

        act(() => {
            window.dispatchEvent(new Event("focus"));
        });
        expect(isComposerFocused()).toBe(true);
    });

    test("refocuses on pageshow, the Safari bfcache path", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());

        act(() => {
            window.dispatchEvent(new Event("pageshow"));
        });
        expect(isComposerFocused()).toBe(true);
    });

    test("refocuses when the document becomes visible again", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());

        act(() => {
            document.dispatchEvent(new Event("visibilitychange"));
        });
        expect(isComposerFocused()).toBe(true);
    });

    test("stays quiet while the document is hidden", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());
        setVisibility("hidden");

        act(() => {
            document.dispatchEvent(new Event("visibilitychange"));
            window.dispatchEvent(new Event("focus"));
        });
        expect(isComposerFocused()).toBe(false);
    });

    test("focuses once when both events arrive together", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());

        const focusSpy = vi.spyOn(composer(), "focus");
        act(() => {
            window.dispatchEvent(new Event("focus"));
            document.dispatchEvent(new Event("visibilitychange"));
        });
        expect(focusSpy).toHaveBeenCalledTimes(1);
    });

    test("does not refocus a composer that already has focus", async () => {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));

        const focusSpy = vi.spyOn(composer(), "focus");
        act(() => {
            window.dispatchEvent(new Event("focus"));
        });
        expect(focusSpy).not.toHaveBeenCalled();
    });
});

describe("typing anywhere", () => {
    async function renderBlurred() {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());
    }

    test("redirects a printable key into the composer", async () => {
        await renderBlurred();

        const event = pressKey({ key: "h" });

        expect(isComposerFocused()).toBe(true);
        expect(document.execCommand).toHaveBeenCalledWith("insertText", false, "h");
        expect(event.defaultPrevented).toBe(true);
    });

    test.each([
        ["the sidebar shortcut", { key: "b", metaKey: true }],
        ["a control chord", { key: "a", ctrlKey: true }],
        ["space, which scrolls the log", { key: " " }],
        ["enter, which activates the focused control", { key: "Enter" }],
        ["an IME composition", { key: "a", isComposing: true }],
    ])("leaves %s alone", async (_label, init) => {
        await renderBlurred();

        const event = pressKey(init);

        expect(isComposerFocused()).toBe(false);
        expect(document.execCommand).not.toHaveBeenCalled();
        expect(event.defaultPrevented).toBe(false);
    });

    test("leaves typing in another field alone", async () => {
        await renderBlurred();
        const other = document.createElement("input");
        document.body.appendChild(other);
        other.focus();

        pressKey({ key: "h" });

        expect(document.activeElement).toBe(other);
        expect(document.execCommand).not.toHaveBeenCalled();
        other.remove();
    });

    test("leaves typing alone while a modal layer is open", async () => {
        await renderBlurred();
        openModalLayer();

        pressKey({ key: "h" });

        expect(isComposerFocused()).toBe(false);
        expect(document.execCommand).not.toHaveBeenCalled();
    });

    test.each(["menu", "listbox"])("leaves an open Radix %s alone so its typeahead still works", async role => {
        await renderBlurred();
        const overlay = document.createElement("div");
        overlay.setAttribute("role", role);
        overlay.setAttribute("data-state", "open");
        document.body.appendChild(overlay);

        const event = pressKey({ key: "o" });

        expect(isComposerFocused()).toBe(false);
        expect(document.execCommand).not.toHaveBeenCalled();
        expect(event.defaultPrevented).toBe(false);
    });

    test("ignores a key another handler already claimed", async () => {
        await renderBlurred();
        const event = new KeyboardEvent("keydown", { key: "h", bubbles: true, cancelable: true });
        event.preventDefault();

        act(() => {
            document.dispatchEvent(event);
        });

        expect(isComposerFocused()).toBe(false);
        expect(document.execCommand).not.toHaveBeenCalled();
    });
});

describe("insertion fallback", () => {
    async function renderBlurred() {
        renderComposer();
        await waitFor(() => expect(isComposerFocused()).toBe(true));
        act(() => (document.activeElement as HTMLElement).blur());
    }

    test("lets the browser type the character when execCommand reports failure", async () => {
        await renderBlurred();
        vi.mocked(document.execCommand).mockReturnValue(false);

        const event = pressKey({ key: "h" });

        expect(isComposerFocused()).toBe(true);
        expect(event.defaultPrevented).toBe(false);
    });

    test("does not throw when execCommand is unavailable", async () => {
        await renderBlurred();
        const saved = document.execCommand;
        onTestFinished(() => {
            document.execCommand = saved;
        });
        // @ts-expect-error deliberately removing a deprecated API to model a hardened webview
        delete document.execCommand;

        let event: KeyboardEvent | undefined;
        expect(() => {
            event = pressKey({ key: "h" });
        }).not.toThrow();
        expect(event?.defaultPrevented).toBe(false);
    });
});

describe("teardown", () => {
    function makeComposer(): HTMLDivElement {
        const el = document.createElement("div");
        el.setAttribute("contenteditable", "true");
        el.tabIndex = 0;
        document.body.appendChild(el);
        return el;
    }

    test("removes its document and window listeners on unmount", () => {
        const element = makeComposer();
        const ref = { current: element };
        const { unmount } = renderHook(() => useChatInputAutoFocus({ ref, enabled: true, composerDisabled: false }));

        unmount();
        vi.mocked(document.execCommand).mockClear();

        act(() => {
            document.dispatchEvent(new KeyboardEvent("keydown", { key: "h", bubbles: true, cancelable: true }));
            window.dispatchEvent(new Event("focus"));
            document.dispatchEvent(new Event("visibilitychange"));
        });

        expect(document.activeElement).not.toBe(element);
        expect(document.execCommand).not.toHaveBeenCalled();
        element.remove();
    });

    test("focuses once a composer that mounted disabled becomes usable", async () => {
        const element = makeComposer();
        const ref = { current: element };
        const { rerender } = renderHook(({ disabled }) => useChatInputAutoFocus({ ref, enabled: true, composerDisabled: disabled }), { initialProps: { disabled: true } });

        await settle();
        expect(document.activeElement).not.toBe(element);

        rerender({ disabled: false });
        await settle();

        expect(document.activeElement).toBe(element);
        element.remove();
    });

    test("stops redirecting typing once the composer is disabled", () => {
        const element = makeComposer();
        const ref = { current: element };
        const { rerender } = renderHook(({ disabled }) => useChatInputAutoFocus({ ref, enabled: true, composerDisabled: disabled }), { initialProps: { disabled: false } });

        rerender({ disabled: true });
        vi.mocked(document.execCommand).mockClear();
        act(() => {
            document.dispatchEvent(new KeyboardEvent("keydown", { key: "h", bubbles: true, cancelable: true }));
        });

        expect(document.execCommand).not.toHaveBeenCalled();
        element.remove();
    });
});
