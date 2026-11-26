// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "../client";

import { findCapabilityBadge, findConnectorBadge, findGatewayBadge, useCapabilities } from "./hooks";
import type { CapabilityInfo } from "./types";

const capabilities: CapabilityInfo[] = [
    { key: "event_mesh_gateway", name: "Event Mesh Gateway", description: "", status: "experimental", platform: "windows", hasEnvOverride: false },
    { key: "stable_thing", name: "Stable", description: "", status: "stable", platform: "linux", hasEnvOverride: false },
];

let getSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    getSpy = vi.spyOn(api.webui, "get");
});

afterEach(() => {
    vi.restoreAllMocks();
});

function wrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("findCapabilityBadge", () => {
    test.each([
        ["experimental", "event_mesh_gateway", "EXPERIMENTAL"],
        ["stable", "stable_thing", null],
    ])("%s capability maps to %s badge", (_status, key, expected) => {
        expect(findCapabilityBadge(capabilities, key)).toBe(expected);
    });

    test("returns EARLY ACCESS and BETA labels for early_access and beta", () => {
        const caps: CapabilityInfo[] = [
            { key: "ea", name: "", description: "", status: "early_access", platform: "linux", hasEnvOverride: false },
            { key: "b", name: "", description: "", status: "beta", platform: "linux", hasEnvOverride: false },
        ];
        expect(findCapabilityBadge(caps, "ea")).toBe("EARLY ACCESS");
        expect(findCapabilityBadge(caps, "b")).toBe("BETA");
    });

    test("returns null for unknown key or undefined list", () => {
        expect(findCapabilityBadge(capabilities, "nope")).toBeNull();
        expect(findCapabilityBadge(undefined, "event_mesh_gateway")).toBeNull();
    });
});

describe("findConnectorBadge", () => {
    const betaOnWindows: CapabilityInfo[] = [{ key: "event_mesh_connector", name: "", description: "", status: "beta", platform: "windows", hasEnvOverride: false }];
    const gaOnLinux: CapabilityInfo[] = [{ key: "event_mesh_connector", name: "", description: "", status: "stable", platform: "linux", hasEnvOverride: false }];

    test("beta capability shows BETA (Windows)", () => {
        expect(findConnectorBadge(betaOnWindows, "event_mesh", false)).toBe("BETA");
    });

    test("stable capability shows no badge (GA elsewhere)", () => {
        expect(findConnectorBadge(gaOnLinux, "event_mesh", false)).toBeNull();
    });

    test("uncatalogued type falls back to the experimental flag", () => {
        expect(findConnectorBadge(betaOnWindows, "email", true)).toBe("EXPERIMENTAL");
        expect(findConnectorBadge(betaOnWindows, "api", false)).toBeNull();
    });

    test("a subtype capability badges that subtype and not its siblings", () => {
        const mongoBeta: CapabilityInfo[] = [{ key: "document_db_mongodb_connector", name: "", description: "", status: "beta", platform: "linux", hasEnvOverride: false }];
        expect(findConnectorBadge(mongoBeta, "document_db", false, "mongodb")).toBe("BETA");
        expect(findConnectorBadge(mongoBeta, "document_db", true, "dynamodb")).toBe("EXPERIMENTAL");
    });

    test("a subtype with no capability of its own falls back to its type's capability", () => {
        const typeOnly: CapabilityInfo[] = [{ key: "sql_connector", name: "", description: "", status: "beta", platform: "linux", hasEnvOverride: false }];
        expect(findConnectorBadge(typeOnly, "sql", false, "postgres")).toBe("BETA");
    });

    test("both web_search providers show EARLY ACCESS from the web_search_connector capability", () => {
        const webSearchEarlyAccess: CapabilityInfo[] = [{ key: "web_search_connector", name: "", description: "", status: "early_access", platform: "linux", hasEnvOverride: false }];
        expect(findConnectorBadge(webSearchEarlyAccess, "web_search", false, "tavily")).toBe("EARLY ACCESS");
        expect(findConnectorBadge(webSearchEarlyAccess, "web_search", false, "exa")).toBe("EARLY ACCESS");
        expect(findConnectorBadge(webSearchEarlyAccess, "search", false, "elasticsearch")).toBeNull();
    });

    test("undefined capabilities fall back to the experimental flag", () => {
        expect(findConnectorBadge(undefined, "email", true)).toBe("EXPERIMENTAL");
        expect(findConnectorBadge(undefined, "event_mesh", false)).toBeNull();
    });
});

describe("findGatewayBadge", () => {
    const betaOnWindows: CapabilityInfo[] = [{ key: "event_mesh_gateway", name: "", description: "", status: "beta", platform: "windows", hasEnvOverride: false }];

    test("beta capability shows BETA (Windows)", () => {
        expect(findGatewayBadge(betaOnWindows, "event_mesh")).toBe("BETA");
    });

    test("a stable (GA) gateway shows no badge", () => {
        const stable: CapabilityInfo[] = [{ key: "event_mesh_gateway", name: "", description: "", status: "stable", platform: "linux", hasEnvOverride: false }];
        expect(findGatewayBadge(stable, "event_mesh")).toBeNull();
    });

    test("uncatalogued type or missing data shows no badge", () => {
        expect(findGatewayBadge(betaOnWindows, "slack")).toBeNull();
        expect(findGatewayBadge(undefined, "event_mesh")).toBeNull();
    });
});

describe("useCapabilities", () => {
    test("unwraps the paginated envelope to the data array", async () => {
        getSpy.mockResolvedValue({
            ok: true,
            json: async () => ({ data: capabilities, meta: { pagination: { pageNumber: 1, pageSize: 2, totalPages: 1, count: 2, nextPage: null } } }),
        } as Response);

        const { result } = renderHook(() => useCapabilities(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(result.current.data).toEqual(capabilities);
        expect(getSpy).toHaveBeenCalledWith("/api/v1/config/capabilities", expect.objectContaining({ fullResponse: true }));
    });
});
