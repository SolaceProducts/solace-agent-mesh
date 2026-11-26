// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { ArtifactVersions } from "../componentDirtyState";

import type { BuildManifest } from "./useBuilderManifest";
import { type DeployMutators, hasSaveableSessionComponent, markComponentsDeployed, type PlatformLookups, type PlatformRecord, resolveAndDeploy, resolveDeployedVersions, withoutVersionsFor } from "./useManifestDeployment";

type Components = NonNullable<BuildManifest["components"]>;

const wf = (over: Partial<Components[number]> = {}): Components[number] => ({ name: "Detector", type: "workflow", filename: "wf.yaml", origin: "session", id: "wf-1", deployed_version: 1, ...over }) as Components[number];

const deployedOnPlatform = (name = "Detector", id = "wf-1"): PlatformLookups => ({
    agentsByName: new Map<string, PlatformRecord>(),
    workflowsByName: new Map<string, PlatformRecord>([[name, { id, name, deploymentStatus: "deployed" }]]),
    gatewaysByName: new Map<string, PlatformRecord>(),
    remoteAgentsByName: new Map<string, PlatformRecord>(),
});

const mutators = () => {
    const deployWorkflow = vi.fn().mockResolvedValue(undefined);
    const m: DeployMutators = {
        deployWorkflow,
        gatewayDeployment: { mutateAsync: vi.fn().mockResolvedValue(undefined) },
        agentDeployment: { mutateAsync: vi.fn().mockResolvedValue(undefined) },
    };
    return { m, deployWorkflow };
};

describe("resolveAndDeploy — a workflow the platform calls deployed", () => {
    test("redeploys it when its config artifact has moved past the deployed version", async () => {
        const { m, deployWorkflow } = mutators();
        const versions: ArtifactVersions = new Map([["wf.yaml", 2]]);

        const out = await resolveAndDeploy([wf()], deployedOnPlatform(), new Set(), m, versions);

        expect(deployWorkflow).toHaveBeenCalledWith({ workflowId: "wf-1", action: "deploy" });
        expect(out.deployed).toEqual(["Detector"]);
        expect(out.missing).toEqual([]);
        expect(out.failed).toEqual([]);
    });

    test("leaves it alone when the artifact matches the deployed version", async () => {
        const { m, deployWorkflow } = mutators();
        const versions: ArtifactVersions = new Map([["wf.yaml", 1]]);

        const out = await resolveAndDeploy([wf()], deployedOnPlatform(), new Set(), m, versions);

        expect(deployWorkflow).not.toHaveBeenCalled();
        expect(out.deployed).toEqual([]);
    });

    test("does not redeploy a deployed AGENT on the same evidence without server apply", async () => {
        const { m } = mutators();
        const tables: PlatformLookups = {
            agentsByName: new Map<string, PlatformRecord>([["Helper", { id: "ag-1", name: "Helper", deploymentStatus: "deployed" }]]),
            workflowsByName: new Map<string, PlatformRecord>(),
            gatewaysByName: new Map<string, PlatformRecord>(),
            remoteAgentsByName: new Map<string, PlatformRecord>(),
        };
        const agent = wf({ name: "Helper", type: "agent", filename: "ag.yaml", id: "ag-1" });
        const versions: ArtifactVersions = new Map([["ag.yaml", 2]]);

        const out = await resolveAndDeploy([agent], tables, new Set(), m, versions);

        expect(m.agentDeployment.mutateAsync).not.toHaveBeenCalled();
        expect(out.deployed).toEqual([]);
    });

    test("redeploys when version evidence is missing, rather than assuming clean", async () => {
        const { m, deployWorkflow } = mutators();

        const out = await resolveAndDeploy([wf()], deployedOnPlatform(), new Set(), m, undefined);

        expect(deployWorkflow).toHaveBeenCalledOnce();
        expect(out.deployed).toEqual(["Detector"]);
    });
});

describe("resolveAndDeploy — with server apply, a deployed agent with no evidence of what is running", () => {
    const matched: ReadonlySet<string> = new Set(["Helper"]);
    const agentTables = (): PlatformLookups => ({
        agentsByName: new Map<string, PlatformRecord>([["Helper", { id: "ag-1", name: "Helper", deploymentStatus: "deployed" }]]),
        workflowsByName: new Map<string, PlatformRecord>(),
        gatewaysByName: new Map<string, PlatformRecord>(),
        remoteAgentsByName: new Map<string, PlatformRecord>(),
    });
    const unstampedAgent = (): Components[number] => ({ name: "Helper", type: "agent", filename: "ag.yaml", origin: "session", status: "deployed", id: "ag-1" }) as Components[number];

    test("redeploys it once, although the save planned it unchanged", async () => {
        const { m } = mutators();
        const versions: ArtifactVersions = new Map([["ag.yaml", 1]]);

        const out = await resolveAndDeploy([unstampedAgent()], agentTables(), new Set(), m, versions, matched);

        expect(m.agentDeployment.mutateAsync).toHaveBeenCalledOnce();
        expect(out.deployed).toEqual(["Helper"]);
    });

    test("leaves an unstamped agent alone when this save did not prove the platform matches it", async () => {
        const { m } = mutators();
        const behind = { ...unstampedAgent(), deployed_version: 3 } as Components[number];

        const out = await resolveAndDeploy([behind], agentTables(), new Set(), m, new Map([["ag.yaml", 4]]), new Set());

        expect(m.agentDeployment.mutateAsync).not.toHaveBeenCalled();
        expect(out.deployed).toEqual([]);
    });

    test("leaves a matched component with no artifact alone, since it can never be stamped", async () => {
        const { m } = mutators();
        const noArtifact = { ...unstampedAgent(), filename: "" } as Components[number];

        const out = await resolveAndDeploy([noArtifact], agentTables(), new Set(), m, new Map(), matched);

        expect(m.agentDeployment.mutateAsync).not.toHaveBeenCalled();
        expect(out.deployed).toEqual([]);
    });

    test("leaves a stamped agent alone while artifact versions have not loaded", async () => {
        const { m } = mutators();
        const stamped = { ...unstampedAgent(), deployed_version: 1 } as Components[number];

        const out = await resolveAndDeploy([stamped], agentTables(), new Set(), m, new Map(), matched);

        expect(m.agentDeployment.mutateAsync).not.toHaveBeenCalled();
        expect(out.deployed).toEqual([]);
    });

    test("redeploys a stamped agent whose artifact has moved past the stamp", async () => {
        const { m } = mutators();
        const stamped = { ...unstampedAgent(), deployed_version: 1 } as Components[number];

        const out = await resolveAndDeploy([stamped], agentTables(), new Set(), m, new Map([["ag.yaml", 2]]), matched);

        expect(out.deployed).toEqual(["Helper"]);
    });

    test("settles Build & Activate: the deploy stamps the version the next render reads", async () => {
        const { m } = mutators();
        const versions: ArtifactVersions = new Map([["ag.yaml", 1]]);
        const manifest = { name: "b", components: [unstampedAgent()] } as BuildManifest;
        const platformStatus = new Map([["Helper", "deployed"]]);
        expect(hasSaveableSessionComponent(manifest, platformStatus, true, versions)).toBe(true);

        const out = await resolveAndDeploy(manifest.components ?? [], agentTables(), new Set(), m, versions, matched);
        const stamped = await resolveDeployedVersions(manifest.components ?? [], new Set(out.deployed), versions, async () => [1]);
        const after = markComponentsDeployed(out.settled, stamped)(manifest);

        expect(hasSaveableSessionComponent(after, platformStatus, true, stamped)).toBe(false);
    });
});

describe("a component whose save declined an edit", () => {
    test("withoutVersionsFor keeps the deploy from stamping it as running its artifact", async () => {
        const { m } = mutators();
        const agent = { name: "Helper", type: "agent", filename: "ag.yaml", origin: "session", id: "ag-1", deployed_version: 1 } as Components[number];
        const tables: PlatformLookups = {
            agentsByName: new Map<string, PlatformRecord>([["Helper", { id: "ag-1", name: "Helper", deploymentStatus: "deployed" }]]),
            workflowsByName: new Map<string, PlatformRecord>(),
            gatewaysByName: new Map<string, PlatformRecord>(),
            remoteAgentsByName: new Map<string, PlatformRecord>(),
        };
        const manifest = { name: "b", components: [agent] } as BuildManifest;
        const versions: ArtifactVersions = new Map([["ag.yaml", 2]]);

        const out = await resolveAndDeploy(manifest.components ?? [], tables, new Set(["Helper"]), m, versions, new Set());
        expect(out.deployed).toEqual(["Helper"]);

        const fetched = await resolveDeployedVersions(manifest.components ?? [], new Set(out.deployed), versions, async () => [2]);
        const stampable = withoutVersionsFor(fetched, manifest.components ?? [], new Set(["Helper"]));
        const after = markComponentsDeployed([{ name: "Helper", id: "ag-1" }], stampable)(manifest);

        expect(after.components?.[0].deployed_version).toBe(1);
    });
});
