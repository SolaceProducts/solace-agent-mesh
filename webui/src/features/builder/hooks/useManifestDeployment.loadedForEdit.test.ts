// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { isLoadedForEdit } from "../loadedForEdit";

import type { BuildManifest } from "./useBuilderManifest";
import { markComponentsDeployed } from "./useManifestDeployment";

const platformAgent = { name: "hunt_agent_a", type: "agent", filename: "", origin: "platform", status: "deployed" };
const editedWorkflow = { name: "HuntWf", type: "workflow", filename: "HuntWf.yaml", origin: "session", status: "deployed", deployed_version: 0 };
const addedAgent = { name: "NewAgent", type: "agent", filename: "NewAgent.yaml", origin: "session", status: "validated" };
const flagged = (components: NonNullable<BuildManifest["components"]>): BuildManifest => ({ name: "HuntWf", loaded_for_edit: true, components });
const at = (workflow: number, added?: number) => new Map([["HuntWf.yaml", workflow], ...(added === undefined ? [] : ([["NewAgent.yaml", added]] as Array<[string, number]>))]);

describe("markComponentsDeployed on an Edit-with-AI session", () => {
    const rows = [
        { name: "workflow with a platform dependency", components: [editedWorkflow, platformAgent], deployed: [{ name: "HuntWf" }], versions: at(1) },
        { name: "workflow without dependencies", components: [editedWorkflow], deployed: [{ name: "HuntWf", id: "wf-1" }], versions: at(2) },
        { name: "workflow plus an agent added by the edit", components: [editedWorkflow, platformAgent, addedAgent], deployed: [{ name: "HuntWf" }, { name: "NewAgent" }], versions: at(1, 0) },
    ];

    test.each(rows)("$name: the marker is gone and the build reads as deployed, not loaded", ({ components, deployed, versions }) => {
        const next = markComponentsDeployed(deployed, versions)(flagged(components));

        expect("loaded_for_edit" in next).toBe(false);
        expect(isLoadedForEdit(next, versions)).toBe(false);
    });

    test("without the marker removal, the same deploy would read as an untouched load", () => {
        const versions = at(1);
        const next = markComponentsDeployed([{ name: "HuntWf" }], versions)(flagged([editedWorkflow, platformAgent]));

        expect(isLoadedForEdit({ ...next, loaded_for_edit: true }, versions)).toBe(true);
    });

    test("leaves every other field of the manifest alone", () => {
        const next = markComponentsDeployed([{ name: "HuntWf", id: "wf-1" }], at(1))({ ...flagged([editedWorkflow, platformAgent]), description: "Edited", full_validation_passed: true });

        expect(next).toMatchObject({ name: "HuntWf", description: "Edited", full_validation_passed: true });
        expect(next.components?.[0]).toMatchObject({ status: "deployed", id: "wf-1", deployed_version: 1 });
        expect(next.components?.[1]).toEqual(platformAgent);
    });
});
