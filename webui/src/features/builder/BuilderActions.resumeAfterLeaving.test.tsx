// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("./hooks/usePlatformCheck", () => ({ usePlatformCheck: () => ({ check: { kind: "passed", warnings: [] }, key: null, checkAgain: () => {} }) }));

import { v2PlatformClient } from "@/testing/helpers/platformResponses";
import { STAMPED_VERSION, withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildActivateApi } from "./BuilderActions";
import type { BuildManifest } from "./hooks/useBuilderManifest";

function newQueryWrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const BUILDS_PATH = "/api/internal/platform/builder/builds";
const PENDING_KEY = "sam_builder_pending_autocreate";
const ACTIVE_SESSION_KEY = "sam_builder_active_session";

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

type Status = "draft" | "validated" | "deployed";

interface ComponentSpec {
    status: Status;
    type?: string;
    origin?: string;
}

function manifestWith(...specs: (Status | ComponentSpec)[]): BuildManifest {
    const components = specs.map(spec => (typeof spec === "string" ? { status: spec } : spec));
    return {
        name: "my-build",
        full_validation_passed: !components.some(c => c.status === "draft"),
        components: components.map((c, i) => ({ filename: `component${i}.yaml`, type: c.type ?? "agent", name: i === 0 ? "TestAgent" : `TestAgent${i}`, status: c.status, origin: c.origin ?? "session" })),
    } as BuildManifest;
}

const GENERATING = manifestWith("draft");
const VALIDATED = manifestWith("validated");
const DEPLOYED = manifestWith("deployed");

const savedBuild = { dryRun: false, operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" }], summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 } };

describe("a build approved before the page was reloaded", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let usePendingAutoCreate: typeof import("./hooks/usePendingAutoCreate").usePendingAutoCreate;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;
    let currentSessionId: string;
    let buildResponse: (body?: unknown) => Promise<unknown>;
    let bridged: BuildActivateApi | null;
    let missingCredentials: ReadonlyMap<string, number> | undefined;

    const platformPost = vi.fn();
    const platformGet = vi.fn();
    const addNotification = vi.fn();
    const displayError = vi.fn();

    const buildCalls = () => platformPost.mock.calls.filter(([url]) => url === BUILDS_PATH).length;
    const deployCalls = () => platformPost.mock.calls.filter(([url]) => String(url).includes("agentDeployments")).length;
    const settle = () => act(() => new Promise<void>(resolve => setTimeout(resolve, 80)));

    async function loadPage() {
        window.location.hash = "#/builder";
        const approved = sessionStorage.getItem(PENDING_KEY);
        if (approved === null) sessionStorage.removeItem(ACTIVE_SESSION_KEY);
        else sessionStorage.setItem(ACTIVE_SESSION_KEY, approved);
        vi.resetModules();
        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
        ({ usePendingAutoCreate } = await import("./hooks/usePendingAutoCreate"));
    }

    beforeEach(async () => {
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.clear();
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        currentSessionId = "s1";
        bridged = null;
        missingCredentials = undefined;
        buildResponse = () => Promise.resolve(savedBuild);
        platformGet.mockResolvedValue({ data: [{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }] });
        platformPost.mockImplementation((url: string, body?: unknown) => (url === BUILDS_PATH ? buildResponse(body) : Promise.resolve({})));

        vi.resetModules();
        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) => (
                <button onClick={onClick} disabled={disabled}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({
            api: { platform: v2PlatformClient({ get: platformGet, post: platformPost }) },
        }));
        vi.doMock("@/utils/api", () => ({
            getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback),
        }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                addNotification,
                displayError,
                uploadArtifactFile: vi.fn(),
                sessionId: currentSessionId,
                allArtifacts: [],
                handleSubmit: vi.fn(),
                isResponding: false,
            }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/skills", () => ({ useSaveSkillFromConfigMutation: () => ({ mutateAsync: vi.fn() }) }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("@/api/builder", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/builder")>()), listManifestVersions: () => Promise.resolve([STAMPED_VERSION]) }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => true }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));

        await loadPage();
        queryWrapper = newQueryWrapper();
    });

    function Page({ sessionId, manifest, actions = true, onApprove }: Readonly<{ sessionId: string; manifest: BuildManifest | null; actions?: boolean; onApprove?: (approve: () => void) => void }>) {
        const [pending, setPending] = usePendingAutoCreate(sessionId);
        onApprove?.(() => setPending(true));
        if (!actions) return null;
        return (
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest}
                componentsMissingCreds={missingCredentials}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                userAgentsRefetch={async () => {
                    await new Promise(resolve => setTimeout(resolve, 0));
                }}
                autoSave={pending}
                onAutoSaveComplete={() => setPending(false)}
                onBuildActivateApiChange={api => {
                    bridged = api;
                }}
            />
        );
    }

    const show = (manifest: BuildManifest | null, sessionId = currentSessionId) => {
        currentSessionId = sessionId;
        return render(<Page sessionId={sessionId} manifest={manifest} />, { wrapper: queryWrapper });
    };

    async function approveWhileGenerating() {
        let approve: () => void = () => {};
        const view = render(<Page sessionId={currentSessionId} manifest={GENERATING} onApprove={a => (approve = a)} />, { wrapper: queryWrapper });
        act(() => approve());
        await settle();
        return view;
    }

    async function reload(view: { unmount: () => void }) {
        const stored = sessionStorage.getItem(PENDING_KEY);
        view.unmount();
        if (stored === null) sessionStorage.removeItem(PENDING_KEY);
        else sessionStorage.setItem(PENDING_KEY, stored);
        await loadPage();
    }

    const switchTo = (view: { rerender: (ui: ReactNode) => void }, sessionId: string, manifest: BuildManifest | null) => {
        currentSessionId = sessionId;
        view.rerender(<Page sessionId={sessionId} manifest={manifest} actions={manifest !== null} />);
    };

    describe("the page is reloaded", () => {
        test("while the build runs: the save and the deploy each happen once when it validates, and not again on a later reload", async () => {
            const first = await approveWhileGenerating();
            expect(buildCalls()).toBe(0);
            await reload(first);

            const reloaded = show(VALIDATED);

            await waitFor(() => expect(buildCalls()).toBe(1));
            await waitFor(() => expect(deployCalls()).toBe(1));
            await settle();
            expect(buildCalls()).toBe(1);
            expect(deployCalls()).toBe(1);
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();

            await reload(reloaded);
            show(DEPLOYED);
            await settle();
            expect(buildCalls()).toBe(1);
            expect(deployCalls()).toBe(1);
        });

        test("while the build still runs after the reload: nothing is saved until it validates", async () => {
            const first = await approveWhileGenerating();
            await reload(first);

            const reloaded = show(GENERATING);
            await settle();
            expect(buildCalls()).toBe(0);
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");

            reloaded.rerender(<Page sessionId="s1" manifest={VALIDATED} />);

            await waitFor(() => expect(buildCalls()).toBe(1));
        });

        test("after the save already finished: it is not saved again", async () => {
            const first = await approveWhileGenerating();
            first.rerender(<Page sessionId="s1" manifest={VALIDATED} />);
            await waitFor(() => expect(buildCalls()).toBe(1));
            await settle();
            await reload(first);

            show(VALIDATED);
            await settle();

            expect(buildCalls()).toBe(1);
        });

        test("after a save that failed: the attempt is over, so it is not retried behind the failure banner", async () => {
            buildResponse = () => Promise.reject(new Error("apply failed"));
            const first = await approveWhileGenerating();
            first.rerender(<Page sessionId="s1" manifest={VALIDATED} />);
            await waitFor(() => expect(buildCalls()).toBe(1));
            await settle();
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
            await reload(first);

            show(VALIDATED);
            await settle();

            expect(buildCalls()).toBe(1);
            expect(deployCalls()).toBe(0);
        });

        test("while the save itself is in flight: the retry after the reload sends the save again and then finishes", async () => {
            let finishSave: () => void = () => {};
            buildResponse = () => new Promise(resolve => (finishSave = () => resolve(savedBuild)));
            const first = await approveWhileGenerating();
            first.rerender(<Page sessionId="s1" manifest={VALIDATED} />);
            await waitFor(() => expect(buildCalls()).toBe(1));
            await reload(first);
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");
            buildResponse = () => Promise.resolve(savedBuild);

            show(VALIDATED);
            await waitFor(() => expect(buildCalls()).toBe(2));
            await waitFor(() => expect(deployCalls()).toBe(1));
            await settle();
            finishSave();
            await settle();

            expect(buildCalls()).toBe(2);
            expect(deployCalls()).toBe(1);
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
        });

        test("with a build already deployed and the approval left behind: nothing is saved, the approval is forgotten, and a later edit is not saved for the user", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();

            const view = show(DEPLOYED);
            await settle();
            expect(buildCalls()).toBe(0);
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();

            view.rerender(<Page sessionId="s1" manifest={VALIDATED} />);
            await settle();

            expect(buildCalls()).toBe(0);
            expect(deployCalls()).toBe(0);
        });

        test("with a deployed agent and an unsaved-by-design connector: the approval is forgotten too", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();

            show(manifestWith("deployed", { status: "validated", type: "connector" }));
            await settle();

            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
            expect(buildCalls()).toBe(0);
        });

        test("a platform component that is not deployed does not keep the approval of a deployed build", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();

            show(manifestWith("deployed", { status: "validated", origin: "platform" }));
            await settle();

            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
        });

        test("while the manifest is not loaded yet (null, then empty): the approval is kept, and the save and deploy run once when it validates", async () => {
            const first = await approveWhileGenerating();
            await reload(first);

            const reloaded = show(null);
            await settle();
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");
            reloaded.rerender(<Page sessionId="s1" manifest={{ name: "my-build", components: [] } as BuildManifest} />);
            await settle();
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");
            expect(buildCalls()).toBe(0);

            reloaded.rerender(<Page sessionId="s1" manifest={VALIDATED} />);

            await waitFor(() => expect(buildCalls()).toBe(1));
            await waitFor(() => expect(deployCalls()).toBe(1));
        });

        test("when the user clicks Build & Activate themselves: the approval ends, so a later edit is neither hidden behind it nor saved without a click", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            const view = show(manifestWith("deployed", "validated", { status: "validated", type: "connector" }));
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));

            await act(async () => {
                await bridged!.run();
            });
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
            const sent = buildCalls();

            view.rerender(<Page sessionId="s1" manifest={manifestWith("validated", "validated", { status: "validated", type: "connector" })} />);
            await settle();

            expect(buildCalls()).toBe(sent);
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));
        });

        test("with some components deployed and one validated: no save fires, and Build & Activate stays available", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();

            show(manifestWith("deployed", "validated"));
            await settle();

            expect(buildCalls()).toBe(0);
            expect(deployCalls()).toBe(0);
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));
        });

        test("when the build becomes deployed after the page mounted with the approval armed: the approval ends", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            const view = show(manifestWith("deployed", "validated"));
            await settle();
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");

            view.rerender(<Page sessionId="s1" manifest={manifestWith("deployed", "deployed")} />);

            await waitFor(() => expect(sessionStorage.getItem(PENDING_KEY)).toBeNull());
        });

        test("when the approval is made after the page mounted: a manual Build & Activate still ends it", async () => {
            let approve: () => void = () => {};
            render(<Page sessionId="s1" manifest={manifestWith("deployed", "validated")} onApprove={a => (approve = a)} />, { wrapper: queryWrapper });
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));
            act(() => approve());
            await settle();
            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");

            await act(async () => {
                await bridged!.run();
            });

            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
        });

        test("the approval ends when the user's own save starts, not when it finishes", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            buildResponse = () => new Promise(() => {});
            show(manifestWith("deployed", "validated"));
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));

            act(() => {
                void bridged!.run();
            });
            await settle();

            expect(buildCalls()).toBe(1);
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
        });

        test("a click that lands while a save is already running does not end the approval", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            buildResponse = () => new Promise(() => {});
            show(VALIDATED);
            await waitFor(() => expect(buildCalls()).toBe(1));

            await act(async () => {
                await bridged!.run();
            });

            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");
            expect(buildCalls()).toBe(1);
        });

        test("a click that is blocked for missing credentials still ends the approval: the user is driving", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            missingCredentials = new Map([["TestAgent", 1]]);
            show(manifestWith("deployed", "validated"));
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));

            await act(async () => {
                await bridged!.run();
            });

            expect(buildCalls()).toBe(0);
            expect(deployCalls()).toBe(0);
            expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Credentials required" }));
            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
        });

        test("a retry that lands while a save is already running does not end the approval", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            buildResponse = () => new Promise(() => {});
            show(VALIDATED);
            await waitFor(() => expect(buildCalls()).toBe(1));

            await act(async () => {
                bridged!.retry();
            });

            expect(sessionStorage.getItem(PENDING_KEY)).toBe("s1");
            expect(buildCalls()).toBe(1);
        });

        test("when the user retries by hand: the approval ends", async () => {
            sessionStorage.setItem(PENDING_KEY, "s1");
            await loadPage();
            show(manifestWith("deployed", "validated"));
            await waitFor(() => expect(bridged).not.toBeNull());

            await act(async () => {
                bridged!.retry();
            });

            expect(sessionStorage.getItem(PENDING_KEY)).toBeNull();
        });

        test("with the build validated and the approval resumed: Build & Activate is hidden while the auto-save owns the flow", async () => {
            const first = await approveWhileGenerating();
            await reload(first);
            buildResponse = () => new Promise(() => {});

            show(VALIDATED);

            await waitFor(() => expect(buildCalls()).toBe(1));
            expect(bridged?.canBuildActivate).toBe(false);
        });
    });

    describe("the user leaves the session inside the page", () => {
        test("another session, then back while the build is validated: the approval is gone, nothing is saved, Build & Activate is offered", async () => {
            const first = await approveWhileGenerating();

            switchTo(first, "s2", GENERATING);
            switchTo(first, "s1", VALIDATED);
            await settle();

            expect(buildCalls()).toBe(0);
            await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));
        });

        test("another session and back while the save is in flight: the save is not started a second time", async () => {
            buildResponse = () => new Promise(() => {});
            const first = await approveWhileGenerating();
            switchTo(first, "s1", VALIDATED);
            await waitFor(() => expect(buildCalls()).toBe(1));

            switchTo(first, "s2", null);
            switchTo(first, "s1", VALIDATED);
            await settle();

            expect(buildCalls()).toBe(1);
        });

        test("a new build session never inherits the approval", async () => {
            const first = await approveWhileGenerating();

            switchTo(first, "brand-new", VALIDATED);
            await settle();

            expect(buildCalls()).toBe(0);
        });

        test("then a reload finds nothing to resume", async () => {
            const first = await approveWhileGenerating();
            switchTo(first, "s2", GENERATING);
            await reload(first);

            show(VALIDATED, "s1");
            await settle();

            expect(buildCalls()).toBe(0);
        });
    });

    test("a session that was never approved is not saved when it is shown validated (Build & Activate stays the user's call)", async () => {
        show(VALIDATED);
        await settle();

        expect(buildCalls()).toBe(0);
        expect(deployCalls()).toBe(0);
        await waitFor(() => expect(bridged?.canBuildActivate).toBe(true));
    });

    describe("replacing a component the Builder did not write", () => {
        test("declining ends the attempt: the approval is forgotten, and a later reload asks nothing and saves nothing", async () => {
            localStorage.removeItem("sam_builder_skip_overwrite_confirm");
            platformGet.mockResolvedValue({ data: [] });
            buildResponse = body => {
                const dryRun = Boolean((body as { dryRun?: boolean } | undefined)?.dryRun);
                return Promise.resolve({
                    dryRun,
                    operations: [{ kind: "agent", name: "TestAgent", action: "update", status: dryRun ? "planned" : "succeeded", resourceId: "agent-1", changedFields: ["systemPrompt"] }],
                    summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
                });
            };
            const first = await approveWhileGenerating();
            await reload(first);

            const reloaded = show(VALIDATED);
            await screen.findByText("Replace existing component?");
            fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
            await waitFor(() => expect(sessionStorage.getItem(PENDING_KEY)).toBeNull());
            const sent = buildCalls();
            await reload(reloaded);

            show(VALIDATED);
            await settle();

            expect(screen.queryByText("Replace existing component?")).toBeNull();
            expect(buildCalls()).toBe(sent);
        });
    });
});
