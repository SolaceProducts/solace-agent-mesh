// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const platformCheckState = vi.hoisted(() => ({ check: { kind: "passed", warnings: [] } as import("./platformCheck").PlatformCheck, checkAgain: () => {} }));
vi.mock("./hooks/usePlatformCheck", () => ({ usePlatformCheck: () => ({ check: platformCheckState.check, key: null, checkAgain: platformCheckState.checkAgain }) }));

import { respondingAsV2, v2DeploymentOf, v2Error, v2Page, v2PageOf } from "@/testing/helpers/platformResponses";
import { STAMPED_VERSION, withCurrentStamp } from "@/testing/helpers/validationStamp";

import { rememberWrittenResources, writtenRecords } from "./builderOwnership";
import type { BuildManifest } from "./hooks/useBuilderManifest";

function newQueryWrapper() {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const BUILDS_PATH = "/api/internal/platform/builder/builds";

type PlatformRecord = { id: string; name: string; deploymentStatus: string };

function listing(records: PlatformRecord[], family = "agents") {
    return (path: string) => {
        if (path.startsWith(`/api/v2/platform/${family}?`)) return Promise.resolve(v2PageOf(path, records));
        if (path.includes("?")) return Promise.resolve(v2Page([]));
        return Promise.resolve(v2Error(404, "not found"));
    };
}

const deploymentOf = (body: unknown) => Promise.resolve(v2DeploymentOf(body));

describe("BuilderActions with server-side apply", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;

    const addNotification = vi.fn();
    const displayError = vi.fn();
    const handleSubmit = vi.fn();
    const platformGet = vi.fn();
    const platformPost = vi.fn();
    const saveSkill = vi.fn();
    const getSkill = vi.fn();
    const buildAnswers = (response: unknown) => platformPost.mockImplementation((url: string, body?: unknown) => (url === BUILDS_PATH ? Promise.resolve(response) : deploymentOf(body)));
    let serverApplyEnabled = true;
    const track = vi.fn();
    let chatResponding = false;
    let chatCancelling = false;

    beforeEach(async () => {
        platformCheckState.check = { kind: "passed", warnings: [] };
        platformCheckState.checkAgain = () => {};
        chatResponding = false;
        chatCancelling = false;
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        platformGet.mockImplementation(listing([]));
        serverApplyEnabled = true;
        saveSkill.mockReset();
        saveSkill.mockResolvedValue({ id: "skill-id-1", name: "TestSkill", action: "created" });
        getSkill.mockReset();
        getSkill.mockResolvedValue({ contentHash: "hash-1" });

        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) => (
                <button onClick={onClick} disabled={disabled}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({
            api: {
                platform: {
                    get: respondingAsV2(platformGet),
                    post: respondingAsV2(platformPost),
                    delete: vi.fn(),
                },
            },
        }));
        vi.doMock("@/utils/api", () => ({
            getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback),
        }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => {
                const busyAtThisRender = chatResponding || chatCancelling;
                return {
                    addNotification,
                    displayError,
                    uploadArtifactFile: vi.fn(),
                    sessionId: "test-session",
                    allArtifacts: [],
                    handleSubmit: (...args: unknown[]) => (busyAtThisRender ? undefined : handleSubmit(...args)),
                    isResponding: chatResponding,
                    isCancelling: chatCancelling,
                };
            },
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({
            useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }),
        }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({
            useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }),
        }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/skills", () => ({
            useSaveSkillFromConfigMutation: () => ({ mutateAsync: (...args: unknown[]) => saveSkill(...args) }),
            skillsApi: { get: (...args: unknown[]) => getSkill(...args) },
        }));

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: [], isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("./hooks/useArtifactVersions", () => ({
            useArtifactVersions: (m: BuildManifest | null) => new Map<string, number>(m?.components?.map(c => [c.filename, STAMPED_VERSION])),
        }));

        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => serverApplyEnabled }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: track, reportingSessionId: () => "session-one" }));

        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            listManifestVersions: (_sessionId: string, filename: string) => Promise.resolve(filename === "agent.yaml" ? [3] : []),
        }));

        const mod = await import("./BuilderActions");
        BuilderActions = withCurrentStamp(mod.BuilderActions, () => STAMPED_VERSION);
        queryWrapper = newQueryWrapper();
    });

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

    const SKILL_YAML = `
name: TestSkill
description: A test skill with a sufficiently long description.
instruction_content: |
  Do the thing the skill is for.
`;

    function manifest(): BuildManifest {
        return {
            name: "my-build",
            full_validation_passed: true,
            components: [{ filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" }],
        };
    }

    function renderActions() {
        return renderActionsWith(manifest());
    }

    function renderActionsWith(m: BuildManifest, onAutoSaveComplete?: () => void) {
        const fetchConfigByFilename = vi.fn().mockImplementation((filename: string) => Promise.resolve(filename === "skill.yaml" ? SKILL_YAML : AGENT_YAML));
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={m}
                fetchConfigByFilename={fetchConfigByFilename}
                onAutoSaveComplete={onAutoSaveComplete}
                userAgentsRefetch={async () => {
                    await new Promise(resolve => setTimeout(resolve, 0));
                }}
                autoSave
            />,
            { wrapper: queryWrapper }
        );
        return fetchConfigByFilename;
    }

    test("one call to the build endpoint replaces the per-kind create loop", async () => {
        buildAnswers({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
        });

        renderActions();

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith(BUILDS_PATH, expect.objectContaining({ buildName: "my-build" })));

        const perKind = platformPost.mock.calls.filter(([url]) => url === "/api/v2/platform/agents");
        expect(perKind).toHaveLength(0);

        const [, body] = platformPost.mock.calls.find(([url]) => url === BUILDS_PATH)!;
        expect((body as { components: unknown[] }).components).toHaveLength(1);
    });

    test("every request to the build endpoint names the Builder session, so the server can record what the session wrote", async () => {
        buildAnswers({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
            dropped: [],
        });

        renderActions();

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith(BUILDS_PATH, expect.objectContaining({ builderSessionId: "test-session" })));
        for (const [url, body] of platformPost.mock.calls.filter(([u]) => u === BUILDS_PATH)) {
            expect(body, String(url)).toMatchObject({ builderSessionId: "test-session" });
        }
    });

    test("a completed build is reported once the server apply lands", async () => {
        buildAnswers({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
        });

        renderActions();

        await waitFor(() => expect(track).toHaveBeenCalledWith("builder:completed", {}));
        expect(track.mock.calls.filter(call => call[0] === "builder:completed")).toHaveLength(1);
    });

    test("the success notification counts what was written, not what was returned", async () => {
        buildAnswers({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 0, updated: 0, unchanged: 1, failed: 0, skipped: 0 },
        });

        renderActions();

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("0 component(s) saved"), "success"));
    });

    describe("replacing a component the Builder did not write", () => {
        const buildCalls = () => platformPost.mock.calls.filter(([url]) => url === BUILDS_PATH).map(([, body]) => Boolean((body as { dryRun?: boolean }).dryRun));
        const deployPosts = () => platformPost.mock.calls.filter(([url]) => String(url).includes("Deployments"));
        const tick = (ms = 100) => new Promise(resolve => setTimeout(resolve, ms));
        const DIALOG = "Replace existing component?";

        const planThenApply = (plannedAction: "update" | "create" | "unchanged", resourceId = "agent-1", kind = "agent", name = "TestAgent") =>
            platformPost.mockImplementation((url: string, body?: { dryRun?: boolean }) => {
                if (url !== BUILDS_PATH) return deploymentOf(body);
                const dryRun = Boolean(body?.dryRun);
                return Promise.resolve({
                    dryRun,
                    operations: [
                        { kind, name, action: plannedAction, status: dryRun ? "planned" : "succeeded", resourceId: dryRun && plannedAction === "create" ? undefined : resourceId, changedFields: plannedAction === "update" ? ["systemPrompt"] : [] },
                    ],
                    summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
                });
            });

        function AutoSaveHost({
            manifest: current,
            onAutoSaveComplete,
            configType = "agent",
            yaml = AGENT_YAML,
            bridge,
        }: Readonly<{ manifest: BuildManifest; onAutoSaveComplete?: () => void; configType?: "agent" | "connector"; yaml?: string; bridge?: (api: import("./BuilderActions").BuildActivateApi | null) => void }>) {
            const [autoSave, setAutoSave] = React.useState(true);
            return (
                <BuilderActions
                    configYaml=""
                    configType={configType}
                    manifest={current}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(yaml)}
                    userAgentsRefetch={async () => {
                        await new Promise(resolve => setTimeout(resolve, 0));
                    }}
                    autoSave={autoSave}
                    onAutoSaveComplete={() => {
                        setAutoSave(false);
                        onAutoSaveComplete?.();
                    }}
                    onBuildActivateApiChange={bridge}
                />
            );
        }

        const CONNECTOR_YAML = '{"name":"mydb","type":"sql","subtype":"postgres","values":{}}';
        const connectorManifest = (): BuildManifest => ({ name: "my-build", full_validation_passed: true, components: [{ filename: "db.yaml", type: "connector", name: "mydb", status: "validated" }] }) as BuildManifest;

        function renderAutoSaveHost(m: BuildManifest = manifest(), onAutoSaveComplete?: () => void) {
            const view = render(<AutoSaveHost manifest={m} onAutoSaveComplete={onAutoSaveComplete} />, { wrapper: queryWrapper });
            return { rerenderWith: (next: BuildManifest) => view.rerender(<AutoSaveHost manifest={next} onAutoSaveComplete={onAutoSaveComplete} />) };
        }

        async function renderBridgedAutoSave(onAutoSaveComplete?: () => void) {
            platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }]));
            let latest: import("./BuilderActions").BuildActivateApi | null = null;
            render(
                <AutoSaveHost
                    manifest={manifest()}
                    onAutoSaveComplete={onAutoSaveComplete}
                    bridge={next => {
                        latest = next;
                    }}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(latest).not.toBeNull());
            return { api: () => latest! };
        }

        async function startBuildActivate() {
            platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }]));
            let latest: import("./BuilderActions").BuildActivateApi | null = null;
            render(
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={manifest()}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                    onBuildActivateApiChange={next => {
                        latest = next;
                    }}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(latest).not.toBeNull());
            return { run: () => latest!.run(), retry: () => latest!.retry(), failure: () => latest!.failure };
        }

        beforeEach(() => localStorage.clear());

        test("waits for the user before writing, then saves and says what it replaced", async () => {
            planThenApply("update");

            renderAutoSaveHost();

            await screen.findByText(DIALOG);
            expect(screen.getByText("Changes: systemPrompt")).toBeTruthy();
            expect(buildCalls()).toEqual([true]);

            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(buildCalls()).toEqual([true, true, false]));
            await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestAgent."), "success"));
        });

        test("writes nothing when the user cancels, ends the auto-save attempt, and leaves a way back", async () => {
            planThenApply("update");
            const onAutoSaveComplete = vi.fn();
            renderAutoSaveHost(manifest(), onAutoSaveComplete);

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick();

            expect(buildCalls()).toEqual([true]);
            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
            expect(onAutoSaveComplete).toHaveBeenCalledTimes(1);
            expect(screen.getByRole("button", { name: "Build & Activate" })).toBeTruthy();

            screen.getByRole("button", { name: "Retry" }).click();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await waitFor(() => expect(buildCalls()).toEqual([true, true, true, false]));
        });

        test("the way back survives the banner being cleared by a later builder turn", async () => {
            planThenApply("update");
            const { rerenderWith } = renderAutoSaveHost();

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick();

            rerenderWith({ ...manifest(), components: [...manifest().components!, { filename: "other.yaml", type: "agent", name: "OtherAgent", status: "validated" }] });
            await waitFor(() => expect(screen.queryByRole("button", { name: "Retry" })).toBeNull());

            expect(screen.getByRole("button", { name: "Build & Activate" })).toBeTruthy();
            expect(buildCalls()).toEqual([true]);
        });

        test("Build & Activate does not deploy anything after the user cancels", async () => {
            planThenApply("update");
            const api = await startBuildActivate();

            const running = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await running;
            await tick();

            expect(deployPosts()).toHaveLength(0);
            expect(buildCalls()).toEqual([true]);
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("Deployed"), expect.anything());
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("Not found on platform"), expect.anything());
            expect(displayError).not.toHaveBeenCalled();
        });

        test("a second Build & Activate while a prompt is pending deploys nothing before the user answers", async () => {
            planThenApply("update");
            const api = await startBuildActivate();

            const first = api.run();
            await screen.findByText(DIALOG);
            await api.run();
            await tick();

            expect(deployPosts()).toHaveLength(0);

            screen.getByRole("button", { name: "Cancel" }).click();
            await first;
            expect(deployPosts()).toHaveLength(0);
            expect(buildCalls()).toEqual([true]);
        });

        test("Try again after a decline deploys nothing when the plan then fails, and keeps the banner", async () => {
            planThenApply("update");
            const api = await startBuildActivate();
            const first = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await first;

            platformPost.mockImplementation((url: string, body?: unknown) => (url === BUILDS_PATH ? Promise.reject(Object.assign(new Error("bad gateway"), { status: 502 })) : deploymentOf(body)));
            api.retry();
            await tick(300);

            expect(deployPosts()).toHaveLength(0);
            expect(api.failure()).toMatchObject({ kind: "retryable", message: expect.stringContaining("bad gateway") });
        });

        test("a first Build & Activate whose plan fails deploys nothing", async () => {
            platformPost.mockImplementation((url: string, body?: unknown) => (url === BUILDS_PATH ? Promise.reject(Object.assign(new Error("bad gateway"), { status: 502 })) : deploymentOf(body)));
            const api = await startBuildActivate();

            await api.run();
            await tick(200);

            expect(deployPosts()).toHaveLength(0);
            expect(api.failure()).toMatchObject({ kind: "retryable" });
        });

        type Row = Record<string, unknown>;
        const agentDeployments = () => deployPosts().filter(([url]) => String(url).includes("agentDeployments"));
        const deployedIds = () => agentDeployments().map(([, body]) => (body as { agentId: string }).agentId);

        type BuildControls = { rewrite?: () => void; rerender?: () => void };
        type BuildOptions = { controls?: BuildControls; agents: PlatformRecord[]; apply: (attempt: number, dryRun: boolean) => Row[]; components?: BuildManifest["components"]; deploysThatFail?: string[]; unreadableManifest?: boolean };

        function installPlatform(opts: BuildOptions) {
            const platform = opts.agents.map(a => ({ ...a }));
            let attempt = 0;
            const answerBuild = (dryRun: boolean) => {
                const rows = opts.apply(dryRun ? attempt : attempt++, dryRun);
                const operations = dryRun ? rows.map(r => ({ ...r, status: "planned", error: undefined })) : rows;
                return { dryRun, operations, summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 } };
            };
            const answerDeployment = (body: { agentId?: string } | undefined) => {
                const agentId = body?.agentId;
                if (agentId && opts.deploysThatFail?.includes(agentId)) return v2Error(500, "deployment refused");
                const deployed = platform.find(a => a.id === agentId);
                if (deployed) deployed.deploymentStatus = "deployed";
                return v2DeploymentOf(body);
            };
            platformGet.mockImplementation(listing(platform));
            platformPost.mockImplementation(async (url: string, body?: { dryRun?: boolean; agentId?: string }) => {
                if (url === BUILDS_PATH) return answerBuild(Boolean(body?.dryRun));
                return answerDeployment(body);
            });
        }

        function BuildHost({ initial, unreadableManifest, controls, onApi }: Readonly<{ initial: BuildManifest; unreadableManifest?: boolean; controls?: BuildControls; onApi: (api: import("./BuilderActions").BuildActivateApi | null) => void }>) {
            const [current, setCurrent] = React.useState(initial);
            const [, setRenders] = React.useState(0);
            if (controls) {
                controls.rewrite = () => setCurrent(prev => ({ ...prev, components: prev.components?.map(c => ({ ...c, status: "draft" })) }));
                controls.rerender = () => setRenders(n => n + 1);
            }
            const saveManifest = React.useCallback(
                async (updater: (prev: BuildManifest) => BuildManifest) => {
                    if (unreadableManifest) return { ok: false, reason: "unreadable" } as import("./hooks/useBuilderManifest").SaveManifestResult;
                    setCurrent(prev => updater(prev));
                    return { ok: true } as import("./hooks/useBuilderManifest").SaveManifestResult;
                },
                [unreadableManifest]
            );
            return (
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={current}
                    saveManifest={saveManifest}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                    userAgentsRefetch={async () => {
                        await new Promise(resolve => setTimeout(resolve, 0));
                    }}
                    onBuildActivateApiChange={onApi}
                />
            );
        }

        async function startBuild(opts: BuildOptions) {
            installPlatform(opts);
            let latest: import("./BuilderActions").BuildActivateApi | null = null;
            const components = opts.components ?? opts.agents.map(a => ({ filename: `${a.name}.yaml`, type: "agent", name: a.name, status: "validated" }));
            render(
                <BuildHost
                    initial={{ name: "my-build", full_validation_passed: true, components }}
                    unreadableManifest={opts.unreadableManifest}
                    controls={opts.controls}
                    onApi={next => {
                        latest = next;
                    }}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(latest).not.toBeNull());
            return { run: () => latest!.run(), retry: () => latest!.retry(), failure: () => latest!.failure };
        }

        const X = { id: "x-1", name: "AgentX", deploymentStatus: "not_deployed" };
        const Y = { id: "y-foreign", name: "AgentY", deploymentStatus: "not_deployed" };
        const Z = { id: "z-foreign", name: "AgentZ", deploymentStatus: "not_deployed" };
        const rejected = (name: string, resourceId: string, httpStatus = 500): Row => ({ kind: "agent", name, action: "update", status: "failed", resourceId, error: { message: `${name} was rejected`, errorId: "e-1", httpStatus } });
        const updated = (name: string, resourceId: string): Row => ({ kind: "agent", name, action: "update", status: "succeeded", resourceId });
        const skipped = (name: string, resourceId: string): Row => ({ kind: "agent", name, action: "update", status: "skipped", resourceId });
        const xUpdatedYRejectedZSkipped = () => [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign"), skipped("AgentZ", "z-foreign")];

        test("a failure after something was written deploys what landed, not what it did not write", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y, Z], apply: xUpdatedYRejectedZSkipped });

            await api.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(200);

            expect(deployedIds()).toEqual(["x-1"]);
        });

        test("a failure held while the Builder answers is sent when its turn ends, though the deploy that followed cleared the banner error", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            chatResponding = true;
            const controls: BuildControls = {};
            const api = await startBuild({ controls, agents: [X, Y], apply: () => [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign", 422)] });

            await api.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(300);
            expect(handleSubmit).not.toHaveBeenCalled();
            expect(api.failure()).toMatchObject({ kind: "fixing" });

            chatResponding = false;
            controls.rerender!();

            await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
            expect(String(handleSubmit.mock.calls[0][2])).toContain("AgentY was rejected");
        });

        test("the failure stays on screen once the deploy of what landed has settled", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y, Z], apply: xUpdatedYRejectedZSkipped });

            await api.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(600);

            expect(api.failure()).toMatchObject({ kind: "retryable", message: expect.stringContaining("AgentY was rejected") });
        });

        test("a failure that wrote nothing deploys nothing and leaves the failure on screen", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: () => [rejected("AgentX", "x-1"), skipped("AgentY", "y-foreign")] });

            await api.run();
            await tick(300);

            expect(deployedIds()).toEqual([]);
            expect(api.failure()).toMatchObject({ kind: "retryable", message: expect.stringContaining("AgentX was rejected") });
        });

        test("a failure the builder is fixing stays in the fixing state once the deploy of what landed has settled", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: () => [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign", 422)] });

            await api.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(600);

            expect(api.failure()).toMatchObject({ kind: "fixing", message: expect.stringContaining("AgentY was rejected") });
        });

        test("a failure after the builder has rewritten the manifest is offered for retry, not left as fixing", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const controls: { rewrite?: () => void } = {};
            const api = await startBuild({ controls, agents: [X, Y], apply: attempt => [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign", attempt === 0 ? 422 : 500)] });
            await api.run();
            await waitFor(() => expect(api.failure()).toMatchObject({ kind: "fixing" }));
            await tick(300);

            controls.rewrite!();
            await tick(100);
            expect(api.failure()).toEqual({ kind: "none" });
            await api.run();

            await waitFor(() => expect(api.failure()).toMatchObject({ kind: "retryable", message: expect.stringContaining("AgentY was rejected") }));
            await tick(300);
            expect(api.failure()).toMatchObject({ kind: "retryable" });
        });

        test("a deploy that fails after a failed save is shown with the save failure", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: () => [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign")], deploysThatFail: ["x-1"] });

            await api.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(300);

            const message = (api.failure() as { message: string }).message;
            expect(message).toContain("AgentY was rejected");
            expect(message).toContain("deployment refused");
        });

        test("a plan that cannot be read after a failed save is shown as blocked, with the save failure", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: () => [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign")], unreadableManifest: true });

            await api.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(300);

            expect(api.failure()).toMatchObject({ kind: "blocked", message: expect.stringContaining("AgentY was rejected") });
        });

        test("the failure of one save is gone once the next Build & Activate lands everything", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: attempt => (attempt === 0 ? [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign")] : [updated("AgentX", "x-1"), updated("AgentY", "y-foreign")]) });
            await api.run();
            await tick(300);
            expect(api.failure()).toMatchObject({ kind: "retryable" });

            await api.run();

            await waitFor(() => expect(deployedIds()).toContain("y-foreign"));
            await waitFor(() => expect(api.failure()).toEqual({ kind: "none" }));
        });

        test("the failure of one save is gone once Try again lands everything", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: attempt => (attempt === 0 ? [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign")] : [updated("AgentX", "x-1"), updated("AgentY", "y-foreign")]) });
            await api.run();
            await tick(300);

            api.retry();

            await waitFor(() => expect(deployedIds()).toContain("y-foreign"));
            await waitFor(() => expect(api.failure()).toEqual({ kind: "none" }));
        });

        test("Try again after a failure at a connector deploys only the consumers that were written", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const bound = (name: string): NonNullable<BuildManifest["components"]>[number] => ({ filename: `${name}.yaml`, type: "agent", name, status: "validated", connectors: ["mydb"] });
            const Q = { id: "q-1", name: "AgentQ", deploymentStatus: "not_deployed" };
            const R = { id: "r-foreign", name: "AgentR", deploymentStatus: "deployed" };
            const connector = (status: "succeeded" | "failed"): Row => ({
                kind: "connector",
                name: "mydb",
                action: "update",
                status,
                resourceId: "conn-1",
                ...(status === "failed" ? { error: { message: "mydb was rejected", errorId: "e-1", httpStatus: 500 } } : {}),
            });
            const api = await startBuild({
                agents: [Q, Z, R],
                components: [{ filename: "db.yaml", type: "connector", name: "mydb", status: "validated" }, bound("AgentQ"), bound("AgentZ"), bound("AgentR")],
                apply: attempt =>
                    attempt === 0
                        ? [connector("failed"), skipped("AgentQ", "q-1"), skipped("AgentZ", "z-foreign"), skipped("AgentR", "r-foreign")]
                        : [connector("succeeded"), updated("AgentQ", "q-1"), rejected("AgentZ", "z-foreign"), skipped("AgentR", "r-foreign")],
            });
            await api.run();
            await tick(200);
            expect(deployedIds()).toEqual([]);

            api.retry();

            await waitFor(() => expect(deployedIds()).toEqual(["q-1"]));
            await tick(300);
            expect(deployedIds()).toEqual(["q-1"]);
        });

        test("a failure after the user confirmed a replacement deploys only what landed", async () => {
            const api = await startBuild({ agents: [X, Y, Z], apply: xUpdatedYRejectedZSkipped });

            const running = api.run();
            await screen.findByText("Replace existing components?");
            screen.getByRole("button", { name: "Replace and save" }).click();
            await running;
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(300);

            expect(deployedIds()).toEqual(["x-1"]);
            expect(buildCalls()).toEqual([true, true, false]);
        });

        test("Try again whose own save fails after writing deploys only what landed", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({ agents: [X, Y], apply: attempt => (attempt === 0 ? [rejected("AgentX", "x-1"), skipped("AgentY", "y-foreign")] : [updated("AgentX", "x-1"), rejected("AgentY", "y-foreign")]) });
            await api.run();
            await tick(200);
            expect(deployedIds()).toEqual([]);

            api.retry();

            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(300);
            expect(deployedIds()).toEqual(["x-1"]);
        });

        test("the agents bound to a changed connector are deployed after a failure, unless their own write failed or was skipped", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const platform = [
                { id: "x-1", name: "AgentX", deploymentStatus: "not_deployed" },
                { id: "y-foreign", name: "AgentY", deploymentStatus: "not_deployed" },
                { id: "w-foreign", name: "AgentW", deploymentStatus: "not_deployed" },
            ];
            platformGet.mockImplementation(listing(platform));
            platformPost.mockImplementation(async (url: string, body?: unknown) =>
                url === BUILDS_PATH
                    ? {
                          dryRun: false,
                          operations: [{ kind: "connector", name: "mydb", action: "update", status: "succeeded", resourceId: "conn-1" }, updated("AgentX", "x-1"), rejected("AgentY", "y-foreign"), skipped("AgentW", "w-foreign")],
                          summary: {},
                      }
                    : v2DeploymentOf(body)
            );
            let latest: import("./BuilderActions").BuildActivateApi | null = null;
            render(
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={{
                        name: "my-build",
                        full_validation_passed: true,
                        components: [
                            { filename: "db.yaml", type: "connector", name: "mydb", status: "validated" },
                            { filename: "x.yaml", type: "agent", name: "AgentX", status: "validated", connectors: ["mydb"] },
                            { filename: "y.yaml", type: "agent", name: "AgentY", status: "validated", connectors: ["mydb"] },
                            { filename: "w.yaml", type: "agent", name: "AgentW", status: "validated", connectors: ["mydb"] },
                        ],
                    }}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                    onBuildActivateApiChange={next => {
                        latest = next;
                    }}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(latest).not.toBeNull());

            await latest!.run();
            await waitFor(() => expect(deployedIds()).toEqual(["x-1"]));
            await tick(200);

            expect(deployedIds()).toEqual(["x-1"]);
        });

        test("Try again re-saves a component whose edit failed after it had been saved earlier", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            const api = await startBuild({
                agents: [X, Y],
                apply: attempt =>
                    attempt === 0
                        ? [
                              { kind: "agent", name: "AgentX", action: "create", status: "succeeded", resourceId: "x-1" },
                              { kind: "agent", name: "AgentY", action: "create", status: "succeeded", resourceId: "y-foreign" },
                          ]
                        : [{ kind: "agent", name: "AgentX", action: "unchanged", status: "succeeded", resourceId: "x-1" }, rejected("AgentY", "y-foreign")],
            });
            await api.run();
            await tick(100);
            await api.run();
            await waitFor(() => expect(api.failure()).toMatchObject({ kind: "retryable", message: expect.stringContaining("AgentY was rejected") }));
            expect(buildCalls()).toEqual([false, false]);

            api.retry();

            await waitFor(() => expect(buildCalls()).toEqual([false, false, false]));
        });

        test("a refetch of the agent list that rejects does not turn a landed save into a failure", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            planThenApply("update");
            platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }]));
            let latest: import("./BuilderActions").BuildActivateApi | null = null;
            let refetches = 0;
            render(
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={manifest()}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                    userAgentsRefetch={() => (refetches++ === 0 ? Promise.reject(new Error("refetch failed")) : Promise.resolve())}
                    onBuildActivateApiChange={next => {
                        latest = next;
                    }}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(latest).not.toBeNull());

            await latest!.run();

            await waitFor(() => expect(deployPosts().length).toBeGreaterThan(0));
            expect(latest!.failure).toEqual({ kind: "none" });
            expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("saved"), "success");
        });

        test("a retry while the retry's own prompt is open deploys nothing and leaves the prompt up", async () => {
            planThenApply("update");
            const api = await startBuildActivate();
            const first = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await first;

            api.retry();
            await screen.findByText(DIALOG);
            api.retry();
            await tick(300);

            expect(deployPosts()).toHaveLength(0);
            expect(screen.queryByText(DIALOG)).not.toBeNull();
            expect(buildCalls()).toEqual([true, true]);
        });

        test("a Build & Activate that lands on a running auto-save does not cancel the deploy that auto-save makes", async () => {
            planThenApply("update");
            const { api } = await renderBridgedAutoSave();

            await screen.findByText(DIALOG);
            await api().run();
            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(deployPosts().length).toBeGreaterThan(0));
        });

        test("a Try again that lands on a running auto-save does not cancel the deploy that auto-save makes", async () => {
            planThenApply("update");
            const { api } = await renderBridgedAutoSave();

            await screen.findByText(DIALOG);
            api().retry();
            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(deployPosts().length).toBeGreaterThan(0));
            expect(buildCalls()).toEqual([true, true, false]);
        });

        test("an auto-save that fails ends the attempt, leaves Build & Activate, and Try again saves once", async () => {
            platformPost.mockImplementation((url: string, body?: unknown) => (url === BUILDS_PATH ? Promise.reject(Object.assign(new Error("bad gateway"), { status: 502 })) : deploymentOf(body)));
            const onAutoSaveComplete = vi.fn();
            const { api } = await renderBridgedAutoSave(onAutoSaveComplete);

            await waitFor(() => expect(onAutoSaveComplete).toHaveBeenCalledTimes(1));
            await waitFor(() => expect(api().canBuildActivate).toBe(true));
            expect(api().failure).toMatchObject({ kind: "retryable", message: expect.stringContaining("bad gateway") });
            expect(buildCalls()).toEqual([true]);

            api().retry();
            await tick(300);

            expect(buildCalls()).toEqual([true, true]);
            expect(deployPosts()).toHaveLength(0);
        });

        test("a build that only saves still completes when its save fails, as it always has", async () => {
            chatResponding = true;
            platformPost.mockImplementation((url: string, body?: unknown) => (url === BUILDS_PATH ? Promise.reject(Object.assign(new Error("bad gateway"), { status: 502 })) : deploymentOf(body)));
            const onAutoSaveComplete = vi.fn();
            render(<BuilderActions configYaml="" configType="connector" manifest={connectorManifest()} fetchConfigByFilename={vi.fn().mockResolvedValue(CONNECTOR_YAML)} onAutoSaveComplete={onAutoSaveComplete} />, { wrapper: queryWrapper });

            await waitFor(() => expect(onAutoSaveComplete).toHaveBeenCalledTimes(1));
        });

        test("an auto-save declined through the in-chat card leaves Build & Activate on offer, with the cancel reported as a decline", async () => {
            planThenApply("update");
            const { api } = await renderBridgedAutoSave();

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick();

            expect(api().canBuildActivate).toBe(true);
            expect(api().failure).toMatchObject({ kind: "declined", message: expect.stringContaining("Save cancelled") });
            expect(buildCalls()).toEqual([true]);
        });

        test("only the first Try again after a decline saves again; the next one only deploys", async () => {
            planThenApply("update");
            const { api } = await renderBridgedAutoSave();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick();

            api().retry();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await waitFor(() => expect(buildCalls()).toEqual([true, true, true, false]));
            await tick(300);

            api().retry();
            await tick(300);

            expect(buildCalls()).toEqual([true, true, true, false]);
        });

        test("a cancel reaches the in-chat plan card as a decline, not a failure", async () => {
            planThenApply("update");
            const api = await startBuildActivate();

            const running = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await running;

            await waitFor(() => expect(api.failure()).toMatchObject({ kind: "declined", message: expect.stringContaining("Save cancelled") }));
        });

        test("cancelling again on Retry asks nothing more and deploys nothing", async () => {
            planThenApply("update");
            renderAutoSaveHost();

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick();

            screen.getByRole("button", { name: "Retry" }).click();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await tick(300);

            expect(screen.queryByText(DIALOG)).toBeNull();
            expect(buildCalls()).toEqual([true, true]);
            expect(deployPosts()).toHaveLength(0);
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("Deployed"), expect.anything());
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("Not found on platform"), expect.anything());
        });

        test("Try again saves again even when every component already reads as saved", async () => {
            planThenApply("unchanged", "agent-1");
            const api = await startBuildActivate();
            await api.run();

            planThenApply("update", "someone-elses-agent");
            const second = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await second;

            api.retry();

            await screen.findByText(DIALOG);
            expect(buildCalls().slice(-1)).toEqual([true]);
        });

        test("Build & Activate works again after a cancel", async () => {
            planThenApply("update");
            const api = await startBuildActivate();

            const first = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await first;

            const second = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await second;

            expect(buildCalls()).toEqual([true, true, true, false]);
            await waitFor(() => expect(deployPosts().length).toBeGreaterThan(0));
        });

        test("ends the auto-save attempt when the user confirms too", async () => {
            planThenApply("update");
            const onAutoSaveComplete = vi.fn();
            renderAutoSaveHost(manifest(), onAutoSaveComplete);

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();

            await waitFor(() => expect(onAutoSaveComplete).toHaveBeenCalledTimes(1));
        });

        test("a build that only saves is not reported complete when the user cancels, and can be retried", async () => {
            chatResponding = true;
            planThenApply("update", "c-1", "connector", "mydb");
            const onAutoSaveComplete = vi.fn();
            render(
                <BuilderActions
                    configYaml=""
                    configType="connector"
                    manifest={{ name: "my-build", full_validation_passed: true, components: [{ filename: "db.yaml", type: "connector", name: "mydb", status: "validated" }] } as BuildManifest}
                    fetchConfigByFilename={vi.fn().mockResolvedValue('{"name":"mydb","type":"sql","subtype":"postgres","values":{}}')}
                    onAutoSaveComplete={onAutoSaveComplete}
                />,
                { wrapper: queryWrapper }
            );

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick(300);

            expect(onAutoSaveComplete).not.toHaveBeenCalled();
            expect(buildCalls()).toEqual([true]);
            screen.getByRole("button", { name: "Retry" }).click();
            await screen.findByText(DIALOG);
        });

        test("declining the approved auto-save of a build that only saves asks once", async () => {
            chatResponding = true;
            planThenApply("update", "c-1", "connector", "mydb");
            render(<AutoSaveHost manifest={connectorManifest()} configType="connector" yaml={CONNECTOR_YAML} />, { wrapper: queryWrapper });

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick(400);

            expect(buildCalls()).toEqual([true]);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("a decline holds against later turns, and a builder turn that writes new content asks again", async () => {
            chatResponding = true;
            planThenApply("update", "c-1", "connector", "mydb");
            const view = render(<AutoSaveHost manifest={connectorManifest()} configType="connector" yaml={CONNECTOR_YAML} />, { wrapper: queryWrapper });
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
            await tick(300);
            expect(buildCalls()).toEqual([true]);

            view.rerender(<AutoSaveHost manifest={connectorManifest()} configType="connector" yaml={CONNECTOR_YAML} />);
            await tick(300);
            expect(buildCalls()).toEqual([true]);

            const next = connectorManifest();
            next.components = [...next.components!, { filename: "db2.yaml", type: "connector", name: "otherdb", status: "validated" }];
            view.rerender(<AutoSaveHost manifest={next} configType="connector" yaml={CONNECTOR_YAML} />);

            await screen.findByText(DIALOG);
            expect(buildCalls()).toEqual([true, true]);
        });

        test("does not ask about a component this session already wrote", async () => {
            planThenApply("update");
            const api = await startBuildActivate();

            const first = api.run();
            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Replace and save" }).click();
            await first;

            await api.run();

            expect(buildCalls()).toEqual([true, true, false, true, false]);
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("asks again when the update targets a different resource than the one it wrote", async () => {
            planThenApply("create", "agent-1");
            const api = await startBuildActivate();
            await api.run();

            planThenApply("update", "someone-elses-agent");
            const next = api.run();

            await screen.findByText(DIALOG);
            screen.getByRole("button", { name: "Cancel" }).click();
            await next;
        });

        test("does not ask for a new component", async () => {
            planThenApply("create");

            renderActions();

            await waitFor(() => expect(buildCalls()).toEqual([true, false]));
            expect(screen.queryByText(DIALOG)).toBeNull();
        });

        test("skips the plan altogether once the user has opted out", async () => {
            localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
            planThenApply("update");

            renderActions();

            await waitFor(() => expect(buildCalls()).toEqual([false]));
            expect(screen.queryByText(DIALOG)).toBeNull();
            await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestAgent."), "success"));
        });
    });

    test("a per-operation failure is surfaced and attributed to its component", async () => {
        buildAnswers({
            dryRun: false,
            operations: [
                {
                    kind: "agent",
                    name: "TestAgent",
                    action: "create",
                    status: "failed",
                    error: {
                        message: "instruction is required",
                        errorId: "e-1",
                        httpStatus: 422,
                        validationDetails: { systemPrompt: ["instruction is required"] },
                    },
                },
            ],
            summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
        });

        renderActions();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        expect(JSON.stringify(handleSubmit.mock.calls[0])).toContain("TestAgent");
    });

    test("the count includes components saved outside the build request", async () => {
        buildAnswers({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
        });

        renderActionsWith({
            name: "my-build",
            full_validation_passed: true,
            components: [
                { filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" },
                { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" },
            ],
        });

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("2 component(s) saved"), "success"));
    });

    test("a build that deploys nothing does not claim it deployed something", async () => {
        buildAnswers({
            dryRun: false,
            operations: [],
            summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
        });

        renderActionsWith({
            name: "my-build",
            full_validation_passed: true,
            components: [{ filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" }],
        });

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("component(s) saved"), "success"));
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("Deployed"), expect.anything());
    });

    test("a warning from the server reaches the author", async () => {
        buildAnswers({
            dryRun: false,
            operations: [
                {
                    kind: "agent",
                    name: "TestAgent",
                    action: "update",
                    status: "succeeded",
                    resourceId: "agent-1",
                    warnings: ["agent config key supports_streaming is not saved by the Builder; the agent keeps the value already stored on the platform, or the default if none"],
                },
            ],
            summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
        });

        renderActions();

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("supports_streaming"), "warning"));
    });

    test("a 422 carrying no field map still routes back to the builder", async () => {
        buildAnswers({
            dryRun: false,
            operations: [
                {
                    kind: "agent",
                    name: "TestAgent",
                    action: "create",
                    status: "failed",
                    error: { message: 'unknown toolset "foo"', errorId: "e-2", httpStatus: 422 },
                },
            ],
            summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
        });

        renderActions();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        expect(JSON.stringify(handleSubmit.mock.calls[0])).toContain("unknown toolset");
    });

    test("a whole-build 422 is sent to the builder about the component it names", async () => {
        platformPost.mockRejectedValue(Object.assign(new Error('agent "TestAgent": unknown toolset "foo"'), { status: 422 }));

        renderActions();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        expect(JSON.stringify(handleSubmit.mock.calls[0])).toContain("Save agent for **TestAgent**");
    });

    test("a whole-build 422 that lands while the builder is still answering is held, then sent when the turn ends", async () => {
        platformPost.mockRejectedValue(Object.assign(new Error('agent "TestAgent": unknown toolset "foo"'), { status: 422 }));
        chatResponding = true;
        let kind = "";
        const tree = () => (
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest()}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                autoSave
                onBuildActivateApiChange={api => {
                    if (api) kind = (api.failure as { kind: string }).kind;
                }}
            />
        );

        const view = render(tree(), { wrapper: queryWrapper });
        await waitFor(() => expect(kind).toBe("fixing"));
        expect(handleSubmit).not.toHaveBeenCalled();

        chatResponding = false;
        act(() => view.rerender(tree()));

        await waitFor(() => expect(handleSubmit).toHaveBeenCalledTimes(1));
        expect(JSON.stringify(handleSubmit.mock.calls[0])).toContain("Save agent for **TestAgent**");
    });

    test("the auto-fix chip names an entrypoint 'entrypoint', never 'gateway'", async () => {
        platformPost.mockRejectedValue(Object.assign(new Error('entrypoint "order_events_entrypoint": unknown agent "Nope"'), { status: 422 }));

        renderActionsWith({
            name: "my-build",
            full_validation_passed: true,
            components: [{ filename: "agent.yaml", type: "gateway", name: "order_events_entrypoint", status: "validated" }],
        });

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const submitted = JSON.stringify(handleSubmit.mock.calls[0]);
        expect(submitted).toContain("Save entrypoint for **order_events_entrypoint**");
        expect(submitted).not.toContain("Save gateway");
    });

    async function runBuildActivate(applyResponse: Record<string, unknown>, buildManifest: BuildManifest = manifest()) {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        buildAnswers(applyResponse);

        let api: import("./BuilderActions").BuildActivateApi | null = null;
        const fetchConfigByFilename = vi.fn().mockResolvedValue(AGENT_YAML);
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={buildManifest}
                fetchConfigByFilename={fetchConfigByFilename}
                onBuildActivateApiChange={next => {
                    api = next;
                }}
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(api).not.toBeNull());
        await api!.run();
        await waitFor(() => expect(platformPost).toHaveBeenCalledWith(BUILDS_PATH, expect.anything()));
    }

    const deployCalls = () => platformPost.mock.calls.filter(([url]) => String(url).includes("agentDeployments"));

    test("a deployed component the save rewrote is deployed again", async () => {
        await runBuildActivate({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
        });

        await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
    });

    test("a component the save CREATED is deployed", async () => {
        await runBuildActivate({
            dryRun: false,
            operations: [{ kind: "agent", name: "TestAgent", action: "create", status: "succeeded", resourceId: "agent-1" }],
            summary: { created: 1, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
        });

        await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
    });

    const unchangedAgent = {
        dryRun: false,
        operations: [{ kind: "agent", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1" }],
        summary: { created: 0, updated: 0, unchanged: 1, failed: 0, skipped: 0 },
    };

    test("a deployed component the save left alone is not deployed again", async () => {
        const stamped = manifest();
        stamped.components = stamped.components?.map(c => ({ ...c, deployed_version: 3 }));

        await runBuildActivate(unchangedAgent, stamped);

        expect(deployCalls()).toHaveLength(0);
    });

    const notApplied = "not applied without entrypoint:*:update, and left unchanged: password";
    const entrypointManifest = (): BuildManifest => ({
        name: "my-build",
        full_validation_passed: true,
        components: [{ filename: "agent.yaml", type: "gateway", name: "TestAgent", status: "validated" }],
    });
    const entrypointDeployCalls = () => platformPost.mock.calls.filter(([url]) => String(url).includes("entrypointDeployments"));

    async function runBuildActivateRecording(applyResponse: Record<string, unknown>, buildManifest: BuildManifest) {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }], "entrypoints"));
        buildAnswers(applyResponse);
        const written: BuildManifest[] = [];
        const saveManifest = vi.fn(async (updater: (m: BuildManifest) => BuildManifest) => {
            written.push(updater(buildManifest));
            return { ok: true } as const;
        });

        let api: import("./BuilderActions").BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={buildManifest}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                saveManifest={saveManifest}
                onBuildActivateApiChange={next => {
                    api = next;
                }}
            />,
            { wrapper: queryWrapper }
        );
        await waitFor(() => expect(api).not.toBeNull());
        await api!.run();
        await waitFor(() => expect(platformPost).toHaveBeenCalledWith(BUILDS_PATH, expect.anything()));
        return written;
    }
    const stampOf = (written: BuildManifest[]) => written.flatMap(m => m.components ?? []).find(c => c.name === "TestAgent" && c.deployed_version !== undefined)?.deployed_version;

    async function runAutoSaveRecording(applyResponse: Record<string, unknown>, buildManifest: BuildManifest) {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }], "entrypoints"));
        buildAnswers(applyResponse);
        const written: BuildManifest[] = [];
        const saveManifest = vi.fn(async (updater: (m: BuildManifest) => BuildManifest) => {
            written.push(updater(buildManifest));
            return { ok: true } as const;
        });
        render(<BuilderActions configYaml="" configType="agent" manifest={buildManifest} fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)} saveManifest={saveManifest} autoSave />, {
            wrapper: queryWrapper,
        });
        await waitFor(() => expect(written.some(m => m.components?.some(c => c.name === "TestAgent" && c.id === "agent-1" && c.status === "deployed"))).toBe(true));
        return written;
    }

    const rewrittenEntrypoint = (changesDeclined: boolean) => ({
        dryRun: false,
        operations: [{ kind: "gateway", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1", ...(changesDeclined ? { changesDeclined, warnings: [notApplied] } : {}) }],
        summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
    });

    test("an auto-saved entrypoint the save rewrote is deployed and stamped", async () => {
        const written = await runAutoSaveRecording(rewrittenEntrypoint(false), entrypointManifest());

        expect(stampOf(written)).toBe(3);
    });

    test("an auto-saved entrypoint whose credential was declined is deployed but never stamped", async () => {
        const written = await runAutoSaveRecording(rewrittenEntrypoint(true), entrypointManifest());

        expect(entrypointDeployCalls().length).toBeGreaterThan(0);
        expect(stampOf(written)).toBeUndefined();
    });

    test("an unchanged component the server declined part of is not deployed to settle", async () => {
        await runBuildActivateRecording(
            {
                dryRun: false,
                operations: [{ kind: "gateway", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1", changesDeclined: true, warnings: [notApplied] }],
                summary: { created: 0, updated: 0, unchanged: 1, failed: 0, skipped: 0 },
            },
            entrypointManifest()
        );

        expect(entrypointDeployCalls()).toHaveLength(0);
    });

    test("a rewritten entrypoint is deployed and stamped", async () => {
        const written = await runBuildActivateRecording(
            {
                dryRun: false,
                operations: [{ kind: "gateway", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" }],
                summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
            },
            entrypointManifest()
        );

        await waitFor(() => expect(stampOf(written)).toBe(3));
    });

    test("a rewritten entrypoint whose credential was declined is deployed but never stamped", async () => {
        const written = await runBuildActivateRecording(
            {
                dryRun: false,
                operations: [{ kind: "gateway", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1", changesDeclined: true, warnings: [notApplied] }],
                summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
            },
            entrypointManifest()
        );

        await waitFor(() => expect(entrypointDeployCalls().length).toBeGreaterThan(0));
        await waitFor(() => expect(written.length).toBeGreaterThan(0));
        expect(stampOf(written)).toBeUndefined();
    });

    test("proof from an earlier save does not settle a later one whose save failed", async () => {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        let builds = 0;
        platformPost.mockImplementation((url: string, body?: unknown) => {
            if (url !== BUILDS_PATH) return deploymentOf(body);
            builds += 1;
            if (builds === 1) {
                return Promise.resolve({
                    dryRun: false,
                    operations: [{ kind: "agent", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1" }],
                    summary: { created: 0, updated: 0, unchanged: 1, failed: 0, skipped: 0 },
                });
            }
            return Promise.reject(Object.assign(new Error('agent "TestAgent": unknown toolset "foo"'), { status: 422 }));
        });

        let api: import("./BuilderActions").BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest()}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                autoSave
                onBuildActivateApiChange={next => {
                    api = next;
                }}
            />,
            { wrapper: queryWrapper }
        );
        await waitFor(() => expect(builds).toBe(1));
        await waitFor(() => expect(api).not.toBeNull());
        const deploysAfterAutoSave = deployCalls().length;

        await api!.run();
        await waitFor(() => expect(builds).toBe(2));

        expect(deployCalls()).toHaveLength(deploysAfterAutoSave);
    });

    test("a deployed component that was never stamped is deployed once, so Build & Activate settles", async () => {
        await runBuildActivate(unchangedAgent);

        await waitFor(() => expect(deployCalls()).toHaveLength(1));
    });

    test("the settling deploy lets the platform skip the restart when the agent already runs its config", async () => {
        await runBuildActivate(unchangedAgent);

        await waitFor(() => expect(deployCalls()).toHaveLength(1));
        expect(deployCalls()[0][1]).toEqual({ agentId: "agent-1", action: "deploy", skipIfCurrent: true });
    });

    test("retrying after a failure still deploys what the retry rewrote", async () => {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));

        let applyAttempts = 0;
        platformPost.mockImplementation((url: string, body?: unknown) => {
            if (url !== BUILDS_PATH) return deploymentOf(body);
            applyAttempts += 1;
            if (applyAttempts === 1) {
                return Promise.reject(Object.assign(new Error("the platform was briefly unavailable"), { status: 503 }));
            }
            return Promise.resolve({
                dryRun: false,
                operations: [{ kind: "agent", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" }],
                summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
            });
        });

        let api: import("./BuilderActions").BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest()}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                onBuildActivateApiChange={next => {
                    api = next;
                }}
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(api).not.toBeNull());
        await api!.run();
        await waitFor(() => expect(applyAttempts).toBe(1));

        api!.retry();

        await waitFor(() => expect(applyAttempts).toBe(2));
        await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
    });

    test("retrying a PARTIAL failure re-saves the component whose write failed", async () => {
        platformGet.mockImplementation(
            listing([
                { id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" },
                { id: "agent-2", name: "OtherAgent", deploymentStatus: "deployed" },
            ])
        );

        let applyAttempts = 0;
        platformPost.mockImplementation((url: string, body?: unknown) => {
            if (url !== BUILDS_PATH) return deploymentOf(body);
            applyAttempts += 1;
            return Promise.resolve({
                dryRun: false,
                operations: [
                    { kind: "agent", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" },
                    {
                        kind: "agent",
                        name: "OtherAgent",
                        action: "update",
                        status: "failed",
                        resourceId: "agent-2",
                        error: { message: "the platform refused the write", httpStatus: 422 },
                    },
                ],
                summary: { created: 0, updated: 1, unchanged: 0, failed: 1, skipped: 0 },
            });
        });

        let api: import("./BuilderActions").BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={{
                    name: "my-build",
                    full_validation_passed: true,
                    components: [
                        { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" },
                        { filename: "other.yaml", type: "agent", name: "OtherAgent", status: "validated" },
                    ],
                }}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                onBuildActivateApiChange={next => {
                    api = next;
                }}
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(api).not.toBeNull());
        await api!.run();
        await waitFor(() => expect(applyAttempts).toBe(1));

        api!.retry();

        await waitFor(() => expect(applyAttempts).toBe(2));
    });

    test("auto-saving a build the server left unchanged deploys nothing", async () => {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        platformPost.mockImplementation((url: string, body?: unknown) =>
            url === BUILDS_PATH
                ? Promise.resolve({
                      dryRun: false,
                      operations: [{ kind: "agent", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1" }],
                      summary: { created: 0, updated: 0, unchanged: 1, failed: 0, skipped: 0 },
                  })
                : deploymentOf(body)
        );

        renderActions();

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith(BUILDS_PATH, expect.anything()));
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(deployCalls()).toHaveLength(0);
    });

    test("a build whose write FAILED neither deploys nor claims success", async () => {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        platformPost.mockImplementation((url: string, body?: unknown) =>
            url === BUILDS_PATH
                ? Promise.resolve({
                      dryRun: false,
                      operations: [
                          {
                              kind: "agent",
                              name: "TestAgent",
                              action: "update",
                              status: "failed",
                              resourceId: "agent-1",
                              error: { message: "the platform refused the write", httpStatus: 422 },
                          },
                      ],
                      summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
                  })
                : deploymentOf(body)
        );

        renderActions();

        await waitFor(() => expect(platformPost).toHaveBeenCalledWith(BUILDS_PATH, expect.anything()));
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(deployCalls()).toHaveLength(0);
        expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
    });

    test("auto-saving a build the server updated does deploy it", async () => {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        platformPost.mockImplementation((url: string, body?: unknown) =>
            url === BUILDS_PATH
                ? Promise.resolve({
                      dryRun: false,
                      operations: [{ kind: "agent", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" }],
                      summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
                  })
                : deploymentOf(body)
        );

        renderActions();

        await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
    });

    test("rotating a connector redeploys the agents bound to it, even though they plan unchanged and the changed-set filter would skip them", async () => {
        const withConnector: BuildManifest = {
            name: "my-build",
            full_validation_passed: true,
            components: [
                { filename: "db.yaml", type: "connector", name: "mydb", status: "validated" },
                { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", connectors: ["mydb"] },
            ],
        };
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        platformPost.mockImplementation((url: string, body?: unknown) =>
            url === BUILDS_PATH
                ? Promise.resolve({
                      dryRun: false,
                      operations: [
                          { kind: "connector", name: "mydb", action: "update", status: "succeeded", resourceId: "conn-1" },
                          { kind: "agent", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1" },
                      ],
                      summary: { created: 0, updated: 1, unchanged: 1, failed: 0, skipped: 0 },
                  })
                : deploymentOf(body)
        );

        renderActionsWith(withConnector);

        await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
    });

    test("Build & Activate redeploys the agents bound to a rotated connector", async () => {
        platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "deployed" }]));
        platformPost.mockImplementation((url: string, body?: unknown) =>
            url === BUILDS_PATH
                ? Promise.resolve({
                      dryRun: false,
                      operations: [
                          { kind: "connector", name: "mydb", action: "update", status: "succeeded", resourceId: "conn-1" },
                          { kind: "agent", name: "TestAgent", action: "unchanged", status: "succeeded", resourceId: "agent-1" },
                      ],
                      summary: { created: 0, updated: 1, unchanged: 1, failed: 0, skipped: 0 },
                  })
                : deploymentOf(body)
        );

        let api: import("./BuilderActions").BuildActivateApi | null = null;
        render(
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={{
                    name: "my-build",
                    full_validation_passed: true,
                    components: [
                        { filename: "db.yaml", type: "connector", name: "mydb", status: "validated" },
                        { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", connectors: ["mydb"] },
                    ],
                }}
                fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                onBuildActivateApiChange={next => {
                    api = next;
                }}
            />,
            { wrapper: queryWrapper }
        );

        await waitFor(() => expect(api).not.toBeNull());
        await api!.run();
        await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
    });

    test("a genuine infrastructure failure raises the modal instead", async () => {
        buildAnswers({
            dryRun: false,
            operations: [
                {
                    kind: "agent",
                    name: "TestAgent",
                    action: "create",
                    status: "failed",
                    error: { message: "internal error", errorId: "e-3", httpStatus: 500 },
                },
            ],
            summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
        });

        renderActions();

        await screen.findByText("Retry");
        expect(handleSubmit).not.toHaveBeenCalled();
    });

    describe("a skill saved in an earlier session", () => {
        const SKILL_AND_AGENT = (): BuildManifest => ({
            name: "my-build",
            full_validation_passed: true,
            components: [
                { filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" },
                { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated" },
            ],
        });
        const SKILL_ONLY = (): BuildManifest => ({
            name: "my-build",
            full_validation_passed: true,
            components: [{ filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" }],
        });
        const DIALOG = "Replace existing component?";
        const tick = (ms = 100) => new Promise(resolve => setTimeout(resolve, ms));
        const buildPosts = () => platformPost.mock.calls.filter(([url]) => url === BUILDS_PATH);
        const dryRuns = () => buildPosts().filter(([, body]) => Boolean((body as { dryRun?: boolean }).dryRun));
        const applies = () => buildPosts().filter(([, body]) => !(body as { dryRun?: boolean }).dryRun);
        const agentUnchangedInPlan = () =>
            platformPost.mockImplementation((url: string, body?: { dryRun?: boolean }) =>
                Promise.resolve(
                    url === BUILDS_PATH
                        ? {
                              dryRun: Boolean(body?.dryRun),
                              operations: [{ kind: "agent", name: "TestAgent", action: body?.dryRun ? "unchanged" : "update", status: body?.dryRun ? "planned" : "succeeded", resourceId: "agent-1" }],
                              summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
                          }
                        : v2DeploymentOf(body)
                )
            );
        const agentUpdated = () =>
            buildAnswers({
                dryRun: false,
                operations: [{ kind: "agent", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" }],
                summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 },
            });
        const failure = (message: string, extra: object) => Object.assign(new Error(message), extra);
        const savedMessage = () => addNotification.mock.calls.map(([text]) => String(text)).find(text => text.includes("component(s) saved"));

        test.each([
            ["created", { id: "skill-9", name: "TestSkill", action: "created" }, false],
            ["unchanged", { id: "skill-9", name: "TestSkill", action: "unchanged" }, false],
        ])("a skill the save left as %s is not a failure and is not named as updated", async (_label, saved, named) => {
            agentUpdated();
            saveSkill.mockResolvedValue(saved);

            renderActionsWith(SKILL_AND_AGENT());

            await waitFor(() => expect(savedMessage()).toBeDefined());
            expect(savedMessage()!.includes("TestSkill")).toBe(named);
            expect(displayError).not.toHaveBeenCalled();
            expect(handleSubmit).not.toHaveBeenCalled();
        });

        test("an unchanged skill counts as saved, so the auto-save goes on to deploy the agent", async () => {
            agentUpdated();
            platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }]));
            saveSkill.mockResolvedValue({ id: "skill-9", name: "TestSkill", action: "unchanged" });

            renderActionsWith(SKILL_AND_AGENT());

            await waitFor(() => expect(platformPost.mock.calls.some(([url]) => String(url).includes("agentDeployments"))).toBe(true));
        });

        test("an unchanged skill is not counted as written", async () => {
            agentUpdated();
            saveSkill.mockResolvedValue({ id: "skill-9", name: "TestSkill", action: "unchanged" });

            renderActionsWith(SKILL_AND_AGENT());

            await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("1 component(s) saved"), "success"));
        });

        test.each([
            ["a server error", { status: 500 }],
            ["a refusal", { status: 403 }],
            ["a dropped connection", { code: "ERR_NETWORK" }],
        ])("a skill save that failed with %s surfaces, saves the rest, and costs no builder turn", async (_label, fields) => {
            agentUpdated();
            saveSkill.mockRejectedValue(failure("failed", fields));

            renderActionsWith(SKILL_AND_AGENT());

            await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Some components were not saved" })));
            expect(applies()).toHaveLength(1);
            expect(handleSubmit).not.toHaveBeenCalled();
        });

        test("a name taken between the lookup and the create is still handed to the builder to rename", async () => {
            agentUpdated();
            saveSkill.mockRejectedValue(failure('skill with name "TestSkill" already exists', { status: 409 }));

            renderActionsWith(SKILL_AND_AGENT());

            await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
            expect(JSON.stringify(handleSubmit.mock.calls[0])).toContain("TestSkill");
        });

        describe("replacing one the Builder did not write", () => {
            const commit = vi.fn();
            const replaceExisting = (id = "skill-9") => saveSkill.mockResolvedValue({ id, name: "TestSkill", action: "replace_deferred", commit });

            beforeEach(() => {
                localStorage.clear();
                commit.mockReset();
                commit.mockResolvedValue(undefined);
            });

            test("is replaced only once the user has answered, together with the build's own overwrites", async () => {
                agentUnchangedInPlan();
                replaceExisting();

                renderActionsWith(SKILL_AND_AGENT());

                await screen.findByText(DIALOG);
                expect(screen.getByText("TestSkill")).toBeTruthy();
                expect(screen.getByText("(skill)")).toBeTruthy();
                expect(screen.getByText("Changes: bundle")).toBeTruthy();
                expect(commit).not.toHaveBeenCalled();
                expect(applies()).toHaveLength(0);

                screen.getByRole("button", { name: "Replace and save" }).click();

                await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
                expect(commit).toHaveBeenCalledTimes(1);
                expect(dryRuns()).toHaveLength(2);
                expect(applies()).toHaveLength(1);
            });

            test("writes nothing when the user cancels, and does not blame the builder", async () => {
                agentUnchangedInPlan();
                replaceExisting();

                renderActionsWith(SKILL_AND_AGENT());

                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Cancel" }).click();
                await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                await tick();

                expect(commit).not.toHaveBeenCalled();
                expect(applies()).toHaveLength(0);
                expect(displayError).not.toHaveBeenCalled();
                expect(handleSubmit).not.toHaveBeenCalled();
                expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
            });

            test("does not ask about a skill an earlier save of this session wrote", async () => {
                agentUnchangedInPlan();
                replaceExisting("skill-mine");
                rememberWrittenResources("test-session", [
                    { id: "skill-mine", kind: "skill", stamp: "hash-1" },
                    { id: "agent-1", kind: "agent" },
                ]);

                renderActionsWith(SKILL_AND_AGENT());

                await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
                expect(screen.queryByText(DIALOG)).toBeNull();
                expect(commit).toHaveBeenCalledTimes(1);
            });

            test.each([
                ["now holds a different bundle", () => getSkill.mockResolvedValue({ contentHash: "hash-2" }), { id: "skill-mine", kind: "skill", stamp: "hash-1" }],
                ["cannot be read back", () => getSkill.mockRejectedValue(new Error("offline")), { id: "skill-mine", kind: "skill", stamp: "hash-1" }],
                ["was recorded without a stamp", () => undefined, { id: "skill-mine", kind: "skill" }],
                ["was recorded before stamps were kept", () => undefined, "skill-mine"],
            ])("asks about a skill the session wrote that %s", async (_label, arrange, remembered) => {
                agentUnchangedInPlan();
                replaceExisting("skill-mine");
                arrange();
                if (typeof remembered === "string") localStorage.setItem("sam_builder_written_resources", JSON.stringify({ "test-session": [remembered] }));
                else rememberWrittenResources("test-session", [remembered]);

                renderActionsWith(SKILL_AND_AGENT());

                await screen.findByText(DIALOG);
                expect(commit).not.toHaveBeenCalled();
                screen.getByRole("button", { name: "Cancel" }).click();
                await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                await tick();
                expect(commit).not.toHaveBeenCalled();
                expect(applies()).toHaveLength(0);
            });

            test("does not ask once the user has opted out of the confirmation", async () => {
                agentUpdated();
                replaceExisting();
                localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");

                renderActionsWith(SKILL_AND_AGENT());

                await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
                expect(screen.queryByText(DIALOG)).toBeNull();
                expect(dryRuns()).toHaveLength(0);
            });

            test("a skill-only build asks about the skill and replaces it", async () => {
                replaceExisting();

                renderActionsWith(SKILL_ONLY());

                await screen.findByText(DIALOG);
                expect(commit).not.toHaveBeenCalled();
                screen.getByRole("button", { name: "Replace and save" }).click();

                await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
                expect(commit).toHaveBeenCalledTimes(1);
            });

            function AutoSaveHost({ manifest: current }: Readonly<{ manifest: BuildManifest }>) {
                const [autoSave, setAutoSave] = React.useState(true);
                return (
                    <BuilderActions
                        configYaml=""
                        configType="agent"
                        manifest={current}
                        fetchConfigByFilename={vi.fn().mockImplementation((filename: string) => Promise.resolve(filename === "skill.yaml" ? SKILL_YAML : AGENT_YAML))}
                        userAgentsRefetch={async () => {
                            await new Promise(resolve => setTimeout(resolve, 0));
                        }}
                        autoSave={autoSave}
                        onAutoSaveComplete={() => setAutoSave(false)}
                    />
                );
            }

            test("declined, then Retry: asks again, lists the skill, and replaces it", async () => {
                agentUnchangedInPlan();
                replaceExisting();
                render(<AutoSaveHost manifest={SKILL_AND_AGENT()} />, { wrapper: queryWrapper });

                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Cancel" }).click();
                await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                await tick();
                expect(commit).not.toHaveBeenCalled();

                screen.getByRole("button", { name: "Retry" }).click();
                await screen.findByText(DIALOG);
                expect(screen.getByText("TestSkill")).toBeTruthy();
                screen.getByRole("button", { name: "Replace and save" }).click();

                await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
                expect(commit).toHaveBeenCalledTimes(1);
                expect(saveSkill).toHaveBeenCalledTimes(2);
            });

            test("a build that only saves says it was cancelled, not that it failed, in a note that is not the error colour", async () => {
                replaceExisting();
                render(<AutoSaveHost manifest={SKILL_ONLY()} />, { wrapper: queryWrapper });

                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Cancel" }).click();
                await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                await tick();

                const note = screen.getByText("Save cancelled. Try again to review and save.");
                expect(note.className).toContain("secondary-text");
                expect(note.className).not.toContain("error");
                expect(screen.queryByText(/couldn.t be completed/)).toBeNull();
            });

            test("a real failure is shown in the error colour", async () => {
                saveSkill.mockRejectedValueOnce(failure("bad gateway", { status: 502 }));
                render(<AutoSaveHost manifest={SKILL_ONLY()} />, { wrapper: queryWrapper });

                await waitFor(() => expect(screen.getByText(/bad gateway/)).toBeTruthy());
                expect(screen.getByText(/bad gateway/).className).toContain("error");
            });

            test("a skills-only build declined once is not left looking saved: Retry asks again", async () => {
                replaceExisting();
                render(<AutoSaveHost manifest={SKILL_ONLY()} />, { wrapper: queryWrapper });

                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Cancel" }).click();
                await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                await tick();
                expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");

                screen.getByRole("button", { name: "Retry" }).click();
                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Replace and save" }).click();

                await waitFor(() => expect(commit).toHaveBeenCalledTimes(1));
            });

            test("an upload that failed is tried again on Retry, not skipped as saved", async () => {
                agentUpdated();
                localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
                replaceExisting();
                commit.mockRejectedValueOnce(Object.assign(new Error("Could not update skill"), { status: 403 }));
                render(<AutoSaveHost manifest={SKILL_AND_AGENT()} />, { wrapper: queryWrapper });

                await waitFor(() => expect(displayError).toHaveBeenCalledWith(expect.objectContaining({ title: "Some components were not saved" })));
                expect(handleSubmit).not.toHaveBeenCalled();

                screen.getByRole("button", { name: "Retry" }).click();

                await waitFor(() => expect(commit).toHaveBeenCalledTimes(2));
                expect(saveSkill).toHaveBeenCalledTimes(2);
            });

            test("remembers a skill it wrote, so the next save does not ask about it", async () => {
                agentUpdated();
                saveSkill.mockResolvedValue({ id: "skill-new", name: "TestSkill", action: "created" });

                renderActionsWith(SKILL_AND_AGENT());

                await waitFor(() => expect(savedMessage()).toBeDefined());
                await waitFor(() => expect(writtenRecords("test-session").get("skill-new")).toEqual({ kind: "skill", stamp: "hash-1" }));
            });
        });

        describe("a skill whose upload fails, driven through Build & Activate", () => {
            const commit = vi.fn();
            const DEPENDENT_AGENT = (): BuildManifest => ({
                name: "my-build",
                full_validation_passed: true,
                components: [
                    { filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" },
                    { filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", depends_on: ["TestSkill"] },
                ],
            });
            const deployCalls = () => platformPost.mock.calls.filter(([url]) => String(url).includes("agentDeployments"));

            async function startBuildActivate(m: BuildManifest, agentYaml = AGENT_YAML) {
                platformGet.mockImplementation(listing([{ id: "agent-1", name: "TestAgent", deploymentStatus: "not_deployed" }]));
                let latest: import("./BuilderActions").BuildActivateApi | null = null;
                render(
                    <BuilderActions
                        configYaml=""
                        configType="agent"
                        manifest={m}
                        fetchConfigByFilename={vi.fn().mockImplementation((filename: string) => Promise.resolve(filename === "skill.yaml" ? SKILL_YAML : agentYaml))}
                        onBuildActivateApiChange={next => {
                            latest = next;
                        }}
                    />,
                    { wrapper: queryWrapper }
                );
                await waitFor(() => expect(latest).not.toBeNull());
                return () => latest!;
            }

            beforeEach(() => {
                localStorage.clear();
                localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
                commit.mockReset();
                commit.mockRejectedValue(Object.assign(new Error('Could not update skill "TestSkill": forbidden'), { status: 403 }));
                saveSkill.mockResolvedValue({ id: "skill-9", name: "TestSkill", action: "replace_deferred", commit });
                platformPost.mockImplementation((url: string, body?: unknown) =>
                    Promise.resolve(
                        url === BUILDS_PATH
                            ? { dryRun: false, operations: [{ kind: "agent", name: "TestAgent", action: "update", status: "succeeded", resourceId: "agent-1" }], summary: { created: 0, updated: 1, unchanged: 0, failed: 0, skipped: 0 } }
                            : v2DeploymentOf(body)
                    )
                );
            });

            test("keeps the failure on screen, and does not deploy the agent bound to the stale skill", async () => {
                const api = await startBuildActivate(DEPENDENT_AGENT());

                await api().run();
                await tick(300);

                expect(commit).toHaveBeenCalledTimes(1);
                expect(deployCalls()).toHaveLength(0);
                expect(api().failure).toMatchObject({ kind: "retryable", message: expect.stringContaining('Could not update skill "TestSkill"') });
            });

            describe("a skill that fails before it is uploaded", () => {
                const agentComponent = (extra: object) => ({ filename: "agent.yaml", type: "agent", name: "TestAgent", status: "validated", ...extra });
                const manifestWith = (extra: object): BuildManifest => ({
                    name: "my-build",
                    full_validation_passed: true,
                    components: [{ filename: "skill.yaml", type: "skill", name: "TestSkill", status: "validated" }, agentComponent(extra)],
                });
                const agentUsingSkillInItsConfig = AGENT_YAML.replace("      agent_card:", "      skills:\n        - name: TestSkill\n      agent_card:");
                const STYLES: [string, object, string][] = [
                    ["depends_on", { depends_on: ["TestSkill"] }, AGENT_YAML],
                    ["the manifest's skills list", { skills: ["TestSkill"] }, AGENT_YAML],
                    ["its own config", {}, agentUsingSkillInItsConfig],
                ];
                const FAILURES: [string, Error][] = [
                    ["the lookup answers 500", Object.assign(new Error('Could not check whether skill "TestSkill" already exists: boom'), { status: 500 })],
                    ["an artifact cannot be loaded", new Error('Skill "TestSkill" references artifact "x" which could not be loaded from the session')],
                    ["the lookup answers 401", Object.assign(new Error('Could not check whether skill "TestSkill" already exists: expired'), { status: 401 })],
                    ["the bundle cannot be assembled", new Error('Skill "TestSkill" reference "a.md" has neither inline content nor an artifact.')],
                ];

                test.each(FAILURES.flatMap(([label, error]) => STYLES.map(([style, extra, yaml]) => [label, style, error, extra, yaml] as const)))(
                    "when %s, the agent that uses the skill through %s is not deployed and the failure stays on screen",
                    async (_label, _style, error, extra, yaml) => {
                        saveSkill.mockRejectedValue(error);
                        const api = await startBuildActivate(manifestWith(extra), yaml);

                        await api().run();
                        await tick(300);

                        expect(deployCalls()).toHaveLength(0);
                        expect(api().failure.kind).not.toBe("none");
                        expect(api().failure).toMatchObject({ message: expect.stringContaining("TestSkill") });
                    }
                );

                test("with builder_server_apply off, the sweep that follows deploys nothing for an agent this session has no id for", async () => {
                    serverApplyEnabled = false;
                    saveSkill.mockRejectedValue(FAILURES[0][1]);
                    const api = await startBuildActivate(manifestWith({ depends_on: ["TestSkill"] }));

                    await api().run();

                    await tick(300);
                    expect(deployCalls()).toHaveLength(0);
                    expect(api().failure).toMatchObject({ kind: "retryable", message: expect.stringContaining("TestSkill") });
                });

                test("an agent that does not use the skill is deployed all the same", async () => {
                    saveSkill.mockRejectedValue(FAILURES[0][1]);
                    const api = await startBuildActivate(manifestWith({ depends_on: ["SomethingElse"] }));

                    await api().run();

                    await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
                });
            });

            test("Retry uploads the skill again, instead of treating it as saved", async () => {
                const api = await startBuildActivate(DEPENDENT_AGENT());
                await api().run();
                await tick(300);
                commit.mockResolvedValue(undefined);

                api().retry();

                await waitFor(() => expect(commit).toHaveBeenCalledTimes(2));
            });

            test("an agent that does not use the skill is still deployed", async () => {
                const independent: BuildManifest = { ...DEPENDENT_AGENT(), components: DEPENDENT_AGENT().components!.map(c => (c.type === "agent" ? { ...c, depends_on: [] } : c)) };
                const api = await startBuildActivate(independent);

                await api().run();

                await waitFor(() => expect(deployCalls().length).toBeGreaterThan(0));
                expect(api().failure).toMatchObject({ kind: "retryable" });
            });
        });

        describe("with builder_server_apply off", () => {
            beforeEach(() => {
                localStorage.clear();
                serverApplyEnabled = false;
            });

            const replaceAfterAsking = () =>
                saveSkill.mockImplementation(async (request: { confirmReplace: (existing: { id: string; name: string }) => Promise<void> }) => {
                    await request.confirmReplace({ id: "skill-9", name: "TestSkill" });
                    return { id: "skill-9", name: "TestSkill", action: "updated" };
                });

            test("asks inline, never defers, and says what it replaced", async () => {
                replaceAfterAsking();

                renderActionsWith(SKILL_ONLY());

                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Replace and save" }).click();

                await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Updated in place: TestSkill"), "success"));
                expect(saveSkill.mock.calls[0][0]).not.toHaveProperty("deferReplace", true);
                expect(buildPosts()).toHaveLength(0);
            });

            test("writes nothing when the user cancels, and does not report success", async () => {
                replaceAfterAsking();

                renderActionsWith(SKILL_ONLY());

                await screen.findByText(DIALOG);
                screen.getByRole("button", { name: "Cancel" }).click();
                await waitFor(() => expect(screen.queryByText(DIALOG)).toBeNull());
                await tick();

                expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
                expect(handleSubmit).not.toHaveBeenCalled();
            });

            test("a conflict the save could not resolve is a loud failure, not a success", async () => {
                saveSkill.mockRejectedValue(failure('A skill named "TestSkill" already exists on the platform with different contents.', { status: 409 }));

                renderActionsWith(SKILL_ONLY());

                await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
                expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("saved successfully"), "success");
            });
        });
    });

    describe("Build & Activate waits for the platform's check", () => {
        const tick = (ms = 100) => new Promise(resolve => setTimeout(resolve, ms));
        const problem = { kind: "agent", name: "TestAgent", message: "model alias is unknown" };
        const CHECKS: Array<{ label: string; check: import("./platformCheck").PlatformCheck; can: boolean }> = [
            { label: "idle", check: { kind: "idle" }, can: false },
            { label: "checking", check: { kind: "checking" }, can: false },
            { label: "refused", check: { kind: "refused", problems: [problem], forbidden: [] }, can: false },
            { label: "notAllowed", check: { kind: "notAllowed", problems: [problem] }, can: false },
            { label: "unavailable", check: { kind: "unavailable", message: "platform down" }, can: false },
            { label: "passed", check: { kind: "passed", warnings: ["a heads up"] }, can: true },
        ];

        async function publishedApi() {
            let latest: import("./BuilderActions").BuildActivateApi | null = null;
            render(
                <BuilderActions
                    configYaml=""
                    configType="agent"
                    manifest={manifest()}
                    fetchConfigByFilename={vi.fn().mockResolvedValue(AGENT_YAML)}
                    onBuildActivateApiChange={next => {
                        latest = next;
                    }}
                />,
                { wrapper: queryWrapper }
            );
            await waitFor(() => expect(latest).not.toBeNull());
            return () => latest!;
        }

        test.each(CHECKS)("with the check $label the bridged button is enabled: $can, and the check is reported", async ({ check, can }) => {
            platformCheckState.check = check;

            const api = await publishedApi();

            await waitFor(() => expect(api().canBuildActivate).toBe(can));
            expect(api().platformCheck).toEqual(check.kind === "idle" ? { kind: "idle" } : check);
        });

        test.each(CHECKS.filter(({ can }) => !can))("with the check $label the unbridged button is not offered", async ({ check }) => {
            platformCheckState.check = check;

            renderActions();

            await tick();
            expect(screen.queryByRole("button", { name: "Build & Activate" })).toBeNull();
        });

        test("the bridge carries checkAgain and askBuilder to the page's check", async () => {
            const checkAgain = vi.fn();
            platformCheckState.checkAgain = checkAgain;
            platformCheckState.check = { kind: "refused", problems: [problem], forbidden: [] };

            const api = await publishedApi();
            api().checkAgain();

            expect(checkAgain).toHaveBeenCalledTimes(1);
            expect(typeof api().askBuilder).toBe("function");
        });

        test("with the flag off the check is not consulted: Build & Activate is on offer and nothing is reported", async () => {
            serverApplyEnabled = false;
            platformCheckState.check = { kind: "refused", problems: [problem], forbidden: [] };

            const api = await publishedApi();

            await waitFor(() => expect(api().canBuildActivate).toBe(true));
            expect(api().platformCheck).toEqual({ kind: "idle" });
        });
    });
});
