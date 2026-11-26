// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

export interface PromptBuilderInitial {
    initialMessage: string | null;
    prefillOnly: boolean;
}

export function resolvePromptBuilderInitial(redirectContext: string | null, taskDescription: string | null | undefined): PromptBuilderInitial {
    if (taskDescription) {
        return { initialMessage: taskDescription, prefillOnly: false };
    }
    if (redirectContext) {
        return { initialMessage: redirectContext, prefillOnly: true };
    }
    return { initialMessage: null, prefillOnly: false };
}

export function consumeNewPromptSeed(mode: "ai-assisted" | "manual", taskDescription: string | null | undefined): PromptBuilderInitial {
    sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS);
    if (mode !== "ai-assisted") {
        return { initialMessage: null, prefillOnly: false };
    }
    const redirectContext = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT);
    sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT);
    return resolvePromptBuilderInitial(redirectContext, taskDescription);
}
