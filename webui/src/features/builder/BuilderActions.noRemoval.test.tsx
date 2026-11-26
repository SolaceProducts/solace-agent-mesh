// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { v2PlatformClient } from "@/testing/helpers/platformResponses";
import { withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildActivateApi } from "./BuilderActions";
import type { BuildManifest } from "./hooks/useBuilderManifest";

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const BUILDS_PATH = "/api/internal/platform/builder/builds";

describe("saving and deploying never remove what the plan dropped", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;

    const platformGet = vi.fn();
    const platformPost = vi.fn();
    const platformDelete = vi.fn();
    const platformPut = vi.fn();
    let failNextBuild = false;

    const AGENT_YAML = `
apps:
  - name: TestAgent
    app_config:
      agent_name: TestAgent
      description: A test agent with a sufficiently long description.
      instruction: |
        ${"x".repeat(120)}
      agent_card:
        description: Another description on agent_card.
`;

    const manifest = (): BuildManifest => ({
        name: "my-build",
        full_validation_passed: true,
        components: [{ filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" }],
    });

    const droppedDeployed = { kind: "agent", id: "dropped-agent-1", name: "Dropped Agent", createdBySession: true, deployment: "deployed" };

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        failNextBuild = false;
        sessionStorage.clear();
        localStorage.clear();
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        platformGet.mockResolvedValue({ data: [{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }] });
        platformPost.mockImplementation((url: string, body?: { dryRun?: boolean }) => {
            if (url !== BUILDS_PATH) return Promise.resolve({});
            if (failNextBuild && !body?.dryRun) {
                failNextBuild = false;
                return Promise.reject(Object.assign(new Error("the platform is unavailable"), { status: 500 }));
            }
            return Promise.resolve({
                dryRun: Boolean(body?.dryRun),
                operations: [{ kind: "agent", name: "TestAgent", action: "create", status: body?.dryRun ? "planned" : "succeeded", resourceId: body?.dryRun ? undefined : "agent-1" }],
                summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
                dropped: [droppedDeployed],
            });
        });

        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) => (
                <button onClick={onClick} disabled={disabled}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({
            api: { platform: v2PlatformClient({ get: platformGet, post: platformPost, put: platformPut, delete: platformDelete }) },
        }));
        vi.doMock("@/utils/api", () => ({ getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback) }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                addNotification: vi.fn(),
                displayError: vi.fn(),
                uploadArtifactFile: vi.fn(),
                sessionId: "test-session",
                allArtifacts: [],
                handleSubmit: vi.fn(),
                isResponding: false,
                isCancelling: false,
            }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/skills", () => ({ useSaveSkillFromConfigMutation: () => ({ mutateAsync: vi.fn() }), getSkill: vi.fn() }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => true }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));
        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            listManifestVersions: (_sessionId: string, filename: string) => Promise.resolve(filename === "agent.yaml" ? [3] : []),
        }));

        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, filename => (filename === "agent.yaml" ? 3 : undefined));
        queryWrapper = newQueryWrapper();
    });

    const otherWrites = () => platformPost.mock.calls.map(([url]) => String(url)).filter(url => url !== BUILDS_PATH && !url.includes("Deployments"));

    function expectNothingWasRemoved() {
        expect(platformDelete).not.toHaveBeenCalled();
        expect(otherWrites()).toEqual([]);
        expect(platformPost.mock.calls.some(([url]) => String(url).includes("removals"))).toBe(false);
    }

    function renderBridged(autoSave = false) {
        let latest: BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest()}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                userAgentsRefetch={async () => {
                    await new Promise(resolve => setTimeout(resolve, 0));
                }}
                autoSave={autoSave}
                onBuildActivateApiChange={next => {
                    latest = next;
                }}
            />,
            { wrapper: queryWrapper }
        );
        return { api: () => latest! };
    }

    const buildsApplied = () => platformPost.mock.calls.filter(([url, body]) => url === BUILDS_PATH && !(body as { dryRun?: boolean }).dryRun);

    test("Build & Activate, which the agent's request_build_activate tool and a held chat trigger run, removes nothing", async () => {
        const { api } = renderBridged();
        await waitFor(() => expect(api()?.canBuildActivate).toBe(true));

        await act(async () => {
            await api().run();
        });

        expect(buildsApplied().length).toBeGreaterThan(0);
        expectNothingWasRemoved();
    });

    test("Try again after a failed save removes nothing", async () => {
        failNextBuild = true;
        const { api } = renderBridged();
        await waitFor(() => expect(api()?.canBuildActivate).toBe(true));
        await act(async () => {
            await api().run();
        });
        await waitFor(() => expect(api().failure.kind).toBe("retryable"));

        act(() => api().retry());

        await waitFor(() => expect(buildsApplied().length).toBeGreaterThan(1));
        expectNothingWasRemoved();
    });

    test("the auto-save a plan approval arms removes nothing", async () => {
        renderBridged(true);

        await waitFor(() => expect(buildsApplied().length).toBeGreaterThan(0));
        expectNothingWasRemoved();
    });

    test("the panel, when it is not bridged to the chat, removes nothing", async () => {
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest()}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                userAgentsRefetch={async () => {
                    await new Promise(resolve => setTimeout(resolve, 0));
                }}
            />,
            { wrapper: queryWrapper }
        );

        await userEvent.click(await screen.findByRole("button", { name: /Build & Activate/ }));

        await waitFor(() => expect(buildsApplied().length).toBeGreaterThan(0));
        expectNothingWasRemoved();
    });

    test("the bridge it publishes carries no way to remove anything", async () => {
        const { api } = renderBridged();
        await waitFor(() => expect(api()).not.toBeNull());

        expect(Object.keys(api()).sort()).toEqual(["askBuilder", "canAskBuilder", "canBuildActivate", "checkAgain", "failure", "isBusy", "platformCheck", "retry", "run", "turnRunning"]);
    });
});
