// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const ALLOWED_REDIRECT_PREFIXES = ["/builder", "/chat", "/prompts", "/scheduled-tasks"];

export function parseSafeRedirectPath(route: string): string | null {
    try {
        const origin = globalThis.location.origin;
        const parsed = new URL(route, origin);
        const allowed = ALLOWED_REDIRECT_PREFIXES.some(p => parsed.pathname === p || parsed.pathname.startsWith(p + "/"));
        if (parsed.origin !== origin || !allowed) {
            return null;
        }
        return parsed.pathname;
    } catch {
        return null;
    }
}

export function appendUrlParams(route: string, urlParams: Record<string, string> | undefined): string {
    if (!urlParams || Object.keys(urlParams).length === 0) return route;
    const params = new URLSearchParams(Object.fromEntries(Object.entries(urlParams).map(([k, v]) => [k, String(v)])));
    return route + (route.includes("?") ? "&" : "?") + params.toString();
}
