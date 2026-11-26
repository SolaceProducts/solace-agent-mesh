// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { toRoutePattern } from "./routePattern";

describe("toRoutePattern", () => {
    test("returns the pathname unchanged when there are no params", () => {
        expect(toRoutePattern("/agent-mesh", {})).toBe("/agent-mesh");
    });

    test("replaces a resource id with its :name placeholder", () => {
        expect(toRoutePattern("/agent-mesh/3f2a-uuid", { id: "3f2a-uuid" })).toBe("/agent-mesh/:id");
    });

    test("redacts a share token so it never reaches analytics", () => {
        expect(toRoutePattern("/share/s3cr3tToken", { shareId: "s3cr3tToken" })).toBe("/share/:shareId");
    });

    test("replaces multiple params in a single path", () => {
        const path = "/connectors/abc/tools/mcp/lookup";
        expect(toRoutePattern(path, { id: "abc", toolName: "lookup" })).toBe("/connectors/:id/tools/mcp/:toolName");
    });

    test("matches the URL-encoded form of a param value", () => {
        expect(toRoutePattern("/eval/a%20b", { exampleKey: "a b" })).toBe("/eval/:exampleKey");
    });

    test("redacts a multi-segment (splat) param value", () => {
        expect(toRoutePattern("/eval/foo/bar", { "*": "foo/bar" })).toBe("/eval/:*");
    });

    test("ignores empty param values", () => {
        expect(toRoutePattern("/agent-mesh", { id: undefined })).toBe("/agent-mesh");
    });

    test("replaces only the trailing occurrence of a splat value, not an earlier coincidental match", () => {
        expect(toRoutePattern("/docs/guide/docs/guide", { "*": "docs/guide" })).toBe("/docs/guide/:*");
    });

    test("does not rewrite a single segment that merely matches a splat value substring", () => {
        expect(toRoutePattern("/files/a/b/c", { "*": "b/c" })).toBe("/files/a/:*");
    });

    test("leaves the path unchanged when a splat value is absent from the pathname", () => {
        expect(toRoutePattern("/agent-mesh", { "*": "not/here" })).toBe("/agent-mesh");
    });

    test("over-redacts a static segment that coincidentally equals a param value (safe direction)", () => {
        expect(toRoutePattern("/agent-mesh/agent-mesh", { id: "agent-mesh" })).toBe("/:id/:id");
    });

    test("fails closed: redacts a UUID segment even when params omit it", () => {
        expect(toRoutePattern("/agent-mesh/550e8400-e29b-41d4-a716-446655440000", { id: undefined })).toBe("/agent-mesh/:id");
    });

    test("fails closed: redacts a long opaque token segment even with no params", () => {
        expect(toRoutePattern("/share/AbCdEf0123456789xyzQwErTy", {})).toBe("/share/:id");
    });

    test("keeps short static segments that are not id-shaped", () => {
        expect(toRoutePattern("/agents/settings", {})).toBe("/agents/settings");
    });
});
