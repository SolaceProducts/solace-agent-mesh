// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

export type Resource = {
    url: string;
    contentType: string;
    text: string;
    status?: number;
    statusText?: string;
};

type ResourceCache = Map<string, Resource>;

const cache: ResourceCache = new Map();
const pendingPromises: Map<string, Promise<Resource>> = new Map();

export async function getResource(url: string): Promise<Resource> {
    if (cache.has(url)) {
        return cache.get(url)!;
    }
    if (pendingPromises.has(url)) {
        return pendingPromises.get(url)!;
    }
    const fetchPromise = (async () => {
        try {
            const response = await api.platform.get(url, { fullResponse: true });
            let resource: Resource;
            if (response.ok) {
                resource = {
                    url,
                    contentType: response.headers.get("content-type") ?? "application/json",
                    text: await response.text(),
                    status: response.status,
                };
            } else {
                resource = {
                    url,
                    contentType: response.headers.get("content-type") ?? "application/json",
                    text: "",
                    status: response.status,
                    statusText: response.statusText,
                };
            }
            cache.set(url, resource);
            return resource;
        } finally {
            pendingPromises.delete(url);
        }
    })();
    pendingPromises.set(url, fetchPromise);
    return fetchPromise;
}

export function invalidateResource(url: string) {
    cache.delete(url);
}

export function clearResourceCache() {
    cache.clear();
    pendingPromises.clear();
}
