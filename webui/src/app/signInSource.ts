// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const SIGN_IN_SOURCE_PARAM = "signInFrom";

const REMEMBERED_SIGN_IN_SOURCE_KEY = "sam_sign_in_source";

const SIGN_IN_SOURCE_LABELS = new Map([["whatsapp", "WhatsApp"]]);

export interface SignInSource {
    key: string;
    label: string;
}

function rememberedSignInSource(): string | null {
    try {
        return sessionStorage.getItem(REMEMBERED_SIGN_IN_SOURCE_KEY);
    } catch {
        return null;
    }
}

export function pendingSignInSource(search: string = globalThis.location.search): SignInSource | null {
    const key = new URLSearchParams(search).get(SIGN_IN_SOURCE_PARAM) || rememberedSignInSource();
    const label = key ? SIGN_IN_SOURCE_LABELS.get(key) : undefined;
    return key && label ? { key, label } : null;
}

export function rememberSignInSource(key: string): void {
    try {
        sessionStorage.setItem(REMEMBERED_SIGN_IN_SOURCE_KEY, key);
    } catch {}
}

export function settleSignInSource(): void {
    try {
        sessionStorage.removeItem(REMEMBERED_SIGN_IN_SOURCE_KEY);
    } catch {}
    const url = new URL(globalThis.location.href);
    if (!url.searchParams.has(SIGN_IN_SOURCE_PARAM)) return;
    url.searchParams.delete(SIGN_IN_SOURCE_PARAM);
    globalThis.history.replaceState(globalThis.history.state, "", url.toString());
}
