// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const ID_LIKE_SEGMENT = /^(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9_-]{20,})$/i;

let publishedRoutePattern = "/";

export function publishRoutePattern(pattern: string): void {
    publishedRoutePattern = pattern;
}

export function currentRoutePattern(): string {
    return publishedRoutePattern;
}

export function toRoutePattern(pathname: string, params: Readonly<Record<string, string | undefined>>): string {
    let result = pathname;
    const singleSegment = new Map<string, string>();

    for (const [name, rawValue] of Object.entries(params)) {
        if (!rawValue) continue;
        for (const value of new Set([rawValue, encodeURIComponent(rawValue)])) {
            if (value.includes("/")) {
                const start = result.lastIndexOf(value);
                if (start !== -1) {
                    result = `${result.slice(0, start)}:${name}${result.slice(start + value.length)}`;
                }
            } else {
                singleSegment.set(value, name);
            }
        }
    }

    return result
        .split("/")
        .map(segment => {
            const name = singleSegment.get(segment);
            if (name) return `:${name}`;
            return ID_LIKE_SEGMENT.test(segment) ? ":id" : segment;
        })
        .join("/");
}
