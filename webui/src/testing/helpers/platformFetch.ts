// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

type Spy = (...args: unknown[]) => unknown;

export interface PlatformSpies {
    get: Spy;
    post: Spy;
    put: Spy;
    patch: Spy;
    delete: Spy;
}

export function platformFetch(spies: PlatformSpies): typeof fetch {
    return async (input, init) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname + url.search;
        const method = (init?.method ?? "GET").toLowerCase() as keyof PlatformSpies;
        const answer = await (typeof init?.body === "string" ? spies[method](path, JSON.parse(init.body)) : spies[method](path));
        return answer instanceof Response ? answer : Response.json(answer ?? null);
    };
}
