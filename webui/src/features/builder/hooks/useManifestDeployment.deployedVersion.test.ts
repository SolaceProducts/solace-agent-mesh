// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import { markComponentsDeployed, resolveDeployedVersions } from "./useManifestDeployment";

describe("resolveDeployedVersions", () => {
    const WORKFLOW = { name: "ImageDimensionsWorkflow", filename: "image_dimensions_workflow.yaml", origin: "session" };
    const PLATFORM_TOOLSET = { name: "image_magick", filename: "", origin: "platform" };

    test("records a version the session artifact list never showed", async () => {
        const fetchVersions = vi.fn().mockResolvedValue([0, 1, 2]);

        const versions = await resolveDeployedVersions([WORKFLOW], new Set([WORKFLOW.name]), new Map(), fetchVersions);

        expect(versions.get(WORKFLOW.filename)).toBe(2);
        expect(fetchVersions).toHaveBeenCalledWith(WORKFLOW.filename);
    });

    test("prefers the freshly fetched version over a lower one already in the map", async () => {
        const fetchVersions = vi.fn().mockResolvedValue([0, 1, 2]);

        const versions = await resolveDeployedVersions([WORKFLOW], new Set([WORKFLOW.name]), new Map([[WORKFLOW.filename, 0]]), fetchVersions);

        expect(versions.get(WORKFLOW.filename)).toBe(2);
    });

    test("keeps the version already known when the fetch fails", async () => {
        const fetchVersions = vi.fn().mockRejectedValue(new Error("boom"));

        const versions = await resolveDeployedVersions([WORKFLOW], new Set([WORKFLOW.name]), new Map([[WORKFLOW.filename, 1]]), fetchVersions);

        expect(versions.get(WORKFLOW.filename)).toBe(1);
    });

    test("leaves the version unknown when the fetch fails and nothing was known", async () => {
        const fetchVersions = vi.fn().mockRejectedValue(new Error("boom"));

        const versions = await resolveDeployedVersions([WORKFLOW], new Set([WORKFLOW.name]), new Map(), fetchVersions);

        expect(versions.get(WORKFLOW.filename)).toBeUndefined();
    });

    test("does not fetch for a platform-origin component", async () => {
        const fetchVersions = vi.fn().mockResolvedValue([3]);

        await resolveDeployedVersions([PLATFORM_TOOLSET], new Set([PLATFORM_TOOLSET.name]), new Map(), fetchVersions);

        expect(fetchVersions).not.toHaveBeenCalled();
    });

    test("does not fetch for a component this pass did not deploy", async () => {
        const other = { name: "OtherAgent", filename: "other_agent.yaml", origin: "session" };
        const fetchVersions = vi.fn().mockResolvedValue([0]);

        const versions = await resolveDeployedVersions([WORKFLOW, other], new Set([WORKFLOW.name]), new Map(), fetchVersions);

        expect(fetchVersions).toHaveBeenCalledTimes(1);
        expect(fetchVersions).toHaveBeenCalledWith(WORKFLOW.filename);
        expect(versions.get(other.filename)).toBeUndefined();
    });

    test("resolves every deployed component in one pass", async () => {
        const agent = { name: "Helper", filename: "helper_agent.yaml", origin: "session" };
        const fetchVersions = vi.fn(async (filename: string) => (filename === WORKFLOW.filename ? [0, 1] : [4]));

        const versions = await resolveDeployedVersions([WORKFLOW, agent, PLATFORM_TOOLSET], new Set([WORKFLOW.name, agent.name, PLATFORM_TOOLSET.name]), new Map(), fetchVersions);

        expect(versions.get(WORKFLOW.filename)).toBe(1);
        expect(versions.get(agent.filename)).toBe(4);
    });

    test("records version 0", async () => {
        const fetchVersions = vi.fn().mockResolvedValue([0]);

        const versions = await resolveDeployedVersions([WORKFLOW], new Set([WORKFLOW.name]), new Map(), fetchVersions);

        expect(versions.get(WORKFLOW.filename)).toBe(0);
    });
});

describe("stamping a deploy into the manifest", () => {
    const manifest = {
        name: "image-dimensions-workflow",
        components: [
            { filename: "image_dimensions_workflow.yaml", type: "workflow", name: "ImageDimensionsWorkflow", status: "validated", origin: "session" },
            { filename: "", type: "toolset", name: "image_magick", status: "deployed", origin: "platform" },
        ],
    };

    test("writes deployed_version for a config the artifact list never showed", async () => {
        const deployedNames = new Set(["ImageDimensionsWorkflow"]);
        const versions = await resolveDeployedVersions(manifest.components, deployedNames, new Map(), async () => [0, 1, 2]);

        const next = markComponentsDeployed([{ name: "ImageDimensionsWorkflow", id: "wf-1" }], versions)(manifest);

        expect(next.components?.[0]).toMatchObject({ status: "deployed", id: "wf-1", deployed_version: 2 });
    });

    test("leaves the field absent rather than stamping a version it could not resolve", async () => {
        const deployedNames = new Set(["ImageDimensionsWorkflow"]);
        const versions = await resolveDeployedVersions(manifest.components, deployedNames, new Map(), async () => []);

        const next = markComponentsDeployed([{ name: "ImageDimensionsWorkflow", id: "wf-1" }], versions)(manifest);

        expect(next.components?.[0]).toMatchObject({ status: "deployed", id: "wf-1" });
        expect(next.components?.[0]).not.toHaveProperty("deployed_version");
    });
});
