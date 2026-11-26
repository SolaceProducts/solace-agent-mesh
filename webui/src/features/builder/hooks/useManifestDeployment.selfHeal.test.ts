// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Workflow } from "@/types/workflow";

import { remoteAgentToV2, workflowToV2 } from "@/testing/data/platformWireV2";
import { respondingAsV2, v2Page } from "@/testing/helpers/platformResponses";
import type { RemoteAgent } from "@/types";

import type { BuildManifest } from "./useBuilderManifest";
import type { SaveResult } from "./useSaveOrchestration";

type PlatformRecord = { id: string; name: string; deploymentStatus: string };
const workflowPage = (records: PlatformRecord[]) => v2Page(records.map(r => workflowToV2(r as Workflow)));
const remoteAgentPage = (records: PlatformRecord[]) => v2Page(records.map(r => remoteAgentToV2(r as RemoteAgent)));

describe("useManifestDeployment self-heal", () => {
    let useManifestDeployment: typeof import("./useManifestDeployment").useManifestDeployment;

    const saveManifest = vi.fn();
    const platformGet = vi.fn();
    let sessionArtifacts: Array<{ filename: string; version: number }> = [];
    let liveCards: Array<{ name: string; displayName?: string }> = [];
    let remoteAgentRecords: PlatformRecord[] = [];

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        saveManifest.mockResolvedValue({ ok: true });
        sessionArtifacts = [{ filename: "wf.yaml", version: 3 }];
        liveCards = [];
        remoteAgentRecords = [];

        platformGet.mockImplementation((path: string) => {
            if (path.startsWith("/api/v2/platform/workflows")) return Promise.resolve(workflowPage([{ id: "wf-1", name: "Wf", deploymentStatus: "deployed" }]));
            if (path.startsWith("/api/v2/platform/remoteAgents")) return Promise.resolve(remoteAgentPage(remoteAgentRecords));
            return Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, totalPages: 1 } } });
        });

        vi.doMock("@/api/client", () => ({
            api: { platform: { get: respondingAsV2(platformGet) } },
        }));
        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: sessionArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ data: liveCards }) }));
        vi.doMock("@/api/agent-deployments", () => ({ useAgentDeploymentMutation: () => ({ mutateAsync: vi.fn() }) }));
        vi.doMock("@/api/gateways", () => ({ useGatewayDeploymentMutation: () => ({ mutateAsync: vi.fn() }) }));
        vi.doMock("./useWorkflowDeployment", () => ({
            useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }),
        }));
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ addNotification: vi.fn(), displayError: vi.fn(), sessionId: "s1", isResponding: false }),
        }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({
            useIsBuilderServerApplyEnabled: () => false,
        }));

        useManifestDeployment = (await import("./useManifestDeployment")).useManifestDeployment;
    });

    function wrapper() {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
    }

    function renderWith(deployedVersion: number | undefined, status = "validated", id?: string, type = "workflow") {
        return renderManifest({
            name: "Wf",
            filename: "wf.yaml",
            type,
            origin: "session",
            status,
            ...(id === undefined ? {} : { id }),
            ...(deployedVersion === undefined ? {} : { deployed_version: deployedVersion }),
        });
    }

    function renderManifest(component: NonNullable<BuildManifest["components"]>[number], proofs: { matched?: string[]; saved?: Map<string, SaveResult> } = {}) {
        const manifest: BuildManifest = { name: "build", components: [component] };
        return renderHook(
            () =>
                useManifestDeployment({
                    manifest,
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
                    matchedOnPlatformRef: { current: proofs.matched ?? [] },
                    savedResultsRef: { current: proofs.saved ?? new Map() },
                }),
            { wrapper: wrapper() }
        );
    }

    test("flips a lagging component to deployed when staleness cannot be proven", async () => {
        renderWith(undefined, "validated", "wf-1");

        await waitFor(() => expect(saveManifest).toHaveBeenCalledTimes(1));

        const updater = saveManifest.mock.calls[0][0] as (prev: BuildManifest) => BuildManifest;
        const next = updater({ name: "build", components: [{ name: "Wf", filename: "wf.yaml", type: "workflow", origin: "session", status: "validated" }] });
        expect(next.components?.[0].status).toBe("deployed");
    });

    test("refuses the flip when the config has provably moved past the deploy", async () => {
        renderWith(1, "validated", "wf-1");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));

        expect(saveManifest).not.toHaveBeenCalled();
    });

    test("regresses a component the platform no longer has", async () => {
        platformGet.mockImplementation(() => Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, totalPages: 1 } } }));

        renderWith(3, "deployed", "wf-1");

        await waitFor(() => expect(saveManifest).toHaveBeenCalledTimes(1));

        const updater = saveManifest.mock.calls[0][0] as (prev: BuildManifest) => BuildManifest;
        const next = updater({ name: "build", components: [{ name: "Wf", filename: "wf.yaml", type: "workflow", origin: "session", status: "deployed" }] });
        expect(next.components?.[0].status).toBe("validated");
    });

    test("does not regress a component whose own kind failed to fetch", async () => {
        platformGet.mockImplementation((path: string) => {
            if (path.startsWith("/api/v2/platform/workflows")) return Promise.reject(new Error("boom"));
            return Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, totalPages: 1 } } });
        });

        renderWith(3, "deployed", "wf-1");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));

        expect(saveManifest).not.toHaveBeenCalled();
    });

    test("does not flip a component with no recorded id to deployed because a same-named record is deployed", async () => {
        renderWith(undefined, "validated");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));

        expect(saveManifest).not.toHaveBeenCalled();
    });

    test("does not flip a component whose recorded id names a record the platform does not hold, even when a same-named one is deployed", async () => {
        renderWith(undefined, "validated", "wf-gone");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));

        expect(saveManifest).not.toHaveBeenCalled();
    });

    test("regresses a component whose recorded record was replaced by a same-named one", async () => {
        renderWith(3, "deployed", "wf-gone");

        await waitFor(() => expect(saveManifest).toHaveBeenCalledTimes(1));

        const updater = saveManifest.mock.calls[0][0] as (prev: BuildManifest) => BuildManifest;
        const next = updater({ name: "build", components: [{ name: "Wf", filename: "wf.yaml", type: "workflow", origin: "session", status: "deployed" }] });
        expect(next.components?.[0].status).toBe("validated");
    });

    test("leaves a legacy deployed component with no id alone while a record answers to its name", async () => {
        renderWith(undefined, "deployed");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));

        expect(saveManifest).not.toHaveBeenCalled();
    });

    test("still regresses a legacy deployed component with no id once nothing answers to its name", async () => {
        platformGet.mockImplementation(() => Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, totalPages: 1 } } }));

        renderWith(undefined, "deployed");

        await waitFor(() => expect(saveManifest).toHaveBeenCalledTimes(1));
    });

    test("offers the deploy action unless the platform holds the session's own record deployed", async () => {
        const own = renderWith(3, "deployed", "wf-1");
        await waitFor(() => expect(own.result.current.hasUndeployedSessionComponents).toBe(false));

        const replaced = renderWith(3, "deployed", "wf-gone");
        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(replaced.result.current.hasUndeployedSessionComponents).toBe(true);
    });

    test("does not regress a component whose record is gone while its own instance is still live", async () => {
        platformGet.mockImplementation(() => Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, totalPages: 1 } } }));
        liveCards = [{ name: "workflow_wf_gone" }];

        renderWith(3, "deployed", "wf-gone");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(saveManifest).not.toHaveBeenCalled();
    });

    test("regresses a component whose record is gone when only a same-named instance of another record is live", async () => {
        platformGet.mockImplementation(() => Promise.resolve({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, totalPages: 1 } } }));
        liveCards = [{ name: "workflow_wf_other", displayName: "Wf" }];

        renderWith(3, "deployed", "wf-gone");

        await waitFor(() => expect(saveManifest).toHaveBeenCalledTimes(1));
    });

    test("flips a lagging remote agent to deployed from the remote-agent list", async () => {
        remoteAgentRecords = [{ id: "ra-1", name: "Wf", deploymentStatus: "deployed" }];

        renderWith(undefined, "validated", "ra-1", "a2a_agent");

        await waitFor(() => expect(saveManifest).toHaveBeenCalledTimes(1));
        const updater = saveManifest.mock.calls[0][0] as (prev: BuildManifest) => BuildManifest;
        const next = updater({ name: "build", components: [{ name: "Wf", filename: "wf.yaml", type: "a2a_agent", origin: "session", status: "validated" }] });
        expect(next.components?.[0].status).toBe("deployed");
    });

    test("reports an undeployed component when its own record is stopped though a same-named record runs", async () => {
        platformGet.mockImplementation((path: string) => {
            if (path.startsWith("/api/v2/platform/workflows")) {
                return Promise.resolve(
                    workflowPage([
                        { id: "wf-1", name: "Renamed", deploymentStatus: "not_deployed" },
                        { id: "wf-2", name: "Wf", deploymentStatus: "deployed" },
                    ])
                );
            }
            return Promise.resolve(v2Page([]));
        });

        const { result } = renderWith(3, "deployed", "wf-1");

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await waitFor(() => expect(result.current.hasUndeployed).toBe(true));
    });

    describe("Build & Activate settles the record its id names", () => {
        const draft = { name: "Wf", filename: "wf.yaml", type: "workflow", origin: "session", status: "draft", id: "wf-gone" } as const;
        const settledBy = (call: unknown[]) => {
            const updater = call[0] as (prev: BuildManifest) => BuildManifest;
            return updater({ name: "build", components: [{ ...draft }] }).components?.[0];
        };
        const deploy = async (
            component: NonNullable<BuildManifest["components"]>[number],
            proofs: { matched?: string[]; saved?: Map<string, SaveResult> } = {},
            changed: readonly string[] | undefined = ["Wf"],
            options: { notWritten?: readonly string[] } = {}
        ) => {
            const { result } = renderManifest(component, proofs);
            await waitFor(() => expect(platformGet).toHaveBeenCalled());
            await new Promise(resolve => setTimeout(resolve, 50));
            saveManifest.mockClear();
            await act(async () => {
                await result.current.handleDeployManifest(changed, options);
            });
        };
        const savedWf = (id: string) => new Map<string, SaveResult>([["Wf", { name: "Wf", type: "workflow", id } as SaveResult]]);

        test("the id the save returned is settled in one write: the record's id and the deployed status", async () => {
            await deploy(draft, { saved: savedWf("wf-1") });

            const settled = saveManifest.mock.calls.map(settledBy).filter(c => c?.status === "deployed");
            expect(settled).toHaveLength(1);
            expect(settled[0]).toMatchObject({ id: "wf-1", status: "deployed" });
        });

        test("a component the save matched unchanged is settled through the id the save returned", async () => {
            await deploy(draft, { saved: savedWf("wf-1"), matched: ["Wf"] }, ["Other"]);

            expect(saveManifest.mock.calls.map(settledBy).some(c => c?.id === "wf-1" && c?.status === "deployed")).toBe(true);
        });

        test("a component with no save result and no recorded id is never credited with a same-named record", async () => {
            await deploy({ name: "Wf", filename: "wf.yaml", type: "workflow", origin: "session", status: "draft" }, {}, undefined, {});

            expect(saveManifest).not.toHaveBeenCalled();
        });

        test("a component whose save failed, with a recorded id the platform no longer lists, is not credited with a same-named record", async () => {
            await deploy(draft, {}, undefined, { notWritten: ["Wf"] });

            expect(saveManifest).not.toHaveBeenCalled();
        });

        test("a deploy-only retry settles the record the manifest already records", async () => {
            await deploy({ ...draft, id: "wf-1" }, {}, undefined);

            expect(saveManifest.mock.calls.map(settledBy).some(c => c?.status === "deployed")).toBe(true);
        });
    });
});
