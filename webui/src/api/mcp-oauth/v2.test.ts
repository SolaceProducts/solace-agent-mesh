// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { discoverMCPToolsV2, discoverMCPToolsWithAuthV2, getMCPOAuthStatusV2, initiateMCPOAuthV2 } from "./v2";

const tool = (name: string, overrides: Record<string, unknown> = {}) => ({ name, description: `${name} tool`, inputSchema: { type: "object" }, outputSchema: null, ...overrides });
const page = (data: unknown[], { pageNumber = 1, count = data.length, nextPage = null as number | null } = {}) => ({ data, meta: { pagination: { pageNumber, count, pageSize: 100, nextPage, totalPages: 1 } } });

describe("MCP discovery and OAuth on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0] as string, method: fetchMock.mock.calls[n][1]?.method, body: fetchMock.mock.calls[n][1]?.body });
    const jsonBody = (n: number) => JSON.parse(call(n).body as string);

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    describe("discoverMCPToolsWithAuthV2", () => {
        test("reads every page, not just the first", async () => {
            respond(200, page([tool("a"), tool("b")], { count: 3, nextPage: 2 }));
            respond(200, page([tool("c")], { pageNumber: 2, count: 3 }));

            const got = await discoverMCPToolsWithAuthV2("https://mcp.example.com/mcp", "streamable-http");

            expect(fetchMock).toHaveBeenCalledTimes(2);
            expect(call(1).url).toContain("/api/v2/platform/connectors/mcp/discover?pageNumber=2&pageSize=100");
            expect(got.tools.map(t => t.name)).toEqual(["a", "b", "c"]);
        });

        test("hands the screens the keys they read and drops null members", async () => {
            respond(200, page([tool("search_orders", { description: "Find orders" }), tool("typed", { outputSchema: { type: "string" } })]));

            const { tools } = await discoverMCPToolsWithAuthV2("https://mcp.example.com/mcp", "sse");

            expect(tools[0]).toEqual({ name: "search_orders", description: "Find orders", inputSchema: { type: "object" } });
            expect("outputSchema" in tools[0]).toBe(false);
            expect(tools[1]).toMatchObject({ outputSchema: { type: "string" } });
        });

        test("keeps a tool's hints so the saved manifest carries them, and drops them when the server declared none", async () => {
            const hints = { readOnlyHint: true, destructiveHint: null, openWorldHint: false };
            respond(200, page([tool("lookup", { annotations: hints }), tool("create", { annotations: null })]));

            const { tools } = await discoverMCPToolsWithAuthV2("https://mcp.example.com/mcp", "sse");

            expect(tools[0].annotations).toEqual(hints);
            expect("annotations" in tools[1]).toBe(false);
        });

        test("sends the snake_case connection type, the apiKey block and the custom headers", async () => {
            respond(200, page([]));

            await discoverMCPToolsWithAuthV2("https://mcp.example.com/mcp", "streamable-http", { type: "apikey", apikey: { in: "header", name: "X-Key", value: "v" } }, { "X-Tenant": "acme" });

            expect(call(0).method).toBe("PUT");
            expect(jsonBody(0)).toEqual({
                serverUrl: "https://mcp.example.com/mcp",
                connectionType: "streamable_http",
                auth: { type: "apikey", apiKey: { in: "header", name: "X-Key", value: "v" } },
                customHeaders: { "X-Tenant": "acme" },
            });
        });

        test("leaves auth and customHeaders out when there are none", async () => {
            respond(200, page([]));
            await discoverMCPToolsWithAuthV2("https://mcp.example.com/mcp", "sse", undefined, null);
            expect(jsonBody(0)).toEqual({ serverUrl: "https://mcp.example.com/mcp", connectionType: "sse" });
        });

        test("rejects with the server's message", async () => {
            respond(502, { message: "Unable to list the MCP server's tools" });
            await expect(discoverMCPToolsWithAuthV2("https://mcp.example.com/mcp", "sse")).rejects.toThrow("Unable to list the MCP server's tools");
        });
    });

    describe("discoverMCPToolsV2", () => {
        test("maps the tools and drops customHeaders, which v2 no longer takes", async () => {
            respond(200, page([tool("search_orders")]));

            const got = await discoverMCPToolsV2({ sessionId: "s1", serverUrl: "https://mcp.example.com/mcp", connectionType: "streamable-http", customHeaders: { "X-Tenant": "acme" } });

            expect(call(0).url).toContain("/api/v2/platform/connectors/mcp/oauth/discoverWithToken?pageNumber=1&pageSize=100");
            expect(jsonBody(0)).toEqual({ sessionId: "s1", serverUrl: "https://mcp.example.com/mcp", connectionType: "streamable_http" });
            expect(got).toEqual({ tools: [{ name: "search_orders", description: "search_orders tool", inputSchema: { type: "object" } }] });
        });

        test("keeps a tool's hints after an OAuth sign-in too", async () => {
            const hints = { readOnlyHint: true, destructiveHint: false, openWorldHint: false };
            respond(200, page([tool("lookup", { annotations: hints })]));

            const got = await discoverMCPToolsV2({ sessionId: "s1", serverUrl: "https://mcp.example.com/mcp", connectionType: "sse" });

            expect(got.tools[0].annotations).toEqual(hints);
        });

        test("fails loudly rather than truncate, since the authorization is spent by the first request", async () => {
            respond(200, page([tool("a")], { count: 150, nextPage: 2 }));

            await expect(discoverMCPToolsV2({ sessionId: "s1", serverUrl: "https://mcp.example.com/mcp", connectionType: "sse" })).rejects.toThrow("150 tools");
            expect(fetchMock).toHaveBeenCalledTimes(1);
        });
    });

    test("initiateMCPOAuthV2 sends the snake_case connection type and returns the session", async () => {
        respond(200, { data: { authorizationUrl: "https://auth.example.com/a", state: "st", sessionId: "s1" } });

        const got = await initiateMCPOAuthV2({ serverUrl: "https://mcp.example.com/mcp", connectionType: "streamable-http", mode: "discovery" });

        expect(call(0)).toMatchObject({ method: "PUT", url: expect.stringContaining("/api/v2/platform/connectors/mcp/oauth/initiate") });
        expect(jsonBody(0).connectionType).toBe("streamable_http");
        expect(got).toEqual({ authorizationUrl: "https://auth.example.com/a", state: "st", sessionId: "s1" });
    });

    test("getMCPOAuthStatusV2 encodes the session id", async () => {
        respond(200, { data: { status: "pending" } });

        expect(await getMCPOAuthStatusV2("a b")).toEqual({ status: "pending" });
        expect(call(0).url).toContain("/api/v2/platform/connectors/mcp/oauth/status?sessionId=a%20b");
    });
});
