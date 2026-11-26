// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import { indexEntries, type PlatformLookupEntry } from "../builderUtils";

import type { BuildManifest } from "./useBuilderManifest";
import {
    deployCandidates,
    type DeployMutators,
    type FetchedKinds,
    flaggedSkippedMessage,
    foreignOnPlatformMessage,
    hasSaveableSessionComponent,
    markComponentsDeployed,
    passManifestUpdate,
    planStatusReconciliation,
    type PlatformLookups,
    recordComponentIds,
    resolveAndDeploy,
} from "./useManifestDeployment";
import type { SaveResult } from "./useSaveOrchestration";

type Components = NonNullable<BuildManifest["components"]>;
type Kind = "workflow" | "gateway" | "a2a_agent" | "agent";

const TABLE_OF = { workflow: "workflowsByName", gateway: "gatewaysByName", a2a_agent: "remoteAgentsByName", agent: "agentsByName" } as const;
const KINDS: Kind[] = ["agent", "workflow", "a2a_agent", "gateway"];

const platform = (kind: Kind, rows: Array<{ id: string; name: string; deploymentStatus: string }>): PlatformLookups => {
    const tables: PlatformLookups = { agentsByName: new Map(), workflowsByName: new Map(), gatewaysByName: new Map(), remoteAgentsByName: new Map() };
    for (const row of rows) indexEntries(tables[TABLE_OF[kind]] as Map<string, PlatformLookupEntry>, row);
    return tables;
};

const comp = (kind: string, over: Partial<Components[number]> = {}): Components[number] => ({ name: "Poet", type: kind, filename: "poet.yaml", origin: "session", ...over }) as Components[number];
const saved = (kind: string, id: string) => new Map<string, SaveResult>([["Poet", { name: "Poet", type: kind, id } as SaveResult]]);

const mutators = (failing = false) => {
    const run = failing ? vi.fn().mockRejectedValue(new Error("deployment refused")) : vi.fn().mockResolvedValue(undefined);
    const m: DeployMutators = { deployWorkflow: run, gatewayDeployment: { mutateAsync: run }, agentDeployment: { mutateAsync: run } };
    return { m, run };
};

const OWN = { id: "own-1", name: "Poet", deploymentStatus: "deployed" };
const OWN_STOPPED = { ...OWN, deploymentStatus: "not_deployed" };
const STRANGER = { id: "stranger-1", name: "Poet", deploymentStatus: "deployed" };

afterEach(() => vi.restoreAllMocks());

describe.each(KINDS)("resolveAndDeploy: %s", kind => {
    test("a record found already running settles the component, whatever status it held but error", async () => {
        for (const status of ["draft", "validated", undefined]) {
            const out = await resolveAndDeploy([comp(kind, { status, id: OWN.id })], platform(kind, [OWN]), new Set(), mutators().m);
            expect(out.settled).toEqual([{ name: "Poet", id: OWN.id, type: kind }]);
        }
    });

    test("the record the save returned is the one settled", async () => {
        const other = { id: "other-1", name: "Other", deploymentStatus: "deployed" };
        const out = await resolveAndDeploy([comp(kind)], platform(kind, [OWN, other]), new Set(), mutators().m, undefined, new Set(), saved(kind, OWN.id));

        expect(out.settled).toEqual([{ name: "Poet", id: OWN.id, type: kind }]);
    });

    test("a record the deploy started settles the component", async () => {
        const { m, run } = mutators();

        const out = await resolveAndDeploy([comp(kind, { id: OWN.id })], platform(kind, [OWN_STOPPED]), new Set(), m);

        expect(run).toHaveBeenCalledTimes(1);
        expect(out.settled).toEqual([{ name: "Poet", id: OWN.id, type: kind }]);
    });

    test("a component with no id and no save result is never credited with a same-named record", async () => {
        const { m, run } = mutators();

        const out = await resolveAndDeploy([comp(kind)], platform(kind, [{ ...STRANGER, deploymentStatus: "not_deployed" }]), new Set(), m);

        expect(run).not.toHaveBeenCalled();
        expect(out).toMatchObject({ settled: [], identified: [], resolvedIds: [], missing: [], foreign: ["Poet"] });
    });

    test("a component with no id and no save result that no record answers to is simply not on the platform", async () => {
        const out = await resolveAndDeploy([comp(kind)], platform(kind, []), new Set(), mutators().m);

        expect(out).toMatchObject({ missing: ["Poet"], foreign: [] });
    });

    test("a component with no id and no save result is missing, not foreign, when only differently named records exist", async () => {
        const out = await resolveAndDeploy([comp(kind)], platform(kind, [{ id: "other-1", name: "Other", deploymentStatus: "deployed" }]), new Set(), mutators().m);

        expect(out).toMatchObject({ missing: ["Poet"], foreign: [] });
    });

    test("a component with an id the platform does not list is missing, not foreign, whatever answers to its name", async () => {
        const out = await resolveAndDeploy([comp(kind, { id: "gone-1" })], platform(kind, [STRANGER]), new Set(), mutators().m);

        expect(out).toMatchObject({ missing: ["Poet"], foreign: [] });
    });

    test("a deploy that fails identifies the record without settling the component", async () => {
        const { m } = mutators(true);

        const out = await resolveAndDeploy([comp(kind, { id: OWN.id })], platform(kind, [OWN_STOPPED]), new Set(), m);

        expect(out.failed).toHaveLength(1);
        expect(out.settled).toEqual([]);
        expect(out.identified).toEqual([{ name: "Poet", id: OWN.id, type: kind }]);
    });

    test("replacing a recorded id is logged with both ids and no name", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        await resolveAndDeploy([comp(kind, { id: "old-1" })], platform(kind, [OWN, { id: "old-1", name: "Poet old", deploymentStatus: "deployed" }]), new Set(), mutators().m, undefined, new Set(), saved(kind, OWN.id));

        const call = warn.mock.calls.find(args => args[0] === "[builder] component id replaced");
        expect(call?.[1]).toEqual({ type: kind, from: "old-1", to: OWN.id });
        expect(JSON.stringify(call)).not.toContain("Poet");
    });

    test("recording a first id, or the same id again, logs nothing", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        await resolveAndDeploy([comp(kind)], platform(kind, [OWN]), new Set(), mutators().m, undefined, new Set(), saved(kind, OWN.id));
        await resolveAndDeploy([comp(kind, { id: OWN.id })], platform(kind, [OWN]), new Set(), mutators().m);

        expect(warn.mock.calls.some(args => args[0] === "[builder] component id replaced")).toBe(false);
    });
});

describe("planStatusReconciliation", () => {
    const ALL: FetchedKinds = { agent: true, workflow: true, gateway: true, a2aAgent: true };
    const FETCHED_KEY: Record<Kind, keyof FetchedKinds> = { agent: "agent", workflow: "workflow", a2a_agent: "a2aAgent", gateway: "gateway" };
    const plan = (components: Components, fetchedKinds: FetchedKinds, statusById: ReadonlyMap<string, string> = new Map()) =>
        planStatusReconciliation({ components, statusById, statusByName: new Map(), fetchedKinds, savedNames: new Set(), liveCards: [], isRunningStale: () => false });

    test.each(KINDS)("%s: absence from its own list counts only when that list was fetched", kind => {
        const deployed = comp(kind, { status: "deployed", id: "own-1" });
        const onlyOthers = Object.fromEntries(Object.entries(ALL).map(([key, value]) => [key, key === FETCHED_KEY[kind] ? false : value])) as unknown as FetchedKinds;
        const onlyThis = Object.fromEntries(Object.entries(ALL).map(([key]) => [key, key === FETCHED_KEY[kind]])) as unknown as FetchedKinds;

        expect(plan([deployed], onlyOthers).downgrade).toEqual([]);
        expect(plan([deployed], onlyThis).downgrade).toEqual([deployed]);
    });

    test.each(KINDS)("%s: a validated component upgrades when its own record is deployed", kind => {
        const validated = comp(kind, { status: "validated", id: "own-1" });

        expect(plan([validated], ALL, new Map([["own-1", "deployed"]])).upgrade).toEqual([validated]);
        expect(plan([validated], ALL, new Map([["other-1", "deployed"]])).upgrade).toEqual([]);
    });

    test.each([
        ["a platform-origin component", { origin: "platform", type: "agent" }],
        ["a skill", { origin: "session", type: "skill" }],
        ["a connector", { origin: "session", type: "connector" }],
    ])("%s is never reconciled", (_label, over) => {
        const component = comp("agent", { status: "deployed", id: "own-1", ...over });
        const validated = { ...component, status: "validated" };

        expect(plan([component], ALL).downgrade).toEqual([]);
        expect(plan([validated], ALL, new Map([["own-1", "deployed"]])).upgrade).toEqual([]);
    });

    test("a session agent beside them is reconciled", () => {
        const session = comp("agent", { status: "deployed", id: "own-1" });

        expect(plan([session, comp("skill", { name: "S", status: "deployed" }), comp("connector", { name: "C", status: "deployed" })], ALL).downgrade).toEqual([session]);
    });
});

describe("hasSaveableSessionComponent: a status short of deployed has the action, except the builder's own error flag", () => {
    const manifestOf = (component: Components[number]): BuildManifest => ({ name: "b", components: [component] });
    const ownRecordDeployed = new Map([["own-1", "deployed"]]);

    test.each(KINDS)("%s: whatever the id and whether the record runs", kind => {
        for (const status of ["draft", "validated", "blocked", undefined]) {
            for (const id of [undefined, "own-1", "gone-1"]) {
                expect(hasSaveableSessionComponent(manifestOf(comp(kind, { status, id })), new Map([["Poet", "deployed"]]), false, new Map([["poet.yaml", 1]]), ownRecordDeployed), `${kind} ${status} ${id}`).toBe(true);
            }
        }
    });

    test.each(KINDS)("%s: error waits for the builder's fix and is never offered, whatever the platform holds", kind => {
        for (const id of [undefined, "own-1", "gone-1"]) {
            for (const byName of [new Map(), new Map([["Poet", "deployed"]])]) {
                const offered = hasSaveableSessionComponent(manifestOf(comp(kind, { status: "error", id, deployed_version: 1 })), byName, false, new Map([["poet.yaml", 1]]), ownRecordDeployed);
                expect(offered, `${kind} ${id}`).toBe(false);
            }
        }
    });

    test.each(["agent", "a2a_agent", "gateway"] as const)("%s: a deployed component whose own record runs, and is current, has nothing to offer", kind => {
        const component = comp(kind, { status: "deployed", id: "own-1", deployed_version: 1 });

        expect(hasSaveableSessionComponent(manifestOf(component), new Map(), false, new Map([["poet.yaml", 1]]), ownRecordDeployed)).toBe(false);
    });
});

describe("markComponentsDeployed", () => {
    test("a real deploy of a component flagged error clears the flag: the builder's failure stands only until a deploy succeeds", () => {
        const flagged = { name: "b", components: [comp("agent", { status: "error" })] } as BuildManifest;

        expect(markComponentsDeployed([{ name: "Poet", id: "p-1", type: "agent" }])(flagged).components?.[0]).toMatchObject({ status: "deployed", id: "p-1" });
    });
});

describe("deployCandidates", () => {
    test("a component the builder flagged error is never a candidate, whatever drives the pass", () => {
        const components = [comp("agent", { status: "validated" }), comp("agent", { name: "Broken", status: "error" }), comp("skill", { name: "Skill" })];

        expect(deployCandidates(components, new Set(), false).map(c => c.name)).toEqual(["Poet"]);
        expect(deployCandidates(components, new Set(["Poet", "Broken"]), true).map(c => c.name)).toEqual(["Poet"]);
    });
});

describe("foreignOnPlatformMessage", () => {
    test("names the way forward without claiming the build should save, whose record it is, or that anything should be removed", () => {
        expect(foreignOnPlatformMessage(["Poet"])).toBe("Poet already exists on the platform and this build could not confirm it is its own; open it from the list to keep working on it, or rename this one and build again.");
        expect(foreignOnPlatformMessage(["Poet", "Flow"])).toBe("Poet, Flow already exist on the platform and this build could not confirm they are its own; open them from the list to keep working on them, or rename these and build again.");
    });
});

describe("flaggedSkippedMessage", () => {
    test("names every component a deploy loop stepped over", () => {
        expect(flaggedSkippedMessage(["Poet", "Flow"])).toBe("Not deployed, flagged with an error by the builder: Poet, Flow.");
    });
});

describe("passManifestUpdate", () => {
    const manifest = { name: "b", loaded_for_edit: true, components: [comp("agent", { status: "draft" }), comp("agent", { name: "Other", status: "error" })] } as BuildManifest;

    test("marks the settled components deployed with their ids and records only the ids of the failed", () => {
        const next = passManifestUpdate([{ name: "Poet", id: "p-1", type: "agent" }], [{ name: "Other", id: "o-1", type: "agent" }], undefined, new Set(["Poet"]))(manifest);

        expect(next.components?.[0]).toMatchObject({ status: "deployed", id: "p-1" });
        expect(next.components?.[1]).toMatchObject({ status: "error", id: "o-1" });
    });

    test("a pass that deployed something ends an Edit-with-AI load", () => {
        const next = passManifestUpdate([{ name: "Poet", id: "p-1", type: "agent" }], [], undefined, new Set(["Poet"]))(manifest);

        expect("loaded_for_edit" in next).toBe(false);
    });

    test("a pass that only found its components running keeps an untouched load a load", () => {
        const next = passManifestUpdate([{ name: "Poet", id: "p-1", type: "agent" }], [], undefined, new Set())(manifest);

        expect(next.loaded_for_edit).toBe(true);
        expect(next.components?.[0]).toMatchObject({ status: "deployed", id: "p-1" });
    });

    test("stamps the version only on the components the pass deployed", () => {
        const both = { name: "b", components: [comp("agent", { filename: "poet.yaml" }), comp("agent", { name: "Other", filename: "other.yaml" })] } as BuildManifest;
        const versions = new Map([
            ["poet.yaml", 3],
            ["other.yaml", 4],
        ]);

        const next = passManifestUpdate(
            [
                { name: "Poet", id: "p-1", type: "agent" },
                { name: "Other", id: "o-1", type: "agent" },
            ],
            [],
            versions,
            new Set(["Poet"])
        )(both);

        expect(next.components?.map(c => c.deployed_version)).toEqual([3, undefined]);
    });

    test("records an id only for the component of the entry's kind", () => {
        const both = { name: "b", components: [comp("agent", { id: "agent-1" }), comp("workflow", { id: "wf-old" })] } as BuildManifest;

        const next = recordComponentIds([{ name: "Poet", id: "wf-new", type: "workflow" }])(both);

        expect(next.components?.map(c => c.id)).toEqual(["agent-1", "wf-new"]);
    });
});
