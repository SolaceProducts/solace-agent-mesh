// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const SCOPE_PATTERN = /^[a-z0-9_*-]+(:[a-zA-Z0-9_*-]+)*$/;

export function isValidScope(scope: string): boolean {
    return SCOPE_PATTERN.test(scope);
}

export function matchScope(granted: string, required: string): boolean {
    if (granted === "*") return true;
    granted = canonicalHeldScope(granted);
    if (granted === required) return true;

    const gParts = granted.split(":");
    const rParts = required.split(":");

    if (gParts.at(-1) === "*") {
        const prefixLen = gParts.length - 1;
        if (rParts.length < prefixLen) return false;
        for (let i = 0; i < prefixLen; i++) {
            if (!matchSegment(gParts[i], rParts[i])) return false;
        }
        return true;
    }

    if (gParts.length !== rParts.length) return false;

    for (let i = 0; i < gParts.length; i++) {
        if (!matchSegment(gParts[i], rParts[i])) return false;
    }
    return true;
}

export function partialGlob(segment: string): boolean {
    if (segment === "*" || segment === "_") return false;
    return segment.includes("*") || segment.includes("?");
}

export function scopeHasPartialGlob(scope: string): boolean {
    const parts = scope.split(":");
    if (partialGlob(parts[0])) return true;
    return parts.length >= 2 && partialGlob(parts[1]);
}

export function hasStrictScope(granted: readonly string[], required: string): boolean {
    return hasScope(
        granted.filter(scope => !scopeHasPartialGlob(scope)),
        required
    );
}

export function hasScope(granted: readonly string[], required: string): boolean {
    return granted.some(g => matchScope(g, required));
}

function canonicalHeldScope(scope: string): string {
    const parts = scope.split(":");
    if (parts.length !== 3 || parts[1] !== "_") return scope;
    return `${parts[0]}:*:${parts[2]}`;
}

function matchSegment(granted: string, required: string): boolean {
    if (granted === "*") return true;
    if (granted.includes("*")) return pathMatch(granted, required);
    return granted === required;
}

function pathMatch(pattern: string, name: string): boolean {
    let re = "";
    for (const ch of pattern) {
        if (ch === "*") re += "[^/]*";
        else if (ch === "?") re += "[^/]";
        else re += ch.replaceAll(/[.+^${}()|[\]\\]/g, String.raw`\$&`);
    }
    return new RegExp(`^${re}$`).test(name);
}
