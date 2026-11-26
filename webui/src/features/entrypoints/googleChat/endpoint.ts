// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const isHttpsBase = (externalBase: string | undefined): boolean => !!externalBase && externalBase.startsWith("https://");

function withoutTrailingSlashes(text: string): string {
    let end = text.length;
    while (end > 0 && text[end - 1] === "/") end--;
    return text.slice(0, end);
}

export function endpointPrefix(externalBase: string): string {
    return `${withoutTrailingSlashes(externalBase)}/gw/`;
}

export function addressSegment(slug: string | undefined, id: string | undefined): string {
    return slug || id || "";
}

export function googleChatWebhookUrl(gateway: { id?: string; slug?: string; values?: Record<string, unknown> }, externalBase: string): string {
    const stored = gateway.values?.endpoint_url;
    if (typeof stored === "string" && stored.trim() !== "") return stored.trim();
    return `${endpointPrefix(externalBase)}${addressSegment(gateway.slug, gateway.id)}/events`;
}
