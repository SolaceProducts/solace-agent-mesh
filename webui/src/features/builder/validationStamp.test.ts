// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { ArtifactVersions } from "./componentDirtyState";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { validationStampState } from "./validationStamp";

type TestComponent = NonNullable<BuildManifest["components"]>[number];

const component = (filename: string, extra: Partial<TestComponent> = {}): TestComponent => ({ filename, type: "agent", name: filename.replace(".yaml", ""), ...extra });

const manifest = (components: TestComponent[], stamp: { passed?: boolean; versions?: Record<string, number> } = { passed: true, versions: { "a.yaml": 1, "b.yaml": 0 } }): BuildManifest => ({
    name: "build",
    components,
    ...(stamp.passed === undefined ? {} : { full_validation_passed: stamp.passed }),
    ...(stamp.versions === undefined ? {} : { full_validation_versions: stamp.versions }),
});

const twoComponents = [component("a.yaml"), component("b.yaml")];
const versionsOf = (entries: Record<string, number>): ArtifactVersions => new Map(Object.entries(entries));

describe("validationStampState", () => {
    it.each([
        ["no manifest", null, new Map()],
        ["a manifest with no stamp", manifest(twoComponents, {}), versionsOf({ "a.yaml": 1, "b.yaml": 0 })],
        ["a stamp that did not pass", manifest(twoComponents, { passed: false, versions: { "a.yaml": 1, "b.yaml": 0 } }), versionsOf({ "a.yaml": 1, "b.yaml": 0 })],
    ])("is missing for %s", (_label, m, versions) => {
        expect(validationStampState(m, versions)).toEqual({ state: "missing", changed: new Set() });
    });

    it("is current when every component is at its validated version", () => {
        expect(validationStampState(manifest(twoComponents), versionsOf({ "a.yaml": 1, "b.yaml": 0 }))).toEqual({ state: "current", changed: new Set() });
    });

    it("is stale, naming the file, when a component moved past its validated version", () => {
        expect(validationStampState(manifest(twoComponents), versionsOf({ "a.yaml": 2, "b.yaml": 0 }))).toEqual({ state: "stale", changed: new Set(["a.yaml"]) });
    });

    it("is unknown while a version is still loading", () => {
        expect(validationStampState(manifest(twoComponents), versionsOf({ "a.yaml": 1 }))).toEqual({ state: "unknown", changed: new Set() });
    });

    it("is unknown when the version map is empty", () => {
        expect(validationStampState(manifest(twoComponents), versionsOf({}))).toEqual({ state: "unknown", changed: new Set() });
    });

    it("is unknown, not stale, when the cache lags behind the validated version", () => {
        const m = manifest([component("a.yaml")], { passed: true, versions: { "a.yaml": 3 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 2 }))).toEqual({ state: "unknown", changed: new Set() });
    });

    it("is stale for every compared file when the stamp predates recorded versions", () => {
        const m = manifest([...twoComponents, component("p.yaml", { origin: "platform" })], { passed: true });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1, "b.yaml": 0 }))).toEqual({ state: "stale", changed: new Set(["a.yaml", "b.yaml"]) });
    });

    it("is stale for a component added after validation", () => {
        const m = manifest([...twoComponents, component("c.yaml")]);
        expect(validationStampState(m, versionsOf({ "a.yaml": 1, "b.yaml": 0, "c.yaml": 0 }))).toEqual({ state: "stale", changed: new Set(["c.yaml"]) });
    });

    it("is stale for a component added after validation whose version is not yet known", () => {
        const m = manifest([...twoComponents, component("c.yaml")]);
        expect(validationStampState(m, versionsOf({ "a.yaml": 1, "b.yaml": 0 }))).toEqual({ state: "stale", changed: new Set(["c.yaml"]) });
    });

    it("compares a component that has no origin", () => {
        const m = manifest([component("a.yaml", { origin: undefined })], { passed: true, versions: { "a.yaml": 1 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 2 }))).toEqual({ state: "stale", changed: new Set(["a.yaml"]) });
    });

    it("ignores platform components", () => {
        const m = manifest([component("a.yaml"), component("", { origin: "platform", name: "live" }), component("p.yaml", { origin: "platform" })], { passed: true, versions: { "a.yaml": 1 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1, "p.yaml": 9 }))).toEqual({ state: "current", changed: new Set() });
    });

    it("ignores blocked components", () => {
        const m = manifest([component("a.yaml"), component("blocked.yaml", { status: "blocked" })], { passed: true, versions: { "a.yaml": 1 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1, "blocked.yaml": 4 }))).toEqual({ state: "current", changed: new Set() });
    });

    it("ignores a component with no filename", () => {
        const m = manifest([component("a.yaml"), component("")], { passed: true, versions: { "a.yaml": 1 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1 }))).toEqual({ state: "current", changed: new Set() });
    });

    it("ignores a component whose filename is only whitespace", () => {
        const m = manifest([component("a.yaml"), component("  ")], { passed: true, versions: { "a.yaml": 1 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1, "  ": 3 }))).toEqual({ state: "current", changed: new Set() });
    });

    it("lets stale win over unknown", () => {
        expect(validationStampState(manifest(twoComponents), versionsOf({ "a.yaml": 2 }))).toEqual({ state: "stale", changed: new Set(["a.yaml"]) });
    });

    it("is current when the stamp covers a build with nothing to compare", () => {
        expect(validationStampState(manifest([], { passed: true, versions: {} }), versionsOf({}))).toEqual({ state: "current", changed: new Set() });
    });

    it("is stale when a validated component was removed from the manifest after validation", () => {
        const m = manifest([component("a.yaml")], { passed: true, versions: { "a.yaml": 1, "b.yaml": 0 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1 }))).toEqual({ state: "stale", changed: new Set() });
    });

    it("is stale for a removed component even while another version is still loading", () => {
        const m = manifest([component("a.yaml")], { passed: true, versions: { "a.yaml": 1, "b.yaml": 0 } });
        expect(validationStampState(m, versionsOf({}))).toEqual({ state: "stale", changed: new Set() });
    });

    it("does not read a recorded platform, blocked or unnamed component as removed", () => {
        const m = manifest([component("a.yaml"), component("p.yaml", { origin: "platform" }), component("blocked.yaml", { status: "blocked" })], { passed: true, versions: { "a.yaml": 1 } });
        expect(validationStampState(m, versionsOf({ "a.yaml": 1 }))).toEqual({ state: "current", changed: new Set() });
    });

    describe("a version that settled without an answer", () => {
        const settledWithout = (versions: Record<string, number>, ...unresolved: string[]): ArtifactVersions => Object.assign(versionsOf(versions), { unresolved: new Set(unresolved) });

        it("is stale, not unknown, naming the file", () => {
            expect(validationStampState(manifest(twoComponents), settledWithout({ "b.yaml": 0 }, "a.yaml"))).toEqual({ state: "stale", changed: new Set(["a.yaml"]) });
        });

        it("stays unknown for a version that is merely still loading", () => {
            expect(validationStampState(manifest(twoComponents), settledWithout({ "b.yaml": 0 }))).toEqual({ state: "unknown", changed: new Set() });
        });

        it("ignores a settled filename that is not compared", () => {
            const m = manifest([component("a.yaml"), component("p.yaml", { origin: "platform" })], { passed: true, versions: { "a.yaml": 1 } });
            expect(validationStampState(m, settledWithout({ "a.yaml": 1 }, "p.yaml"))).toEqual({ state: "current", changed: new Set() });
        });
    });
});
