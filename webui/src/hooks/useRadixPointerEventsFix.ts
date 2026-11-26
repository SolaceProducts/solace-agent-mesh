// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

let pendingCheck = 0;

function scheduleCheck() {
    if (pendingCheck) return;
    pendingCheck = globalThis.requestAnimationFrame(() => {
        pendingCheck = 0;
        clearStalePointerEvents();
    });
}

const OPEN_MODAL_DIALOG_SELECTOR = '[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"]';

function clearStalePointerEvents() {
    if (document.body.style.pointerEvents !== "none") return;
    const hasOpenModalDialog = document.querySelector(OPEN_MODAL_DIALOG_SELECTOR);
    if (!hasOpenModalDialog) {
        document.body.style.removeProperty("pointer-events");
    }
}

export function deferPanelClose(close: () => void): void {
    globalThis.requestAnimationFrame(close);
}

export function useRadixPointerEventsFix() {
    useEffect(() => {
        scheduleCheck();

        const observer = new MutationObserver(scheduleCheck);
        observer.observe(document.body, {
            attributes: true,
            attributeFilter: ["style"],
            childList: true,
            subtree: true,
        });

        return () => {
            observer.disconnect();
            if (pendingCheck) {
                globalThis.cancelAnimationFrame(pendingCheck);
                pendingCheck = 0;
            }
        };
    }, []);
}
