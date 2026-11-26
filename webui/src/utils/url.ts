// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const PROTOCOL_REGEX = /^https?:\/\//;

const HTTP_URL_PATTERN = /^https?:\/\/[^\s/?#][^\s]*$/i;

export const HTTP_URL_FORMAT_HINT = 'Must start with "http://" or "https://" followed by a host, with no spaces.';

export function isHttpUrl(value: string | null | undefined): boolean {
    const trimmed = (value ?? "").trim();
    return trimmed === "" || HTTP_URL_PATTERN.test(trimmed);
}

export function getCleanDomain(url: string): string {
    try {
        const domain = url.replace(PROTOCOL_REGEX, "").split("/")[0];
        return domain.startsWith("www.") ? domain.substring(4) : domain;
    } catch {
        return url;
    }
}

export function getFaviconUrl(domain: string, size: number = 32): string {
    return `https://www.google.com/s2/favicons?domain=${domain}&sz=${size}`;
}

export function getHashQueryParams(): URLSearchParams {
    const hash = window.location.hash;
    const queryIndex = hash.indexOf("?");
    return new URLSearchParams(queryIndex >= 0 ? hash.slice(queryIndex + 1) : "");
}

export function getHashPath(): string {
    const hash = window.location.hash.replace(/^#/, "");
    return hash.split("?")[0];
}

const POST_LOGIN_REDIRECT_KEY = "sam_post_login_redirect";

export function stashPostLoginRedirect(): void {
    try {
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, window.location.href);
    } catch {}
}

export function consumePostLoginRedirect(): string {
    let stashed: string | null;
    try {
        stashed = localStorage.getItem(POST_LOGIN_REDIRECT_KEY);
        localStorage.removeItem(POST_LOGIN_REDIRECT_KEY);
    } catch {
        return "/";
    }
    if (!stashed) return "/";
    try {
        const url = new URL(stashed);
        if (url.origin === window.location.origin) return url.href;
    } catch {}
    return "/";
}

const SIGN_IN_COMPLETED_KEY = "sam_sign_in_completed";

const SIGN_IN_COMPLETED_TTL_MS = 60_000;

export function markSignInCompleted(): void {
    try {
        sessionStorage.setItem(SIGN_IN_COMPLETED_KEY, String(Date.now()));
    } catch {}
}

export function signInJustCompleted(): boolean {
    let markedAt: number;
    try {
        markedAt = Number(sessionStorage.getItem(SIGN_IN_COMPLETED_KEY));
    } catch {
        return false;
    }
    return markedAt > 0 && Date.now() - markedAt < SIGN_IN_COMPLETED_TTL_MS;
}

export function clearSignInCompleted(): void {
    try {
        sessionStorage.removeItem(SIGN_IN_COMPLETED_KEY);
    } catch {}
}
