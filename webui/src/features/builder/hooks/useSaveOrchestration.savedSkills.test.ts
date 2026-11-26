// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import { withSavedVersions } from "../componentDirtyState";

import type { BuildManifest } from "./useBuilderManifest";
import { saveOnlyHeldByPlatform } from "./useRecordSavedComponents";
import { type OverwriteGuard, runPerKindSaves, runServerApply, type SavedVersions, savedVersionsIn, saveProofOf, type SaveResult, type SaveRun, withSavedAt } from "./useSaveOrchestration";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;
const manifest = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;

const components = [comp("notes", "skill"), comp("other", "skill")];

const skill = (name: string, action: SaveResult["action"], extra: Partial<SaveResult> = {}): SaveResult => ({ name, type: "skill", id: `id-${name}`, action, ...extra });

const guardAnswering = (confirm: OverwriteGuard["confirm"]): OverwriteGuard => ({ owns: () => false, wrote: () => false, remember: vi.fn().mockResolvedValue(undefined), confirm });

const runSaving = (results: Record<string, SaveResult | Error>, ordered: Comp[] = components): SaveRun => ({
    changed: [],
    declined: [],
    notWritten: [],
    ordered,
    results: new Map(),
    skip: () => false,
    attribute: () => {},
    fetchConfigByFilename: vi.fn().mockResolvedValue("name: x"),
    saveOne: vi.fn(async (_yaml: string, c: Comp) => {
        const result = results[c.name];
        if (result instanceof Error) throw result;
        return result ?? null;
    }),
});

describe("which skills a save leaves recorded", () => {
    test.each([
        ["a skill created", { notes: skill("notes", "created") }, { changed: ["notes"], matched: [] }],
        ["a skill replaced in place", { notes: skill("notes", "updated") }, { changed: ["notes"], matched: [] }],
        ["a skill found identical", { notes: skill("notes", "unchanged") }, { changed: [], matched: ["notes"] }],
        ["one of each", { notes: skill("notes", "created"), other: skill("other", "unchanged") }, { changed: ["notes"], matched: ["other"] }],
        ["a skill that failed beside one that landed", { notes: skill("notes", "created"), other: new Error("upload refused") }, { changed: ["notes"], matched: [] }],
    ])("server-side apply, %s", async (_label, results, expected) => {
        const run = runSaving(results);

        const outcome = await runServerApply(run, manifest(components), vi.fn(), guardAnswering(vi.fn().mockResolvedValue(undefined)));

        expect(outcome.heldByPlatform).toEqual(expected);
        expect(saveOnlyHeldByPlatform(components, saveProofOf(outcome, run.declined))).toEqual(components.map(c => c.name).filter(name => [...expected.changed, ...expected.matched].includes(name)));
    });

    test("server-side apply, a replacement is recorded once its upload has landed", async () => {
        const commit = vi.fn().mockResolvedValue(undefined);
        const run = runSaving({ notes: skill("notes", undefined, { commit }) });

        const outcome = await runServerApply(run, manifest(components), vi.fn(), guardAnswering(vi.fn().mockResolvedValue(undefined)));

        expect(commit).toHaveBeenCalledTimes(1);
        expect(outcome.heldByPlatform).toEqual({ changed: ["notes"], matched: [] });
    });

    test("server-side apply, a replacement whose upload fails is not recorded", async () => {
        const run = runSaving({ notes: skill("notes", undefined, { commit: vi.fn().mockRejectedValue(new Error("upload refused")) }) });

        const outcome = await runServerApply(run, manifest(components), vi.fn(), guardAnswering(vi.fn().mockResolvedValue(undefined)));

        expect(outcome.heldByPlatform).toEqual({ changed: [], matched: [] });
        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["notes"]);
    });

    test("server-side apply, a replacement the user declines ends the save with nothing uploaded and nothing to record", async () => {
        const commit = vi.fn();
        const run = runSaving({ notes: skill("notes", undefined, { commit }) });

        await expect(runServerApply(run, manifest(components), vi.fn(), guardAnswering(vi.fn().mockRejectedValue(new Error("User cancelled"))))).rejects.toThrow("User cancelled");

        expect(commit).not.toHaveBeenCalled();
    });

    test.each([
        ["a skill created", { notes: skill("notes", "created") }, { changed: ["notes"], matched: [] }],
        ["a skill replaced in place", { notes: skill("notes", "updated") }, { changed: ["notes"], matched: [] }],
        ["a skill found identical", { notes: skill("notes", "unchanged") }, { changed: [], matched: ["notes"] }],
    ])("the per-kind path, %s", async (_label, results, expected) => {
        const outcome = await runPerKindSaves(runSaving(results), components, new Map());

        expect(outcome.heldByPlatform).toEqual(expected);
    });

    test("the per-kind path does not read an agent's save as a skill's", async () => {
        const agent = comp("Agent", "agent");
        const run = { ...runSaving({}, [agent]), saveOne: vi.fn().mockResolvedValue({ name: "Agent", type: "agent", id: "id-Agent" } satisfies SaveResult) };

        const outcome = await runPerKindSaves(run, [agent], new Map());

        expect(outcome.heldByPlatform).toEqual({ changed: [], matched: [] });
    });
});

describe("the versions a mount saved, kept per session", () => {
    const saved = (sessionId: string, ...entries: Array<[string, number]>): SavedVersions => ({ sessionId, byFile: new Map(entries) });

    test.each([
        ["the session they were saved in", "s1", [["notes", 2]]],
        ["another session that happens to name a component the same", "s2", []],
    ])("are read for %s", (_label, sessionId, expected) => {
        expect([...savedVersionsIn(saved("s1", ["notes", 2]), sessionId)]).toEqual(expected);
    });

    test("a save in the same session adds to what was saved there", () => {
        const next = withSavedAt(saved("s1", ["notes", 2]), "s1", new Map([["db", 5]]));

        expect(next.sessionId).toBe("s1");
        expect([...next.byFile]).toEqual([
            ["notes", 2],
            ["db", 5],
        ]);
    });

    test("a later save of the same name replaces its version", () => {
        expect([...withSavedAt(saved("s1", ["notes", 2]), "s1", new Map([["notes", 4]])).byFile]).toEqual([["notes", 4]]);
    });

    test("a save in another session starts over: what was saved elsewhere is dropped", () => {
        const next = withSavedAt(saved("s1", ["notes", 2]), "s2", new Map([["db", 5]]));

        expect(next.sessionId).toBe("s2");
        expect([...next.byFile]).toEqual([["db", 5]]);
    });
});

describe("the version this mount saved, laid over the manifest's own record", () => {
    const manifestComp = (name: string, filename: string, deployed_version?: number) => ({ name, filename, type: "connector", status: "validated", origin: "session", deployed_version }) as Comp;
    const versionsOf = (components: ReturnType<typeof withSavedVersions>) => components?.map(c => c.deployed_version);

    test.each([
        ["the manifest has no record", manifestComp("db", "db.json"), new Map([["db.json", 2]]), 2],
        ["the mount saved a later version than the manifest records", manifestComp("db", "db.json", 1), new Map([["db.json", 3]]), 3],
        ["the manifest records a later number than this mount saved: that bare number may belong to another file, so this mount's own record wins", manifestComp("db", "db.json", 4), new Map([["db.json", 3]]), 3],
        ["version 0 is a real version", manifestComp("db", "db.json"), new Map([["db.json", 0]]), 0],
        ["the mount saved nothing of this file", manifestComp("db", "db.json", 1), new Map(), 1],
        ["the mount saved another file under the same name", manifestComp("db", "db_v2.json", 1), new Map([["db.json", 5]]), 1],
    ])("%s", (_label, component, saved, expected) => {
        expect(versionsOf(withSavedVersions([component], saved))).toEqual([expected]);
    });

    test("an absent manifest has no components to lay it over", () => {
        expect(withSavedVersions(undefined, new Map([["db.json", 1]]))).toBeUndefined();
    });
});
