// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it } from "vitest";

import { MOBILE_BREAKPOINT } from "@/hooks/useMobile";

import { canFocusComposer, focusComposerAtEnd, isPrintableTypeAnywhereKey } from "./useChatInputAutoFocus";

const key = (init: Partial<globalThis.KeyboardEvent>): globalThis.KeyboardEvent => ({ key: "a", isComposing: false, keyCode: 65, metaKey: false, ctrlKey: false, altKey: false, getModifierState: () => false, ...init }) as globalThis.KeyboardEvent;

function makeComposer(text = ""): HTMLDivElement {
    const el = document.createElement("div");
    el.setAttribute("contenteditable", "true");
    el.tabIndex = 0;
    if (text) el.textContent = text;
    document.body.appendChild(el);
    return el;
}

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

afterEach(() => {
    document.body.innerHTML = "";
    setPointer({ coarse: false });
    setVisibility("visible");
    globalThis.getSelection()?.removeAllRanges();
});

describe("isPrintableTypeAnywhereKey", () => {
    it.each(["a", "Z", "7", "/", "?", "é"])("accepts the printable key %s", k => {
        expect(isPrintableTypeAnywhereKey(key({ key: k }))).toBe(true);
    });

    it("rejects space so it keeps scrolling a long chat log", () => {
        expect(isPrintableTypeAnywhereKey(key({ key: " " }))).toBe(false);
    });

    it.each(["Enter", "Tab", "Escape", "Backspace", "ArrowDown", "Dead", "F5"])("rejects the non-printable key %s", k => {
        expect(isPrintableTypeAnywhereKey(key({ key: k }))).toBe(false);
    });

    it("rejects modifier chords that belong to other shortcuts", () => {
        expect(isPrintableTypeAnywhereKey(key({ key: "b", metaKey: true }))).toBe(false);
        expect(isPrintableTypeAnywhereKey(key({ key: "a", ctrlKey: true }))).toBe(false);
        expect(isPrintableTypeAnywhereKey(key({ key: "a", altKey: true }))).toBe(false);
    });

    it("accepts AltGr, which produces a genuine printable character on Windows", () => {
        expect(isPrintableTypeAnywhereKey(key({ key: "€", ctrlKey: true, altKey: true, getModifierState: () => true }))).toBe(true);
    });

    it("rejects the VoiceOver modifier, which reports Ctrl+Alt without AltGraph", () => {
        expect(isPrintableTypeAnywhereKey(key({ key: "h", ctrlKey: true, altKey: true }))).toBe(false);
    });

    it("rejects Ctrl+Alt when the runtime cannot report AltGraph", () => {
        expect(isPrintableTypeAnywhereKey({ key: "h", isComposing: false, keyCode: 72, metaKey: false, ctrlKey: true, altKey: true } as globalThis.KeyboardEvent)).toBe(false);
    });

    it("rejects keys while an IME is composing", () => {
        expect(isPrintableTypeAnywhereKey(key({ isComposing: true }))).toBe(false);
        expect(isPrintableTypeAnywhereKey(key({ keyCode: 229 }))).toBe(false);
    });
});

describe("canFocusComposer", () => {
    const passive = (element: HTMLElement | null, composerDisabled = false) => canFocusComposer({ element, composerDisabled, mode: "passive" });
    const typing = (element: HTMLElement | null, composerDisabled = false) => canFocusComposer({ element, composerDisabled, mode: "typing" });

    it("allows focus when nothing else holds it", () => {
        expect(passive(makeComposer())).toBe(true);
    });

    it("refuses a missing or detached element", () => {
        expect(passive(null)).toBe(false);
        expect(passive(document.createElement("div"))).toBe(false);
    });

    it("refuses a disabled composer", () => {
        expect(passive(makeComposer(), true)).toBe(false);
    });

    it("refuses a non-editable composer", () => {
        const el = makeComposer();
        el.setAttribute("contenteditable", "false");
        expect(passive(el)).toBe(false);
    });

    it("refuses a hidden document", () => {
        setVisibility("hidden");
        expect(passive(makeComposer())).toBe(false);
    });

    it("refuses a touch-only pointer so the soft keyboard stays down", () => {
        setPointer({ coarse: true });
        expect(passive(makeComposer())).toBe(false);
    });

    it("allows a hybrid device with both a touchscreen and a trackpad/mouse", () => {
        setPointer({ coarse: true, fine: true });
        expect(passive(makeComposer())).toBe(true);
    });

    it("refuses on a narrow-viewport touch device with a paired mouse, so the soft keyboard stays down", () => {
        setPointer({ coarse: true, fine: true, width: MOBILE_BREAKPOINT - 1 });
        expect(passive(makeComposer())).toBe(false);
    });

    it("refuses when the composer already holds focus", () => {
        const el = makeComposer();
        el.focus();
        expect(passive(el)).toBe(false);
    });

    it("refuses when a descendant of the composer holds focus", () => {
        const el = makeComposer();
        const child = document.createElement("input");
        el.appendChild(child);
        child.focus();
        expect(passive(el)).toBe(false);
    });

    it("refuses while a Radix modal layer is open", () => {
        const el = makeComposer();
        const dialog = document.createElement("div");
        dialog.setAttribute("role", "dialog");
        dialog.setAttribute("data-state", "open");
        document.body.appendChild(dialog);
        expect(passive(el)).toBe(false);
        expect(typing(el)).toBe(false);

        dialog.setAttribute("role", "alertdialog");
        expect(passive(el)).toBe(false);

        dialog.setAttribute("data-state", "closed");
        expect(passive(el)).toBe(true);
    });

    it("ignores a non-dialog data-state, which accordions and tabs also carry", () => {
        const el = makeComposer();
        const accordion = document.createElement("div");
        accordion.setAttribute("data-state", "open");
        document.body.appendChild(accordion);
        expect(passive(el)).toBe(true);
    });

    it("refuses while the user has text selected", () => {
        const el = makeComposer();
        const paragraph = document.createElement("p");
        paragraph.textContent = "some response text";
        document.body.appendChild(paragraph);

        const range = document.createRange();
        range.selectNodeContents(paragraph);
        globalThis.getSelection()?.addRange(range);

        expect(passive(el)).toBe(false);
        expect(typing(el)).toBe(false);
    });

    it("takes focus from a button only when the user is typing", () => {
        const el = makeComposer();
        const button = document.createElement("button");
        document.body.appendChild(button);
        button.focus();

        expect(passive(el)).toBe(false);
        expect(typing(el)).toBe(true);
    });

    it("never takes focus from another text-entry surface", () => {
        const el = makeComposer();
        const input = document.createElement("input");
        document.body.appendChild(input);
        input.focus();

        expect(passive(el)).toBe(false);
        expect(typing(el)).toBe(false);
    });

    it("treats a checkbox as a non-text surface", () => {
        const el = makeComposer();
        const checkbox = document.createElement("input");
        checkbox.type = "checkbox";
        document.body.appendChild(checkbox);
        checkbox.focus();

        expect(typing(el)).toBe(true);
    });

    it.each(["textarea", "select"])("never takes focus from a focused %s", tag => {
        const el = makeComposer();
        const other = document.createElement(tag);
        document.body.appendChild(other);
        other.focus();

        expect(typing(el)).toBe(false);
    });

    it("never takes focus from another contenteditable surface", () => {
        const el = makeComposer();
        const other = makeComposer();
        other.focus();

        expect(typing(el)).toBe(false);
    });

    it.each(["menu", "listbox"])("refuses while an open Radix %s owns the keyboard", role => {
        const el = makeComposer();
        const overlay = document.createElement("div");
        overlay.setAttribute("role", role);
        overlay.setAttribute("data-state", "open");
        document.body.appendChild(overlay);

        expect(passive(el)).toBe(false);
        expect(typing(el)).toBe(false);
    });

    it.each(["combobox", "menu", "listbox", "grid", "tree"])("never takes focus from a focused %s widget", role => {
        const el = makeComposer();
        const widget = document.createElement("div");
        widget.setAttribute("role", role);
        widget.tabIndex = 0;
        document.body.appendChild(widget);
        widget.focus();

        expect(typing(el)).toBe(false);
    });

    it("refuses while a hand-rolled overlay with no data-state is open", () => {
        const el = makeComposer();
        const lightbox = document.createElement("div");
        lightbox.setAttribute("role", "dialog");
        lightbox.setAttribute("aria-modal", "true");
        document.body.appendChild(lightbox);

        expect(passive(el)).toBe(false);
        expect(typing(el)).toBe(false);
    });

    it("ignores an overlay that has finished closing", () => {
        const el = makeComposer();
        const overlay = document.createElement("div");
        overlay.setAttribute("role", "dialog");
        overlay.setAttribute("data-state", "closed");
        document.body.appendChild(overlay);

        expect(passive(el)).toBe(true);
    });

    it("never takes focus from a resize separator mid-drag", () => {
        const el = makeComposer();
        const grip = document.createElement("div");
        grip.setAttribute("role", "separator");
        grip.tabIndex = 0;
        document.body.appendChild(grip);
        grip.focus();

        expect(typing(el)).toBe(false);
    });

    it("never takes focus from an item inside a typeahead widget", () => {
        const el = makeComposer();
        const menu = document.createElement("div");
        menu.setAttribute("role", "menu");
        const item = document.createElement("div");
        item.setAttribute("role", "menuitem");
        item.tabIndex = 0;
        menu.appendChild(item);
        document.body.appendChild(menu);
        item.focus();

        expect(typing(el)).toBe(false);
    });
});

describe("focusComposerAtEnd", () => {
    it("focuses the element", () => {
        const el = makeComposer();
        focusComposerAtEnd(el);
        expect(document.activeElement).toBe(el);
    });

    it("puts a collapsed caret after existing text", () => {
        const el = makeComposer("hello");
        focusComposerAtEnd(el);

        const range = globalThis.getSelection()?.getRangeAt(0);
        expect(range?.collapsed).toBe(true);
        expect(el.contains(range!.startContainer)).toBe(true);
        expect(range!.startOffset).toBe(el.childNodes.length);
    });

    it("leaves a caret the user already parked mid-draft", () => {
        const el = makeComposer("hello");
        const textNode = el.firstChild!;
        const range = document.createRange();
        range.setStart(textNode, 2);
        range.collapse(true);
        globalThis.getSelection()?.addRange(range);

        focusComposerAtEnd(el);

        const after = globalThis.getSelection()?.getRangeAt(0);
        expect(after?.startContainer).toBe(textNode);
        expect(after?.startOffset).toBe(2);
    });

    it("does not throw without an element", () => {
        expect(() => focusComposerAtEnd(null)).not.toThrow();
    });
});
