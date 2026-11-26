// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import { buildEndpointUrl, formatTeamsWebhookUrl, resolveGatewayBaseUrl } from "./teams";

describe("resolveGatewayBaseUrl", () => {
    test("prepends window.location.origin to path-only URLs", () => {
        expect(resolveGatewayBaseUrl("/gw/abc")).toBe(`${window.location.origin}/gw/abc`);
    });

    test("returns absolute URLs unchanged", () => {
        expect(resolveGatewayBaseUrl("https://gw-abc.example.com")).toBe("https://gw-abc.example.com");
    });

    test("returns absolute URLs with a path segment unchanged", () => {
        expect(resolveGatewayBaseUrl("https://gw-abc.example.com/extra")).toBe("https://gw-abc.example.com/extra");
    });
});

describe("resolveGatewayBaseUrl dev-mode port substitution", () => {
    afterEach(() => {
        vi.unstubAllGlobals();
    });

    test("substitutes dev server port with backend port when they differ", () => {
        vi.stubGlobal("__SAM_BACKEND_PORT__", "8000");
        vi.stubGlobal("location", { ...window.location, origin: "http://localhost:3000" });

        expect(resolveGatewayBaseUrl("/gw/abc")).toBe("http://localhost:8000/gw/abc");
    });

    test("no substitution when ports already match", () => {
        vi.stubGlobal("__SAM_BACKEND_PORT__", "8000");
        vi.stubGlobal("location", { ...window.location, origin: "http://localhost:8000" });

        expect(resolveGatewayBaseUrl("/gw/abc")).toBe("http://localhost:8000/gw/abc");
    });

    test("no substitution when __SAM_BACKEND_PORT__ is empty", () => {
        vi.stubGlobal("__SAM_BACKEND_PORT__", "");

        expect(resolveGatewayBaseUrl("/gw/abc")).toBe(`${window.location.origin}/gw/abc`);
    });

    test("no substitution when __SAM_BACKEND_PORT__ is undefined", () => {
        vi.stubGlobal("__SAM_BACKEND_PORT__", undefined);

        expect(resolveGatewayBaseUrl("/gw/abc")).toBe(`${window.location.origin}/gw/abc`);
    });
});

describe("buildEndpointUrl", () => {
    test("preserves the /gw/{id} prefix when appending an API path to a path-only base", () => {
        expect(buildEndpointUrl("/gw/abc", "/api/messages")).toBe(`${window.location.origin}/gw/abc/api/messages`);
    });

    test("preserves the gateway path segment of an absolute base URL", () => {
        expect(buildEndpointUrl("https://gw-abc.example.com/gw/abc", "/health")).toBe("https://gw-abc.example.com/gw/abc/health");
    });

    test("normalizes a missing leading slash on the path", () => {
        expect(buildEndpointUrl("/gw/abc", "health")).toBe(`${window.location.origin}/gw/abc/health`);
    });

    test("trims a trailing slash from the base URL before concatenating", () => {
        expect(buildEndpointUrl("/gw/abc/", "/api/messages")).toBe(`${window.location.origin}/gw/abc/api/messages`);
    });
});

describe("formatTeamsWebhookUrl", () => {
    test("resolves a path-only base URL against the UI origin", () => {
        expect(formatTeamsWebhookUrl("/gw/abc")).toBe(`${window.location.origin}/gw/abc/api/messages`);
    });

    test("passes an absolute base URL through unchanged", () => {
        expect(formatTeamsWebhookUrl("https://gw-abc.example.com")).toBe("https://gw-abc.example.com/api/messages");
    });
});
