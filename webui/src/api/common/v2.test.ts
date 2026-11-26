// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error, parseInstant, platformV2, postDeployment } from "./v2";

describe("parseInstant", () => {
    test("reads the v2 date-time", () => {
        expect(parseInstant("2023-11-14T22:13:20.123Z")?.getTime()).toBe(1_700_000_000_123);
    });

    test("null is an unknown instant", () => {
        expect(parseInstant(null)).toBeNull();
    });

    test.each(["", "yesterday", "2023-11-14T22:13:20.123+00:00", "2023-11-14T22:13:20Z", "2023-11-14", "2023-13-45T25:61:61.999Z"])("rejects %j", value => {
        expect(() => parseInstant(value)).toThrow(TypeError);
    });
});

describe("platformV2", () => {
    const fetchMock = vi.fn<typeof fetch>();

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : typeof body === "string" ? body : JSON.stringify(body), { status, headers }));

    test("unwraps a single resource from data", async () => {
        respond(200, { data: { id: "a1" } });

        await expect(platformV2.get("/agents/a1")).resolves.toEqual({ id: "a1" });
        expect(fetchMock.mock.calls[0][0]).toBe("/api/v2/platform/agents/a1");
    });

    test("keeps a list's page envelope", async () => {
        const page = { data: [{ id: "a1" }], meta: { pagination: { pageNumber: 1, count: 1, pageSize: 20, nextPage: null, totalPages: 1 } } };
        respond(200, page);

        await expect(platformV2.list("/agents")).resolves.toEqual(page);
    });

    test("sends the body as JSON", async () => {
        respond(201, { data: { id: "a1" } });

        await platformV2.post("/agents", { name: "a" });
        const init = fetchMock.mock.calls[0][1];
        expect(init?.method).toBe("POST");
        expect(init?.body).toBe(`{"name":"a"}`);
    });

    test("a 204 resolves empty", async () => {
        respond(204);

        await expect(platformV2.delete("/agents/a1")).resolves.toBeUndefined();
    });

    test("rejects with the error envelope", async () => {
        respond(422, { message: "Validation failed", errorId: "e1", validationDetails: { name: ["is required"] } });

        const err = await platformV2.post("/agents", {}).catch((e: unknown) => e);
        expect(err).toBeInstanceOf(ApiV2Error);
        expect(err).toMatchObject({ status: 422, message: "Validation failed: name: is required", errorId: "e1", validationDetails: { name: ["is required"] } });
    });

    test("an error keeps the Location it points at", async () => {
        respond(502, { message: "Unable to deploy", errorId: "e2" }, { Location: "/api/v2/platform/entrypointDeployments/d1" });

        await expect(platformV2.post("/entrypointDeployments", {})).rejects.toMatchObject({ status: 502, message: "Unable to deploy", location: "/api/v2/platform/entrypointDeployments/d1" });
    });

    test("a failed deploy whose record can't be read rejects with the deploy's error, not the read's", async () => {
        respond(502, { message: "Unable to deploy agent", errorId: "e1" }, { Location: "/api/v2/platform/agentDeployments/d1" });
        respond(403, { message: "Forbidden", errorId: "e2" });

        await expect(postDeployment("/agentDeployments", {})).rejects.toMatchObject({ status: 502, message: "Unable to deploy agent", errorId: "e1" });
        expect(fetchMock.mock.calls[1][0]).toBe("/api/v2/platform/agentDeployments/d1");
    });

    test("listPut keeps the page envelope a verb path answers", async () => {
        const page = { data: [{ key: "gpt-4o" }], meta: { pagination: { pageNumber: 1, pageSize: 100, count: 1, totalPages: 1, nextPage: null } } };
        respond(200, page);

        await expect(platformV2.listPut("/supportedModels", { provider: "openai" })).resolves.toEqual(page);
        expect(fetchMock.mock.calls[0][0]).toBe("/api/v2/platform/supportedModels");
        expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: "PUT", body: JSON.stringify({ provider: "openai" }) });
    });

    test("a body that is not the envelope still rejects with the status", async () => {
        respond(502, "<html>Bad Gateway</html>");

        await expect(platformV2.get("/agents/a1")).rejects.toMatchObject({ status: 502, message: "Request failed: HTTP 502", errorId: undefined });
    });
});
