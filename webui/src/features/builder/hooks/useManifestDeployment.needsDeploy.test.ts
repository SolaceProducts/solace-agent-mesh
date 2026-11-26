// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import { deployCandidates, needsDeploy } from "./useManifestDeployment";

describe("needsDeploy", () => {
    const nothingChanged: ReadonlySet<string> = new Set();

    test("a component that is not deployed always needs deploying", () => {
        expect(needsDeploy(undefined, "MyAgent", nothingChanged)).toBe(true);
        expect(needsDeploy("pending", "MyAgent", nothingChanged)).toBe(true);
        expect(needsDeploy("deploy_failed", "MyAgent", nothingChanged)).toBe(true);
    });

    test("a deployed component the save left alone is finished", () => {
        expect(needsDeploy("deployed", "MyAgent", nothingChanged)).toBe(false);
    });

    test("a deployed component the save rewrote is stale, not finished", () => {
        expect(needsDeploy("deployed", "MyAgent", new Set(["MyAgent"]))).toBe(true);
    });

    test("the match survives the manifest and platform spelling a name differently", () => {
        expect(needsDeploy("deployed", "My Agent", new Set(["my agent"]))).toBe(true);
        expect(needsDeploy("deployed", "My_Agent", new Set(["MyAgent"]))).toBe(true);
    });

    test("a rewrite of a different component does not drag this one in", () => {
        expect(needsDeploy("deployed", "MyAgent", new Set(["SomeoneElse"]))).toBe(false);
    });
});

describe("deployCandidates", () => {
    type Comp = NonNullable<BuildManifest["components"]>[number];
    const agent = (name: string, extra: Partial<Comp> = {}): Comp => ({ filename: `${name}.yaml`, type: "agent", name, status: "validated", ...extra }) as Comp;

    test("keeps every session component when the deploy is not limited", () => {
        expect(deployCandidates([agent("A"), agent("B")], new Set(["A"]), false).map(c => c.name)).toEqual(["A", "B"]);
    });

    test("limited to what was rewritten, a name matches whatever its spelling", () => {
        const components = [agent("Agent X"), agent("Agent Y")];
        expect(deployCandidates(components, new Set(["agent_x"]), true).map(c => c.name)).toEqual(["Agent X"]);
    });

    test("never offers platform-owned components, skills or connectors", () => {
        const components = [agent("A", { origin: "platform" }), agent("S", { type: "skill" }), agent("C", { type: "connector" }), agent("B")];
        expect(deployCandidates(components, new Set(["A", "S", "C", "B"]), true).map(c => c.name)).toEqual(["B"]);
    });
});
