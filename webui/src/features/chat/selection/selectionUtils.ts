// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function getSelectedText(): string | null {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0) {
        return null;
    }
    const text = selection.toString().trim();
    return text || null;
}

export function getSelectionRange(): Range | null {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0) {
        return null;
    }
    return selection.getRangeAt(0);
}

export function getSelectionBoundingRect(): DOMRect | null {
    const range = getSelectionRange();
    if (!range) {
        return null;
    }
    return range.getBoundingClientRect();
}

export function calculateMenuPosition(rect: DOMRect): { x: number; y: number } {
    const menuWidth = 160;
    const menuHeight = 40;
    const padding = 8;

    let x = rect.left;
    let y = rect.top - menuHeight - padding;

    if (x + menuWidth > window.innerWidth) {
        x = window.innerWidth - menuWidth - padding;
    }

    if (x < padding) {
        x = padding;
    }

    if (y < padding) {
        y = rect.bottom + padding;
    }

    return { x, y };
}

export const MAX_SELECTION_LENGTH = 5000;

export function isValidSelection(text: string | null): boolean {
    if (!text) {
        return false;
    }
    const trimmed = text.trim();
    return trimmed.length >= 3 && trimmed.length <= MAX_SELECTION_LENGTH && /\S/.test(trimmed);
}

export function clearBrowserSelection(): void {
    const selection = window.getSelection();
    if (selection) {
        selection.removeAllRanges();
    }
}

export function isSelectionContainedInElement(range: Range, container: HTMLElement): boolean {
    const startContainer = range.startContainer;
    const endContainer = range.endContainer;

    const startInContainer = container.contains(startContainer);
    const endInContainer = container.contains(endContainer);

    return startInContainer && endInContainer;
}
