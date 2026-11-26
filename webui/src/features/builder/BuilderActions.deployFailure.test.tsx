// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode, useCallback, useRef, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { respondingAsV2, v2DeploymentOf, v2Error, v2PageOf } from "@/testing/helpers/platformResponses";
import { withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildActivateApi } from "./BuilderActions";
import type { BuildManifest } from "./hooks/useBuilderManifest";

const BUILDS_PATH = "/api/internal/platform/builder/builds";

const hop = <T,>(value: T) => new Promise<T>(resolve => setTimeout(() => resolve(value), 0));

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

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

describe("BuilderActions: a deploy failure survives the rest of the pass", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;

    const addNotification = vi.fn();
    const displayError = vi.fn();
    const platformGet = vi.fn();
    const platformPost = vi.fn();
    const handleSubmit = vi.fn();
    let agentLive: boolean;
    let gwLive: boolean;
    let agentUnreported: boolean;
    let gwRejection: { status: number } | null;
    let apiOf: BuildActivateApi | null;
    let manifestNow: BuildManifest;
    let kinds: string[];

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        agentLive = false;
        gwLive = false;
        agentUnreported = false;
        gwRejection = { status: 500 };
        gwStatus = "validated";
        recordedVersions = undefined;
        apiOf = null;
        kinds = [];
        manifestNow = initialManifest();

        platformGet.mockImplementation((path: string) => {
            if (path.startsWith("/api/v2/platform/agents")) return hop(v2PageOf(path, [{ id: "agent-1", name: "TestAgent", deploymentStatus: agentLive && !agentUnreported ? "deployed" : "not_deployed" }]));
            if (path.startsWith("/api/v2/platform/entrypoints")) return hop(v2PageOf(path, [{ id: "gw-1", name: "TestGw", deploymentStatus: gwLive ? "deployed" : "deploy_failed" }]));
            return hop({ data: [] });
        });
        platformPost.mockImplementation((url: string, body: unknown) => {
            if (url === BUILDS_PATH) {
                return hop({
                    dryRun: false,
                    operations: [
                        { kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" },
                        { kind: "gateway", name: "TestGw", action: "create", status: "succeeded", resourceId: "gw-1" },
                    ],
                    summary: { created: 2, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
                });
            }
            if (url.includes("entrypointDeployments")) {
                if (!gwRejection) {
                    gwLive = true;
                    return hop(v2DeploymentOf(body));
                }
                return hop(v2Error(gwRejection.status, "internal error"));
            }
            if (url.includes("agentDeployments")) {
                agentLive = true;
                return hop(v2DeploymentOf(body));
            }
            return hop({});
        });

        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) => (
                <button onClick={onClick} disabled={disabled}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({
            api: { platform: { get: respondingAsV2(platformGet), post: respondingAsV2(platformPost), delete: vi.fn() } },
        }));
        vi.doMock("@/utils/api", () => ({ getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback) }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ addNotification, displayError, uploadArtifactFile: vi.fn(), sessionId: "test-session", allArtifacts: [], handleSubmit }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/skills", () => ({ useSaveSkillFromConfigMutation: () => ({ mutateAsync: vi.fn() }) }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => true }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));
        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            listManifestVersions: () => Promise.resolve([3]),
        }));

        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => 3);
        queryWrapper = newQueryWrapper();
    });

    let gwStatus: string | undefined;
    let recordedVersions: Record<string, number> | undefined;
    const initialManifest = (): BuildManifest => ({
        name: "my-build",
        full_validation_passed: true,
        ...(recordedVersions ? { full_validation_versions: recordedVersions } : {}),
        components: [
            { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" },
            { filename: "gw.yaml", type: "gateway", name: "TestGw", status: gwStatus },
            { filename: "existing.yaml", type: "agent", name: "Existing", status: "validated", origin: "platform" },
        ],
    });

    let pushManifest: (m: BuildManifest) => void;
    function Host({ autoSave = false }: Readonly<{ autoSave?: boolean }>) {
        const [manifest, setManifest] = useState<BuildManifest>(initialManifest);
        const latest = useRef(manifest);
        const replace = useCallback((next: BuildManifest) => {
            latest.current = next;
            manifestNow = next;
            setManifest(next);
        }, []);
        pushManifest = replace;
        const saveManifest = useCallback(
            async (updater: (m: BuildManifest) => BuildManifest) => {
                replace(updater(latest.current));
                return { ok: true } as const;
            },
            [replace]
        );
        return (
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                saveManifest={saveManifest}
                userAgentsRefetch={() => hop(undefined)}
                autoSave={autoSave}
                onBuildActivateApiChange={next => {
                    apiOf = next;
                    if (next) kinds.push(next.failure.kind);
                }}
            />
        );
    }

    const statusOf = (name: string) => manifestNow.components?.find(c => c.name === name)?.status;

    async function runUntilAgentFlipped() {
        render(<Host />, { wrapper: queryWrapper });
        await waitFor(() => expect(apiOf).not.toBeNull());
        await apiOf!.run();
        await waitFor(() => expect(statusOf("TestAgent")).toBe("deployed"));
        await act(async () => {
            await hop(undefined);
        });
    }

    test("one entrypoint fails while the agent deploys: the banner is still up once the manifest settles", async () => {
        await runUntilAgentFlipped();

        expect(statusOf("TestGw")).not.toBe("deployed");
        expect(kinds).toContain("retryable");
        expect(apiOf!.failure).toMatchObject({ kind: "retryable", message: expect.stringContaining("Deploy failed: TestGw (internal error)") });
    });

    test("the same holds on the auto-save path, which deploys through handleDeploy and stops at the failure", async () => {
        render(<Host autoSave />, { wrapper: queryWrapper });
        await waitFor(() => expect(kinds).toContain("retryable"));

        window.dispatchEvent(new Event("focus"));
        await waitFor(() => expect(statusOf("TestAgent")).toBe("deployed"));
        await act(async () => {
            await hop(undefined);
        });

        expect(apiOf!.failure).toMatchObject({ kind: "retryable", message: "internal error" });
    });

    test("a pass that fails part-way records the agent it had deployed as deployed, with its id", async () => {
        agentUnreported = true;
        render(<Host autoSave />, { wrapper: queryWrapper });
        await waitFor(() => expect(kinds).toContain("retryable"));

        await waitFor(() => expect(manifestNow.components?.find(c => c.name === "TestAgent")?.id).toBe("agent-1"));
        expect(statusOf("TestAgent")).toBe("deployed");
        expect(manifestNow.components?.find(c => c.name === "TestAgent")?.deployed_version).toBe(3);
        expect(manifestNow.components?.find(c => c.name === "TestGw")?.status).not.toBe("deployed");
    });

    test("the failed component recovering clears it, since the pass no longer has a failure to report", async () => {
        await runUntilAgentFlipped();
        expect(apiOf!.failure.kind).toBe("retryable");

        gwLive = true;
        window.dispatchEvent(new Event("focus"));

        await waitFor(() => expect(statusOf("TestGw")).toBe("deployed"));
        await waitFor(() => expect(apiOf!.failure.kind).toBe("none"));
    });

    test("a redeploy that fails for a component already deployed still stands through an unrelated flip", async () => {
        gwStatus = "deployed";
        gwLive = true;
        await runUntilAgentFlipped();

        expect(statusOf("TestGw")).toBe("deployed");
        expect(kinds).toContain("retryable");
        expect(apiOf!.failure).toMatchObject({ kind: "retryable", message: expect.stringContaining("TestGw") });
    });

    test("a failed component with no status still stands", async () => {
        gwStatus = undefined;
        await runUntilAgentFlipped();

        expect(apiOf!.failure.kind).toBe("retryable");
    });

    test("a failure the builder agent is fixing stays in the fixing state through the flips", async () => {
        gwRejection = { status: 422 };
        render(<Host autoSave />, { wrapper: queryWrapper });
        await waitFor(() => expect(kinds).toContain("fixing"));
        expect(handleSubmit).toHaveBeenCalledTimes(1);
        const chip = JSON.stringify(handleSubmit.mock.calls[0]);
        expect(chip).toContain("Deploy entrypoint for **TestGw**");
        expect(chip).not.toContain("Deploy gateway");

        window.dispatchEvent(new Event("focus"));
        await waitFor(() => expect(statusOf("TestAgent")).toBe("deployed"));
        await act(async () => {
            await hop(undefined);
        });

        expect(apiOf!.failure.kind).toBe("fixing");
        expect(kinds).not.toContain("retryable");
    });

    test("a kept flip does not restart the auto-fix watchdog", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            agentUnreported = true;
            gwRejection = { status: 422 };
            render(<Host autoSave />, { wrapper: queryWrapper });
            await waitFor(() => expect(kinds).toContain("fixing"));

            await act(async () => {
                await vi.advanceTimersByTimeAsync(20_000);
            });
            act(() => {
                pushManifest({ ...manifestNow, components: manifestNow.components?.map(c => (c.name === "TestAgent" ? { ...c, status: "deployed" } : c)) });
            });
            await act(async () => {
                await vi.advanceTimersByTimeAsync(15_000);
            });

            expect(apiOf!.failure.kind).toBe("retryable");
        } finally {
            vi.useRealTimers();
        }
    });

    test("editing a deployed component in place clears it", async () => {
        await runUntilAgentFlipped();
        expect(apiOf!.failure.kind).toBe("retryable");

        act(() => {
            pushManifest({ ...manifestNow, components: manifestNow.components?.map(c => (c.name === "TestAgent" ? { ...c, status: "validated" } : c)) });
        });

        await waitFor(() => expect(apiOf!.failure.kind).toBe("none"));
    });

    test("a retry that deploys everything clears it", async () => {
        await runUntilAgentFlipped();
        expect(apiOf!.failure.kind).toBe("retryable");

        gwRejection = null;
        await act(async () => {
            apiOf!.retry();
            await hop(undefined);
        });

        await waitFor(() => expect(apiOf!.failure.kind).toBe("none"));
    });

    test("a retry that fails again still shows it", async () => {
        await runUntilAgentFlipped();

        await act(async () => {
            apiOf!.retry();
            await hop(undefined);
        });

        await waitFor(() => expect(kinds.lastIndexOf("none")).toBeGreaterThan(kinds.indexOf("retryable")));
        await waitFor(() => expect(kinds.at(-1)).toBe("retryable"));
    });

    test("a re-stamp that records new component versions clears it, with the stamp still true", async () => {
        const stampedActions = (await import("./BuilderActions")).BuilderActions;
        BuilderActions = stampedActions;
        recordedVersions = { "agent.yaml": 1, "gw.yaml": 1 };
        await runUntilAgentFlipped();
        expect(apiOf!.failure.kind).toBe("retryable");

        act(() => {
            pushManifest({ ...manifestNow, full_validation_passed: true, full_validation_versions: { "agent.yaml": 2, "gw.yaml": 1 } });
        });

        await waitFor(() => expect(apiOf!.failure.kind).toBe("none"));
    });

    test("a build turn that rewrites the plan afterwards still clears it", async () => {
        await runUntilAgentFlipped();
        expect(apiOf!.failure.kind).toBe("retryable");

        act(() => {
            pushManifest({ ...manifestNow, components: [...(manifestNow.components ?? []), { filename: "extra.yaml", type: "agent", name: "Extra", status: "validated" }] });
        });

        await waitFor(() => expect(apiOf!.failure.kind).toBe("none"));
    });
});
