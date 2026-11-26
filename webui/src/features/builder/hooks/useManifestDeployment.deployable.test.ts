// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import { hasSaveableSessionComponent, needsDeploy } from "./useManifestDeployment";

const manifestWith = (components: NonNullable<BuildManifest["components"]>): BuildManifest => ({ name: "b", components }) as BuildManifest;

describe("hasSaveableSessionComponent", () => {
    const noStatus = new Map<string, string>();
    const deployed = (name: string) => new Map([[name, "deployed"]]);

    test("a deployed session workflow is still saveable without server-side apply", () => {
        const m = manifestWith([{ filename: "wf.yaml", type: "workflow", name: "Detector", origin: "session", status: "deployed" }]);
        expect(hasSaveableSessionComponent(m, deployed("Detector"), false)).toBe(true);
    });

    test("a deployed agent is NOT saveable without server-side apply — its save path is create-only", () => {
        const m = manifestWith([{ filename: "a.yaml", type: "agent", name: "Chat", origin: "session", status: "deployed" }]);
        expect(hasSaveableSessionComponent(m, deployed("Chat"), false)).toBe(false);
    });

    test("a workflow whose artifact has moved past the deployed version is saveable", () => {
        const m = manifestWith([{ filename: "wf.yaml", type: "workflow", name: "Detector", origin: "session", deployed_version: 0 }]);
        expect(hasSaveableSessionComponent(m, deployed("Detector"), false, new Map([["wf.yaml", 2]]))).toBe(true);
    });

    test("a workflow already deployed from its current artifact version is NOT saveable", () => {
        const m = manifestWith([{ filename: "wf.yaml", type: "workflow", name: "Detector", origin: "session", status: "deployed", deployed_version: 2 }]);
        expect(hasSaveableSessionComponent(m, deployed("Detector"), false, new Map([["wf.yaml", 2]]))).toBe(false);
    });

    test("omitting versions reproduces the unconditional pre-deployed_version answer", () => {
        const m = manifestWith([{ filename: "wf.yaml", type: "workflow", name: "Detector", origin: "session", deployed_version: 2 }]);
        expect(hasSaveableSessionComponent(m, deployed("Detector"), false)).toBe(true);
    });

    test("server-side apply makes a deployed agent saveable when its artifact has moved on", () => {
        const m = manifestWith([{ filename: "a.yaml", type: "agent", name: "Chat", origin: "session", deployed_version: 1 }]);
        expect(hasSaveableSessionComponent(m, deployed("Chat"), true, new Map([["a.yaml", 2]]))).toBe(true);
    });

    test("server-side apply does NOT keep a deployed, up-to-date component saveable", () => {
        const versions = new Map([["a.yaml", 2]]);
        const agent = manifestWith([{ filename: "a.yaml", type: "agent", name: "Chat", origin: "session", status: "deployed", deployed_version: 2 }]);
        expect(hasSaveableSessionComponent(agent, deployed("Chat"), true, versions)).toBe(false);
        const wf = manifestWith([{ filename: "wf.yaml", type: "workflow", name: "Detector", origin: "session", status: "deployed", deployed_version: 2 }]);
        expect(hasSaveableSessionComponent(wf, deployed("Detector"), true, new Map([["wf.yaml", 2]]))).toBe(false);
    });

    test("server-side apply still offers a component the platform is not running", () => {
        const versions = new Map([["a.yaml", 2]]);
        const m = manifestWith([{ filename: "a.yaml", type: "agent", name: "Chat", origin: "session", deployed_version: 2 }]);
        expect(hasSaveableSessionComponent(m, new Map([["Chat", "deploy_failed"]]), true, versions)).toBe(true);
        expect(hasSaveableSessionComponent(m, noStatus, true, versions)).toBe(true);
    });

    test("server-side apply with no version evidence still answers yes", () => {
        const m = manifestWith([{ filename: "a.yaml", type: "agent", name: "Chat", origin: "session", status: "deployed" }]);
        expect(hasSaveableSessionComponent(m, deployed("Chat"), true)).toBe(true);
    });

    test("a never-deployed component is saveable either way", () => {
        const m = manifestWith([{ filename: "a.yaml", type: "agent", name: "Chat", origin: "session", status: "validated" }]);
        expect(hasSaveableSessionComponent(m, noStatus, false)).toBe(true);
    });

    test("a clean workflow that is NOT running on the platform is still saveable", () => {
        const versions = new Map([["wf.yaml", 2]]);
        const m = manifestWith([{ filename: "wf.yaml", type: "workflow", name: "Detector", origin: "session", deployed_version: 2 }]);
        expect(hasSaveableSessionComponent(m, new Map([["Detector", "not_deployed"]]), false, versions)).toBe(true);
        expect(hasSaveableSessionComponent(m, new Map([["Detector", "deploy_failed"]]), false, versions)).toBe(true);
        expect(hasSaveableSessionComponent(m, noStatus, false, versions)).toBe(true);
    });

    test("platform-origin components and non-deployables never qualify", () => {
        expect(hasSaveableSessionComponent(manifestWith([{ filename: "", type: "workflow", name: "Existing", origin: "platform" }]), noStatus, true)).toBe(false);
        expect(hasSaveableSessionComponent(manifestWith([{ filename: "s.yaml", type: "skill", name: "S", origin: "session" }]), noStatus, true)).toBe(false);
        expect(hasSaveableSessionComponent(manifestWith([{ filename: "c.json", type: "connector", name: "C", origin: "session" }]), noStatus, true)).toBe(false);
    });
});

describe("needsDeploy", () => {
    const nothingChanged = new Set<string>();

    test("a deployed component the save left alone does not need deploying", () => {
        expect(needsDeploy("deployed", "Chat", nothingChanged)).toBe(false);
    });

    test("anything not deployed needs deploying", () => {
        expect(needsDeploy("not_deployed", "Chat", nothingChanged)).toBe(true);
        expect(needsDeploy("deploy_failed", "Chat", nothingChanged)).toBe(true);
        expect(needsDeploy(undefined, "Chat", nothingChanged)).toBe(true);
    });

    test("a deployed component the save rewrote needs deploying, matched raw or normalized", () => {
        expect(needsDeploy("deployed", "Chat", new Set(["Chat"]))).toBe(true);
        expect(needsDeploy("deployed", "Order Agent", new Set(["order-agent"]))).toBe(true);
    });
});
