// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, test, vi } from "vitest";

import { indexEntries, type PlatformLookupEntry } from "./builderUtils";
import { CanvasViewProvider } from "./canvas/canvasViewContext";
import { useDerivedCardStatus } from "./canvas/useDerivedCardStatus";
import { isRunningStaleConfig } from "./componentDirtyState";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import {
    type DeployMutators,
    foldDeploymentStatuses,
    foldDeploymentStatusesById,
    hasSaveableSessionComponent,
    markComponentsDeployed,
    planStatusReconciliation,
    type PlatformLookups,
    reconcileComponentStatuses,
    resolveAndDeploy,
    resolveDeployedVersions,
} from "./hooks/useManifestDeployment";
import type { SaveResult } from "./hooks/useSaveOrchestration";
import { isEffectiveTargetDeployed } from "./TestModePanel";

const OWN = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const OTHER = "01a160ce-7f96-7e82-80dd-7e47b3032891";
const GONE = "01a160ff-0000-7000-8000-000000000001";
const CREATED = "01a160ee-0000-7000-8000-000000000002";
const NAME = "Poet";

type Kind = "agent" | "workflow" | "a2a_agent" | "gateway";
type IdState = "none" | "own" | "stale";
type Platform = "own record live" | "own record stopped" | "record deleted" | "replaced by a same-named record";
type Mesh = "card present" | "card absent" | "own card served regardless";
type Status = "draft" | "validated" | "deployed" | "error" | "blocked" | undefined;
type Stamp = "unstamped" | "stamped";

const KINDS: Kind[] = ["agent", "workflow", "a2a_agent", "gateway"];
const ID_STATES: IdState[] = ["none", "own", "stale"];
const PLATFORMS: Platform[] = ["own record live", "own record stopped", "record deleted", "replaced by a same-named record"];
const MESHES: Mesh[] = ["card present", "card absent", "own card served regardless"];
const STATUSES: Status[] = ["draft", "validated", "deployed", "error", "blocked", undefined];
const STAMPS: Stamp[] = ["unstamped", "stamped"];

interface Row {
    kind: Kind;
    idState: IdState;
    platform: Platform;
    mesh: Mesh;
    status: Status;
    stamp: Stamp;
}

const hasCard = (kind: Kind) => kind !== "gateway";
const meshesOf = (kind: Kind) => MESHES.filter(mesh => hasCard(kind) || mesh === "card absent");
const ROWS: Row[] = KINDS.flatMap(kind => ID_STATES.flatMap(idState => PLATFORMS.flatMap(platform => meshesOf(kind).flatMap(mesh => STATUSES.flatMap(status => STAMPS.map(stamp => ({ kind, idState, platform, mesh, status, stamp })))))));
const label = (r: Row) => `${r.kind} | id ${r.idState} | ${r.platform} | ${r.mesh} | manifest ${r.status ?? "without status"} | ${r.stamp}`;

type Component = NonNullable<BuildManifest["components"]>[number];
interface PlatformRecord {
    id: string;
    deploymentStatus: string;
}
interface World {
    manifest: BuildManifest;
    records: PlatformRecord[];
    servedIds: string[];
}

const cardNameOf = (kind: Kind, id: string) => `${kind === "workflow" ? "workflow" : "agent"}_${id.replaceAll("-", "_")}`;
const versionsOf = () => new Map([["poet.yaml", 1]]);

function componentOf(r: Row): Component {
    return {
        filename: "poet.yaml",
        type: r.kind,
        name: NAME,
        origin: "session",
        ...(r.status === undefined ? {} : { status: r.status }),
        ...(r.idState === "none" ? {} : { id: r.idState === "own" ? OWN : GONE }),
        ...(r.stamp === "stamped" ? { deployed_version: 1 } : {}),
    } as Component;
}

function recordsOf(r: Row): PlatformRecord[] {
    if (r.platform === "own record live") return [{ id: OWN, deploymentStatus: "deployed" }];
    if (r.platform === "own record stopped") return [{ id: OWN, deploymentStatus: "not_deployed" }];
    if (r.platform === "replaced by a same-named record") return [{ id: OTHER, deploymentStatus: "deployed" }];
    return [];
}

function servedOf(r: Row, records: PlatformRecord[]): string[] {
    if (r.mesh === "card absent") return [];
    const live = records.filter(rec => rec.deploymentStatus === "deployed").map(rec => rec.id);
    return r.mesh === "own card served regardless" ? [...new Set([...live, OWN])] : live;
}

function tablesOf(kind: Kind, records: PlatformRecord[]): PlatformLookups {
    const tables: PlatformLookups = { agentsByName: new Map(), workflowsByName: new Map(), gatewaysByName: new Map(), remoteAgentsByName: new Map() };
    const table = { agent: tables.agentsByName, workflow: tables.workflowsByName, a2a_agent: tables.remoteAgentsByName, gateway: tables.gatewaysByName }[kind] as Map<string, PlatformLookupEntry>;
    for (const rec of records) indexEntries(table, { id: rec.id, name: NAME, deploymentStatus: rec.deploymentStatus });
    return tables;
}

const componentIn = (w: World) => w.manifest.components?.[0] as Component;
const cardsOf = (w: World) => w.servedIds.map(id => ({ name: cardNameOf(componentIn(w).type as Kind, id) }));

function platformMaps(w: World) {
    const tables = tablesOf(componentIn(w).type as Kind, w.records);
    const maps = [tables.agentsByName, tables.workflowsByName, tables.gatewaysByName, tables.remoteAgentsByName];
    return { byName: foldDeploymentStatuses(maps), byId: foldDeploymentStatusesById(maps) };
}

const ALL_FETCHED = { agent: true, workflow: true, gateway: true, a2aAgent: true };

function settle(w: World): World {
    let manifest = w.manifest;
    for (let pass = 0; pass < 3; pass++) {
        const { byName, byId } = platformMaps({ ...w, manifest });
        const plan = planStatusReconciliation({
            components: manifest.components ?? [],
            statusById: byId,
            statusByName: byName,
            fetchedKinds: ALL_FETCHED,
            savedNames: new Set(),
            liveCards: cardsOf({ ...w, manifest }),
            isRunningStale: c => isRunningStaleConfig(c, versionsOf(), false),
        });
        if (plan.upgrade.length === 0 && plan.downgrade.length === 0) break;
        manifest = reconcileComponentStatuses(new Set(plan.upgrade.map(c => c.name)), new Set(plan.downgrade.map(c => c.name)))(manifest);
    }
    return { ...w, manifest };
}

interface View {
    active: boolean;
    testable: boolean;
    offered: boolean;
}

function view(w: World): View {
    const component = componentIn(w);
    const cards = cardsOf(w);
    const { byName, byId } = platformMaps(w);
    const data = {
        id: component.id,
        name: NAME,
        filename: component.filename,
        type: component.type,
        status: component.status,
        origin: "session",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
    } as ComponentCanvasData;
    const wrapper = ({ children }: { children: ReactNode }) => (
        <CanvasViewProvider builderActiveTab="build" testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} liveAgentCards={cards}>
            {children}
        </CanvasViewProvider>
    );
    const active = renderHook(() => useDerivedCardStatus(data, component), { wrapper }).result.current.isActive;
    const testable = isEffectiveTargetDeployed({ type: component.type as "agent", name: NAME }, [component], cards, { versions: versionsOf(), serverApplyEnabled: false });
    const offered = hasSaveableSessionComponent(w.manifest, byName, false, versionsOf(), byId);
    return { active, testable, offered };
}

const mutators = (): DeployMutators => ({
    deployWorkflow: vi.fn().mockResolvedValue(undefined),
    gatewayDeployment: { mutateAsync: vi.fn().mockResolvedValue(undefined) },
    agentDeployment: { mutateAsync: vi.fn().mockResolvedValue(undefined) },
});

async function buildAndActivate(w: World): Promise<World> {
    const component = componentIn(w);
    const records = w.records.length === 0 ? [{ id: CREATED, deploymentStatus: "not_deployed" }] : w.records;
    const savedAs = new Map([[NAME, { name: NAME, type: component.type, id: records[0].id } as SaveResult]]);
    const out = await resolveAndDeploy([component], tablesOf(component.type as Kind, records), new Set([NAME]), mutators(), versionsOf(), new Set(), savedAs);
    expect(out.missing).toEqual([]);
    expect(out.failed).toEqual([]);
    const versions = await resolveDeployedVersions([component], new Set(out.deployed), versionsOf(), async () => [1]);
    const manifest = markComponentsDeployed(out.settled, versions)(w.manifest);
    const deployedRecords = records.map(rec => ({ ...rec, deploymentStatus: "deployed" }));
    return settle({ manifest, records: deployedRecords, servedIds: deployedRecords.map(rec => rec.id) });
}

describe("Build & Activate leaves no component stuck", () => {
    test("the table is the full product of kind, id, platform, mesh, status and stamp", () => {
        const expected = KINDS.reduce((sum, kind) => sum + ID_STATES.length * PLATFORMS.length * meshesOf(kind).length * STATUSES.length * STAMPS.length, 0);
        expect(ROWS).toHaveLength(expected);
        expect(new Set(ROWS.map(label)).size).toBe(ROWS.length);
    });

    test.each(ROWS.map(r => [label(r), r] as const))("%s", async (_name, r) => {
        const records = recordsOf(r);
        const settled = settle({ manifest: { name: "b", components: [componentOf(r)] }, records, servedIds: servedOf(r, records) });
        const before = view(settled);

        if (r.idState === "none" && r.status !== "deployed") expect(before.active).toBe(false);
        const ownRecordRuns = r.idState === "own" && (r.platform === "own record live" || r.mesh === "own card served regardless");
        if (r.idState !== "none" && !ownRecordRuns) expect(before.active).toBe(false);

        if (r.status === "blocked" || r.status === "error") return;

        expect((before.active && before.testable) || before.offered, JSON.stringify(before)).toBe(true);

        const after = view(await buildAndActivate(settled));
        expect(after, JSON.stringify(after)).toEqual({ active: true, testable: true, offered: false });
    });
});
