// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ArtifactVersions } from "./componentDirtyState";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { dropLoadedForEdit, hasEditsSinceLoad, isLoadedForEdit } from "./loadedForEdit";

type Components = NonNullable<BuildManifest["components"]>;

const platformAgent = { name: "hunt_agent_a", type: "agent", filename: "", origin: "platform", status: "deployed" };
const loadedWorkflow = { name: "HuntWf", type: "workflow", filename: "HuntWf.yaml", origin: "session", status: "deployed", deployed_version: 0 };

const at = (version?: number): ArtifactVersions => (version === undefined ? new Map() : new Map([["HuntWf.yaml", version]]));

describe("hasEditsSinceLoad", () => {
    const rows: Array<{ name: string; components: Components | undefined; versions: ArtifactVersions | undefined; edited: boolean }> = [
        { name: "no components at all", components: undefined, versions: at(), edited: false },
        { name: "workflow with a platform dependency, as seeded, artifact still at the loaded version", components: [loadedWorkflow, platformAgent], versions: at(0), edited: false },
        { name: "workflow without dependencies, as seeded", components: [loadedWorkflow], versions: at(0), edited: false },
        { name: "as seeded, before the artifact versions have loaded (unknown is not an edit)", components: [loadedWorkflow, platformAgent], versions: at(), edited: false },
        { name: "as seeded, version lookup absent altogether", components: [loadedWorkflow, platformAgent], versions: undefined, edited: false },
        { name: "only platform components", components: [platformAgent], versions: at(), edited: false },
        { name: "the workflow artifact moved past the loaded version", components: [loadedWorkflow, platformAgent], versions: at(1), edited: true },
        { name: "the workflow artifact moved past the loaded version, no dependencies", components: [loadedWorkflow], versions: at(3), edited: true },
        { name: "a session seeded before the stamp existed, or the loaded workflow re-saved without its stamp", components: [{ ...loadedWorkflow, deployed_version: undefined }, platformAgent], versions: at(0), edited: true },
        { name: "the loaded workflow was knocked back from deployed", components: [{ ...loadedWorkflow, status: "validated" }, platformAgent], versions: at(0), edited: true },
        { name: "the agent added a component still being drafted", components: [loadedWorkflow, platformAgent, { name: "NewAgent", type: "agent", filename: "NewAgent.yaml", origin: "session", status: "draft" }], versions: at(0), edited: true },
        { name: "the agent added a component that is validated but not deployed", components: [loadedWorkflow, { name: "NewAgent", type: "agent", filename: "NewAgent.yaml", origin: "session", status: "validated" }], versions: at(0), edited: true },
        { name: "the agent added a deployed component it never stamped", components: [loadedWorkflow, { name: "NewAgent", type: "agent", filename: "NewAgent.yaml", origin: "session", status: "deployed" }], versions: at(0), edited: true },
        { name: "a connector was added to the loaded session", components: [loadedWorkflow, { name: "Db", type: "connector", filename: "Db.yaml", origin: "session", status: "validated" }], versions: at(0), edited: true },
        { name: "after the edit was deployed: stamp and artifact agree again", components: [{ ...loadedWorkflow, deployed_version: 1 }, platformAgent], versions: at(1), edited: false },
    ];

    test.each(rows)("$name -> edited: $edited", ({ components, versions, edited }) => {
        expect(hasEditsSinceLoad(components, versions)).toBe(edited);
    });
});

describe("isLoadedForEdit", () => {
    const manifest = (extra: Partial<BuildManifest>): BuildManifest => ({ name: "HuntWf", components: [loadedWorkflow, platformAgent], ...extra });

    const rows: Array<{ name: string; manifest: BuildManifest | null | undefined; versions: ArtifactVersions; loaded: boolean }> = [
        { name: "no manifest", manifest: undefined, versions: at(0), loaded: false },
        { name: "null manifest", manifest: null, versions: at(0), loaded: false },
        { name: "flagged, untouched", manifest: manifest({ loaded_for_edit: true }), versions: at(0), loaded: true },
        { name: "flagged, versions not loaded yet", manifest: manifest({ loaded_for_edit: true }), versions: at(), loaded: true },
        { name: "flagged, then edited", manifest: manifest({ loaded_for_edit: true }), versions: at(1), loaded: false },
        { name: "flagged, a component added", manifest: manifest({ loaded_for_edit: true, components: [loadedWorkflow, { name: "N", type: "agent", filename: "N.yaml", origin: "session", status: "draft" }] }), versions: at(0), loaded: false },
        { name: "never flagged (a build made in this session)", manifest: manifest({}), versions: at(0), loaded: false },
        { name: "flag dropped explicitly", manifest: manifest({ loaded_for_edit: false }), versions: at(0), loaded: false },
    ];

    test.each(rows)("$name -> loaded: $loaded", ({ manifest: m, versions, loaded }) => {
        expect(isLoadedForEdit(m, versions)).toBe(loaded);
    });
});

describe("dropLoadedForEdit", () => {
    test("removes the marker and leaves everything else as it was", () => {
        const prev: BuildManifest = { name: "HuntWf", description: "d", loaded_for_edit: true, full_validation_passed: true, components: [loadedWorkflow, platformAgent] };
        const next = dropLoadedForEdit(prev);
        expect("loaded_for_edit" in next).toBe(false);
        expect(next).toEqual({ name: "HuntWf", description: "d", full_validation_passed: true, components: [loadedWorkflow, platformAgent] });
    });
});
