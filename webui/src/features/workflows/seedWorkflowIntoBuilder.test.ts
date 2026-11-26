// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { load as yamlLoad } from "js-yaml";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import * as builderService from "@/api/builder/service";
import { api } from "@/api/client";

import type { Workflow } from "@/types/workflow";

import { SAM_WORKFLOW_CONFIG_MIME_TYPE } from "@/features/builder/builderUtils";
import { type BuildManifest, SAM_BUILD_MANIFEST_MIME_TYPE } from "@/features/builder/hooks/useBuilderManifest";
import { isLoadedForEdit } from "@/features/builder/loadedForEdit";
import { validationStampState } from "@/features/builder/validationStamp";

import type { Agent } from "@/types";

import { seedWorkflowIntoBuilder } from "./seedWorkflowIntoBuilder";

vi.mock("@/api/builder/service", () => ({ uploadArtifact: vi.fn(), listManifestVersions: vi.fn() }));

const uploadArtifact = builderService.uploadArtifact as unknown as ReturnType<typeof vi.fn>;
const listManifestVersions = builderService.listManifestVersions as unknown as ReturnType<typeof vi.fn>;

const workflow = {
    id: "wf-1",
    name: "doc_flow",
    displayName: "Doc Flow",
    description: "top-level description",
    appConfig: { description: "config description", nodes: [{ id: "n1", type: "agent", agent_name: "Analyzer" }] },
    deploymentStatus: "deployed",
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
} as unknown as Workflow;

const nestedWorkflow = {
    id: "wf-2",
    name: "nested_flow",
    displayName: "Nested Flow",
    description: "top-level description",
    appConfig: { tools: [], toolsets: [], workflow: { description: "nested config description", nodes: [{ id: "n1", type: "agent", agent_name: "Analyzer" }] } },
    deploymentStatus: "deployed",
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
} as unknown as Workflow;

const agents = [{ name: "Analyzer", systemPrompt: "analyze things" }] as unknown as Agent[];

describe("seedWorkflowIntoBuilder", () => {
    beforeEach(() => {
        uploadArtifact.mockReset();
        listManifestVersions.mockReset();
        listManifestVersions.mockResolvedValue([0]);
    });
    afterEach(() => vi.restoreAllMocks());

    test("uploads only the workflow config + manifest (agents are reference-only), with the DAG in the manifest", async () => {
        uploadArtifact.mockResolvedValue({ ok: true });
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const post: any = vi.spyOn(api.webui, "post" as any).mockResolvedValue({ ok: true });

        await seedWorkflowIntoBuilder("sess-1", workflow, agents);

        expect(post).toHaveBeenCalledWith("/api/v1/sessions", { id: "sess-1" });
        expect(post.mock.invocationCallOrder[0]).toBeLessThan(uploadArtifact.mock.invocationCallOrder[0]);

        const byName: Record<string, unknown[]> = {};
        for (const c of uploadArtifact.mock.calls) byName[c[1] as string] = c;
        expect(Object.keys(byName).sort()).toEqual(["build_manifest.yaml", "doc_flow.yaml"]);
        expect(byName["Analyzer.yaml"]).toBeUndefined();

        expect(byName["doc_flow.yaml"][3]).toBe(SAM_WORKFLOW_CONFIG_MIME_TYPE);
        expect(byName["build_manifest.yaml"][3]).toBe(SAM_BUILD_MANIFEST_MIME_TYPE);

        const manifestYaml = byName["build_manifest.yaml"][2] as string;
        expect(manifestYaml).toMatch(/origin: session/);
        expect(manifestYaml).toMatch(/origin: platform/);
        expect(manifestYaml).toMatch(/loaded_for_edit: true/);
        expect(manifestYaml).toContain("doc_flow.yaml");
        expect(manifestYaml).toContain("Analyzer");
        expect(manifestYaml).toMatch(/agent_name: Analyzer/);
        expect(byName["doc_flow.yaml"][2]).toMatch(/apps:/);
        expect(byName["doc_flow.yaml"][2]).toMatch(/nodes:/);
    });

    test("warns (not silently skips) a referenced agent missing from the list, and adds no component for it", async () => {
        uploadArtifact.mockResolvedValue({ ok: true });
        vi.spyOn(api.webui, "post" as any).mockResolvedValue({ ok: true } as never);
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        await seedWorkflowIntoBuilder("sess-1", workflow, []);

        expect(warn).toHaveBeenCalledWith(expect.stringMatching(/references agent "Analyzer" not found/));
        const manifestCall = uploadArtifact.mock.calls.find(c => c[1] === "build_manifest.yaml")!;
        const manifestYaml = manifestCall[2] as string;
        expect(manifestYaml).not.toMatch(/origin: platform/);
        expect(manifestYaml).toMatch(/agent_name: Analyzer/);
    });

    test("reads nodes + description from a nested (config-apply-authored) appConfig, with the nested description winning over the top-level one", async () => {
        uploadArtifact.mockResolvedValue({ ok: true });
        vi.spyOn(api.webui, "post" as any).mockResolvedValue({ ok: true } as never);

        await seedWorkflowIntoBuilder("sess-1", nestedWorkflow, agents);

        const manifestCall = uploadArtifact.mock.calls.find(c => c[1] === "build_manifest.yaml")!;
        const manifestYaml = manifestCall[2] as string;

        expect(manifestYaml).toMatch(/agent_name: Analyzer/);
        expect(manifestYaml).toMatch(/origin: platform/);
        expect(manifestYaml).toMatch(/description: nested config description/);
    });

    test.each([
        { name: "flat appConfig with a referenced agent", wf: workflow, agentList: agents, componentNames: ["doc_flow", "Analyzer"] },
        { name: "nested appConfig with a referenced agent", wf: nestedWorkflow, agentList: agents, componentNames: ["nested_flow", "Analyzer"] },
        { name: "referenced agent missing from the agent list", wf: workflow, agentList: [] as Agent[], componentNames: ["doc_flow"] },
        { name: "workflow without any agent step", wf: { ...workflow, appConfig: { description: "d", nodes: [] } } as unknown as Workflow, agentList: agents, componentNames: ["doc_flow"] },
    ])("stamps only the workflow with the version it uploaded: $name", async ({ wf, agentList, componentNames }) => {
        uploadArtifact.mockResolvedValue({ ok: true });
        vi.spyOn(api.webui, "post" as any).mockResolvedValue({ ok: true } as never);
        vi.spyOn(console, "warn").mockImplementation(() => {});

        await seedWorkflowIntoBuilder("sess-1", wf, agentList);

        const manifestYaml = uploadArtifact.mock.calls.find(c => c[1] === "build_manifest.yaml")![2] as string;
        const manifest = yamlLoad(manifestYaml) as { loaded_for_edit: boolean; components: Array<{ name: string; type: string; origin: string; status: string; deployed_version?: number }> };

        expect(manifest.loaded_for_edit).toBe(true);
        expect(manifest.components.map(c => c.name)).toEqual(componentNames);

        const file = `${wf.name}.yaml`;
        const seeded = manifest as unknown as BuildManifest;
        expect(isLoadedForEdit(seeded, new Map([[file, 0]]))).toBe(true);
        expect(isLoadedForEdit(seeded, new Map())).toBe(true);
        expect(isLoadedForEdit(seeded, new Map([[file, 1]]))).toBe(false);
        expect(validationStampState(seeded, new Map([[file, 0]])).state).toBe("current");
        expect(validationStampState(seeded, new Map([[file, 1]])).state).toBe("stale");
        for (const c of manifest.components) {
            if (c.type === "workflow") {
                expect(c).toMatchObject({ origin: "session", status: "deployed", deployed_version: 0 });
            } else {
                expect(c.deployed_version).toBeUndefined();
            }
        }
    });

    describe("full-validation stamp", () => {
        const stampOf = () => {
            const manifestYaml = uploadArtifact.mock.calls.find(c => c[1] === "build_manifest.yaml")![2] as string;
            return yamlLoad(manifestYaml) as BuildManifest;
        };

        beforeEach(() => {
            uploadArtifact.mockResolvedValue({ ok: true });
            vi.spyOn(api.webui, "post" as any).mockResolvedValue({ ok: true } as never);
        });

        test("records the version the first seed wrote for the workflow, and nothing for platform agents", async () => {
            await seedWorkflowIntoBuilder("sess-1", workflow, agents);

            const manifest = stampOf();
            expect(manifest.full_validation_passed).toBe(true);
            expect(manifest.full_validation_versions).toEqual({ "doc_flow.yaml": 0 });
            expect(listManifestVersions).toHaveBeenCalledWith("sess-1", "doc_flow.yaml");
        });

        test.each([
            ["the read-back fails", () => listManifestVersions.mockRejectedValue(new Error("network down"))],
            ["the read-back finds no version", () => listManifestVersions.mockResolvedValue([])],
        ])("still writes the manifest, without recorded versions, when %s, so the Builder validates again", async (_label, arrange) => {
            vi.spyOn(console, "warn").mockImplementation(() => {});
            arrange();

            await seedWorkflowIntoBuilder("sess-1", workflow, agents);

            const manifest = stampOf();
            expect(uploadArtifact.mock.calls.map(c => c[1])).toEqual(["doc_flow.yaml", "build_manifest.yaml"]);
            expect(manifest.full_validation_passed).toBe(true);
            expect(manifest.full_validation_versions).toBeUndefined();
            expect(manifest.components?.find(c => c.name === "doc_flow")?.deployed_version).toBeUndefined();
            expect(validationStampState(manifest, new Map([["doc_flow.yaml", 0]])).state).toBe("stale");
        });

        test("records the higher version when the seed lands on a file the session already holds", async () => {
            listManifestVersions.mockResolvedValue([0, 1, 2]);

            await seedWorkflowIntoBuilder("sess-1", workflow, agents);

            expect(stampOf().full_validation_versions).toEqual({ "doc_flow.yaml": 2 });
        });

        test("records the same version as deployed, so a re-seed into an existing file does not read as edited", async () => {
            listManifestVersions.mockResolvedValue([0, 1, 2]);

            await seedWorkflowIntoBuilder("sess-1", workflow, agents);

            const manifest = stampOf();
            const component = manifest.components!.find(c => c.name === "doc_flow")!;
            expect(component.deployed_version).toBe(2);
            expect(isLoadedForEdit(manifest, new Map([["doc_flow.yaml", 2]]))).toBe(true);
            expect(isLoadedForEdit(manifest, new Map([["doc_flow.yaml", 3]]))).toBe(false);
        });

        test("reads the versions only after the workflow upload and before the manifest upload", async () => {
            await seedWorkflowIntoBuilder("sess-1", workflow, agents);

            const workflowUpload = uploadArtifact.mock.calls.findIndex(c => c[1] === "doc_flow.yaml");
            const manifestUpload = uploadArtifact.mock.calls.findIndex(c => c[1] === "build_manifest.yaml");
            expect(listManifestVersions.mock.invocationCallOrder[0]).toBeGreaterThan(uploadArtifact.mock.invocationCallOrder[workflowUpload]);
            expect(listManifestVersions.mock.invocationCallOrder[0]).toBeLessThan(uploadArtifact.mock.invocationCallOrder[manifestUpload]);
        });
    });

    test("throws when an artifact upload fails, so the caller can fall back to agent-load", async () => {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        vi.spyOn(api.webui, "post" as any).mockResolvedValue({ ok: true } as never);
        uploadArtifact.mockResolvedValue({ ok: false });

        await expect(seedWorkflowIntoBuilder("sess-1", workflow, agents)).rejects.toThrow(/upload/i);
    });
});
