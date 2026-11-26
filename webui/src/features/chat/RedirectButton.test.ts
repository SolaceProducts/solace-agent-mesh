// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

const ALLOWED_PREFIXES = ["/builder", "/chat", "/prompts"];

function isRouteAllowed(route: string): boolean {
    const trimmed = (route || "").trim();
    try {
        const parsed = new URL(trimmed, "https://localhost");
        if (parsed.origin !== "https://localhost" || !ALLOWED_PREFIXES.some(p => parsed.pathname.startsWith(p))) {
            return false;
        }
        return true;
    } catch {
        return false;
    }
}

describe("RedirectButton URL validation", () => {
    describe("valid routes", () => {
        test("allows /builder", () => {
            expect(isRouteAllowed("/builder")).toBe(true);
        });

        test("allows /builder with subpath", () => {
            expect(isRouteAllowed("/builder/some-session")).toBe(true);
        });

        test("allows /chat", () => {
            expect(isRouteAllowed("/chat")).toBe(true);
        });

        test("allows /chat with subpath", () => {
            expect(isRouteAllowed("/chat/session-123")).toBe(true);
        });

        test("allows /prompts", () => {
            expect(isRouteAllowed("/prompts")).toBe(true);
        });

        test("allows routes with query parameters", () => {
            expect(isRouteAllowed("/builder?param=value")).toBe(true);
        });
    });

    describe("blocked routes", () => {
        test("blocks absolute external URL", () => {
            expect(isRouteAllowed("https://evil.com")).toBe(false);
        });

        test("blocks javascript: protocol", () => {
            expect(isRouteAllowed("javascript:alert(1)")).toBe(false);
        });

        test("blocks data: protocol", () => {
            expect(isRouteAllowed("data:text/html,<h1>hi</h1>")).toBe(false);
        });

        test("blocks routes outside allowed prefixes", () => {
            expect(isRouteAllowed("/admin")).toBe(false);
        });

        test("blocks root path", () => {
            expect(isRouteAllowed("/")).toBe(false);
        });

        test("blocks empty string", () => {
            expect(isRouteAllowed("")).toBe(false);
        });
    });

    describe("bypass attempts", () => {
        test("blocks protocol-relative URL //evil.com", () => {
            expect(isRouteAllowed("//evil.com")).toBe(false);
        });

        test("blocks backslash bypass /\\evil.com", () => {
            expect(isRouteAllowed("/\\evil.com")).toBe(false);
        });

        test("blocks URL-encoded path /%2fbuilder", () => {
            expect(isRouteAllowed("/%2f%2fevil.com")).toBe(false);
        });

        test("blocks leading whitespace bypass", () => {
            expect(isRouteAllowed("  https://evil.com")).toBe(false);
        });

        test("allows /builder.evil.com as a same-origin path (literal pathname, not a domain)", () => {
            expect(isRouteAllowed("/builder.evil.com")).toBe(true);
        });
    });
});
