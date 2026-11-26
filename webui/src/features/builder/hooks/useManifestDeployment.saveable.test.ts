// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import { hasSaveableSessionComponent } from "./useManifestDeployment";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, extra: Partial<Comp> = {}): Comp => ({ filename: `${name}.yaml`, type: "agent", name, status: "validated", origin: "session", ...extra }) as Comp;

const manifestOf = (...components: Comp[]): BuildManifest => ({ name: "b", components });

const deployed = (...names: string[]) => new Map(names.map(n => [n, "deployed"]));

describe("hasSaveableSessionComponent", () => {
    test("a session component the platform has never seen is saveable in both modes", () => {
        const m = manifestOf(comp("A"));
        expect(hasSaveableSessionComponent(m, new Map(), false)).toBe(true);
        expect(hasSaveableSessionComponent(m, new Map(), true)).toBe(true);
    });

    test("an already-deployed session component is saveable only with server-side apply", () => {
        const m = manifestOf(comp("A", { status: "deployed" }));
        expect(hasSaveableSessionComponent(m, deployed("A"), false)).toBe(false);
        expect(hasSaveableSessionComponent(m, deployed("A"), true)).toBe(true);
    });

    describe("a recorded id judges by the session's own record", () => {
        const withId = comp("A", { id: "own-id", status: "deployed" });
        const byId = (entries: Array<[string, string]>) => new Map(entries);

        test("its own record deployed: nothing to offer on the per-kind path", () => {
            expect(hasSaveableSessionComponent(manifestOf(withId), deployed("A"), false, undefined, byId([["own-id", "deployed"]]))).toBe(false);
        });

        test("its own record gone while a same-named one is deployed: the action is offered", () => {
            expect(hasSaveableSessionComponent(manifestOf(withId), deployed("A"), false, undefined, byId([["other-id", "deployed"]]))).toBe(true);
        });

        test("a legacy component with no recorded id and status deployed is still judged by name, as before", () => {
            expect(hasSaveableSessionComponent(manifestOf(comp("A", { status: "deployed" })), deployed("A"), false, undefined, byId([["other-id", "deployed"]]))).toBe(false);
        });

        test("a component with no recorded id and a status short of deployed is offered the action, whatever answers to its name", () => {
            for (const status of ["draft", "validated"]) {
                expect(hasSaveableSessionComponent(manifestOf(comp("A", { status })), deployed("A"), false, undefined, byId([["other-id", "deployed"]]))).toBe(true);
            }
        });
    });

    test("platform-origin components are never saveable — they are referenced, not authored", () => {
        const m = manifestOf(comp("A", { origin: "platform" }));
        expect(hasSaveableSessionComponent(m, new Map(), true)).toBe(false);
        expect(hasSaveableSessionComponent(m, new Map(), false)).toBe(false);
    });

    describe("skills", () => {
        const skill = (extra: Partial<Comp> = {}) => comp("notes", { type: "skill", status: "validated", deployed_version: 1, ...extra });
        const versionsAt = (n: number) => new Map([["notes.yaml", n]]);

        test.each([
            ["edited past its saved version, apply on", [skill()], 2, true, true],
            ["edited past its saved version, apply off", [skill()], 2, false, true],
            ["edited, beside a deployed agent", [comp("A", { deployed_version: 1 }), skill()], 2, true, true],
            ["unchanged since its saved version", [skill()], 1, true, false],
            ["unchanged, apply off", [skill()], 1, false, false],
            ["no saved version recorded: cannot be told from a pre-record save", [skill({ deployed_version: undefined })], 2, true, false],
            ["platform-origin", [skill({ origin: "platform" })], 2, true, false],
        ])("%s", (_label, components, version, serverApplyEnabled, expected) => {
            const status = deployed("A");
            expect(hasSaveableSessionComponent(manifestOf(...components), status, serverApplyEnabled, new Map([...versionsAt(version), ["A.yaml", 1]]))).toBe(expected);
        });

        test("a skill with no versions loaded yet offers nothing", () => {
            expect(hasSaveableSessionComponent(manifestOf(skill()), new Map(), true, new Map())).toBe(false);
        });
    });

    describe("connectors", () => {
        const connector = (extra: Partial<Comp> = {}) => comp("db", { type: "connector", status: "validated", deployed_version: 1, ...extra });
        const versionsAt = (n: number) => new Map([["db.yaml", n]]);

        test.each([
            ["edited past its saved version, alone", [connector()], new Map<string, string>(), 2, true, true],
            ["edited past its saved version, beside a deployed agent", [comp("A", { deployed_version: 1 }), connector()], deployed("A"), 2, true, true],
            ["edited, but server-side apply is off", [connector()], new Map<string, string>(), 2, false, false],
            ["unchanged since its saved version", [connector()], new Map<string, string>(), 1, true, false],
            ["no saved version recorded", [connector({ deployed_version: undefined })], new Map<string, string>(), 2, true, false],
            ["platform-origin", [connector({ origin: "platform" })], new Map<string, string>(), 2, true, false],
        ])("%s", (_label, components, status, version, serverApplyEnabled, expected) => {
            const versions = new Map([...versionsAt(version), ["A.yaml", 1]]);
            expect(hasSaveableSessionComponent(manifestOf(...components), status, serverApplyEnabled, versions)).toBe(expected);
        });

        test("a clean connector beside a clean deployed agent offers nothing", () => {
            const m = manifestOf(comp("A", { status: "deployed", deployed_version: 1 }), connector());
            expect(
                hasSaveableSessionComponent(
                    m,
                    deployed("A"),
                    true,
                    new Map([
                        ["A.yaml", 1],
                        ["db.yaml", 1],
                    ])
                )
            ).toBe(false);
        });
    });

    test("a deployed component alongside an undeployed one is saveable in both modes", () => {
        const m = manifestOf(comp("A"), comp("B"));
        expect(hasSaveableSessionComponent(m, deployed("A"), false)).toBe(true);
        expect(hasSaveableSessionComponent(m, deployed("A"), true)).toBe(true);
    });

    test("an empty or absent manifest is never saveable", () => {
        expect(hasSaveableSessionComponent(undefined, new Map(), true)).toBe(false);
        expect(hasSaveableSessionComponent(manifestOf(), new Map(), true)).toBe(false);
    });

    test("legacy mode resolves deployment status through the normalized name", () => {
        const m = manifestOf(comp("My Agent", { status: "deployed" }));
        expect(hasSaveableSessionComponent(m, new Map([["myagent", "deployed"]]), false)).toBe(false);
    });
});
