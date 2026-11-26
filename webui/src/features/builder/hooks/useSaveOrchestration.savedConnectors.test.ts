// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import type { BuildManifest } from "./useBuilderManifest";
import { saveOnlyHeldByPlatform } from "./useRecordSavedComponents";
import { runServerApply, type SaveOutcome, saveProofOf, type SaveRun } from "./useSaveOrchestration";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;
const manifest = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;

const makeRun = (components: Comp[]): SaveRun => ({
    changed: [],
    declined: [],
    notWritten: [],
    ordered: components,
    results: new Map(),
    skip: () => false,
    attribute: () => {},
    fetchConfigByFilename: vi.fn().mockResolvedValue("name: x"),
    saveOne: vi.fn().mockResolvedValue(null),
});

const op = (kind: string, name: string, action: BuildOperationResult["action"], extra: Partial<BuildOperationResult> = {}): BuildOperationResult =>
    ({ kind, name, action, status: "succeeded", resourceId: `id-${name}`, ...extra }) as BuildOperationResult;

const summary = { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 };

async function heldAfterSave(components: Comp[], operations: BuildOperationResult[]): Promise<string[]> {
    const run = makeRun(components);
    const outcome: SaveOutcome = await runServerApply(run, manifest(components), vi.fn().mockResolvedValue({ operations, summary }));
    return saveOnlyHeldByPlatform(components, saveProofOf(outcome, run.declined));
}

describe("which connectors a save leaves recorded", () => {
    const components = [comp("db", "connector"), comp("cache", "connector"), comp("tok", "connector"), comp("Agent", "agent")];

    test.each([
        ["an updated connector", [op("connector", "db", "update")], ["db"]],
        ["a created connector", [op("connector", "db", "create")], ["db"]],
        ["a connector planned unchanged", [op("connector", "cache", "unchanged")], ["cache"]],
        ["a connector whose edit was partly declined, updated", [op("connector", "tok", "update", { changesDeclined: true })], []],
        ["a connector whose edit was partly declined, unchanged", [op("connector", "tok", "unchanged", { changesDeclined: true })], []],
        ["an agent update alongside an unchanged connector", [op("agent", "Agent", "update"), op("connector", "cache", "unchanged")], ["cache"]],
        ["every connector at once, one declined", [op("connector", "db", "update"), op("connector", "cache", "unchanged"), op("connector", "tok", "update", { changesDeclined: true })], ["db", "cache"]],
    ])("%s", async (_label, operations, expected) => {
        expect(await heldAfterSave(components, operations)).toEqual(expected);
    });

    test("a failed apply throws before anything is recorded", async () => {
        const run = makeRun(components);
        const applyBuild = vi.fn().mockResolvedValue({ operations: [op("connector", "db", "update"), op("agent", "Agent", "update", { status: "failed", error: { message: "boom", errorId: "e1", httpStatus: 422 } })], summary });
        await expect(runServerApply(run, manifest(components), applyBuild)).rejects.toThrow();
    });
});
