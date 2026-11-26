// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { WrittenRecord } from "./builderOwnership";

describe("builderOwnership", () => {
    let ownership: typeof import("./builderOwnership");
    const STORAGE_KEY = "sam_builder_written_resources";

    beforeEach(async () => {
        vi.resetModules();
        localStorage.clear();
        ownership = await import("./builderOwnership");
    });

    afterEach(() => {
        vi.restoreAllMocks();
        localStorage.clear();
    });

    function withStorageThrowing(run: () => void) {
        const original = Object.getOwnPropertyDescriptor(window, "localStorage")!;
        const denied = () => {
            throw new Error("denied");
        };
        Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: denied, setItem: denied } });
        try {
            run();
        } finally {
            Object.defineProperty(window, "localStorage", original);
        }
    }

    const storedIds = (session: string): string[] => (JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}")[session] ?? []).map((entry: string | { id: string }) => (typeof entry === "string" ? entry : entry.id));

    test("remembers the resources a session wrote, with their kind and stamp", () => {
        ownership.rememberWrittenResources("s1", [{ id: "id-A", kind: "connector", stamp: "t1" }]);
        expect([...ownership.writtenRecords("s1")]).toEqual([["id-A", { kind: "connector", stamp: "t1" }]]);
    });

    test("keeps sessions apart, on read and on write", () => {
        ownership.rememberWrittenResources("s1", [{ id: "id-A", kind: "agent" }]);
        ownership.rememberWrittenResources("s2", [{ id: "id-B", kind: "agent" }]);

        expect([...ownership.writtenRecords("s1").keys()]).toEqual(["id-A"]);
        expect([...ownership.writtenRecords("s2").keys()]).toEqual(["id-B"]);
        expect(ownership.writtenRecords("s3").size).toBe(0);
    });

    test("survives a reload, and a later write merges with what was stored", async () => {
        ownership.rememberWrittenResources("s1", [{ id: "id-A", kind: "agent" }]);

        vi.resetModules();
        const reloaded = await import("./builderOwnership");
        reloaded.rememberWrittenResources("s1", [{ id: "id-B", kind: "agent" }]);

        expect([...reloaded.writtenRecords("s1").keys()].sort()).toEqual(["id-A", "id-B"]);
        expect(storedIds("s1").sort()).toEqual(["id-A", "id-B"]);
    });

    test("does not list an id twice, and a later write of it replaces the stamp", () => {
        ownership.rememberWrittenResources("s1", [{ id: "id-A", kind: "connector", stamp: "old" }]);
        ownership.rememberWrittenResources("s1", [
            { id: "id-A", kind: "connector", stamp: "new" },
            { id: "id-B", kind: "agent" },
        ]);

        expect(storedIds("s1")).toEqual(["id-A", "id-B"]);
        expect(ownership.writtenRecords("s1").get("id-A")).toEqual({ kind: "connector", stamp: "new" });
    });

    test("reads a record stored before kinds and stamps were kept as an id with neither", () => {
        localStorage.setItem(STORAGE_KEY, JSON.stringify({ s1: ["id-old"] }));

        expect(ownership.writtenRecords("s1").get("id-old")).toEqual({});

        ownership.rememberWrittenResources("s1", [{ id: "id-new", kind: "skill", stamp: "h" }]);
        expect([...ownership.writtenRecords("s1").keys()].sort()).toEqual(["id-new", "id-old"]);
    });

    test("forgets the oldest sessions past the cap, not the newest", () => {
        for (let i = 0; i < 55; i++) ownership.rememberWrittenResources(`s${i}`, [{ id: `id-${i}`, kind: "agent" }]);

        const stored = Object.keys(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}"));
        expect(stored).toHaveLength(50);
        expect(stored).toContain("s54");
        expect(stored).not.toContain("s0");
    });

    test("a session written again counts as the most recent, so it outlives older ones", () => {
        for (let i = 0; i < 50; i++) ownership.rememberWrittenResources(`s${i}`, [{ id: `id-${i}`, kind: "agent" }]);
        ownership.rememberWrittenResources("s0", [{ id: "id-more", kind: "agent" }]);
        ownership.rememberWrittenResources("s50", [{ id: "id-50", kind: "agent" }]);

        const stored = Object.keys(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}"));
        expect(stored).toContain("s0");
        expect(stored).not.toContain("s1");
    });

    test("still remembers within the page when storage is unavailable", () => {
        withStorageThrowing(() => {
            ownership.rememberWrittenResources("s1", [{ id: "id-A", kind: "connector", stamp: "t1" }]);

            expect(ownership.writtenRecords("s1").get("id-A")).toEqual({ kind: "connector", stamp: "t1" });
        });
    });

    test.each([
        ["not json", "{not json"],
        ["an array", "[1,2]"],
        ["null", "null"],
        ["a string", '"x"'],
        ["a session that is a string", '{"s1":"id-A"}'],
        ["a session that is an object", '{"s1":{"x":"id-A"}}'],
        ["a session that is a number", '{"s1":7}'],
        ["a session whose entries are neither ids nor records", '{"s1":[7,null,{"kind":"agent"}]}'],
    ])("reads a record that is %s as empty rather than throwing", (_label, raw) => {
        localStorage.setItem(STORAGE_KEY, raw);

        expect(ownership.writtenRecords("s1").size).toBe(0);
        expect(() => ownership.rememberWrittenResources("s1", [{ id: "id-A", kind: "agent" }])).not.toThrow();
        expect(ownership.writtenRecords("s1").has("id-A")).toBe(true);
    });

    test("writing nothing does not touch storage", () => {
        ownership.rememberWrittenResources("s1", []);
        expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
    });

    describe("ownsResource", () => {
        const connector: WrittenRecord = { kind: "connector", stamp: "t1" };
        const skill: WrittenRecord = { kind: "skill", stamp: "h1" };
        const legacy: WrittenRecord = {};

        test.each<[string, WrittenRecord | undefined, string, string | undefined, boolean]>([
            ["nothing was written", undefined, "agent", undefined, false],
            ["nothing was written, whatever the platform holds", undefined, "connector", "t1", false],
            ["an agent the session wrote", { kind: "agent" }, "agent", undefined, true],
            ["an agent from a record that predates kinds", legacy, "agent", undefined, true],
            ["a workflow, gateway or remote agent is told by id alone", { kind: "workflow" }, "workflow", "anything", true],
            ["a connector the platform still holds as written", connector, "connector", "t1", true],
            ["a connector the platform now holds differently", connector, "connector", "t2", false],
            ["a connector the platform copy of which cannot be read", connector, "connector", undefined, false],
            ["a connector written without a stamp", { kind: "connector" }, "connector", undefined, false],
            ["a connector from a record that predates kinds", legacy, "connector", "t1", false],
            ["a skill the platform still holds as written", skill, "skill", "h1", true],
            ["a skill the platform now holds differently", skill, "skill", "h2", false],
            ["a skill whose platform copy cannot be read", skill, "skill", undefined, false],
            ["a skill from a record that predates kinds", legacy, "skill", "h1", false],
        ])("%s", (_label, record, kind, platformStamp, expected) => {
            expect(ownership.ownsResource(record, kind, platformStamp)).toBe(expected);
        });
    });
});
