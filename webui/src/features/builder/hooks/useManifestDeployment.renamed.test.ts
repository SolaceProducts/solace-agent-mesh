// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import { indexEntries, type PlatformLookupEntry } from "../builderUtils";

import type { BuildManifest } from "./useBuilderManifest";
import { type DeployMutators, markComponentsDeployed, type PlatformLookups, recordComponentIds, resolveAndDeploy } from "./useManifestDeployment";
import type { SaveResult } from "./useSaveOrchestration";

type Components = NonNullable<BuildManifest["components"]>;
type Kind = "workflow" | "gateway" | "a2a_agent" | "agent";
type Row = { id: string; name: string; displayName?: string; deploymentStatus?: string };

const KINDS: Kind[] = ["workflow", "gateway", "a2a_agent", "agent"];
const TABLE_OF = { workflow: "workflowsByName", gateway: "gatewaysByName", a2a_agent: "remoteAgentsByName", agent: "agentsByName" } as const;

const platform = (kind: Kind, rows: Row[]): PlatformLookups => {
    const tables: PlatformLookups = { agentsByName: new Map(), workflowsByName: new Map(), gatewaysByName: new Map(), remoteAgentsByName: new Map() };
    for (const row of rows) indexEntries(tables[TABLE_OF[kind]] as Map<string, PlatformLookupEntry>, row);
    return tables;
};

const comp = (kind: Kind, over: Partial<Components[number]> = {}): Components[number] => ({ name: "EntPetPipeline", type: kind, filename: "pipeline.yaml", origin: "session", ...over }) as Components[number];
const saved = (kind: Kind, id: string, name = "EntPetPipeline") => new Map<string, SaveResult>([[name, { name, type: kind, id } as SaveResult]]);

const mutators = () => {
    const deployWorkflow = vi.fn().mockResolvedValue(undefined);
    const gateway = vi.fn().mockResolvedValue(undefined);
    const agent = vi.fn().mockResolvedValue(undefined);
    const m: DeployMutators = { deployWorkflow, gatewayDeployment: { mutateAsync: gateway }, agentDeployment: { mutateAsync: agent } };
    return { m, deployed: { workflow: deployWorkflow, gateway, a2a_agent: agent, agent } as Record<Kind, ReturnType<typeof vi.fn>> };
};

const OLD: Row = { id: "old-1", name: "EntPetFlow", deploymentStatus: "deployed" };
const NEW: Row = { id: "new-1", name: "EntPetPipeline", deploymentStatus: "not_deployed" };
const STRANGER: Row = { id: "stranger-1", name: "EntPetPipeline", deploymentStatus: "deployed" };

describe("resolveAndDeploy: the record a deploy acts on is found by id", () => {
    test.each(KINDS)("%s: the id this session's save returned wins over a stale manifest id", async kind => {
        const { m, deployed } = mutators();

        const out = await resolveAndDeploy([comp(kind, { id: OLD.id })], platform(kind, [OLD, NEW]), new Set(), m, undefined, new Set(), saved(kind, NEW.id));

        expect(deployed[kind]).toHaveBeenCalledTimes(1);
        expect(JSON.stringify(deployed[kind].mock.calls[0])).toContain(NEW.id);
        expect(JSON.stringify(deployed[kind].mock.calls[0])).not.toContain(OLD.id);
        expect(out.resolvedIds).toEqual([{ name: "EntPetPipeline", id: NEW.id, type: kind }]);
    });

    test.each(KINDS)("%s: with no save in front of it, the manifest's own recorded id is the record, whatever it is called", async kind => {
        const { m, deployed } = mutators();
        const renamed: Row = { id: "gw-1", name: "EntOrdersGateway", deploymentStatus: "not_deployed" };

        const out = await resolveAndDeploy([comp(kind, { name: "EntOrders", id: renamed.id })], platform(kind, [renamed]), new Set(), m);

        expect(JSON.stringify(deployed[kind].mock.calls[0])).toContain(renamed.id);
        expect(out.resolvedIds).toEqual([]);
    });

    test.each(KINDS)("%s: a component with no id and no save result is never found by name, even when a record answers to it", async kind => {
        const { m, deployed } = mutators();

        const out = await resolveAndDeploy([comp(kind)], platform(kind, [STRANGER]), new Set(), m);

        expect(deployed[kind]).not.toHaveBeenCalled();
        expect(out.foreign).toEqual(["EntPetPipeline"]);
        expect(out.missing).toEqual([]);
        expect(out.settled).toEqual([]);
        expect(out.identified).toEqual([]);
    });

    test.each(KINDS)("%s: a recorded id the platform no longer lists is not replaced by a same-named record", async kind => {
        const { m, deployed } = mutators();

        const out = await resolveAndDeploy([comp(kind, { id: "deleted-1" })], platform(kind, [STRANGER]), new Set(), m);

        expect(deployed[kind]).not.toHaveBeenCalled();
        expect(out.missing).toEqual(["EntPetPipeline"]);
        expect(out.resolvedIds).toEqual([]);
    });

    test.each(KINDS)("%s: an id the save returned that the platform does not list is missing, not a same-named record", async kind => {
        const { m } = mutators();

        const out = await resolveAndDeploy([comp(kind)], platform(kind, [STRANGER]), new Set(), m, undefined, new Set(), saved(kind, "not-listed-1"));

        expect(out.missing).toEqual(["EntPetPipeline"]);
        expect(out.settled).toEqual([]);
    });

    test("a save result of another kind that shares the name is not the component's id", async () => {
        const { m, deployed } = mutators();

        const out = await resolveAndDeploy([comp("workflow", { id: NEW.id })], platform("workflow", [NEW]), new Set(), m, undefined, new Set(), saved("agent", "agent-9"));

        expect(JSON.stringify(deployed.workflow.mock.calls[0])).toContain(NEW.id);
        expect(out.resolvedIds).toEqual([]);
    });

    test.each(KINDS)("%s: a manifest id never resolves to a record of another kind", async kind => {
        const { m, deployed } = mutators();
        const otherKind: Kind = kind === "agent" ? "workflow" : "agent";
        const tables = platform(otherKind, [{ id: "ag-1", name: "Helper", deploymentStatus: "deployed" }]);

        const out = await resolveAndDeploy([comp(kind, { name: "Helper", id: "ag-1" })], tables, new Set(), m);

        expect(deployed[kind]).not.toHaveBeenCalled();
        expect(out.missing).toEqual(["Helper"]);
    });

    test("two records that share a normalized name are told apart by id", async () => {
        const { m, deployed } = mutators();
        const tables = platform("workflow", [
            { id: "wf-1", name: "Weather-Report", deploymentStatus: "deployed" },
            { id: "wf-2", name: "weather_report", deploymentStatus: "not_deployed" },
        ]);

        const out = await resolveAndDeploy([comp("workflow", { name: "weatherreport", id: "wf-2" })], tables, new Set(), m);

        expect(deployed.workflow).toHaveBeenCalledWith({ workflowId: "wf-2", action: "deploy" });
        expect(out.resolvedIds).toEqual([]);
    });

    test("an id still finds its own record when another record holds the table key its name files under", async () => {
        const { m, deployed } = mutators();
        const tables = platform("workflow", [
            { id: "wf-new", name: "weatherreport", displayName: "Weather Report (new)", deploymentStatus: "not_deployed" },
            { id: "wf-old", name: "Weather-Report", deploymentStatus: "deployed" },
        ]);

        await resolveAndDeploy([comp("workflow", { name: "weatherreport", id: "wf-new" })], tables, new Set(), m);

        expect(deployed.workflow).toHaveBeenCalledWith({ workflowId: "wf-new", action: "deploy" });
    });

    test("reports nothing to record when the manifest id already names the resolved record", async () => {
        const { m } = mutators();

        const out = await resolveAndDeploy([comp("workflow", { id: NEW.id })], platform("workflow", [NEW]), new Set(), m);

        expect(out.resolvedIds).toEqual([]);
        expect(out.settled).toEqual([{ name: "EntPetPipeline", id: NEW.id, type: "workflow" }]);
    });
});

describe("recordComponentIds", () => {
    const manifest = (components: Components) => ({ components }) as BuildManifest;

    test("replaces an id the manifest already carries", () => {
        const after = recordComponentIds([{ name: "EntPetPipeline", id: "new-1", type: "workflow" }])(manifest([comp("workflow", { id: "old-1" })]));

        expect(after.components?.[0].id).toBe("new-1");
    });

    test("leaves a component of another kind that shares the name, and unlisted ones, alone", () => {
        const after = recordComponentIds([{ name: "EntPetPipeline", id: "new-1", type: "workflow" }])(manifest([comp("agent", { id: "agent-1" }), comp("workflow", { name: "Other", id: "other-1" }), comp("workflow", { id: "old-1" })]));

        expect(after.components?.map(c => c.id)).toEqual(["agent-1", "other-1", "new-1"]);
    });
});

describe("markComponentsDeployed carries the kind", () => {
    test("settles the component of the entry's kind and leaves a same-named component of another kind alone", () => {
        const manifest = { components: [comp("agent", { id: "agent-1", status: "validated" }), comp("workflow", { id: "old-1", status: "validated" })] } as BuildManifest;

        const after = markComponentsDeployed([{ name: "EntPetPipeline", id: "new-1", type: "workflow" }])(manifest);

        expect(after.components?.map(c => [c.id, c.status])).toEqual([
            ["agent-1", "validated"],
            ["new-1", "deployed"],
        ]);
    });
});
