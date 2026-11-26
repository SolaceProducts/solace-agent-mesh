// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { claimedKeys, getSectionMap } from "./sectionMaps";

describe("getSectionMap", () => {
    test("returns a non-empty section map for each known config type", () => {
        for (const type of ["agent", "workflow", "gateway", "skill", "connector"] as const) {
            const map = getSectionMap(type);
            expect(map.length).toBeGreaterThan(0);
        }
    });

    test("returns an empty map for unknown config types", () => {
        expect(getSectionMap("unknown")).toEqual([]);
    });

    test("every entry has an id, label, icon, and at least one key", () => {
        const map = getSectionMap("agent");
        for (const entry of map) {
            expect(entry.id).toBeTruthy();
            expect(entry.label).toBeTruthy();
            expect(entry.icon).toBeTruthy();
            expect(entry.keys.length).toBeGreaterThan(0);
        }
    });

    test("agent section map covers core identity fields", () => {
        const map = getSectionMap("agent");
        const keys = claimedKeys(map);
        expect(keys.has("agent_name")).toBe(true);
        expect(keys.has("description")).toBe(true);
        expect(keys.has("instruction")).toBe(true);
        expect(keys.has("agent_card")).toBe(true);
        expect(keys.has("tools")).toBe(true);
    });

    test("workflow section map includes workflow-specific fields", () => {
        const map = getSectionMap("workflow");
        const keys = claimedKeys(map);
        expect(keys.has("agent_name")).toBe(true);
        expect(map.some(e => e.keys.some(k => k === "nodes" || k === "agent_type"))).toBe(true);
    });

    test("workflow section map claims the workflow-level decision block", () => {
        const decision = getSectionMap("workflow").find(e => e.keys.includes("decision"));
        expect(decision?.label).toBe("Decision Model");
    });

    test("section ids are unique within a map", () => {
        for (const type of ["agent", "workflow", "gateway", "skill", "connector"] as const) {
            const ids = getSectionMap(type).map(e => e.id);
            expect(new Set(ids).size).toBe(ids.length);
        }
    });
});

describe("claimedKeys", () => {
    test("collects all keys across entries", () => {
        const keys = claimedKeys([
            { id: "a", label: "A", icon: (() => null) as never, keys: ["k1", "k2"] },
            { id: "b", label: "B", icon: (() => null) as never, keys: ["k3"] },
        ]);
        expect(keys).toEqual(new Set(["k1", "k2", "k3"]));
    });

    test("deduplicates across entries", () => {
        const keys = claimedKeys([
            { id: "a", label: "A", icon: (() => null) as never, keys: ["shared", "k1"] },
            { id: "b", label: "B", icon: (() => null) as never, keys: ["shared", "k2"] },
        ]);
        expect(keys).toEqual(new Set(["shared", "k1", "k2"]));
    });

    test("returns an empty set for an empty map", () => {
        expect(claimedKeys([])).toEqual(new Set());
    });
});
