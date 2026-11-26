// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, test } from "vitest";

import { brokerInstanceName, isSessionComponentLive } from "./builderUtils";
import { CanvasViewProvider } from "./canvas/canvasViewContext";
import { useDerivedCardStatus } from "./canvas/useDerivedCardStatus";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import { ownPlatformStatus, shouldUpgradeToDeployed } from "./hooks/useManifestDeployment";
import { isEffectiveTargetDeployed } from "./TestModePanel";

const OWN_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const OTHER_ID = "01a160ce-7f96-7e82-80dd-7e47b3032891";
const GONE_ID = "01a160ff-0000-7000-8000-000000000001";
const NAME = "Haiku Poet";

type Kind = "agent" | "workflow" | "a2a_agent" | "gateway" | "skill" | "connector";
type RecordedId = "none" | "own" | "deleted or replaced record";
type Mesh = "own instance" | "own instance beside unrelated cards" | "same-named instance of another record" | "a plain card named like the component" | "nothing";
type Status = "draft" | "validated" | "deployed";

const KINDS: Kind[] = ["agent", "workflow", "a2a_agent", "gateway", "skill", "connector"];
const RECORDED_IDS: RecordedId[] = ["none", "own", "deleted or replaced record"];
const MESH: Mesh[] = ["own instance", "own instance beside unrelated cards", "same-named instance of another record", "a plain card named like the component", "nothing"];
const STATUSES: Status[] = ["draft", "validated", "deployed"];
const PUBLISHES_CARD = new Set<Kind>(["agent", "workflow", "a2a_agent"]);

const idOf = (recorded: RecordedId) => ({ none: undefined, own: OWN_ID, "deleted or replaced record": GONE_ID })[recorded];
const holdsOwnInstance = (mesh: Mesh) => mesh === "own instance" || mesh === "own instance beside unrelated cards";
const cardsFor = (kind: Kind, mesh: Mesh) => {
    const brokerKind = kind === "workflow" ? "workflow" : "agent";
    const own = { name: `${brokerKind}_${OWN_ID.replaceAll("-", "_")}`, displayName: NAME };
    const unrelated = [
        { name: `${brokerKind}_${OTHER_ID.replaceAll("-", "_")}`, displayName: "Other" },
        { name: "OrchestratorAgent", displayName: "Orchestrator" },
    ];
    if (mesh === "own instance") return [own];
    if (mesh === "own instance beside unrelated cards") return [unrelated[0], own, unrelated[1]];
    if (mesh === "same-named instance of another record") return [{ name: `${brokerKind}_${OTHER_ID.replaceAll("-", "_")}`, displayName: NAME }];
    if (mesh === "a plain card named like the component") return [{ name: NAME, displayName: NAME }];
    return [];
};

interface Row {
    kind: Kind;
    recorded: RecordedId;
    mesh: Mesh;
    status: Status;
    live: boolean;
    activeOnCanvas: boolean;
    testable: boolean;
}

function row(kind: Kind, recorded: RecordedId, mesh: Mesh, status: Status): Row {
    const live = PUBLISHES_CARD.has(kind) && recorded === "own" && holdsOwnInstance(mesh);
    const claimsDeployed = status === "deployed";
    const testable = PUBLISHES_CARD.has(kind) ? claimsDeployed || live : claimsDeployed;
    return { kind, recorded, mesh, status, live, activeOnCanvas: claimsDeployed || live, testable };
}

const ROWS = KINDS.flatMap(kind => RECORDED_IDS.flatMap(recorded => MESH.flatMap(mesh => STATUSES.map(status => row(kind, recorded, mesh, status)))));
const label = (r: Row) => `${r.kind} | id ${r.recorded} | mesh ${r.mesh} | manifest ${r.status}`;

function canvasActive(r: Row): boolean {
    const component = { id: idOf(r.recorded), name: NAME, type: r.kind, status: r.status, origin: "session" };
    const data = {
        id: component.id,
        name: NAME,
        filename: "c.yaml",
        type: r.kind,
        status: r.status,
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
        <CanvasViewProvider builderActiveTab="build" testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} liveAgentCards={cardsFor(r.kind, r.mesh)}>
            {children}
        </CanvasViewProvider>
    );
    return renderHook(() => useDerivedCardStatus(data, component), { wrapper }).result.current.isActive;
}

function testGate(r: Row): boolean {
    const component = { id: idOf(r.recorded), name: NAME, type: r.kind, filename: "c.yaml", status: r.status, origin: "session" };
    return isEffectiveTargetDeployed({ type: r.kind as "agent", name: NAME }, [component], cardsFor(r.kind, r.mesh), { versions: undefined, serverApplyEnabled: false });
}

function selfHealUpgrades(r: Row): boolean {
    const component = { id: idOf(r.recorded) };
    const platformRecordIdsDeployed = new Map<string, string>();
    if (holdsOwnInstance(r.mesh)) platformRecordIdsDeployed.set(OWN_ID, "deployed");
    if (r.mesh === "same-named instance of another record") platformRecordIdsDeployed.set(OTHER_ID, "deployed");
    return shouldUpgradeToDeployed({ status: r.status, platformStatus: ownPlatformStatus(component, platformRecordIdsDeployed), runningStaleConfig: false });
}

describe("session component liveness: every consumer reads the one predicate", () => {
    test("the table covers every kind, recorded id, mesh state and manifest status", () => {
        expect(ROWS).toHaveLength(KINDS.length * RECORDED_IDS.length * MESH.length * STATUSES.length);
    });

    test.each(ROWS.map(r => [label(r), r] as const))("%s", (_name, r) => {
        const component = { id: idOf(r.recorded), name: NAME, type: r.kind };
        expect(isSessionComponentLive(component, cardsFor(r.kind, r.mesh))).toBe(r.live);
        expect(canvasActive(r)).toBe(r.activeOnCanvas);
        expect(testGate(r)).toBe(r.testable);
        const platformHoldsOwnRecord = r.recorded === "own" && holdsOwnInstance(r.mesh);
        expect(selfHealUpgrades(r)).toBe(r.status === "validated" && platformHoldsOwnRecord);
    });

    test("a never-deployed session (draft, no id) next to a same-named live component reads neither Active nor testable", () => {
        const r = row("agent", "none", "same-named instance of another record", "draft");
        expect(canvasActive(r)).toBe(false);
        expect(testGate(r)).toBe(false);
        expect(selfHealUpgrades(r)).toBe(false);
    });

    test("the card name is the broker instance name the platform mints from the id", () => {
        expect(brokerInstanceName("agent", OWN_ID)).toBe("agent_01a160d0_2474_7426_830a_ae0397fcdc9a");
        expect(brokerInstanceName("a2a_agent", OWN_ID)).toBe("agent_01a160d0_2474_7426_830a_ae0397fcdc9a");
        expect(brokerInstanceName("workflow", OWN_ID)).toBe("workflow_01a160d0_2474_7426_830a_ae0397fcdc9a");
        expect(brokerInstanceName("gateway", OWN_ID)).toBeUndefined();
        expect(brokerInstanceName(undefined, OWN_ID)).toBeUndefined();
    });
});
