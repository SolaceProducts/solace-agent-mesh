// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { v2PlatformClient } from "@/testing/helpers/platformResponses";
import { STAMPED_VERSION, withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildManifest, SaveManifestResult } from "./hooks/useBuilderManifest";
import { FLIP_FAILED_MESSAGE } from "./hooks/useManifestDeployment";

function newQueryWrapper() {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("BuilderActions", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let RawBuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;

    const addNotification = vi.fn();
    const uploadArtifactFile = vi.fn();
    const platformPost = vi.fn();
    const platformGet = vi.fn();
    const platformDelete = vi.fn();
    const handleSubmit = vi.fn();
    const displayError = vi.fn();
    let builderIsResponding = false;
    let builderIsCancelling = false;
    let activeSessionId = "test-session";
    const workflowDeploy = vi.fn();
    const track = vi.fn();

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        builderIsResponding = false;
        builderIsCancelling = false;
        activeSessionId = "test-session";
        platformGet.mockResolvedValue({ data: [] });

        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled, tooltip }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean; tooltip?: string }) => (
                <button onClick={onClick} disabled={disabled} title={tooltip}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({
            api: {
                platform: v2PlatformClient({ get: platformGet, post: platformPost, delete: platformDelete }),
            },
        }));
        vi.doMock("@/utils/api", () => ({
            getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback),
        }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => {
                const busyAtThisRender = builderIsResponding || builderIsCancelling;
                return {
                    addNotification,
                    displayError,
                    uploadArtifactFile,
                    sessionId: activeSessionId,
                    allArtifacts: [],
                    handleSubmit: (...args: unknown[]) => (busyAtThisRender ? undefined : handleSubmit(...args)),
                    isResponding: builderIsResponding,
                    isCancelling: builderIsCancelling,
                };
            },
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({
            useAuthContext: () => ({
                userInfo: { username: "sam_dev_user" },
            }),
        }));

        workflowDeploy.mockResolvedValue(undefined);
        vi.doMock("./hooks/useWorkflowDeployment", () => ({
            useWorkflowDeployment: () => ({ deploy: (...args: unknown[]) => workflowDeploy(...args), isLoading: false }),
        }));

        vi.doMock("./ConnectorEditDialog", () => ({
            ConnectorEditDialog: () => null,
        }));

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({
                data: [],
                isLoading: false,
                refetch: () => Promise.resolve(),
            }),
        }));

        vi.doMock("./hooks/useArtifactVersions", () => ({
            useArtifactVersions: (m: BuildManifest | null) => new Map<string, number>(m?.components?.map(c => [c.filename, STAMPED_VERSION])),
        }));

        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            listManifestVersions: (_sessionId: string, filename: string) => Promise.resolve(filename === "wf.yaml" ? [4] : filename === "agent.yaml" ? [3] : []),
        }));

        vi.doMock("@/api/skills", () => ({
            useSaveSkillFromConfigMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue({ id: "skill-id-1" }) }),
        }));

        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({
            useIsBuilderServerApplyEnabled: () => false,
        }));

        vi.doMock("@/common/product-analytics/report", () => ({ report: track, reportingSessionId: () => "session-one" }));

        const mod = await import("./BuilderActions");
        RawBuilderActions = mod.BuilderActions;
        BuilderActions = withCurrentStamp(mod.BuilderActions, () => STAMPED_VERSION);
        queryWrapper = newQueryWrapper();
    });

    const VALID_AGENT_YAML = `
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

    const SHORT_DESC_AGENT_YAML = `
apps:
  - name: Tiny
    app_config:
      agent_name: Tiny
      description: short
      instruction: ${"x".repeat(120)}
`;

    const GATEWAY_YAML = `
apps:
  - name: order_gateway_app
    app_config:
      agent_card:
        description: Routes incoming order events to the OrderValidator agent.
      default_user_identity: gateway-bot@example.com
      event_handlers:
        - name: order_events
          target_agent_name: OrderValidator
          subscriptions:
            - topic: Orders/incoming
              qos: 1
      output_handlers:
        - name: validation_result
          topic_expression: static:Orders/validated
`;

    const WORKFLOW_YAML = `
apps:
  - name: Detector
    app_config:
      agent_name: Detector
      agent_type: workflow
      description: A workflow with a sufficiently long description to validate.
      nodes: []
      output_mapping: {}
`;

    function manifestWith(overrides: Partial<BuildManifest> = {}): BuildManifest {
        return {
            name: "my-build",
            full_validation_passed: true,
            components: [{ filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" }],
            ...overrides,
        };
    }

    test("auto-save fires platform POST and success notification for each session component", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/agents", expect.objectContaining({ name: "TestAgent" })));
        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success"));
    });

    test("a completed build is reported once the per-kind save lands", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(track).toHaveBeenCalledWith("builder:completed", {}));
        expect(track.mock.calls.filter(call => call[0] === "builder:completed")).toHaveLength(1);
    });

    test("a second save run in the same session is not reported again", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        const first = render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });
        await waitFor(() => expect(track.mock.calls.filter(call => call[0] === "builder:completed")).toHaveLength(1));
        first.unmount();

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost.mock.calls.filter(([url]) => url === "/api/v2/platform/agents")).toHaveLength(2));
        expect(track.mock.calls.filter(call => call[0] === "builder:completed")).toHaveLength(1);
    });

    test("a build that failed to save is not reported as completed", async () => {
        platformPost.mockRejectedValue(new Error("the platform refused the write"));
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost).toHaveBeenCalled());
        expect(track.mock.calls.filter(call => call[0] === "builder:completed")).toHaveLength(0);
    });

    test("auto-deploy chains after auto-save and triggers agentDeployments", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/agentDeployments", expect.objectContaining({ agentId: "new-id", action: "deploy" })));
        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Deployed"), "info"));
    });

    test("an agent declaring a config-level skills block binds the session-saved skill via skillIds", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const AGENT_WITH_SKILL_YAML = `
apps:
  - name: AdvisorAgent
    app_config:
      agent_name: AdvisorAgent
      description: A test agent with a sufficiently long description.
      instruction: |
        ${"x".repeat(120)}
      skills:
        - name: WeatherLookup
      agent_card:
        description: Another description on agent_card.
        skills:
          - id: advisor
            name: Advisor Ability
            description: Display-only card ability
`;
        const SKILL_YAML = `
name: WeatherLookup
description: Returns a short canned weather string for a given city name.
instruction_content: |
  Look up the canned weather string for the requested city and answer with it.
`;
        const fetchConfigByFilename = vi.fn().mockImplementation((filename: string) => Promise.resolve(filename === "s.yaml" ? SKILL_YAML : AGENT_WITH_SKILL_YAML));

        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [
                        { filename: "s.yaml", type: "skill", name: "WeatherLookup", status: "validated" },
                        { filename: "agent.yaml", type: "agent", name: "AdvisorAgent", status: "validated" },
                    ],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/agents", expect.objectContaining({ name: "AdvisorAgent", skillIds: ["skill-id-1"] })));
        const agentPayload = platformPost.mock.calls.find(([url]) => url === "/api/v2/platform/agents")?.[1] as Record<string, unknown>;
        expect(agentPayload.skillRefs).toBeUndefined();
        expect(agentPayload.loadableSkillNames).toBeUndefined();
        expect(agentPayload.skills).toEqual([{ id: "advisor", name: "Advisor Ability", description: "Display-only card ability" }]);
    });

    test("a skill-only build saves but emits NO false 'Deployed N component(s)' toast", async () => {
        const SKILL_YAML = `
name: WeatherLookup
description: Returns a short canned weather string for a given city name.
instruction_content: Reply with a one-line weather summary for the city.
`;
        const fetchConfigByFilename = vi.fn().mockResolvedValue(SKILL_YAML);

        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "s.yaml", type: "skill", name: "WeatherLookup", status: "validated" }],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("1 component(s) saved successfully"), "success"));
        expect(addNotification).not.toHaveBeenCalledWith(expect.stringMatching(/Deployed \d+ component/), expect.anything());
        expect(platformPost).not.toHaveBeenCalledWith("/api/v2/platform/agentDeployments", expect.anything());
    });

    test("gateway component flows through save (createGateway) then deploy (gatewayDeployment)", async () => {
        platformPost.mockResolvedValue({ id: "gw-id-1" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(GATEWAY_YAML);

        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "gw.yaml", type: "gateway", name: "OrderGateway", gateway_type: "event_mesh", status: "validated" }],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() =>
            expect(platformPost).toHaveBeenCalledWith(
                "/api/v2/platform/entrypoints",
                expect.objectContaining({
                    name: "OrderGateway",
                    entrypointType: "event_mesh",
                    values: expect.objectContaining({
                        default_user_identity: "gateway-bot@example.com",
                        event_handlers: expect.any(Array),
                    }),
                })
            )
        );
        await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/entrypointDeployments", expect.objectContaining({ entrypointId: "gw-id-1", action: "deploy" })));
    });

    test("auto-save routes client-side validation errors to the builder agent (auto-fix)", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(SHORT_DESC_AGENT_YAML);

        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "a.yaml", type: "agent", name: "Tiny", status: "validated" }],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const call = handleSubmit.mock.calls[0];
        expect(call[2]).toEqual(expect.stringContaining("Validation failed"));
        expect(call[8]).toEqual(expect.objectContaining({ auto_fix: true, resume_build: true }));
        expect(addNotification).not.toHaveBeenCalledWith(expect.anything(), "warning");
        expect(platformPost).not.toHaveBeenCalled();
    });

    describe("an auto-fix requested while the Builder is still answering", () => {
        type Api = { failure: { kind: string; message?: string }; retry: () => void; run: () => Promise<void> };
        const err422 = Object.assign(new Error("agent config is invalid"), { response: { status: 422 } });

        const mount = (failure: "save" | "deploy" | "settle", autoSaveProp = true) => {
            const manifest = manifestWith({ components: [{ filename: "a.yaml", type: "agent", name: "Tiny", status: "validated" }] });
            if (failure === "deploy") {
                platformPost.mockImplementation((url: string) => (url.includes("Deployments") ? Promise.reject(err422) : Promise.resolve({ id: "new-id" })));
            }
            if (failure === "settle") platformPost.mockResolvedValue({ id: "new-id" });
            const refreshAgentList = failure === "settle" ? () => Promise.reject(new Error("could not refresh the agent list")) : undefined;
            let yamlNow = failure === "save" ? SHORT_DESC_AGENT_YAML : VALID_AGENT_YAML;
            const fetchConfigByFilename = vi.fn(() => Promise.resolve(yamlNow));
            const rejectWith = (message: string) => platformPost.mockRejectedValue(Object.assign(new Error(message), { response: { status: 422 } }));
            let api: Api | null = null;
            const tree = (m: BuildManifest = manifest) => (
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={m}
                    fetchConfigByFilename={fetchConfigByFilename}
                    autoSave={autoSaveProp}
                    userAgentsRefetch={refreshAgentList}
                    onBuildActivateApiChange={next => {
                        if (next) api = next as unknown as Api;
                    }}
                />
            );
            const view = render(tree(), { wrapper: queryWrapper });
            return {
                rerender: (m?: BuildManifest) => act(() => view.rerender(tree(m))),
                kind: () => api?.failure.kind,
                retry: () => act(() => api!.retry()),
                run: () =>
                    act(async () => {
                        await api!.run();
                    }),
                useYaml: (yaml: string) => {
                    yamlNow = yaml;
                },
                reads: () => fetchConfigByFilename.mock.calls.length,
                rejectWith,
            };
        };

        test.each([
            ["a save failure, the turn ends", "save", "end", 1, "fixing"],
            ["a deploy failure, the turn ends", "deploy", "end", 1, "fixing"],
            ["a save failure, the turn is stopped", "save", "stop", 0, "retryable"],
            ["a deploy failure, the turn is stopped", "deploy", "stop", 0, "retryable"],
            ["a save failure, the session changes", "save", "switch", 0, "retryable"],
            ["a deploy failure, the session changes", "deploy", "switch", 0, "retryable"],
        ] as const)("%s", async (_label, failure, ending, delivered, finalKind) => {
            builderIsResponding = true;
            const view = mount(failure);
            await waitFor(() => expect(view.kind()).toBe("fixing"));
            expect(handleSubmit).not.toHaveBeenCalled();

            if (ending === "stop") {
                builderIsCancelling = true;
                view.rerender();
                builderIsCancelling = false;
            }
            if (ending === "switch") activeSessionId = "another-session";
            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).toHaveBeenCalledTimes(delivered);
            expect(view.kind()).toBe(finalKind);
            if (delivered) {
                const call = handleSubmit.mock.calls[0];
                expect(call[2]).toEqual(expect.stringContaining(failure === "save" ? "Validation failed" : "Deploy"));
                expect(call[8]).toEqual(expect.objectContaining({ auto_fix: true, resume_build: true }));
            }
        });

        test("is dropped when the Builder's own turn clears the failure, and the card stops saying it is fixing", async () => {
            builderIsResponding = true;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe("fixing"));

            view.rerender(manifestWith({ components: [{ filename: "a.yaml", type: "agent", name: "Tiny", status: "draft" }] }));
            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).not.toHaveBeenCalled();
            expect(view.kind()).toBe("none");
        });

        test("Try again while it is held leaves the card saying it is fixing, and the fix goes out once", async () => {
            builderIsResponding = true;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe("fixing"));

            const readsBefore = view.reads();
            view.retry();
            await waitFor(() => expect(view.reads()).toBeGreaterThan(readsBefore));
            await waitFor(() => expect(view.kind()).toBe("fixing"));
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));
            expect(handleSubmit).not.toHaveBeenCalled();
            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).toHaveBeenCalledTimes(1);
            expect(view.kind()).toBe("fixing");
        });

        test.each([
            ["the Builder is idle: the fix goes out at once", false, 1],
            ["the Builder is answering: the fix is held, then goes out when the turn ends", true, 0],
        ])("a deploy-time failure with no component is handled alike held and idle: %s", async (_label, responding, sentBeforeTurnEnds) => {
            builderIsResponding = responding;
            const view = mount("settle");
            await waitFor(() => expect(view.kind()).toBe("fixing"));
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));
            expect(handleSubmit).toHaveBeenCalledTimes(sentBeforeTurnEnds);

            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).toHaveBeenCalledTimes(1);
            expect(String(handleSubmit.mock.calls[0][2])).toContain("Deploy component");
            expect(String(handleSubmit.mock.calls[0][2])).toContain("could not refresh the agent list");
            expect(view.kind()).toBe("fixing");
        });

        test("a failure reported while Stop is pending and the Builder is no longer answering is not sent, and the card offers Try again", async () => {
            builderIsResponding = false;
            builderIsCancelling = true;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe("retryable"));

            builderIsCancelling = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).not.toHaveBeenCalled();
            expect(view.kind()).toBe("retryable");
        });

        test("Try again discards a held fix, and the 503 that follows is not sent when the turn ends", async () => {
            builderIsResponding = true;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe("fixing"));

            view.useYaml(VALID_AGENT_YAML);
            platformPost.mockRejectedValue(Object.assign(new Error("Service Unavailable"), { response: { status: 503 } }));
            view.retry();
            await waitFor(() => expect(view.kind()).toBe("retryable"));
            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).not.toHaveBeenCalled();
            expect(view.kind()).toBe("retryable");
        });

        test("a save that Build & Activate starts discards a held fix for the failure before it", async () => {
            builderIsResponding = true;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe("fixing"));

            view.useYaml(VALID_AGENT_YAML);
            platformPost.mockRejectedValue(Object.assign(new Error("Service Unavailable"), { response: { status: 503 } }));
            await view.run();
            await waitFor(() => expect(view.kind()).toBe("retryable"));
            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).not.toHaveBeenCalled();
        });

        test("a second and a third different failure in one flow: two fixes go out, then the card gives up", async () => {
            const view = mount("save");
            await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));

            view.useYaml(VALID_AGENT_YAML);
            view.rejectWith("B broke the config");
            await view.run();
            expect(handleSubmit).toHaveBeenCalledTimes(2);
            expect(String(handleSubmit.mock.calls[1][2])).toContain("B broke the config");

            view.rejectWith("C broke the config");
            await view.run();
            expect(handleSubmit).toHaveBeenCalledTimes(2);
            expect(view.kind()).toBe("retryable");
        });

        test("the same failure raised again after its fix went out is not sent a second time", async () => {
            const view = mount("save");
            await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));

            await view.run();
            await view.run();

            expect(handleSubmit).toHaveBeenCalledTimes(1);
        });

        test("not bridged, a failure the budget refuses is shown as a modal, not swallowed", async () => {
            const manifest = manifestWith({ components: [{ filename: "a.yaml", type: "agent", name: "Tiny", status: "validated" }] });
            let yamlNow = SHORT_DESC_AGENT_YAML;
            const fetchConfigByFilename = vi.fn(() => Promise.resolve(yamlNow));
            const tree = (autoSave: boolean) => <BuilderActions configYaml="" configType="agent" manifest={manifest} fetchConfigByFilename={fetchConfigByFilename} autoSave={autoSave} />;
            const view = render(tree(true), { wrapper: queryWrapper });
            await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
            act(() => view.rerender(tree(false)));
            yamlNow = VALID_AGENT_YAML;

            for (const message of ["B broke the config", "C broke the config"]) {
                platformPost.mockRejectedValue(Object.assign(new Error(message), { response: { status: 422 } }));
                await act(async () => {
                    fireEvent.click(await screen.findByText("Build & Activate"));
                });
                await waitFor(() => expect(String(JSON.stringify(handleSubmit.mock.calls)) + JSON.stringify(displayError.mock.calls)).toContain(message));
            }

            expect(handleSubmit).toHaveBeenCalledTimes(2);
            expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ error: expect.stringContaining("C broke the config") }));
            expect(displayError).not.toHaveBeenCalledWith(expect.objectContaining({ error: expect.stringContaining("B broke the config") }));
        });

        test("Try again that only redeploys discards a held fix for the earlier deploy failure", async () => {
            builderIsResponding = true;
            const view = mount("deploy");
            await waitFor(() => expect(view.kind()).toBe("fixing"));
            const deploysBefore = platformPost.mock.calls.filter(call => String(call[0]).includes("Deployments")).length;

            platformPost.mockImplementation((url: string) => (url.includes("Deployments") ? Promise.reject(Object.assign(new Error("Service Unavailable"), { response: { status: 503 } })) : Promise.resolve({ id: "new-id" })));
            view.retry();
            await waitFor(() => expect(platformPost.mock.calls.filter(call => String(call[0]).includes("Deployments")).length).toBeGreaterThan(deploysBefore));
            await waitFor(() => expect(view.kind()).toBe("retryable"));
            builderIsResponding = false;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).not.toHaveBeenCalled();
            expect(view.kind()).toBe("retryable");
        });

        test.each([
            ["Stop and the end of the turn land in one render", { responding: true, cancelling: false }, "fixing", { responding: false, cancelling: true }, 0, "retryable"],
            ["the hold is raised while Stop is pending, then both clear in one render", { responding: true, cancelling: true }, "retryable", { responding: false, cancelling: false }, 0, "retryable"],
        ] as const)("%s", async (_label, during, kindBefore, after, delivered, finalKind) => {
            builderIsResponding = during.responding;
            builderIsCancelling = during.cancelling;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe(kindBefore));

            builderIsResponding = after.responding;
            builderIsCancelling = after.cancelling;
            view.rerender();
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(handleSubmit).toHaveBeenCalledTimes(delivered);
            expect(view.kind()).toBe(finalKind);
        });

        test("is sent once, however often the Builder goes idle afterwards", async () => {
            builderIsResponding = true;
            const view = mount("save");
            await waitFor(() => expect(view.kind()).toBe("fixing"));

            for (const responding of [false, true, false, true, false]) {
                builderIsResponding = responding;
                view.rerender();
                await act(async () => new Promise(resolve => setTimeout(resolve, 20)));
            }

            expect(handleSubmit).toHaveBeenCalledTimes(1);
        });

        test("is sent straight away when the Builder is idle", async () => {
            mount("save");

            await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
        });

        describe("timers", () => {
            const withFakeTimers = async (body: () => Promise<void>) => {
                vi.useFakeTimers({ shouldAdvanceTime: true });
                try {
                    await body();
                } finally {
                    vi.useRealTimers();
                }
            };
            const advance = (ms: number) =>
                act(() => {
                    vi.advanceTimersByTime(ms);
                });

            test("the watchdog counts from the send, not from the failure", () =>
                withFakeTimers(async () => {
                    builderIsResponding = true;
                    const view = mount("save");
                    await waitFor(() => expect(view.kind()).toBe("fixing"));

                    advance(31_000);
                    expect(view.kind()).toBe("fixing");

                    builderIsResponding = false;
                    view.rerender();
                    expect(handleSubmit).toHaveBeenCalledTimes(1);

                    advance(25_000);
                    expect(view.kind()).toBe("fixing");
                    advance(10_000);
                    expect(view.kind()).toBe("retryable");
                }));

            test("Try again after the watchdog gave up sends the same fix again", () =>
                withFakeTimers(async () => {
                    const view = mount("save");
                    await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));

                    advance(31_000);
                    expect(view.kind()).toBe("retryable");
                    view.retry();

                    await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(2));
                    expect(handleSubmit.mock.calls[1][2]).toBe(handleSubmit.mock.calls[0][2]);
                    expect(view.kind()).toBe("fixing");
                }));

            test("a hold that outlasts its limit is dropped and Try again is offered", () =>
                withFakeTimers(async () => {
                    builderIsResponding = true;
                    const view = mount("save");
                    await waitFor(() => expect(view.kind()).toBe("fixing"));

                    advance(115_000);
                    expect(view.kind()).toBe("fixing");
                    advance(10_000);
                    expect(view.kind()).toBe("retryable");

                    builderIsResponding = false;
                    view.rerender();
                    expect(handleSubmit).not.toHaveBeenCalled();
                }));
        });
    });

    test("a gateway written as a declarative entrypoint file fails the save instead of reporting success", async () => {
        const ENTRYPOINT_FILE = ["kind: entrypoint", "name: OrderGateway", "spec:", "  type: event_mesh", "  values:", "    event_rules: []"].join("\n");
        const fetchConfigByFilename = vi.fn().mockResolvedValue(ENTRYPOINT_FILE);

        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "gw.yaml", type: "gateway", name: "OrderGateway", gateway_type: "event_mesh", status: "validated" }],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const call = handleSubmit.mock.calls[0];
        expect(call[2]).toEqual(expect.stringMatching(/"OrderGateway".*event_handlers.*sam config apply/s));
        expect(call[8]).toEqual(expect.objectContaining({ auto_fix: true, resume_build: true }));
        expect(platformPost).not.toHaveBeenCalledWith("/api/v2/platform/entrypoints", expect.anything());
        expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
    });

    test("auto-save iterates over session components and skips platform-origin", async () => {
        platformPost.mockResolvedValue({ data: { id: "new-id" } });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [
                        { filename: "a.yaml", type: "agent", name: "A", status: "validated" },
                        {
                            filename: "b.yaml",
                            type: "agent",
                            name: "PlatformAgent",
                            status: "validated",
                            origin: "platform",
                        },
                    ],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(platformPost.mock.calls.filter(([url]) => url === "/api/v2/platform/agents").length).toBe(1));
        expect(fetchConfigByFilename).toHaveBeenCalledWith("a.yaml", expect.anything());
        expect(fetchConfigByFilename).not.toHaveBeenCalledWith("b.yaml", expect.anything());
    });

    test("auto-save waits for full_validation_passed before posting", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        const { rerender } = render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith({ full_validation_passed: false })} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await new Promise(resolve => setTimeout(resolve, 50));
        expect(platformPost).not.toHaveBeenCalled();

        platformPost.mockResolvedValue({ data: { id: "new-id" } });
        const Wrapper = queryWrapper;
        rerender(
            <Wrapper>
                <BuilderActions configYaml="" configType="agent" manifest={manifestWith({ full_validation_passed: true })} fetchConfigByFilename={fetchConfigByFilename} autoSave />
            </Wrapper>
        );

        await waitFor(() => expect(platformPost.mock.calls.filter(([url]) => url === "/api/v2/platform/agents").length).toBe(1));
    });

    test("platformPost 5xx surfaces an error modal and does NOT route to auto-fix", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        const serverErr = Object.assign(new Error("Internal Server Error"), {
            response: { status: 500 },
        });
        platformPost.mockRejectedValue(serverErr);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost).toHaveBeenCalled());
        await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ error: expect.stringContaining("Internal Server Error") })));
        expect(handleSubmit).not.toHaveBeenCalled();
    });

    test("when bridged to chat, a 5xx failure is suppressed from the global modal and surfaced via the bridge (single in-plan surface)", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        const serverErr = Object.assign(new Error("Internal Server Error"), { response: { status: 500 } });
        platformPost.mockRejectedValue(serverErr);
        const failures: Array<{ kind: string; message?: string }> = [];
        const onBuildActivateApiChange = vi.fn((api: { failure: { kind: string; message?: string } } | null) => {
            if (api) failures.push(api.failure);
        });

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave onBuildActivateApiChange={onBuildActivateApiChange} />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost).toHaveBeenCalled());
        await waitFor(() => expect(failures.at(-1)?.kind).toBe("retryable"));
        expect(failures.at(-1)?.message).toContain("Internal Server Error");
        expect(displayError).not.toHaveBeenCalled();
    });

    describe("the deploy sweep after a per-kind save", () => {
        type Api = { failure: { kind: string; message?: string }; retry: () => void; run: () => void };
        const agentYaml = (name: string) => VALID_AGENT_YAML.replaceAll("TestAgent", name);
        const twoAgents = manifestWith({
            components: [
                { filename: "first.yaml", type: "agent", name: "TestAgent", status: "validated" },
                { filename: "second.yaml", type: "agent", name: "SecondAgent", status: "validated" },
            ],
        });
        const fetchTwoAgents = (filename: string) => Promise.resolve(agentYaml(filename === "first.yaml" ? "TestAgent" : "SecondAgent"));

        const mount = (bridged: boolean, autoSave: boolean) => {
            const shown: string[] = [];
            let api: Api | null = null;
            const tree = (withAutoSave: boolean) => (
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={twoAgents}
                    fetchConfigByFilename={fetchTwoAgents}
                    autoSave={withAutoSave}
                    onBuildActivateApiChange={
                        bridged
                            ? next => {
                                  if (!next) return;
                                  api = next as unknown as Api;
                                  const failure = (next as unknown as Api).failure;
                                  if (failure.message) shown.push(failure.message);
                              }
                            : undefined
                    }
                />
            );
            const view = render(tree(autoSave), { wrapper: queryWrapper });
            return { shown, api: () => api!, endApproval: () => view.rerender(tree(false)) };
        };
        const sweepRan = async (lookingFor: string, misses: ReturnType<typeof vi.spyOn>) => waitFor(() => expect(misses).toHaveBeenCalledWith("[builder] deploy lookup miss", expect.objectContaining({ lookingFor })));
        const sweepMisses = () => vi.spyOn(console, "warn").mockImplementation(() => {});

        test.each([
            ["a server error", Object.assign(new Error("Internal Server Error"), { response: { status: 500 } }), "Internal Server Error"],
            ["a rejected config", Object.assign(new Error("agent config is invalid"), { response: { status: 422 } }), "agent config is invalid"],
        ])("bridged: after %s, the failed component is not reported missing but the one never reached is", async (_label, rejection, reason) => {
            platformPost.mockRejectedValue(rejection);
            const warn = sweepMisses();
            const view = mount(true, true);
            await waitFor(() => expect(view.shown.some(m => m.includes(reason))).toBe(true));
            view.endApproval();

            act(() => view.api().retry());
            await sweepRan("SecondAgent", warn);
            await waitFor(() => expect(view.shown.some(m => m.includes("Not found on platform: SecondAgent"))).toBe(true));

            expect(view.shown.filter(m => /Not found on platform:[^\n]*TestAgent/.test(m))).toEqual([]);
            expect(view.shown.at(-1)).toContain('Failed to save "TestAgent"');
            expect(view.shown.at(-1)).toContain("Not found on platform: SecondAgent");
            warn.mockRestore();
        });

        test("not bridged: the toast and the action bar name only the component the save never reached", async () => {
            platformPost.mockRejectedValue(Object.assign(new Error("Internal Server Error"), { response: { status: 500 } }));
            const warn = sweepMisses();
            const view = mount(false, true);
            await waitFor(() => expect(displayError).toHaveBeenCalled());
            view.endApproval();

            fireEvent.click(await screen.findByText("Retry"));
            await sweepRan("SecondAgent", warn);
            await waitFor(() => expect(addNotification.mock.calls.some(call => String(call[0]).includes("Not found on platform: SecondAgent"))).toBe(true));

            expect(addNotification.mock.calls.filter(call => /Not found on platform:[^\n]*TestAgent/.test(String(call[0])))).toEqual([]);
            expect(document.body.textContent).not.toMatch(/Not found on platform:[^.]*TestAgent/);
            expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ error: expect.stringContaining('Failed to save "TestAgent"') }));
            warn.mockRestore();
        });

        test("a build of one component: the banner names it and never says it is missing", async () => {
            platformPost.mockRejectedValue(Object.assign(new Error("Internal Server Error"), { response: { status: 500 } }));
            const warn = sweepMisses();
            const shown: string[] = [];
            let api: Api | null = null;
            const tree = (autoSave: boolean) => (
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={manifestWith()}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(VALID_AGENT_YAML)}
                    autoSave={autoSave}
                    onBuildActivateApiChange={next => {
                        if (!next) return;
                        api = next as unknown as Api;
                        const failure = (next as unknown as Api).failure;
                        if (failure.message) shown.push(failure.message);
                    }}
                />
            );
            const view = render(tree(true), { wrapper: queryWrapper });
            await waitFor(() => expect(shown.length).toBeGreaterThan(0));
            view.rerender(tree(false));

            act(() => api!.retry());
            await sweepRan("TestAgent", warn);
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));

            expect(shown.filter(m => m.includes("Not found on platform"))).toEqual([]);
            expect(shown.at(-1)).toBe('Failed to save "TestAgent": Internal Server Error');
            warn.mockRestore();
        });

        test("a component an earlier click saved is deployed by the next click, which skips it as already on the platform", async () => {
            const record = { id: "saved-1", name: "TestAgent", deploymentStatus: "not_deployed" };
            let created = false;
            let deployAttempts = 0;
            platformGet.mockImplementation((path: string) => Promise.resolve({ data: created && path.startsWith("/api/v2/platform/agents") ? [record] : [] }));
            platformPost.mockImplementation(async (url: string) => {
                if (url.includes("agentDeployments")) {
                    deployAttempts += 1;
                    if (deployAttempts === 1) throw new Error("deployment refused");
                    record.deploymentStatus = "deployed";
                    return {};
                }
                created = true;
                return { id: record.id };
            });
            let api: Api | null = null;
            const saveManifest = vi.fn(async (_updater: (prev: BuildManifest) => BuildManifest) => ({ ok: true }) as SaveManifestResult);
            render(
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={manifestWith()}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(VALID_AGENT_YAML)}
                    saveManifest={saveManifest}
                    onBuildActivateApiChange={next => (api = (next as unknown as Api) ?? api)}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(api).not.toBeNull());

            await act(async () => api!.run());
            await waitFor(() => expect(deployAttempts).toBe(1));
            await act(async () => new Promise(resolve => setTimeout(resolve, 50)));
            saveManifest.mockClear();

            await act(async () => api!.run());

            await waitFor(() => expect(deployAttempts).toBe(2));
            const written = saveManifest.mock.calls.map(call => call[0](manifestWith()).components?.[0]);
            expect(written.some(component => component?.status === "deployed" && component?.id === "saved-1")).toBe(true);
        });

        test("not bridged: the toast names a component this build could not confirm is its own", async () => {
            platformGet.mockImplementation((path: string) => Promise.resolve({ data: path.startsWith("/api/v2/platform/agents") ? [{ id: "stranger-1", name: "TestAgent", deploymentStatus: "deployed" }] : [] }));
            render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={vi.fn().mockResolvedValue(VALID_AGENT_YAML)} />, { wrapper: queryWrapper });

            fireEvent.click(await screen.findByText("Build & Activate"));

            await waitFor(() => expect(addNotification.mock.calls.some(call => String(call[0]).includes("TestAgent already exists on the platform and this build could not confirm it is its own"))).toBe(true));
            expect(addNotification.mock.calls.some(call => String(call[0]).includes("Save the build first"))).toBe(false);
        });

        test("a component the platform already holds that this build holds no id for is named without claiming whose it is, not reported as unsaved", async () => {
            platformGet.mockImplementation((path: string) => Promise.resolve({ data: path.startsWith("/api/v2/platform/agents") ? [{ id: "stranger-1", name: "TestAgent", deploymentStatus: "deployed" }] : [] }));
            let api: Api | null = null;
            render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={vi.fn().mockResolvedValue(VALID_AGENT_YAML)} onBuildActivateApiChange={next => (api = (next as unknown as Api) ?? api)} />, {
                wrapper: queryWrapper,
            });
            await waitFor(() => expect(api).not.toBeNull());

            await act(async () => api!.run());

            await waitFor(() =>
                expect(api!.failure.message ?? "").toContain("TestAgent already exists on the platform and this build could not confirm it is its own; open it from the list to keep working on it, or rename this one and build again.")
            );
            expect(api!.failure.message).not.toContain("Save the build first");
            expect(api!.failure.message).not.toContain("not created by");
            expect(api!.failure.message).not.toContain("remove");
            expect(platformPost.mock.calls.some(call => String(call[0]).includes("agentDeployments"))).toBe(false);
        });

        test("the session's own record, which an earlier click saved and no id was recorded for, is not called somebody else's, and a second click ends the same way without deploying", async () => {
            platformGet.mockImplementation((path: string) => Promise.resolve({ data: path.startsWith("/api/v2/platform/agents") ? [{ id: "own-1", name: "TestAgent", deploymentStatus: "not_deployed" }] : [] }));
            let api: Api | null = null;
            render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={vi.fn().mockResolvedValue(VALID_AGENT_YAML)} onBuildActivateApiChange={next => (api = (next as unknown as Api) ?? api)} />, {
                wrapper: queryWrapper,
            });
            await waitFor(() => expect(api).not.toBeNull());

            for (let click = 0; click < 2; click++) {
                await act(async () => api!.run());
                await waitFor(() => expect(api!.failure.message ?? "").toContain("TestAgent already exists on the platform and this build could not confirm it is its own"));
                expect(api!.failure.message).not.toMatch(/not created by|somebody else|remove/);
            }
            expect(platformPost.mock.calls.some(call => String(call[0]).includes("agentDeployments"))).toBe(false);
        });

        test("a retry whose save succeeds still reports every component the platform does not list", async () => {
            platformPost.mockRejectedValueOnce(Object.assign(new Error("Internal Server Error"), { response: { status: 500 } })).mockResolvedValue({ id: "new-id" });
            const warn = sweepMisses();
            const view = mount(true, true);
            await waitFor(() => expect(view.shown.some(m => m.includes("Internal Server Error"))).toBe(true));
            view.endApproval();

            act(() => view.api().retry());
            await sweepRan("TestAgent", warn);

            await waitFor(() => expect(view.shown.some(m => m.includes("Not found on platform: TestAgent, SecondAgent"))).toBe(true));
            warn.mockRestore();
        });
    });

    test("platformPost 4xx routes to agent auto-fix instead of toasting", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        const schemaErr = Object.assign(new Error("name 'TestAgent' violates the schema rule"), { response: { status: 422 } });
        platformPost.mockRejectedValue(schemaErr);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const call = handleSubmit.mock.calls[0];
        expect(call[2]).toEqual(expect.stringContaining("schema rule"));
        expect(call[8]).toEqual(expect.objectContaining({ auto_fix: true, resume_build: true }));
        expect(addNotification).not.toHaveBeenCalledWith(expect.anything(), "warning");
    });

    test("platformPost 401 surfaces an error modal (auth needs user action, not a rebuild)", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        const authErr = Object.assign(new Error("Unauthorized"), {
            response: { status: 401 },
        });
        platformPost.mockRejectedValue(authErr);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ error: expect.stringContaining("Unauthorized") })));
        expect(handleSubmit).not.toHaveBeenCalled();
    });

    test("fetchConfigByFilename rejection routes to auto-fix (treated as a fixable client-side error)", async () => {
        const fetchConfigByFilename = vi.fn().mockRejectedValue(new Error("Failed to decode YAML: unexpected token"));

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const call = handleSubmit.mock.calls[0];
        expect(call[2]).toEqual(expect.stringContaining("Failed to decode YAML"));
        expect(call[8]).toEqual(expect.objectContaining({ auto_fix: true }));
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("fetchConfigByFilename network rejection (ERR_NETWORK) surfaces an error modal, not auto-fix", async () => {
        const networkErr = Object.assign(new Error("Network error: connection refused"), {
            code: "ERR_NETWORK",
        });
        const fetchConfigByFilename = vi.fn().mockRejectedValue(networkErr);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ error: expect.stringContaining("Network error") })));
        expect(handleSubmit).not.toHaveBeenCalled();
    });

    test("validation error clears when manifest contents change", async () => {
        const fetchConfigByFilename = vi.fn().mockResolvedValue(SHORT_DESC_AGENT_YAML);

        const { rerender } = render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "a.yaml", type: "agent", name: "Tiny", status: "validated" }],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
                autoSave
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());

        expect(screen.queryByTitle(/Validation failed/)).not.toBeNull();

        rerender(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "a.yaml", type: "agent", name: "Tiny", status: "draft" }],
                })}
                fetchConfigByFilename={fetchConfigByFilename}
            />
        );

        await waitFor(() => expect(screen.queryByTitle(/Validation failed/)).toBeNull());
    });

    test("shows 'Build & Activate' for a restored, validated, undeployed build", async () => {
        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={vi.fn()} />, { wrapper: queryWrapper });
        expect(await screen.findByText("Build & Activate")).toBeInTheDocument();
    });

    describe("a validation stamp that records the version each component was validated at", () => {
        const stampedAt = (version: number) => manifestWith({ full_validation_versions: { "agent.yaml": version } });

        test("a stamp recorded at the version the config stands at offers Build & Activate", async () => {
            render(<RawBuilderActions configYaml="" configType="agent" manifest={stampedAt(STAMPED_VERSION)} fetchConfigByFilename={vi.fn()} />, { wrapper: queryWrapper });
            expect(await screen.findByText("Build & Activate")).toBeInTheDocument();
        });

        test("a stamp recorded before the config was edited does not", async () => {
            render(<RawBuilderActions configYaml="" configType="agent" manifest={stampedAt(STAMPED_VERSION - 1)} fetchConfigByFilename={vi.fn()} />, { wrapper: queryWrapper });
            await new Promise(resolve => setTimeout(resolve, 50));
            expect(screen.queryByText("Build & Activate")).toBeNull();
        });

        test("a stamp that recorded no versions does not", async () => {
            render(<RawBuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={vi.fn()} />, { wrapper: queryWrapper });
            await new Promise(resolve => setTimeout(resolve, 50));
            expect(screen.queryByText("Build & Activate")).toBeNull();
        });

        test("auto-save does not post a build whose stamp predates an edit", async () => {
            const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
            render(<RawBuilderActions configYaml="" configType="agent" manifest={stampedAt(STAMPED_VERSION - 1)} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

            await new Promise(resolve => setTimeout(resolve, 50));
            expect(platformPost).not.toHaveBeenCalled();
        });

        test("auto-save posts a build whose stamp is current", async () => {
            platformPost.mockResolvedValue({ id: "new-id" });
            const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
            render(<RawBuilderActions configYaml="" configType="agent" manifest={stampedAt(STAMPED_VERSION)} fetchConfigByFilename={fetchConfigByFilename} autoSave />, { wrapper: queryWrapper });

            await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/agents", expect.objectContaining({ name: "TestAgent" })));
        });
    });

    test("a non-empty credentials gate refuses Build & Activate before anything is saved", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} componentsMissingCreds={new Map([["EntKeyAPI", 1]])} />, { wrapper: queryWrapper });

        fireEvent.click(await screen.findByText("Build & Activate"));

        await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Credentials required", error: expect.stringContaining("EntKeyAPI (1 credential)") })));
        expect(fetchConfigByFilename).not.toHaveBeenCalled();
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("a non-empty credentials gate lets the auto-save land but holds back the auto-deploy", async () => {
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} autoSave componentsMissingCreds={new Map([["EntKeyAPI", 1]])} />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/agents", expect.objectContaining({ name: "TestAgent" })));
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 100));
        });
        expect(platformPost).not.toHaveBeenCalledWith("/api/v2/platform/agentDeployments", expect.anything());
    });

    test("hides 'Build & Activate' while the fresh auto-save flow owns deploy (autoSave)", async () => {
        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={vi.fn()} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(platformGet).toHaveBeenCalled());
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
        });
        expect(screen.queryByText("Build & Activate")).toBeNull();
    });

    test("hides 'Build & Activate' until the build is fully validated", async () => {
        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith({ full_validation_passed: false })} fetchConfigByFilename={vi.fn()} />, { wrapper: queryWrapper });
        await waitFor(() => expect(screen.queryByText("Build & Activate")).toBeNull());
    });

    test("hides 'Build & Activate' while a component is blocked, whatever the stamp says, and saves and deploys nothing", async () => {
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [
                        { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session" },
                        { filename: "inventory_api.json", type: "connector", name: "inventory_api", status: "blocked", origin: "session", blocked_reason: "the spec URL" },
                    ],
                })}
                fetchConfigByFilename={vi.fn()}
                autoSave
            />,
            { wrapper: queryWrapper }
        );
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
        });
        expect(screen.queryByText("Build & Activate")).toBeNull();
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("reload-safe deploy resolves by platform id even when the name does not match", async () => {
        platformGet.mockResolvedValue({
            data: [{ id: "agent-uuid-1", name: "renamed_on_platform", deploymentStatus: "not_deployed" }],
        });
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifestWith({
                    components: [{ filename: "a.yaml", type: "agent", name: "TestAgent", id: "agent-uuid-1", status: "validated", origin: "session" }],
                })}
                fetchConfigByFilename={vi.fn()}
            />,
            { wrapper: queryWrapper }
        );

        fireEvent.click(await screen.findByText("Build & Activate"));

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith("/api/v2/platform/agentDeployments", expect.objectContaining({ agentId: "agent-uuid-1", action: "deploy" })));
    });

    const namedSession = (spy: { mock: { calls: unknown[][] } }) => spy.mock.calls.some(call => (call[1] as { forSession?: string } | undefined)?.forSession === "test-session");

    test("in-flow deploy records the platform id into the manifest (markComponentsDeployed)", async () => {
        platformGet.mockResolvedValue({ data: [] });
        platformPost.mockResolvedValue({ id: "new-id" });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(VALID_AGENT_YAML);
        const applied: BuildManifest[] = [];
        const saveManifest = vi.fn(async (updater: (m: BuildManifest) => BuildManifest) => {
            applied.push(updater(manifestWith()));
            return { ok: true } as SaveManifestResult;
        });

        render(<BuilderActions configYaml="" configType="agent" manifest={manifestWith()} fetchConfigByFilename={fetchConfigByFilename} saveManifest={saveManifest} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(applied.some(m => m.components?.some(c => c.name === "TestAgent" && c.status === "deployed" && c.id === "new-id"))).toBe(true));

        expect(applied.some(m => m.components?.some(c => c.name === "TestAgent" && c.deployed_version === 3))).toBe(true);
        expect(namedSession(saveManifest)).toBe(true);
    });

    test("a FAILED workflow deploy is not recorded as deployed and stamps no version", async () => {
        platformGet.mockResolvedValue({ data: [] });
        platformPost.mockResolvedValue({ id: "wf-uuid-1" });
        workflowDeploy.mockRejectedValue(new Error("deployer did not ACK"));

        const workflowManifest = manifestWith({
            components: [{ filename: "wf.yaml", type: "workflow", name: "Detector", status: "validated", origin: "session" }],
        });
        const applied: BuildManifest[] = [];
        const saveManifest = vi.fn(async (updater: (m: BuildManifest) => BuildManifest): Promise<SaveManifestResult> => {
            applied.push(updater(workflowManifest));
            return { ok: true };
        });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(WORKFLOW_YAML);

        render(<BuilderActions configYaml="" configType="workflow" manifest={workflowManifest} fetchConfigByFilename={fetchConfigByFilename} saveManifest={saveManifest} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(workflowDeploy).toHaveBeenCalledWith(expect.objectContaining({ workflowId: "wf-uuid-1", action: "deploy" })));

        expect(applied.some(m => m.components?.some(c => c.name === "Detector" && c.deployed_version !== undefined))).toBe(false);
        expect(applied.some(m => m.components?.some(c => c.name === "Detector" && c.status === "deployed"))).toBe(false);
    });

    test("a successful workflow deploy DOES stamp the version behind it", async () => {
        platformGet.mockResolvedValue({ data: [] });
        platformPost.mockResolvedValue({ id: "wf-uuid-1" });

        const workflowManifest = manifestWith({
            components: [{ filename: "wf.yaml", type: "workflow", name: "Detector", status: "validated", origin: "session" }],
        });
        const applied: BuildManifest[] = [];
        const saveManifest = vi.fn(async (updater: (m: BuildManifest) => BuildManifest): Promise<SaveManifestResult> => {
            applied.push(updater(workflowManifest));
            return { ok: true };
        });
        const fetchConfigByFilename = vi.fn().mockResolvedValue(WORKFLOW_YAML);

        render(<BuilderActions configYaml="" configType="workflow" manifest={workflowManifest} fetchConfigByFilename={fetchConfigByFilename} saveManifest={saveManifest} autoSave />, { wrapper: queryWrapper });

        await waitFor(() => expect(applied.some(m => m.components?.some(c => c.name === "Detector" && c.status === "deployed"))).toBe(true));
        expect(applied.some(m => m.components?.some(c => c.name === "Detector" && c.deployed_version === 4))).toBe(true);
    });

    describe("an id-only manifest write (the deploy itself did not run or failed)", () => {
        const noIdManifest = () =>
            manifestWith({
                components: [{ filename: "a.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session", id: "agent-uuid-9" }],
            });
        const lostWrite = vi.fn(async () => ({ ok: false, reason: "failed" }) as SaveManifestResult);

        test("does not claim the deploy succeeded when the deploy failed", async () => {
            platformGet.mockResolvedValue({ data: [{ id: "agent-uuid-9", name: "TestAgent", deploymentStatus: "not_deployed" }] });
            platformPost.mockRejectedValue(new Error("deploy refused"));
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            const manifest = noIdManifest();

            render(<BuilderActions configYaml="" configType="agent" manifest={manifest} fetchConfigByFilename={vi.fn()} saveManifest={lostWrite} />, { wrapper: queryWrapper });
            fireEvent.click(await screen.findByText("Build & Activate"));

            await waitFor(() => expect(warn).toHaveBeenCalledWith("[builder] could not record platform ids", expect.anything()));
            expect(addNotification).not.toHaveBeenCalledWith(FLIP_FAILED_MESSAGE, "warning");
            warn.mockRestore();
        });

        test("stays silent when the id-only write lands", async () => {
            platformGet.mockResolvedValue({ data: [{ id: "agent-uuid-9", name: "TestAgent", deploymentStatus: "not_deployed" }] });
            platformPost.mockRejectedValue(new Error("deploy refused"));
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            const saved = vi.fn(async () => ({ ok: true }) as SaveManifestResult);

            render(<BuilderActions configYaml="" configType="agent" manifest={noIdManifest()} fetchConfigByFilename={vi.fn()} saveManifest={saved} />, { wrapper: queryWrapper });
            fireEvent.click(await screen.findByText("Build & Activate"));

            await waitFor(() => expect(saved).toHaveBeenCalled());
            expect(warn).not.toHaveBeenCalledWith("[builder] could not record platform ids", expect.anything());
            expect(addNotification).not.toHaveBeenCalledWith(FLIP_FAILED_MESSAGE, "warning");
            warn.mockRestore();
        });

        test("still reports a lost write after a deploy that succeeded", async () => {
            platformGet.mockResolvedValue({ data: [{ id: "agent-uuid-9", name: "TestAgent", deploymentStatus: "not_deployed" }] });
            platformPost.mockResolvedValue({});

            render(<BuilderActions configYaml="" configType="agent" manifest={noIdManifest()} fetchConfigByFilename={vi.fn()} saveManifest={lostWrite} />, { wrapper: queryWrapper });
            fireEvent.click(await screen.findByText("Build & Activate"));

            await waitFor(() => expect(addNotification).toHaveBeenCalledWith(FLIP_FAILED_MESSAGE, "warning"));
        });
    });

    test("reload-safe deploy records the deployed status for the record the manifest's id names", async () => {
        platformGet.mockResolvedValue({
            data: [{ id: "agent-uuid-9", name: "TestAgent", deploymentStatus: "not_deployed" }],
        });
        const noIdManifest = manifestWith({
            components: [{ filename: "a.yaml", type: "agent", name: "TestAgent", status: "validated", origin: "session", id: "agent-uuid-9" }],
        });
        const applied: BuildManifest[] = [];
        const saveManifest = vi.fn(async (updater: (m: BuildManifest) => BuildManifest) => {
            applied.push(updater(noIdManifest));
            return { ok: true } as SaveManifestResult;
        });

        render(<BuilderActions configYaml="" configType="agent" manifest={noIdManifest} fetchConfigByFilename={vi.fn()} saveManifest={saveManifest} />, { wrapper: queryWrapper });

        fireEvent.click(await screen.findByText("Build & Activate"));

        await waitFor(() => expect(applied.some(m => m.components?.some(c => c.name === "TestAgent" && c.id === "agent-uuid-9" && c.status === "deployed"))).toBe(true));
        expect(namedSession(saveManifest)).toBe(true);
    });
});

describe("BuilderActions, builder_server_apply off — a save that lands with a component unwritten", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;
    const platformPost = vi.fn();
    const platformGet = vi.fn();
    const saveSkill = vi.fn();

    const GATEWAY = `
apps:
  - name: order_gateway_app
    app_config:
      agent_card:
        description: Routes incoming order events to the OrderValidator agent.
      default_user_identity: gateway-bot@example.com
      event_handlers:
        - name: order_events
          target_agent_name: OrderValidator
          subscriptions:
            - topic: Orders/incoming
              qos: 1
`;
    const AGENT = `
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
    const WORKFLOW = `
apps:
  - name: Detector
    app_config:
      agent_name: Detector
      agent_type: workflow
      description: A workflow with a sufficiently long description to validate.
      nodes: []
      output_mapping: {}
`;
    const SKILL = `
name: WeatherLookup
description: Returns a short canned weather string for a given city name.
instruction_content: Reply with a one-line weather summary for the city.
`;
    const YAML_BY_FILENAME: Record<string, string> = { "wf.yaml": WORKFLOW, "agent.yaml": AGENT, "s.yaml": SKILL, "gw.yaml": GATEWAY };
    const COMPONENT_BY_FILENAME: Record<string, NonNullable<BuildManifest["components"]>[number]> = {
        "wf.yaml": { filename: "wf.yaml", type: "workflow", name: "Detector", status: "validated", origin: "session" },
        "agent.yaml": { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" },
        "s.yaml": { filename: "s.yaml", type: "skill", name: "WeatherLookup", status: "validated" },
        "gw.yaml": { filename: "gw.yaml", type: "gateway", name: "OrderGateway", gateway_type: "event_mesh", status: "validated" },
    };

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.clear();
        platformGet.mockResolvedValue({ data: [] });
        saveSkill.mockResolvedValue({ id: "skill-id-1", name: "WeatherLookup", action: "created" });
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
        vi.doMock("@/utils/api", () => ({ getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback) }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ addNotification: vi.fn(), displayError: vi.fn(), uploadArtifactFile: vi.fn(), sessionId: "test-session", allArtifacts: [], handleSubmit: vi.fn(), isResponding: false }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn().mockResolvedValue(undefined), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/skills", () => ({ useSaveSkillFromConfigMutation: () => ({ mutateAsync: (...args: unknown[]) => saveSkill(...args) }) }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: (m: BuildManifest | null) => new Map<string, number>(m?.components?.map(c => [c.filename, STAMPED_VERSION])) }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => false }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));
        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, () => STAMPED_VERSION);
        queryWrapper = newQueryWrapper();
    });

    type Breakage = { gatewayCreate?: boolean; agentRead?: boolean; workflowRead?: boolean; skillSave?: boolean };
    const READ_FAILURE = "Could not resolve config: artifact expired";
    const GATEWAY_FAILURE = "gateway create failed";
    const SKILL_FAILURE = "skill upload failed";

    const SHAPES: ReadonlyArray<{ name: string; files: string[]; passes: Breakage[]; rewrites: Array<{ url: string } | "skill"> }> = [
        {
            name: "a workflow saved earlier whose re-read then fails, beside a gateway that failed to create",
            files: ["wf.yaml", "gw.yaml"],
            passes: [{ gatewayCreate: true }, { workflowRead: true }, {}],
            rewrites: [{ url: "/api/v2/platform/entrypoints" }],
        },
        { name: "an agent whose config cannot be read, beside a gateway", files: ["agent.yaml", "gw.yaml"], passes: [{ agentRead: true }, {}], rewrites: [{ url: "/api/v2/platform/agents" }] },
        { name: "a skill whose upload fails, beside a gateway", files: ["s.yaml", "gw.yaml"], passes: [{ skillSave: true }, {}], rewrites: ["skill"] },
        {
            name: "a gateway that fails to create and an agent that cannot be read, together",
            files: ["agent.yaml", "gw.yaml"],
            passes: [{ gatewayCreate: true, agentRead: true }, {}],
            rewrites: [{ url: "/api/v2/platform/agents" }, { url: "/api/v2/platform/entrypoints" }],
        },
        {
            name: "a skill and a gateway that both fail, then the gateway alone",
            files: ["s.yaml", "gw.yaml"],
            passes: [{ skillSave: true, gatewayCreate: true }, { gatewayCreate: true }, {}],
            rewrites: ["skill", { url: "/api/v2/platform/entrypoints" }],
        },
    ];

    test.each(SHAPES)("Retry saves again after $name, and the failure it kept clears", async ({ files, passes, rewrites }) => {
        let breakage: Breakage = passes[0];
        const landed: string[] = [];
        platformPost.mockImplementation((url: string) => {
            if (url === "/api/v2/platform/entrypoints" && breakage.gatewayCreate) return Promise.reject(Object.assign(new Error(GATEWAY_FAILURE), { status: 500 }));
            landed.push(url);
            return Promise.resolve({ id: url.includes("workflows") ? "wf-1" : url.includes("agents") ? "agent-1" : "gw-1" });
        });
        saveSkill.mockImplementation(() => {
            if (breakage.skillSave) return Promise.reject(Object.assign(new Error(SKILL_FAILURE), { status: 500 }));
            landed.push("skill");
            return Promise.resolve({ id: "skill-id-1", name: "WeatherLookup", action: "created" });
        });
        const fetchConfigByFilename = vi.fn(async (filename: string) => {
            if (filename === "agent.yaml" && breakage.agentRead) throw new Error(READ_FAILURE);
            if (filename === "wf.yaml" && breakage.workflowRead) throw new Error(READ_FAILURE);
            return YAML_BY_FILENAME[filename];
        });
        let latest: import("./BuilderActions").BuildActivateApi | null = null;
        const manifest: BuildManifest = { name: "my-build", full_validation_passed: true, components: files.map(file => COMPONENT_BY_FILENAME[file]) };
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest}
                fetchConfigByFilename={fetchConfigByFilename}
                onBuildActivateApiChange={next => {
                    latest = next;
                }}
            />,
            { wrapper: queryWrapper }
        );
        await waitFor(() => expect(latest).not.toBeNull());

        await latest!.run();
        await waitFor(() => expect(latest!.failure.kind).not.toBe("none"));

        for (const pass of passes.slice(1)) {
            breakage = pass;
            const readsBefore = fetchConfigByFilename.mock.calls.length;
            latest!.retry();
            await waitFor(() => expect(fetchConfigByFilename.mock.calls.length).toBeGreaterThan(readsBefore));
            await waitFor(() => expect(latest!.isBusy).toBe(false));
        }

        for (const rewrite of rewrites) {
            if (rewrite === "skill") expect(landed).toContain("skill");
            else expect(landed).toContain(rewrite.url);
        }
        const failureNow = latest!.failure.kind === "none" ? "" : latest!.failure.message;
        for (const kept of [READ_FAILURE, GATEWAY_FAILURE, SKILL_FAILURE]) expect(failureNow).not.toContain(kept);
    });
});
