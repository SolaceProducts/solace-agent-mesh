// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { type NavItemConfig } from "@/types/nav";

import { findExactNavMatchLabel, routeMatchesExactly, routeMatchesPrefix } from "./routeMatch";

describe("routeMatchesPrefix", () => {
    test.each([
        ["/agent-mesh", "/agent-mesh", true],
        ["/agent-mesh/123", "/agent-mesh", true],
        ["/agents", "/agent-mesh", false],
        ["/chat", "/agent-mesh", false],
    ])("pathname %s vs pattern %s -> %s", (pathname, pattern, expected) => {
        expect(routeMatchesPrefix(pathname, pattern)).toBe(expected);
    });

    test("supports pattern arrays and regular expressions", () => {
        expect(routeMatchesPrefix("/conversations/9", ["/chat", "/conversations"])).toBe(true);
        expect(routeMatchesPrefix("/users/123", /^\/users\/\d+$/)).toBe(true);
        expect(routeMatchesPrefix("/users", /^\/users\/\d+$/)).toBe(false);
    });

    test("returns false without a routeMatch", () => {
        expect(routeMatchesPrefix("/agent-mesh", undefined)).toBe(false);
    });
});

describe("routeMatchesExactly", () => {
    test.each([
        ["/agent-mesh", true],
        ["/agent-mesh/", true],
        ["/agent-mesh/123", false],
        ["/agent-mesh-other", false],
    ])("pathname %s vs /agents -> %s", (pathname, expected) => {
        expect(routeMatchesExactly(pathname, "/agent-mesh")).toBe(expected);
    });
});

describe("findExactNavMatchLabel", () => {
    const items: NavItemConfig[] = [
        { id: "agents", label: "Agents", routeMatch: "/agent-mesh" },
        { id: "hidden", label: "Secret", routeMatch: "/secret", hidden: true },
        {
            id: "evals",
            label: "Evaluations",
            routeMatch: "/evaluations",
            children: [
                { id: "reports", label: "Reports", routeMatch: "/evaluations" },
                { id: "lab", label: "Experiment Lab", routeMatch: "/evaluations/lab" },
                { id: "hiddenChild", label: "Hidden Child", routeMatch: "/evaluations/hidden", hidden: true },
            ],
        },
    ];

    test("returns the label of the exactly matching item", () => {
        expect(findExactNavMatchLabel(items, "/agent-mesh")).toBe("Agents");
    });

    test("returns null for detail routes and unknown paths", () => {
        expect(findExactNavMatchLabel(items, "/agent-mesh/123")).toBeNull();
        expect(findExactNavMatchLabel(items, "/nowhere")).toBeNull();
    });

    test("a child match wins over its group parent, mirroring the sidebar highlight", () => {
        expect(findExactNavMatchLabel(items, "/evaluations")).toBe("Reports");
        expect(findExactNavMatchLabel(items, "/evaluations/lab")).toBe("Experiment Lab");
    });

    test("hidden items and hidden children contribute no label", () => {
        expect(findExactNavMatchLabel(items, "/secret")).toBeNull();
        expect(findExactNavMatchLabel(items, "/evaluations/hidden")).toBeNull();
    });
});
