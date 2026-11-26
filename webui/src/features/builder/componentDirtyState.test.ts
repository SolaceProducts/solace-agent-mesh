// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { artifactVersionMap, componentFreshness, hasUnsavedSaveOnlyEdits, isComponentDirty, mergeArtifactVersions, saveOnlyEditState, versionToRecord } from "./componentDirtyState";

const versions = artifactVersionMap([
    { filename: "image_object_detection_workflow.yaml", version: 2 },
    { filename: "build_manifest.yaml", version: 2143 },
]);

describe("artifactVersionMap", () => {
    it("keeps the highest version when a filename appears more than once", () => {
        const map = artifactVersionMap([
            { filename: "wf.yaml", version: 1 },
            { filename: "wf.yaml", version: 3 },
            { filename: "wf.yaml", version: 2 },
        ]);
        expect(map.get("wf.yaml")).toBe(3);
    });

    it("ignores entries with no filename or a non-numeric version", () => {
        const map = artifactVersionMap([{ version: 4 }, { filename: "a.yaml" }, { filename: "b.yaml", version: "3" }]);
        expect(map.size).toBe(0);
    });

    it("tolerates an absent artifact list", () => {
        expect(artifactVersionMap(undefined).size).toBe(0);
    });
});

describe("isComponentDirty", () => {
    it("reports dirty when the artifact has moved past the deployed version", () => {
        const comp = { filename: "image_object_detection_workflow.yaml", origin: "session", deployed_version: 0 };
        expect(isComponentDirty(comp, versions)).toBe(true);
    });

    it("reports clean once the deployed version has caught up", () => {
        const comp = { filename: "image_object_detection_workflow.yaml", origin: "session", deployed_version: 2 };
        expect(isComponentDirty(comp, versions)).toBe(false);
    });

    it("never reports a platform-origin component dirty", () => {
        expect(isComponentDirty({ filename: "", origin: "platform" }, versions)).toBe(false);
        expect(isComponentDirty({ filename: "x.yaml", origin: "platform", deployed_version: 0 }, versions)).toBe(false);
    });

    it("treats a manifest with no deployed_version as dirty", () => {
        const comp = { filename: "image_object_detection_workflow.yaml", origin: "session" };
        expect(isComponentDirty(comp, versions)).toBe(true);
    });

    it("treats an unloaded artifact list as dirty rather than clean", () => {
        const comp = { filename: "image_object_detection_workflow.yaml", origin: "session", deployed_version: 2 };
        expect(isComponentDirty(comp, undefined)).toBe(true);
        expect(isComponentDirty(comp, new Map())).toBe(true);
    });

    it("reports a not-yet-saved session component dirty", () => {
        expect(isComponentDirty({ origin: "session" }, versions)).toBe(true);
    });
});

describe("mergeArtifactVersions", () => {
    it("supplies a version the session list omitted entirely", () => {
        const listed = artifactVersionMap([{ filename: "build_manifest.yaml", version: 7 }]);
        const merged = mergeArtifactVersions(listed, new Map([["wf.yaml", 3]]));
        expect(merged.get("wf.yaml")).toBe(3);
        expect(merged.get("build_manifest.yaml")).toBe(7);
    });

    it("keeps the higher version when both sources know the filename", () => {
        const listed = artifactVersionMap([{ filename: "wf.yaml", version: 5 }]);
        expect(mergeArtifactVersions(listed, new Map([["wf.yaml", 2]])).get("wf.yaml")).toBe(5);
        expect(mergeArtifactVersions(listed, new Map([["wf.yaml", 9]])).get("wf.yaml")).toBe(9);
    });

    it("returns the base untouched when the overlay is empty", () => {
        const listed = artifactVersionMap([{ filename: "wf.yaml", version: 1 }]);
        expect(mergeArtifactVersions(listed, new Map())).toBe(listed);
    });

    it("carries a version 0 overlay onto a filename the list never had, since 0 is a real first deploy and dropping it leaves the component dirty forever", () => {
        const merged = mergeArtifactVersions(new Map(), new Map([["wf.yaml", 0]]));
        expect(merged.get("wf.yaml")).toBe(0);
        expect(versionToRecord({ filename: "wf.yaml", origin: "session" }, merged)).toBe(0);
    });

    it("makes an otherwise-unknowable component answerable", () => {
        const comp = { filename: "wf.yaml", origin: "session", deployed_version: 3 };
        expect(isComponentDirty(comp, new Map())).toBe(true);
        expect(isComponentDirty(comp, mergeArtifactVersions(new Map(), new Map([["wf.yaml", 3]])))).toBe(false);
        expect(isComponentDirty(comp, mergeArtifactVersions(new Map(), new Map([["wf.yaml", 4]])))).toBe(true);
    });
});

describe("versionToRecord", () => {
    it("returns the current artifact version for a session component", () => {
        expect(versionToRecord({ filename: "image_object_detection_workflow.yaml", origin: "session" }, versions)).toBe(2);
    });

    it("returns undefined when there is nothing to record", () => {
        expect(versionToRecord({ filename: "", origin: "platform" }, versions)).toBeUndefined();
        expect(versionToRecord({ filename: "unknown.yaml", origin: "session" }, versions)).toBeUndefined();
    });

    it("records a genuine version 0 rather than dropping it", () => {
        const v0 = artifactVersionMap([{ filename: "wf.yaml", version: 0 }]);
        expect(versionToRecord({ filename: "wf.yaml", origin: "session" }, v0)).toBe(0);
    });
});

describe("componentFreshness", () => {
    it("is clean for a platform-origin component", () => {
        expect(componentFreshness({ filename: "", origin: "platform" }, new Map())).toBe("clean");
    });

    it("is stale for a session component that was never saved", () => {
        expect(componentFreshness({ origin: "session" }, new Map())).toBe("stale");
    });

    it("is unknown with no deployed_version recorded", () => {
        expect(componentFreshness({ filename: "wf.yaml", origin: "session" }, new Map([["wf.yaml", 2]]))).toBe("unknown");
    });

    it("is unknown when the versions map has no entry yet", () => {
        expect(componentFreshness({ filename: "wf.yaml", origin: "session", deployed_version: 0 }, new Map())).toBe("unknown");
    });

    it("is stale when the artifact has moved past the deployed version", () => {
        expect(componentFreshness({ filename: "wf.yaml", origin: "session", deployed_version: 0 }, new Map([["wf.yaml", 2]]))).toBe("stale");
    });

    it("is clean when the versions match", () => {
        expect(componentFreshness({ filename: "wf.yaml", origin: "session", deployed_version: 2 }, new Map([["wf.yaml", 2]]))).toBe("clean");
    });
});

describe("hasUnsavedSaveOnlyEdits", () => {
    const versionsAt = (n: number) => new Map([["db.yaml", n]]);
    const component = (type: string, extra: Record<string, unknown> = {}) => ({ type, origin: "session", filename: "db.yaml", deployed_version: 1, ...extra });

    describe.each(["connector", "skill"])("a %s", type => {
        it.each([
            ["config moved past the saved version", {}, versionsAt(2), true, true],
            ["config moved several versions past", { deployed_version: 0 }, versionsAt(5), true, true],
            ["config still at the saved version", {}, versionsAt(1), true, false],
            ["a record above the file's current version cannot belong to it: not an edit, and not proof either", { deployed_version: 5 }, versionsAt(2), true, false],
            ["no saved version recorded: cannot be told from a pre-record save", { deployed_version: undefined }, versionsAt(2), true, false],
            ["current version not resolved yet", {}, new Map<string, number>(), true, false],
            ["no versions map at all", {}, undefined, true, false],
            ["platform-origin component is referenced, not authored", { origin: "platform" }, versionsAt(2), true, false],
            ["no filename: nothing saved to compare", { filename: "" }, versionsAt(2), true, false],
        ])("%s", (_label, extra, versions, serverApplyEnabled, expected) => {
            expect(hasUnsavedSaveOnlyEdits(component(type, extra), versions, serverApplyEnabled)).toBe(expected);
        });
    });

    it.each([
        ["a connector needs server-side apply: the per-kind path cannot update one", "connector", false, false],
        ["a skill is updated through its own upload, so it does not", "skill", false, true],
        ["a connector with server-side apply on", "connector", true, true],
        ["a skill with server-side apply on", "skill", true, true],
    ])("%s", (_label, type, serverApplyEnabled, expected) => {
        expect(hasUnsavedSaveOnlyEdits(component(type), versionsAt(2), serverApplyEnabled)).toBe(expected);
    });

    it.each(["agent", "workflow", "gateway", "a2a_agent", "toolset"])("a %s with the same staleness is not a save-only component", type => {
        expect(hasUnsavedSaveOnlyEdits(component(type), versionsAt(2), true)).toBe(false);
    });
});

describe("saveOnlyEditState", () => {
    const comp = (name: string, extra: Record<string, unknown> = {}) => ({ name, type: "connector", origin: "session", filename: `${name}.yaml`, deployed_version: 1, ...extra });
    const skill = (name: string, extra: Record<string, unknown> = {}) => comp(name, { type: "skill", ...extra });
    const at = (n: number, ...names: string[]) => new Map(names.map(name => [`${name}.yaml`, n]));
    const none = { hasUnsavedEdits: false, allMatchLastSave: false, hasSkill: false, leftOut: [] as string[] };

    it.each([
        ["every component at the version its save recorded", [comp("a"), comp("b")], at(1, "a", "b"), { allMatchLastSave: true, leftOut: ["a", "b"] }],
        [
            "one edited since its save",
            [comp("a"), comp("b")],
            new Map([
                ["a.yaml", 1],
                ["b.yaml", 3],
            ]),
            { hasUnsavedEdits: true, leftOut: ["a"] },
        ],
        ["both edited", [comp("a"), comp("b")], at(2, "a", "b"), { hasUnsavedEdits: true }],
        ["one with no record of a save", [comp("a"), comp("b", { deployed_version: undefined })], at(1, "a", "b"), { leftOut: ["a"] }],
        ["versions not resolved yet", [comp("a")], new Map<string, number>(), {}],
        ["no versions map", [comp("a")], undefined, {}],
        ["platform components are ignored", [comp("a"), comp("p", { origin: "platform", deployed_version: undefined })], at(1, "a"), { allMatchLastSave: true, leftOut: ["a"] }],
        ["no components", [], at(1), {}],
        ["no manifest", undefined, at(1), {}],
        ["a skill at the version its save recorded is never proven unchanged: its bundle also packs other artifacts", [skill("a")], at(1, "a"), { hasSkill: true }],
        ["a skill beside connectors at their recorded versions: the connectors are unchanged, the build is not", [comp("a"), skill("s")], at(1, "a", "s"), { hasSkill: true, leftOut: ["a"] }],
        ["a skill edited since its save is still an edit", [skill("s")], at(2, "s"), { hasUnsavedEdits: true, hasSkill: true }],
        ["a record above the file's current version cannot belong to that file: unknown, not unchanged", [comp("a", { deployed_version: 6 })], at(1, "a"), {}],
        ["a record equal to the current version is unchanged", [comp("a", { deployed_version: 0 })], at(0, "a"), { allMatchLastSave: true, leftOut: ["a"] }],
    ])("%s", (_label, components, versions, expected) => {
        expect(saveOnlyEditState(components, versions, true)).toEqual({ ...none, ...expected });
    });

    it("reads a connector as having no edits when server-side apply is off, since nothing could save it", () => {
        const state = saveOnlyEditState([comp("db")], at(5, "db"), false);
        expect(state.hasUnsavedEdits).toBe(false);
    });
});
