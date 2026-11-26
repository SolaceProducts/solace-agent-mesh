// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type RefObject, useCallback, useEffect, useRef } from "react";

import { isTouchPrimaryDevice } from "@/hooks/useCoarsePointer";

import { isNativeImeComposing } from "@/utils/ime";

type ComposerFocusMode = "passive" | "typing";

export interface ComposerFocusGuardParams {
    element: HTMLElement | null;
    composerDisabled: boolean;
    mode: ComposerFocusMode;
}

const NON_TEXT_INPUT_TYPES = new Set(["checkbox", "radio", "button", "submit", "reset", "range", "color", "file", "image"]);

const KEYBOARD_OWNING_ROLE_SELECTOR = '[role="combobox"], [role="listbox"], [role="menu"], [role="menubar"], [role="grid"], [role="tree"], [role="textbox"], [role="searchbox"], [role="spinbutton"], [role="separator"], [role="application"]';

const OPEN_OVERLAY_SELECTOR = '[role="dialog"]:not([data-state="closed"]), [role="alertdialog"]:not([data-state="closed"]), [role="menu"]:not([data-state="closed"]), [role="listbox"]:not([data-state="closed"])';

function ownsKeyboardInput(element: Element | null): boolean {
    if (!element || element === document.body || element === document.documentElement) return false;
    const node = element as HTMLElement;
    if (node.isContentEditable || node.getAttribute("contenteditable") === "true") return true;
    switch (node.tagName) {
        case "TEXTAREA":
        case "SELECT":
        case "IFRAME":
            return true;
        case "INPUT":
            return !NON_TEXT_INPUT_TYPES.has((node as HTMLInputElement).type);
        default:
            return node.closest(KEYBOARD_OWNING_ROLE_SELECTOR) !== null;
    }
}

function hasOpenOverlay(): boolean {
    return document.querySelector(OPEN_OVERLAY_SELECTOR) !== null;
}

function hasRangeSelection(): boolean {
    const selection = globalThis.getSelection?.();
    return !!selection && selection.rangeCount > 0 && !selection.isCollapsed;
}

export function canFocusComposer({ element, composerDisabled, mode }: ComposerFocusGuardParams): boolean {
    if (!element?.isConnected) return false;
    if (composerDisabled) return false;
    if (element.getAttribute("contenteditable") !== "true") return false;
    if (document.visibilityState === "hidden") return false;
    if (isTouchPrimaryDevice()) return false;

    const active = document.activeElement;
    if (active === element || element.contains(active)) return false;
    if (hasOpenOverlay()) return false;
    if (hasRangeSelection()) return false;

    if (mode === "passive") {
        return active === null || active === document.body || active === document.documentElement;
    }
    return !ownsKeyboardInput(active);
}

export function focusComposerAtEnd(element: HTMLElement | null): void {
    if (!element) return;

    const selection = globalThis.getSelection?.();
    const parked = selection?.rangeCount ? selection.getRangeAt(0) : null;
    const draftCaret = parked?.collapsed && element.contains(parked.startContainer) ? parked.cloneRange() : null;

    element.focus({ preventScroll: true });
    if (!selection) return;

    try {
        const range = draftCaret ?? document.createRange();
        if (!draftCaret) {
            range.selectNodeContents(element);
            range.collapse(false);
        }
        selection.removeAllRanges();
        selection.addRange(range);
    } catch {}
}

function holdsFocus(element: HTMLElement): boolean {
    return element.contains(document.activeElement);
}

interface LegacyEditingDocument {
    execCommand?: (commandId: string, showUI?: boolean, value?: string) => boolean;
}

function insertTextAtCaret(text: string): boolean {
    return (document as unknown as LegacyEditingDocument).execCommand?.("insertText", false, text) ?? false;
}

function isAltGrChordNotVoiceOver(event: KeyboardEvent): boolean {
    return event.ctrlKey && event.altKey && (event.getModifierState?.("AltGraph") ?? false);
}

export function isPrintableTypeAnywhereKey(event: KeyboardEvent): boolean {
    if (isNativeImeComposing(event)) return false;
    if (event.key.length !== 1) return false;
    if (event.key === " ") return false;
    if (event.metaKey) return false;
    if (event.ctrlKey || event.altKey) {
        return isAltGrChordNotVoiceOver(event);
    }
    return true;
}

export interface ChatInputAutoFocusOptions {
    ref: RefObject<HTMLDivElement | null>;
    enabled: boolean;
    composerDisabled: boolean;
}

export function useChatInputAutoFocus({ ref, enabled, composerDisabled }: ChatInputAutoFocusOptions): void {
    const composerDisabledRef = useRef(composerDisabled);
    composerDisabledRef.current = composerDisabled;

    const focusIfIdle = useCallback(() => {
        if (canFocusComposer({ element: ref.current, composerDisabled: composerDisabledRef.current, mode: "passive" })) {
            focusComposerAtEnd(ref.current);
        }
    }, [ref]);

    useEffect(() => {
        if (!enabled) return;
        const frame = requestAnimationFrame(focusIfIdle);
        return () => cancelAnimationFrame(frame);
    }, [enabled, composerDisabled, focusIfIdle]);

    useEffect(() => {
        if (!enabled) return;

        const focusIfVisible = () => {
            if (document.visibilityState === "visible") focusIfIdle();
        };

        window.addEventListener("focus", focusIfIdle);
        window.addEventListener("pageshow", focusIfIdle);
        document.addEventListener("visibilitychange", focusIfVisible);
        return () => {
            window.removeEventListener("focus", focusIfIdle);
            window.removeEventListener("pageshow", focusIfIdle);
            document.removeEventListener("visibilitychange", focusIfVisible);
        };
    }, [enabled, focusIfIdle]);

    useEffect(() => {
        if (!enabled) return;

        const redirectTyping = (event: KeyboardEvent) => {
            if (event.defaultPrevented) return;
            if (!isPrintableTypeAnywhereKey(event)) return;
            const element = ref.current;
            if (!canFocusComposer({ element, composerDisabled: composerDisabledRef.current, mode: "typing" })) return;

            focusComposerAtEnd(element);
            if (!element || !holdsFocus(element)) return;
            if (insertTextAtCaret(event.key)) {
                event.preventDefault();
            }
        };

        document.addEventListener("keydown", redirectTyping);
        return () => document.removeEventListener("keydown", redirectTyping);
    }, [enabled, ref]);
}
