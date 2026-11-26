// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type NavItemConfig } from "@/types/nav";

type RouteMatch = NonNullable<NavItemConfig["routeMatch"]>;

const patternsOf = (routeMatch: RouteMatch): ReadonlyArray<string | RegExp> => (Array.isArray(routeMatch) ? routeMatch : [routeMatch]);

export function routeMatchesPrefix(pathname: string, routeMatch: NavItemConfig["routeMatch"]): boolean {
    if (!routeMatch) return false;
    return patternsOf(routeMatch).some(pattern => (pattern instanceof RegExp ? pattern.test(pathname) : pathname.startsWith(pattern)));
}

export function routeMatchesExactly(pathname: string, routeMatch: NavItemConfig["routeMatch"]): boolean {
    if (!routeMatch) return false;
    const trimmed = trimTrailingSlashes(pathname);
    return patternsOf(routeMatch).some(pattern => (pattern instanceof RegExp ? pattern.test(pathname) : trimmed === pattern));
}

function trimTrailingSlashes(pathname: string): string {
    let end = pathname.length;
    while (end > 0 && pathname[end - 1] === "/") end--;
    return end === 0 ? "/" : pathname.slice(0, end);
}

export function findExactNavMatchLabel(items: NavItemConfig[], pathname: string): string | null {
    for (const item of items) {
        if (item.hidden) continue;
        const childLabel = item.children ? findExactNavMatchLabel(item.children, pathname) : null;
        if (childLabel) return childLabel;
        if (routeMatchesExactly(pathname, item.routeMatch)) return item.label;
    }
    return null;
}
