// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Connector } from "@/types";

import { useConnectorTools } from "./useConnectorTools";

const getResourceMock = vi.fn();
vi.mock("@/features/connectors/utils/resourcesCache", () => ({
    getResource: (...args: unknown[]) => getResourceMock(...args),
}));

const op = (toolName: string) => ({
    tool_name: toolName,
    tool_description: `Describe ${toolName}`,
    topic: `acme/${toolName}`,
    input_schema: { type: "object", properties: {} },
});

const makeConnector = (operations: ReturnType<typeof op>[]): Connector => ({ id: "conn-1", name: "Conn", description: "", type: "event_mesh", subtype: "solace", values: { operations } }) as unknown as Connector;

describe("useConnectorTools — event_mesh", () => {
    test("derives one tool per operation", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: makeConnector([op("get_order"), op("create_order")]), enabled: true }));

        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.totalCount).toBe(2);
        expect(result.current.tools.map(t => t.name)).toEqual(["get_order", "create_order"]);
        expect(result.current.tools[0]).toMatchObject({ method: "EVENT_MESH", endpoint: "acme/get_order" });
    });

    test("recomputes when operations change", async () => {
        const { result, rerender } = renderHook(({ connector }) => useConnectorTools({ connector, enabled: true }), {
            initialProps: { connector: makeConnector([op("get_order")]) },
        });

        await waitFor(() => expect(result.current.tools).toHaveLength(1));

        rerender({ connector: makeConnector([op("get_order"), op("create_order")]) });

        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.tools.map(t => t.name)).toEqual(["get_order", "create_order"]);
    });
});

describe("useConnectorTools — disabled / no tools", () => {
    test("returns no tools when not enabled", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: makeConnector([op("get_order")]), enabled: false }));
        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.tools).toHaveLength(0);
        expect(result.current.totalCount).toBe(0);
    });

    test("returns no tools when the connector has none configured", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: makeConnector([]), enabled: true }));
        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.tools).toHaveLength(0);
    });
});

describe("useConnectorTools — mcp", () => {
    const mcpConnector = (manifest: unknown, selected: string): Connector => ({ id: "mcp-1", name: "MCP", description: "", type: "mcp", subtype: "http", values: { manifest, selected_tools: selected } }) as unknown as Connector;

    const manifest = [
        { name: "search", description: "Search the catalogue", inputSchema: { type: "object" } },
        { name: "ping", description: "Liveness", inputSchema: null },
    ];

    test("selects only the chosen tools and reports the full manifest total", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: mcpConnector(manifest, "search"), enabled: true }));
        await waitFor(() => expect(result.current.tools).toHaveLength(1));
        expect(result.current.tools[0]).toMatchObject({ name: "search", method: "MCP" });
        expect(result.current.totalCount).toBe(2);
    });

    test("parses a JSON-string manifest", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: mcpConnector(JSON.stringify(manifest), "search,ping"), enabled: true }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.tools.map(t => t.name)).toEqual(["search", "ping"]);
    });

    test("exposes the whole manifest when no tools are explicitly selected", async () => {
        const connector = { id: "mcp-1", name: "MCP", description: "", type: "mcp", subtype: "http", values: { manifest } } as unknown as Connector;
        const { result } = renderHook(() => useConnectorTools({ connector, enabled: true }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.tools.map(t => t.name)).toEqual(["search", "ping"]);
        expect(result.current.totalCount).toBe(2);
    });

    test("falls back to runtime tool names when the MCP connector has no manifest", async () => {
        const connector = { id: "mcp-2", name: "MCP", description: "", type: "mcp", subtype: "http", values: {} } as unknown as Connector;
        const { result } = renderHook(() => useConnectorTools({ connector, enabled: true, runtimeToolNames: ["getJiraIssue", "searchJiraIssuesUsingJql"] }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.tools.map(t => t.name)).toEqual(["getJiraIssue", "searchJiraIssuesUsingJql"]);
        expect(result.current.tools[0]).toMatchObject({ method: "MCP" });
    });
});

describe("useConnectorTools — api", () => {
    beforeEach(() => getResourceMock.mockReset());

    const apiConnector = (allowList: string): Connector => ({ id: "api-1", name: "API", description: "", type: "api", subtype: "http", values: { specification_url: "https://example.com/spec.json", allow_list: allowList } }) as unknown as Connector;

    const spec = {
        paths: {
            "/orders": { get: { operationId: "listOrders", summary: "List orders", description: "" } },
            "/orders/{id}": { get: { operationId: "getOrder", summary: "Get order", description: "" } },
        },
    };

    test("fetches the spec and filters to the allow-list", async () => {
        getResourceMock.mockResolvedValue({ status: 200, contentType: "application/json", text: JSON.stringify(spec) });
        const { result } = renderHook(() => useConnectorTools({ connector: apiConnector("listOrders"), enabled: true }));
        await waitFor(() => expect(result.current.tools).toHaveLength(1));
        expect(result.current.tools[0].operationId).toBe("listOrders");
        expect(result.current.totalCount).toBe(2);
    });

    test("lists the configured operationIds when the spec resource is absent and no agent is running", async () => {
        getResourceMock.mockResolvedValue({ status: 404, contentType: "application/json", text: "" });
        const { result } = renderHook(() => useConnectorTools({ connector: apiConnector("listOrders,getOrder"), enabled: true }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.error).toBeNull();
        expect(result.current.tools.map(t => t.operationId)).toEqual(["listOrders", "getOrder"]);
        expect(result.current.tools[0]).toMatchObject({ name: "listOrders", method: "OPENAPI" });
    });

    test("prefers runtime tool names over the allow_list when the spec resource is absent", async () => {
        getResourceMock.mockResolvedValue({ status: 404, contentType: "application/json", text: "" });
        const { result } = renderHook(() => useConnectorTools({ connector: apiConnector("listOrders"), enabled: true, runtimeToolNames: ["list_my_expenses", "whoami"] }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.error).toBeNull();
        expect(result.current.tools.map(t => t.name)).toEqual(["list_my_expenses", "whoami"]);
        expect(result.current.tools[0]).toMatchObject({ operationId: "list_my_expenses", method: "OPENAPI" });
        expect(result.current.totalCount).toBe(2);
    });

    test("warns but still lists runtime tool names when the spec fetch fails with a non-404 status", async () => {
        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        getResourceMock.mockResolvedValue({ status: 500, contentType: "application/json", text: "" });
        const { result } = renderHook(() => useConnectorTools({ connector: apiConnector("listOrders"), enabled: true, runtimeToolNames: ["whoami"] }));
        await waitFor(() => expect(result.current.tools).toHaveLength(1));
        expect(result.current.error).toBeNull();
        expect(result.current.tools[0].name).toBe("whoami");
        expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("500"));
        warnSpy.mockRestore();
    });

    test("surfaces the error on a non-404 failure when no runtime names exist, even with an allow_list set", async () => {
        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        getResourceMock.mockResolvedValue({ status: 500, contentType: "application/json", text: "" });
        const { result } = renderHook(() => useConnectorTools({ connector: apiConnector("listOrders"), enabled: true }));
        await waitFor(() => expect(result.current.error).toContain("500"));
        expect(result.current.tools).toHaveLength(0);
        warnSpy.mockRestore();
    });

    test("lists runtime tool names when no allow_list is set, since allow-all must not gate api tool loading", async () => {
        getResourceMock.mockResolvedValue({ status: 404, contentType: "application/json", text: "" });
        const connector = { id: "api-1", name: "API", description: "", type: "api", subtype: "http", values: { specification_url: "https://example.com/spec.json" } } as unknown as Connector;
        const { result } = renderHook(() => useConnectorTools({ connector, enabled: true, runtimeToolNames: ["whoami"] }));
        await waitFor(() => expect(result.current.tools).toHaveLength(1));
        expect(result.current.tools[0].name).toBe("whoami");
    });

    test("prefers the stored spec over runtime names when the resource exists", async () => {
        getResourceMock.mockResolvedValue({ status: 200, contentType: "application/json", text: JSON.stringify(spec) });
        const { result } = renderHook(() => useConnectorTools({ connector: apiConnector("listOrders"), enabled: true, runtimeToolNames: ["whoami"] }));
        await waitFor(() => expect(result.current.tools).toHaveLength(1));
        expect(result.current.tools[0].operationId).toBe("listOrders");
    });

    const urllessApiConnector = (allowList: string): Connector => ({ id: "api-2", name: "API", description: "", type: "api", subtype: "http", values: { allow_list: allowList } }) as unknown as Connector;

    test("lists the configured operationIds without fetching the resource when no specification_url is set", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: urllessApiConnector("listOrders,getOrder"), enabled: true }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.tools.map(t => t.operationId)).toEqual(["listOrders", "getOrder"]);
        expect(result.current.error).toBeNull();
        expect(getResourceMock).not.toHaveBeenCalled();
    });

    test("prefers runtime tool names over the allow_list without fetching the resource when no specification_url is set", async () => {
        const { result } = renderHook(() => useConnectorTools({ connector: urllessApiConnector("listOrders"), enabled: true, runtimeToolNames: ["whoami", "list_my_expenses"] }));
        await waitFor(() => expect(result.current.tools).toHaveLength(2));
        expect(result.current.tools.map(t => t.name)).toEqual(["whoami", "list_my_expenses"]);
        expect(result.current.error).toBeNull();
        expect(getResourceMock).not.toHaveBeenCalled();
    });
});
