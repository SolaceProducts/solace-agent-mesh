// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { configure } from "storybook/test";
import { afterEach, beforeEach } from "vitest";

import { getWorker } from "./mswWorker";

configure({ asyncUtilTimeout: 5000 });

const unhandledCollectionReads = new Set<string>();

function isUnhandledCollectionRead(method: string, url: string): boolean {
    if (method !== "GET") {
        return false;
    }
    const { pathname } = new URL(url);
    const resource = pathname.replace(/^\/api\/v\d+\//, "").replace(/^platform\//, "");
    return pathname !== resource && resource.length > 0 && !resource.includes("/");
}

getWorker().events.on("request:unhandled", ({ request }) => {
    if (isUnhandledCollectionRead(request.method, request.url)) {
        unhandledCollectionReads.add(`${request.method} ${new URL(request.url).pathname}`);
    }
});

beforeEach(() => {
    unhandledCollectionReads.clear();
});

afterEach(() => {
    if (unhandledCollectionReads.size > 0) {
        const offenders = [...unhandledCollectionReads].sort().join("\n  ");
        unhandledCollectionReads.clear();
        throw new Error(`Story made collection GET(s) with no MSW handler. Add handlers:\n  ${offenders}`);
    }
});
