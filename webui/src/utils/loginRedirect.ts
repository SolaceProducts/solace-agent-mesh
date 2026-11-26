// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface NavigateToLoginOptions {
    userInitiated?: boolean;
    onPopupOpened?: (popup: Window) => void;
}

function readSandboxAttributeBecauseSandboxPropertyIsUnreliable(frame: HTMLElement | null): string | null {
    return frame?.getAttribute("sandbox") ?? null;
}

export function topIsNavigable({ userInitiated = false }: NavigateToLoginOptions = {}): boolean {
    try {
        if (globalThis.top?.location.origin !== globalThis.location.origin) {
            return false;
        }
        const sandbox = readSandboxAttributeBecauseSandboxPropertyIsUnreliable(globalThis.frameElement as HTMLElement | null);
        if (sandbox === null) {
            return true;
        }
        const tokens = sandbox.split(/\s+/);
        if (tokens.includes("allow-top-navigation")) {
            return true;
        }
        return userInitiated && tokens.includes("allow-top-navigation-by-user-activation");
    } catch {
        return false;
    }
}

export const FRAMED_LOGIN_BLOCKED_EVENT = "sam:framed-login-blocked";

export type FramedLoginBlockedReason = "popup-refused" | "no-gesture";

export interface FramedLoginBlockedDetail {
    reason: FramedLoginBlockedReason;
}

export const LOGIN_POPUP_NAME = "sam-login";

export const AUTH_COMPLETE_MESSAGE = "sam:auth-complete";

const FRAMED_LOGIN_BLOCKED_WARNING =
    "[auth] Cannot escape the embedding frame, and the identity provider refuses to render inside one " +
    '(login.microsoftonline.com sends "X-Frame-Options: deny"), so the in-frame login was NOT attempted — ' +
    `it would leave a blank frame. Sign-in needs a popup or a top-level window; listen for the ` +
    `"${FRAMED_LOGIN_BLOCKED_EVENT}" event to surface a sign-in control.`;

function openLoginPopup(loginUrl: string, options: NavigateToLoginOptions): boolean {
    try {
        const popup = globalThis.open(loginUrl, LOGIN_POPUP_NAME, "popup=yes,width=520,height=680");
        if (!popup) {
            return false;
        }
        options.onPopupOpened?.(popup);
        popup.focus?.();
        return true;
    } catch {
        return false;
    }
}

function signalFramedLoginBlocked(reason: FramedLoginBlockedReason): void {
    console.warn(FRAMED_LOGIN_BLOCKED_WARNING);
    globalThis.dispatchEvent(new CustomEvent<FramedLoginBlockedDetail>(FRAMED_LOGIN_BLOCKED_EVENT, { detail: { reason } }));
}

export function navigateToLogin(loginUrl: string, options: NavigateToLoginOptions = {}): boolean {
    const framed = globalThis.top && globalThis.top !== globalThis.self;
    if (framed && topIsNavigable(options)) {
        try {
            globalThis.top!.location.href = loginUrl;
            return true;
        } catch {}
    }
    if (framed) {
        if (options.userInitiated) {
            if (openLoginPopup(loginUrl, options)) {
                return true;
            }
            signalFramedLoginBlocked("popup-refused");
            return false;
        }
        signalFramedLoginBlocked("no-gesture");
        return false;
    }
    globalThis.location.href = loginUrl;
    return true;
}
