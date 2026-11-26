// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import { isAgentFixableError } from "../builderUtils";

import type { BuildManifest } from "./useBuilderManifest";
import { deployAfterSave, runPerKindSaves, type SaveAllResult, savedOrPartial, type SaveRun } from "./useSaveOrchestration";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;

function makeRun(components: Comp[], saveOne: SaveRun["saveOne"], readFails: string[] = []) {
    const run: SaveRun = {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results: new Map(),
        skip: () => false,
        attribute: () => {},
        fetchConfigByFilename: vi.fn(async (filename: string) => {
            if (readFails.includes(filename)) throw new Error("config expired");
            return "apps: []";
        }),
        saveOne,
    };
    return run;
}

const failing = (...names: string[]) =>
    vi.fn(async (_yaml: string, c: Comp) => {
        if (names.includes(c.name)) throw new Error(`${c.name} refused`);
        return { name: c.name, type: c.type, id: `id-${c.name}` };
    }) as unknown as SaveRun["saveOne"];

describe("runPerKindSaves records the components it did not write", () => {
    test.each([
        ["an agent fails, the later ones are not reached", [comp("A", "agent"), comp("B", "agent"), comp("W", "workflow")], failing("A"), [], ["A"]],
        ["a skill fails before any agent", [comp("S", "skill"), comp("A", "agent")], failing("S"), [], ["S"]],
        ["a later agent fails after one was written", [comp("A", "agent"), comp("B", "agent")], failing("B"), [], ["B"]],
        ["a config that cannot be read, then an agent that fails", [comp("O", "agent"), comp("A", "agent")], failing("A"), ["O.yaml"], ["O", "A"]],
    ] as const)("%s", async (_label, components, saveOne, readFails, wantNotWritten) => {
        const run = makeRun([...components], saveOne, [...readFails]);

        await expect(runPerKindSaves(run, [...components], new Map())).rejects.toThrow("refused");

        expect(run.notWritten).toEqual(wantNotWritten);
    });

    test("a config that cannot be read is named in the error even though a later save is the one that threw", async () => {
        const components = [comp("O", "agent"), comp("A", "agent")];
        const run = makeRun(components, failing("A"), ["O.yaml"]);

        await expect(runPerKindSaves(run, components, new Map())).rejects.toThrow(/Also failed — O: config expired/);
    });

    test("an axios-shaped 500 stays not agent-fixable after a read failure rewrapped it too", async () => {
        const components = [comp("O", "agent"), comp("Acme", "agent")];
        const run = makeRun(components, vi.fn().mockRejectedValue(Object.assign(new Error("internal error"), { response: { status: 500 } })) as unknown as SaveRun["saveOne"], ["O.yaml"]);

        const err = await runPerKindSaves(run, components, new Map()).catch(e => e);

        expect(isAgentFixableError(err)).toBe(false);
        expect(err.message).toMatch(/Also failed — O/);
    });

    test("a save with nothing failing records nothing", async () => {
        const components = [comp("A", "agent"), comp("W", "workflow")];
        const run = makeRun(components, failing());

        await runPerKindSaves(run, components, new Map());

        expect(run.notWritten).toEqual([]);
    });
});

describe("deployAfterSave carries those names into the sweep", () => {
    const rows: Array<[string, SaveAllResult, ReturnType<typeof deployAfterSave>]> = [
        ["a failure with names", savedOrPartial(true, undefined, ["A"]), { deploy: true, changedOnPlatform: undefined, options: { notWritten: ["A"] } }],
        ["a failure with none", { kind: "failed", changedOnPlatform: undefined, notWritten: [] }, { deploy: true, changedOnPlatform: undefined, options: { notWritten: [] } }],
        ["a completed save", { kind: "saved", changedOnPlatform: undefined }, { deploy: true, changedOnPlatform: undefined, options: {} }],
    ];
    test.each(rows)("%s", (_label, saved, want) => {
        expect(deployAfterSave(saved)).toEqual(want);
    });
});
