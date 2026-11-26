// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "../common";

import { getVersionV2 } from "./v2";

describe("version on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status }));

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("GETs /api/v2/platform/version and unwraps the data envelope", async () => {
        respond(200, { data: { products: [{ key: "sam-gateway-go", name: "Gateway", version: "1.2.3" }], minClientVersion: null, apiVersion: null } });

        const result = await getVersionV2();

        expect(String(fetchMock.mock.calls[0][0])).toContain("/api/v2/platform/version");
        expect(fetchMock.mock.calls[0][1]?.method ?? "GET").toBe("GET");
        expect(result.products).toEqual([{ id: "sam-gateway-go", name: "Gateway", version: "1.2.3", description: undefined }]);
    });

    test("maps each product key onto the v1 id, in order", async () => {
        respond(200, {
            data: {
                products: [
                    { key: "sam-gateway-go", name: "Gateway", version: "1.0.0" },
                    { key: "sam-str-go", name: "Runtime", version: "2.0.0" },
                ],
                minClientVersion: "0.9.0",
                apiVersion: "2",
            },
        });

        const result = await getVersionV2();

        expect(result.products.map(p => p.id)).toEqual(["sam-gateway-go", "sam-str-go"]);
    });

    test("rejects with ApiV2Error on a 401", async () => {
        respond(401, { message: "Unauthorized", errorId: "e1" });

        await expect(getVersionV2()).rejects.toBeInstanceOf(ApiV2Error);
    });
});
