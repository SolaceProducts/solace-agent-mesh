// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import { recordSavedVersions, saveOnlyHeldByPlatform, type SaveProof } from "./useRecordSavedComponents";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string, extra: Partial<Comp> = {}): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session", ...extra }) as Comp;

const proof = (extra: Partial<SaveProof> = {}): SaveProof => ({ changed: [], matched: [], declined: [], ...extra });

describe("saveOnlyHeldByPlatform", () => {
    const components = [
        comp("db", "connector"),
        comp("cache", "connector"),
        comp("Agent", "agent"),
        comp("ext", "connector", { origin: "platform" }),
        comp("draft", "connector", { filename: "" }),
        comp("pet_store", "connector"),
        comp("notes", "skill"),
        comp("ext_skill", "skill", { origin: "platform" }),
        comp("draft_skill", "skill", { filename: "" }),
    ];

    test.each([
        ["a connector the save wrote", proof({ changed: ["db"] }), ["db"]],
        ["a connector the plan proved identical", proof({ matched: ["cache"] }), ["cache"]],
        ["both kinds in one save", proof({ changed: ["db"], matched: ["cache"] }), ["db", "cache"]],
        ["a connector the save wrote but declined part of", proof({ changed: ["db"], declined: ["db"] }), []],
        ["a declined connector beside a clean one", proof({ changed: ["db", "cache"], declined: ["db"] }), ["cache"]],
        ["an agent the save wrote is neither a connector nor a skill", proof({ changed: ["Agent"] }), []],
        ["a skill the save wrote", proof({ changed: ["notes"] }), ["notes"]],
        ["a skill the lookup found identical", proof({ matched: ["notes"] }), ["notes"]],
        ["a skill and a connector in one save", proof({ changed: ["notes"], matched: ["db"] }), ["db", "notes"]],
        ["a skill the save wrote but declined part of", proof({ changed: ["notes"], declined: ["notes"] }), []],
        ["a platform-origin skill", proof({ changed: ["ext_skill"] }), []],
        ["a skill with no config artifact", proof({ changed: ["draft_skill"] }), []],
        ["a platform-origin connector", proof({ changed: ["ext"] }), []],
        ["a connector with no config artifact", proof({ changed: ["draft"] }), []],
        ["a save that changed and matched nothing", proof(), []],
        ["a name spelled differently from the manifest's", proof({ changed: ["PetStore"] }), ["pet_store"]],
        ["a declined name spelled differently from the manifest's", proof({ changed: ["pet_store"], declined: ["PetStore"] }), []],
    ])("%s", (_label, p, expected) => {
        expect(saveOnlyHeldByPlatform(components, p)).toEqual(expected);
    });

    test("an absent manifest holds nothing", () => {
        expect(saveOnlyHeldByPlatform(undefined, proof({ changed: ["db"] }))).toEqual([]);
    });
});

describe("recordSavedVersions", () => {
    const manifest = (...components: Comp[]): BuildManifest => ({ name: "b", components });
    const apply = (names: string[], versions: [string, number][], m: BuildManifest) => recordSavedVersions(new Set(names), new Map(versions))(m).components ?? [];

    test("stamps a named skill the same way", () => {
        const [notes] = apply(["notes"], [["notes.yaml", 5]], manifest(comp("notes", "skill", { deployed_version: 2 })));
        expect(notes.deployed_version).toBe(5);
    });

    test("stamps the named connector with the version resolved for its artifact", () => {
        const [db] = apply(["db"], [["db.yaml", 3]], manifest(comp("db", "connector", { deployed_version: 1 })));
        expect(db.deployed_version).toBe(3);
    });

    test("a version of 0 is a real version", () => {
        const [db] = apply(["db"], [["db.yaml", 0]], manifest(comp("db", "connector")));
        expect(db.deployed_version).toBe(0);
    });

    test.each([
        ["a connector that was not named", ["other"], [["db.yaml", 3]] as [string, number][], comp("db", "connector", { deployed_version: 1 })],
        ["a connector whose version did not resolve", ["db"], [] as [string, number][], comp("db", "connector", { deployed_version: 1 })],
        ["an agent sharing the name", ["db"], [["db.yaml", 3]] as [string, number][], comp("db", "agent", { deployed_version: 1 })],
        ["a skill that was not named", ["other"], [["notes.yaml", 3]] as [string, number][], comp("notes", "skill", { deployed_version: 1 })],
    ])("leaves %s untouched", (_label, names, versions, original) => {
        const [out] = apply(names, versions, manifest(original));
        expect(out).toEqual(original);
    });

    test("leaves the rest of the manifest alone", () => {
        const agent = comp("Agent", "agent", { deployed_version: 4 });
        const m = { ...manifest(comp("db", "connector"), agent), full_validation_passed: true };
        const out = recordSavedVersions(new Set(["db"]), new Map([["db.yaml", 2]]))(m);
        expect(out.full_validation_passed).toBe(true);
        expect(out.components?.[1]).toEqual(agent);
    });
});
