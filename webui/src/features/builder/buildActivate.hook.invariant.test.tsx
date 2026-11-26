// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { respondingAsV2, v2PageOf } from "@/testing/helpers/platformResponses";

import type { BuildManifest } from "./hooks/useBuilderManifest";
import type { SaveResult } from "./hooks/useSaveOrchestration";

const NAME = "Poet";
const RECORD = "rec-1";
const GONE = "rec-gone";

type Kind = "agent" | "workflow" | "a2a_agent" | "gateway";
type IdState = "none" | "own" | "stale";
type SaveOutcome = "written" | "matched" | "skipped" | "failed";
type Status = "draft" | "validated" | "error" | "deployed";

const KINDS: Kind[] = ["agent", "workflow", "a2a_agent", "gateway"];
const ID_STATES: IdState[] = ["none", "own", "stale"];
const OUTCOMES: SaveOutcome[] = ["written", "matched", "skipped", "failed"];
const STATUSES: Status[] = ["draft", "validated", "error", "deployed"];
const SERVER_APPLY = [true, false] as const;

interface Row {
    kind: Kind;
    idState: IdState;
    outcome: SaveOutcome;
    serverApply: boolean;
    status: Status;
}

const ROWS: Row[] = KINDS.flatMap(kind => ID_STATES.flatMap(idState => OUTCOMES.flatMap(outcome => SERVER_APPLY.flatMap(serverApply => STATUSES.map(status => ({ kind, idState, outcome, serverApply, status }))))));
const label = (r: Row) => `${r.kind} | id ${r.idState} | save ${r.outcome} | server apply ${r.serverApply ? "on" : "off"} | manifest ${r.status}`;

const LIST_PATH: Record<Kind, string> = { agent: "/api/v2/platform/agents", workflow: "/api/v2/platform/workflows", a2a_agent: "/api/v2/platform/remoteAgents", gateway: "/api/v2/platform/entrypoints" };

describe("Build & Activate through the real deploy pass", () => {
    let useManifestDeployment: typeof import("./hooks/useManifestDeployment").useManifestDeployment;
    const platformGet = vi.fn();
    const saveManifest = vi.fn();
    const deployAgent = vi.fn();
    const deployGateway = vi.fn();
    const deployWorkflow = vi.fn();
    const listVersions = vi.fn();
    const setOutcome = vi.fn();
    const notify = vi.fn();
    let recordKind: Kind = "agent";
    let recordStatus = "not_deployed";
    let extraRecords: Array<{ id: string; name: string; deploymentStatus: string }> = [];

    beforeAll(async () => {
        vi.resetModules();
        vi.doMock("@/api/client", () => ({ api: { platform: { get: respondingAsV2(platformGet) } } }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ data: [] }) }));
        vi.doMock("@/api/agent-deployments", () => ({ useAgentDeploymentMutation: () => ({ mutateAsync: deployAgent }) }));
        vi.doMock("@/api/gateways", () => ({ useGatewayDeploymentMutation: () => ({ mutateAsync: deployGateway }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: deployWorkflow, isLoading: false }) }));
        vi.doMock("@/providers/contexts", () => ({ useChatContext: () => ({ addNotification: (...args: unknown[]) => notify(...args), displayError: vi.fn(), sessionId: "s1", isResponding: false }) }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => false }));
        vi.doMock("@/api/builder", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/builder")>()), listManifestVersions: (...args: unknown[]) => listVersions(...args) }));
        useManifestDeployment = (await import("./hooks/useManifestDeployment")).useManifestDeployment;
    });

    beforeEach(() => {
        vi.clearAllMocks();
        recordStatus = "not_deployed";
        extraRecords = [];
        listVersions.mockResolvedValue([3]);
        saveManifest.mockResolvedValue({ ok: true });
        for (const spy of [deployAgent, deployGateway, deployWorkflow]) spy.mockResolvedValue(undefined);
        platformGet.mockImplementation((path: string) => {
            const records = path.startsWith(LIST_PATH[recordKind]) ? [{ id: RECORD, name: NAME, deploymentStatus: recordStatus }, ...extraRecords] : [];
            return Promise.resolve(v2PageOf(path, records));
        });
    });

    const wrapper = () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
    };

    const componentOf = (r: Row) => ({ filename: "poet.yaml", type: r.kind, name: NAME, origin: "session", status: r.status, ...(r.idState === "none" ? {} : { id: r.idState === "own" ? RECORD : GONE }) });
    const deploysOf = (kind: Kind) => ({ agent: deployAgent, a2a_agent: deployAgent, workflow: deployWorkflow, gateway: deployGateway })[kind];
    const writtenBy = (call: unknown[], component: object) => (call[0] as (prev: BuildManifest) => BuildManifest)({ name: "b", components: [component] } as BuildManifest).components?.[0];

    async function runPass(r: Row) {
        recordKind = r.kind;
        const component = componentOf(r);
        const saved = new Map<string, SaveResult>(r.outcome === "written" || r.outcome === "matched" ? [[NAME, { name: NAME, type: r.kind, id: RECORD } as SaveResult]] : []);
        const { result } = renderHook(
            () =>
                useManifestDeployment({
                    manifest: { name: "b", components: [component] } as BuildManifest,
                    saveManifest,
                    savedComponents: new Map(),
                    allSaved: false,
                    autoSavedInThisFlowRef: { current: false },
                    autoDeployTriggeredRef: { current: false },
                    reportErrorToAgent: vi.fn(),
                    setError: vi.fn(),
                    setBlockedError: vi.fn(),
                    setDeployOutcomeError: setOutcome,
                    clearUnlessDeployOutcome: vi.fn(),
                    matchedOnPlatformRef: { current: r.outcome === "matched" ? [NAME] : [] },
                    savedResultsRef: { current: saved },
                }),
            { wrapper: wrapper() }
        );
        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 20));
        });
        saveManifest.mockClear();
        listVersions.mockClear();
        const changed = r.serverApply ? (r.outcome === "written" ? [NAME] : []) : undefined;
        await act(async () => {
            await result.current.handleDeployManifest(changed, { notWritten: r.outcome === "failed" ? [NAME] : [] });
        });
        return component;
    }

    test("the table is the full product of kind, id, save outcome, server apply and status", () => {
        expect(ROWS).toHaveLength(KINDS.length * ID_STATES.length * OUTCOMES.length * SERVER_APPLY.length * STATUSES.length);
    });

    test.each(ROWS.map(r => [label(r), r] as const))("%s", async (_name, r) => {
        const component = await runPass(r);
        const credited = r.status !== "error" && (r.outcome === "written" || r.outcome === "matched" || r.idState === "own");

        if (credited) {
            expect(JSON.stringify(deploysOf(r.kind).mock.calls)).toContain(RECORD);
            const settled = saveManifest.mock.calls.map(call => writtenBy(call, component)).filter(c => c?.status === "deployed");
            expect(settled).toHaveLength(1);
            expect(settled[0]).toMatchObject({ id: RECORD, status: "deployed" });
        } else {
            expect(deploysOf(r.kind)).not.toHaveBeenCalled();
            const writes = saveManifest.mock.calls.map(call => writtenBy(call, component));
            expect(writes.some(c => c?.id === RECORD || (c?.status === "deployed" && component.status !== "deployed"))).toBe(false);
        }
    });

    describe("a deploy that fails", () => {
        test.each(KINDS)("%s: records the id the save returned, and no status", async kind => {
            const r: Row = { kind, idState: "stale", outcome: "written", serverApply: true, status: "draft" };
            for (const spy of [deployAgent, deployGateway, deployWorkflow]) spy.mockRejectedValue(new Error("deployment refused"));

            const component = await runPass(r);

            const writes = saveManifest.mock.calls.map(call => writtenBy(call, component));
            expect(writes).toHaveLength(1);
            expect(writes[0]).toMatchObject({ id: RECORD, status: "draft" });
        });

        test.each(KINDS)("%s: records nothing for a component the save did not return an id for", async kind => {
            const r: Row = { kind, idState: "stale", outcome: "failed", serverApply: true, status: "draft" };
            for (const spy of [deployAgent, deployGateway, deployWorkflow]) spy.mockRejectedValue(new Error("deployment refused"));

            await runPass(r);

            expect(saveManifest).not.toHaveBeenCalled();
        });
    });

    describe("what a pass stamps", () => {
        test("a component found already running is settled without a version this pass did not deploy", async () => {
            recordStatus = "deployed";
            const r: Row = { kind: "agent", idState: "own", outcome: "skipped", serverApply: false, status: "validated" };

            const component = await runPass(r);

            const writes = saveManifest.mock.calls.map(call => writtenBy(call, component));
            expect(deployAgent).not.toHaveBeenCalled();
            expect(listVersions).not.toHaveBeenCalled();
            expect(writes).toHaveLength(1);
            expect(writes[0]).toMatchObject({ id: RECORD, status: "deployed" });
            expect(writes[0]?.deployed_version).toBeUndefined();
        });

        test("a component the pass deployed is stamped with the version it published", async () => {
            const component = await runPass({ kind: "agent", idState: "own", outcome: "written", serverApply: true, status: "validated" });

            expect(writtenBy(saveManifest.mock.calls[0], component)).toMatchObject({ status: "deployed", deployed_version: 3 });
        });

        test("a component whose save declined an edit is settled without a version the platform does not hold", async () => {
            recordKind = "agent";
            const component = componentOf({ kind: "agent", idState: "own", outcome: "written", serverApply: true, status: "validated" });
            const { result } = renderHook(
                () =>
                    useManifestDeployment({
                        manifest: { name: "b", components: [component] } as BuildManifest,
                        saveManifest,
                        savedComponents: new Map(),
                        allSaved: false,
                        autoSavedInThisFlowRef: { current: false },
                        autoDeployTriggeredRef: { current: false },
                        reportErrorToAgent: vi.fn(),
                        setError: vi.fn(),
                        setBlockedError: vi.fn(),
                        setDeployOutcomeError: vi.fn(),
                        clearUnlessDeployOutcome: vi.fn(),
                        declinedOnPlatformRef: { current: [NAME] },
                        savedResultsRef: { current: new Map([[NAME, { name: NAME, type: "agent", id: RECORD } as SaveResult]]) },
                    }),
                { wrapper: wrapper() }
            );
            await waitFor(() => expect(platformGet).toHaveBeenCalled());
            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 20));
            });
            saveManifest.mockClear();

            await act(async () => {
                await result.current.handleDeployManifest([NAME], {});
            });

            const written = writtenBy(saveManifest.mock.calls[0], component);
            expect(written).toMatchObject({ id: RECORD, status: "deployed" });
            expect(written?.deployed_version).toBeUndefined();
        });
    });

    describe("an in-flow pass that fails part-way", () => {
        const inFlow = async (declined: string[] = []) => {
            recordKind = "agent";
            const agent = { filename: "poet.yaml", type: "agent", name: NAME, origin: "session", status: "validated" };
            const gateway = { filename: "gw.yaml", type: "gateway", name: "Gw", origin: "session", status: "validated" };
            const savedComponents = new Map<string, SaveResult>([
                [NAME, { name: NAME, type: "agent", id: RECORD } as SaveResult],
                ["Gw", { name: "Gw", type: "gateway", id: "gw-1" } as SaveResult],
            ]);
            const { result } = renderHook(
                () =>
                    useManifestDeployment({
                        manifest: { name: "b", components: [agent, gateway] } as BuildManifest,
                        saveManifest,
                        savedComponents,
                        allSaved: false,
                        autoSavedInThisFlowRef: { current: false },
                        autoDeployTriggeredRef: { current: false },
                        reportErrorToAgent: vi.fn(),
                        setError: vi.fn(),
                        setBlockedError: vi.fn(),
                        setDeployOutcomeError: vi.fn(),
                        clearUnlessDeployOutcome: vi.fn(),
                        declinedOnPlatformRef: { current: declined },
                    }),
                { wrapper: wrapper() }
            );
            await waitFor(() => expect(platformGet).toHaveBeenCalled());
            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 20));
            });
            saveManifest.mockClear();
            await act(async () => {
                await result.current.handleDeploy();
            });
            const apply = (call: unknown[]) => (call[0] as (prev: BuildManifest) => BuildManifest)({ name: "b", components: [agent, gateway] } as BuildManifest).components;
            return saveManifest.mock.calls.map(apply);
        };

        test("records the component it had deployed as deployed, with its id and the version it published", async () => {
            deployGateway.mockRejectedValue(new Error("deployment refused"));

            const writes = await inFlow();

            expect(writes).toHaveLength(1);
            expect(writes[0]?.[0]).toMatchObject({ id: RECORD, status: "deployed", deployed_version: 3 });
            expect(writes[0]?.[1]).toMatchObject({ status: "validated" });
        });

        test("does not stamp a version on a component whose save declined an edit", async () => {
            deployGateway.mockRejectedValue(new Error("deployment refused"));

            const writes = await inFlow([NAME]);

            expect(writes[0]?.[0]).toMatchObject({ id: RECORD, status: "deployed" });
            expect(writes[0]?.[0]?.deployed_version).toBeUndefined();
        });

        test("writes nothing when the first deploy is the one that fails", async () => {
            deployAgent.mockRejectedValue(new Error("deployment refused"));

            const writes = await inFlow();

            expect(writes).toEqual([]);
        });
    });

    describe("the retry after a failed pass (the in-flow deploy of every saved component) never deploys a component the builder flagged error", () => {
        const fine = { filename: "ok.yaml", type: "agent", name: "Fine", origin: "session", status: "validated" };
        const flagged = (type: string, name: string) => ({ filename: "broken.yaml", type, name, origin: "session", status: "error", id: RECORD, deployed_version: 1 });

        let isDeployingAfterRetry: boolean | undefined;

        const retryOver = async (components: object[], saved: SaveResult[], changed?: string[]) => {
            recordKind = "agent";
            const savedComponents = new Map<string, SaveResult>(saved.map(entry => [entry.name, entry]));
            const { result } = renderHook(
                () =>
                    useManifestDeployment({
                        manifest: { name: "b", components } as BuildManifest,
                        saveManifest,
                        savedComponents,
                        allSaved: true,
                        autoSavedInThisFlowRef: { current: false },
                        autoDeployTriggeredRef: { current: false },
                        reportErrorToAgent: vi.fn(),
                        setError: vi.fn(),
                        setBlockedError: vi.fn(),
                        setDeployOutcomeError: setOutcome,
                        clearUnlessDeployOutcome: vi.fn(),
                        changedOnPlatformRef: { current: changed },
                    }),
                { wrapper: wrapper() }
            );
            await waitFor(() => expect(platformGet).toHaveBeenCalled());
            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 20));
            });
            saveManifest.mockClear();
            await act(async () => {
                await result.current.handleDeploy();
            });
            isDeployingAfterRetry = result.current.isDeploying;
            const apply = (call: unknown[]) => (call[0] as (prev: BuildManifest) => BuildManifest)({ name: "b", components } as BuildManifest).components;
            return saveManifest.mock.calls.map(apply);
        };

        const retry = (kind: Kind) => retryOver([fine, flagged(kind, NAME)], [{ name: "Fine", type: "agent", id: "fine-1" } as SaveResult, { name: NAME, type: kind, id: RECORD } as SaveResult]);

        const savedFine = { name: "Fine", type: "agent", id: "fine-1" } as SaveResult;
        const savedFlagged = { name: NAME, type: "agent", id: RECORD } as SaveResult;
        const flaggedAnnouncements = () => notify.mock.calls.filter(call => String(call[0]).includes("flagged with an error"));

        test("a saved result that cannot be planned is reported and the deploy spinner ends, not left on", async () => {
            await retryOver([fine], [{ name: undefined, type: "agent", id: "r1" } as unknown as SaveResult]);

            expect(isDeployingAfterRetry).toBe(false);
            expect(setOutcome).toHaveBeenCalledTimes(1);
            expect(deployAgent).not.toHaveBeenCalled();
        });

        test("a flagged component the save did not change is neither deployed nor announced", async () => {
            await retryOver([fine, flagged("agent", NAME)], [savedFine, savedFlagged], ["Fine"]);

            expect(JSON.stringify(deployAgent.mock.calls)).toContain("fine-1");
            expect(JSON.stringify(deployAgent.mock.calls)).not.toContain(RECORD);
            expect(flaggedAnnouncements()).toHaveLength(0);
        });

        test("a flagged component the save changed is announced once, and an unchanged sibling is not deployed", async () => {
            await retryOver([fine, flagged("agent", NAME)], [savedFine, savedFlagged], [NAME]);

            expect(deployAgent).not.toHaveBeenCalled();
            expect(flaggedAnnouncements()).toEqual([["Not deployed, flagged with an error by the builder: Poet.", "warning"]]);
        });

        test("a retry that has only flagged components to deploy deploys nothing and names them", async () => {
            await retryOver([flagged("agent", NAME)], [{ name: NAME, type: "agent", id: RECORD } as SaveResult]);

            expect(deployAgent).not.toHaveBeenCalled();
            expect(notify).toHaveBeenCalledWith("Not deployed, flagged with an error by the builder: Poet.", "warning");
        });

        test("a retry that skips several flagged components names them all in one message", async () => {
            await retryOver(
                [fine, flagged("agent", NAME), flagged("workflow", "Flow")],
                [{ name: "Fine", type: "agent", id: "fine-1" } as SaveResult, { name: NAME, type: "agent", id: RECORD } as SaveResult, { name: "Flow", type: "workflow", id: "flow-1" } as SaveResult]
            );

            expect(notify).toHaveBeenCalledWith("Not deployed, flagged with an error by the builder: Poet, Flow.", "warning");
            expect(deployWorkflow).not.toHaveBeenCalled();
        });

        test("a retry that skips a flagged component beside one it deploys names only the flagged one", async () => {
            await retry("agent");

            expect(notify).toHaveBeenCalledWith("Not deployed, flagged with an error by the builder: Poet.", "warning");
            expect(notify).toHaveBeenCalledWith("Deployed 1 component(s).", "info");
        });

        test("a retry with nothing flagged says nothing about skipped components", async () => {
            await retryOver([fine], [{ name: "Fine", type: "agent", id: "fine-1" } as SaveResult]);

            expect(notify.mock.calls.some(call => String(call[0]).includes("flagged with an error"))).toBe(false);
        });

        test("a save result whose name the platform spells differently is still matched to its flagged component", async () => {
            await retryOver([fine, flagged("agent", "Poet Agent")], [{ name: "Fine", type: "agent", id: "fine-1" } as SaveResult, { name: "poet_agent", type: "agent", id: RECORD } as SaveResult]);

            expect(JSON.stringify(deployAgent.mock.calls)).toContain("fine-1");
            expect(JSON.stringify(deployAgent.mock.calls)).not.toContain(RECORD);
        });

        test("a flagged component of one kind does not shield a same-named component of another kind", async () => {
            const gateway = { filename: "gw.yaml", type: "gateway", name: NAME, origin: "session", status: "validated" };

            await retryOver([flagged("agent", NAME), gateway], [{ name: NAME, type: "gateway", id: "gw-1" } as SaveResult]);

            expect(deployGateway).toHaveBeenCalledTimes(1);
            expect(JSON.stringify(deployGateway.mock.calls)).toContain("gw-1");
        });

        test.each(KINDS)("%s: the flagged component is not deployed, its flag stands, and its sibling is settled", async kind => {
            const writes = await retry(kind);

            expect(JSON.stringify(deployAgent.mock.calls)).toContain("fine-1");
            expect(JSON.stringify(deployAgent.mock.calls)).not.toContain(RECORD);
            expect(deployWorkflow).not.toHaveBeenCalled();
            expect(deployGateway).not.toHaveBeenCalled();
            expect(writes.length).toBeGreaterThan(0);
            expect(writes.every(components => components?.[1]?.status === "error")).toBe(true);
            expect(writes.some(components => components?.[0]?.status === "deployed")).toBe(true);
        });
    });

    describe("a sibling's click never touches a component the builder flagged error", () => {
        test.each(["not_deployed", "deployed"])("with the error component's own record %s", async status => {
            recordKind = "agent";
            recordStatus = status;
            extraRecords = [{ id: "other-1", name: "Other", deploymentStatus: "not_deployed" }];
            const broken = { filename: "poet.yaml", type: "agent", name: NAME, origin: "session", status: "error", id: RECORD, deployed_version: 1 };
            const sibling = { filename: "other.yaml", type: "agent", name: "Other", origin: "session", status: "validated", id: "other-1" };
            const saved = new Map<string, SaveResult>([["Other", { name: "Other", type: "agent", id: "other-1" } as SaveResult]]);
            const { result } = renderHook(
                () =>
                    useManifestDeployment({
                        manifest: { name: "b", components: [broken, sibling] } as BuildManifest,
                        saveManifest,
                        savedComponents: new Map(),
                        allSaved: false,
                        autoSavedInThisFlowRef: { current: false },
                        autoDeployTriggeredRef: { current: false },
                        reportErrorToAgent: vi.fn(),
                        setError: vi.fn(),
                        setBlockedError: vi.fn(),
                        setDeployOutcomeError: vi.fn(),
                        clearUnlessDeployOutcome: vi.fn(),
                        matchedOnPlatformRef: { current: [] },
                        savedResultsRef: { current: saved },
                    }),
                { wrapper: wrapper() }
            );
            await waitFor(() => expect(platformGet).toHaveBeenCalled());
            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 20));
            });
            saveManifest.mockClear();
            deployAgent.mockClear();

            await act(async () => {
                await result.current.handleDeployManifest(["Other", NAME], {});
            });

            expect(JSON.stringify(deployAgent.mock.calls)).toContain("other-1");
            expect(JSON.stringify(deployAgent.mock.calls)).not.toContain(RECORD);
            const writes = saveManifest.mock.calls.map(call => (call[0] as (prev: BuildManifest) => BuildManifest)({ name: "b", components: [broken, sibling] } as BuildManifest).components);
            expect(writes.every(components => components?.[0]?.status === "error")).toBe(true);
            expect(writes.some(components => components?.[1]?.status === "deployed")).toBe(true);
        });
    });

    describe("a component this session holds no id for", () => {
        test.each(KINDS)("%s: a same-named record is named without a claim about whose it is, and the banner stands on it", async kind => {
            await runPass({ kind, idState: "none", outcome: "skipped", serverApply: false, status: "validated" });

            expect(setOutcome).toHaveBeenCalledWith(expect.stringContaining("already exists on the platform and this build could not confirm it is its own"), [NAME]);
        });

        test.each(KINDS)("%s: one the save already reported as failed is not reported a second time", async kind => {
            await runPass({ kind, idState: "none", outcome: "failed", serverApply: false, status: "validated" });

            expect(setOutcome).not.toHaveBeenCalled();
        });
    });
});
