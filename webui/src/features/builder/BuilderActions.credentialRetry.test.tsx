// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("./hooks/usePlatformCheck", () => ({ usePlatformCheck: () => ({ check: { kind: "passed", warnings: [] }, key: null, checkAgain: () => {} }) }));

import { builderKeys } from "@/api/builder";

import { respondingAsV2, v2DeploymentOf, v2Error, v2Page, v2PageOf, v2ResourceOf } from "@/testing/helpers/platformResponses";
import { withCurrentStamp } from "@/testing/helpers/validationStamp";

import type { BuildActivateApi } from "./BuilderActions";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { credentialComponentAwaitingSave, savedCredentialVersions } from "./hooks/useCredentialResave";

const BUILDS_PATH = "/api/internal/platform/builder/builds";
const AGENTS_PATH = "/api/v2/platform/agents";
const CONNECTORS_PATH = "/api/v2/platform/connectors";
const PLACEHOLDER = "<<__SAM_REQUIRED__>>";
const CHAT_KEY = "partner-key-777";
const SECOND_KEY = "rotated-key-888";

const AGENT_YAML = `
apps:
  - name: AgentA
    app_config:
      agent_name: AgentA
      description: A test agent with a sufficiently long description.
      instruction: |
        ${"x".repeat(120)}
      agent_card:
        description: Another description on agent_card.
`;

const connectorJson = (password: string) => JSON.stringify({ name: "mydb", type: "sql", subtype: "postgres", values: { hostname: "db.example.com", password } });

const SESSION_CONNECTOR = { filename: "db.json", type: "connector", name: "mydb", status: "validated" };
const PLATFORM_CONNECTOR = { filename: "", type: "connector", name: "mydb", origin: "platform", status: "validated" };
const BOUND_AGENT = { filename: "agent.yaml", type: "agent", name: "AgentA", status: "validated", connectors: ["mydb"] };

type Component = Record<string, unknown>;

function newQueryWrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("Try again after a credential-blocked build", () => {
    let BuilderActions: typeof import("./BuilderActions").BuilderActions;
    let queryWrapper: ReturnType<typeof newQueryWrapper>;
    let queryClient: QueryClient;
    let responding = false;
    let sessionInUse = "test-session";

    const platformGet = vi.fn();
    const platformPost = vi.fn();
    let serverApplyEnabled = true;

    let artifactPassword = PLACEHOLDER;
    const artifactVersions = new Map<string, number>();
    let storedPassword: string | undefined;
    let agentOnPlatform = false;
    let readVersions: number[] | undefined;
    const versionsListed = (url: string) => {
        if (readVersions) return readVersions;
        const filename = decodeURIComponent(url.split("/").at(-2) ?? "");
        return Array.from({ length: (artifactVersions.get(filename) ?? -1) + 1 }, (_unused, version) => version);
    };
    const agentDeployedWith: Array<string | undefined> = [];

    const buildPosts = () => platformPost.mock.calls.filter(([url]) => url === BUILDS_PATH);
    const deployPosts = () => platformPost.mock.calls.filter(([url]) => String(url).includes("agentDeployments"));
    const connectorContentSentTo = (call: unknown[]) => (call[1] as { components: Array<{ kind: string; content: string }> }).components.find(c => c.kind === "connector")?.content ?? "";
    const giveKeyInChat = (key: string) => {
        artifactPassword = key;
        artifactVersions.set("db.json", (artifactVersions.get("db.json") ?? 0) + 1);
    };
    const tick = (ms = 150) => act(() => new Promise<void>(resolve => setTimeout(resolve, ms)));

    function answerBuild(body: { components: Array<{ kind: string; name: string; content: string }> }) {
        const operations = body.components.map(component => {
            if (component.kind === "agent") {
                const action = agentOnPlatform ? "unchanged" : "create";
                agentOnPlatform = true;
                return { kind: "agent", name: component.name, action, status: "succeeded", resourceId: "agent-1" };
            }
            if (component.kind !== "connector") {
                return { kind: component.kind, name: component.name, action: "unchanged", status: "succeeded", resourceId: `${component.kind}-1` };
            }
            const incoming = (JSON.parse(component.content) as { values: { password: string } }).values.password;
            const action = storedPassword === undefined ? "create" : "update";
            let changed = storedPassword === undefined;
            if (incoming !== PLACEHOLDER && incoming !== storedPassword) {
                storedPassword = incoming;
                changed = true;
            }
            storedPassword ??= incoming;
            return { kind: "connector", name: component.name, action: changed ? action : "unchanged", status: "succeeded", resourceId: "conn-1" };
        });
        return { dryRun: false, operations, summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 } };
    }

    function answerDeployment(agentId: string) {
        if (storedPassword === PLACEHOLDER) {
            return v2Error(422, 'agent cannot be deployed until all required fields are provided: connectors.mydb.password: connector "mydb" has no value for password; set it on the Connectors page');
        }
        agentDeployedWith.push(storedPassword);
        return v2DeploymentOf({ agentId });
    }

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        sessionStorage.clear();
        localStorage.clear();
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        serverApplyEnabled = true;
        artifactPassword = PLACEHOLDER;
        readVersions = undefined;
        responding = false;
        sessionInUse = "test-session";
        artifactVersions.clear();
        for (const filename of ["db.json", "agent.yaml", "events.yaml", "remote.json"]) artifactVersions.set(filename, 0);
        storedPassword = undefined;
        agentOnPlatform = false;
        agentDeployedWith.length = 0;

        platformGet.mockImplementation((path: string) => {
            if (path.startsWith(AGENTS_PATH)) return Promise.resolve(v2PageOf(path, agentOnPlatform ? [{ id: "agent-1", name: "AgentA", deploymentStatus: agentDeployedWith.length > 0 ? "deployed" : "not_deployed" }] : []));
            if (path.startsWith(CONNECTORS_PATH)) return Promise.resolve(v2PageOf(path, storedPassword === undefined ? [] : [{ id: "conn-1", name: "mydb", type: "sql", subtype: "postgres" }]));
            return Promise.resolve(v2Page([]));
        });
        platformPost.mockImplementation(async (url: string, body?: { agentId?: string; name?: string; components?: never }) => {
            if (url === BUILDS_PATH) return answerBuild(body as never);
            if (String(url).includes("agentDeployments")) return answerDeployment(body?.agentId ?? "");
            if (String(url).includes("Deployments")) return v2DeploymentOf(body);
            if (url === AGENTS_PATH) agentOnPlatform = true;
            return v2ResourceOf(url, { id: url === AGENTS_PATH ? "agent-1" : "conn-1", name: body?.name ?? "" });
        });

        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) => (
                <button onClick={onClick} disabled={disabled}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/api/client", () => ({
            api: { webui: { get: (url: string) => Promise.resolve({ data: versionsListed(url) }) }, platform: { get: respondingAsV2(platformGet), post: respondingAsV2(platformPost), delete: vi.fn() } },
        }));
        vi.doMock("@/utils/api", () => ({ getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback) }));
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                addNotification: vi.fn(),
                displayError: vi.fn(),
                uploadArtifactFile: vi.fn(),
                sessionId: sessionInUse,
                allArtifacts: [],
                handleSubmit: vi.fn(),
                isResponding: responding,
                isCancelling: false,
            }),
        }));
        vi.doMock("@/providers/contexts/AuthContext", () => ({ useAuthContext: () => ({ userInfo: { username: "sam_dev_user" } }) }));
        vi.doMock("./hooks/useWorkflowDeployment", () => ({ useWorkflowDeployment: () => ({ deploy: vi.fn(), isLoading: false }) }));
        vi.doMock("./ConnectorEditDialog", () => ({ ConnectorEditDialog: () => null }));
        vi.doMock("@/api/skills", () => ({ useSaveSkillFromConfigMutation: () => ({ mutateAsync: vi.fn() }) }));
        vi.doMock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [...artifactVersions].map(([filename, version]) => ({ filename, version })), isLoading: false, refetch: () => Promise.resolve() }) }));
        vi.doMock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => serverApplyEnabled }));
        vi.doMock("@/common/product-analytics/report", () => ({ report: vi.fn(), reportingSessionId: () => "session-one" }));

        BuilderActions = withCurrentStamp((await import("./BuilderActions")).BuilderActions, filename => artifactVersions.get(filename));
        queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        queryWrapper = newQueryWrapper(queryClient);
    });

    const fetchConfigByFilename = (filename: string) => Promise.resolve(filename === "db.json" ? connectorJson(artifactPassword) : AGENT_YAML);

    function Host({ components, creds, onApi }: Readonly<{ components: Component[]; creds: ReadonlyMap<string, number>; onApi: (api: BuildActivateApi | null) => void }>) {
        const [autoSave, setAutoSave] = React.useState(true);
        const manifest = { name: "my-build", full_validation_passed: true, components } as unknown as BuildManifest;
        return (
            <BuilderActions
                configYaml=""
                configType="agent"
                manifest={manifest}
                fetchConfigByFilename={fetchConfigByFilename}
                userAgentsRefetch={() => new Promise<void>(resolve => setTimeout(resolve, 0))}
                autoSave={autoSave}
                onAutoSaveComplete={() => setAutoSave(false)}
                componentsMissingCreds={creds}
                onBuildActivateApiChange={onApi}
            />
        );
    }

    async function blockedBuild(components: Component[], saved: () => number = () => buildPosts().length) {
        let latest: BuildActivateApi | null = null;
        const creds = new Map([["mydb", 1]]);
        const view = render(<Host components={components} creds={creds} onApi={next => (latest = next)} />, { wrapper: queryWrapper });
        await waitFor(() => expect(saved()).toBe(1));
        await tick();
        const rerender = (next: ReadonlyMap<string, number>) => view.rerender(<Host components={components} creds={next} onApi={api => (latest = api)} />);
        return { api: () => latest!, rerender };
    }

    const NO_CREDS: ReadonlyMap<string, number> = new Map();

    test("the first pass saves the placeholder connector and holds the deploy", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

        expect(storedPassword).toBe(PLACEHOLDER);
        expect(deployPosts()).toHaveLength(0);
        expect(build.api().failure).toMatchObject({ message: expect.stringContaining("Credentials are missing") });
    });

    test("a key given in the chat reaches the connector before Try again deploys the agent", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

        giveKeyInChat(CHAT_KEY);
        build.rerender(NO_CREDS);
        act(() => build.api().retry());

        await waitFor(() => expect(agentDeployedWith).toEqual([CHAT_KEY]));
        expect(storedPassword).toBe(CHAT_KEY);
        expect(buildPosts()).toHaveLength(2);
        expect(connectorContentSentTo(buildPosts()[1])).toContain(CHAT_KEY);
        expect(platformPost.mock.invocationCallOrder[platformPost.mock.calls.indexOf(buildPosts()[1])]).toBeLessThan(platformPost.mock.invocationCallOrder[platformPost.mock.calls.indexOf(deployPosts()[0])]);
        await waitFor(() => expect(build.api().failure).toEqual({ kind: "none" }));
    });

    test("Try again with the credential still unset writes nothing new and deploys nothing", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

        act(() => build.api().retry());
        await tick();
        act(() => build.api().retry());
        await tick();

        expect(storedPassword).toBe(PLACEHOLDER);
        expect(agentDeployedWith).toEqual([]);
        expect(deployPosts()).toHaveLength(0);
        expect(build.api().failure).toMatchObject({ message: expect.stringContaining("Credentials are missing") });
    });

    test("a refusal from the platform is shown, not reported as a deployed build", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

        giveKeyInChat(CHAT_KEY);
        build.rerender(NO_CREDS);
        platformPost.mockImplementation(async (url: string, body?: { agentId?: string }) => {
            if (url === BUILDS_PATH)
                return {
                    dryRun: false,
                    operations: [
                        { kind: "connector", name: "mydb", action: "unchanged", status: "succeeded", resourceId: "conn-1" },
                        { kind: "agent", name: "AgentA", action: "unchanged", status: "succeeded", resourceId: "agent-1" },
                    ],
                    summary: {},
                };
            if (String(url).includes("agentDeployments")) return answerDeployment(body?.agentId ?? "");
            return {};
        });
        act(() => build.api().retry());

        await waitFor(() => expect(deployPosts()).toHaveLength(1));
        await waitFor(() => expect(build.api().failure).toMatchObject({ message: expect.stringContaining("cannot be deployed until all required fields are provided") }));
        expect(agentDeployedWith).toEqual([]);
    });

    test("a key rotated after a successful Try again reaches the connector on the next one", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);
        giveKeyInChat(CHAT_KEY);
        build.rerender(NO_CREDS);
        act(() => build.api().retry());
        await waitFor(() => expect(agentDeployedWith).toEqual([CHAT_KEY]));
        await tick();

        giveKeyInChat(SECOND_KEY);
        build.rerender(NO_CREDS);
        act(() => build.api().retry());

        await waitFor(() => expect(agentDeployedWith).toEqual([CHAT_KEY, SECOND_KEY]));
        expect(storedPassword).toBe(SECOND_KEY);
    });

    test("a save that read a version the list trailed records what it read, so Try again after the list catches up deploys without putting the build again", async () => {
        readVersions = [0, 1, 2];
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);
        artifactVersions.set("db.json", 2);
        build.rerender(NO_CREDS);
        await tick();

        act(() => build.api().retry());
        await waitFor(() => expect(deployPosts().length).toBeGreaterThan(0));
        await tick();

        expect(buildPosts()).toHaveLength(1);
    });

    test("a Try again with nothing changed since the save deploys without putting the build again", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);
        giveKeyInChat(CHAT_KEY);
        build.rerender(NO_CREDS);
        act(() => build.api().retry());
        await waitFor(() => expect(agentDeployedWith).toEqual([CHAT_KEY]));
        await tick();
        expect(buildPosts()).toHaveLength(2);

        const deploysBefore = deployPosts().length;
        act(() => build.api().retry());
        await waitFor(() => expect(deployPosts().length).toBeGreaterThan(deploysBefore));
        await tick();

        expect(buildPosts()).toHaveLength(2);
        expect(storedPassword).toBe(CHAT_KEY);
        expect(agentDeployedWith.every(value => value === CHAT_KEY)).toBe(true);
    });

    test("a value set on the resource's page is not written over by Try again when the artifact has not changed", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

        storedPassword = CHAT_KEY;
        build.rerender(NO_CREDS);
        act(() => build.api().retry());

        await waitFor(() => expect(agentDeployedWith.length).toBeGreaterThanOrEqual(1));
        expect(agentDeployedWith.every(value => value === CHAT_KEY)).toBe(true);
        expect(buildPosts()).toHaveLength(1);
    });

    describe("a key given in the chat that the version lookup has not caught up with", () => {
        test("Try again while the Builder is still replying puts the build again", async () => {
            const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

            artifactPassword = CHAT_KEY;
            responding = true;
            build.rerender(NO_CREDS);
            act(() => build.api().retry());

            await waitFor(() => expect(agentDeployedWith.length).toBeGreaterThanOrEqual(1));
            expect(buildPosts()).toHaveLength(2);
            expect(storedPassword).toBe(CHAT_KEY);
        });

        test("Try again after an edit invalidated the version lookup, before it came back, puts the build again", async () => {
            const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

            const key = builderKeys.manifestVersions("test-session", "db.json");
            queryClient.setQueryData(key, [0]);
            await queryClient.invalidateQueries({ queryKey: key, refetchType: "none" });
            artifactPassword = CHAT_KEY;
            build.rerender(NO_CREDS);
            act(() => build.api().retry());

            await waitFor(() => expect(agentDeployedWith.length).toBeGreaterThanOrEqual(1));
            expect(buildPosts()).toHaveLength(2);
            expect(storedPassword).toBe(CHAT_KEY);
        });

        test("Try again while the version lookup is still fetching puts the build again, once the lookup has answered", async () => {
            const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

            void queryClient.fetchQuery({ queryKey: builderKeys.manifestVersions("test-session", "db.json"), queryFn: () => new Promise<number[]>(resolve => setTimeout(() => resolve([0]), 50)) });
            artifactPassword = CHAT_KEY;
            build.rerender(NO_CREDS);
            act(() => build.api().retry());

            await waitFor(() => expect(agentDeployedWith.length).toBeGreaterThanOrEqual(1));
            expect(buildPosts()).toHaveLength(2);
        });

        test("a save in another session proves nothing about this one", async () => {
            const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);

            sessionInUse = "another-session";
            build.rerender(NO_CREDS);
            act(() => build.api().retry());

            await waitFor(() => expect(buildPosts()).toHaveLength(2));
        });

        test("a key given in the chat while the save is in flight is put again by Try again", async () => {
            let release: () => void = () => {};
            const gate = new Promise<void>(resolve => (release = resolve));
            const platform = platformPost.getMockImplementation()!;
            let held = false;
            platformPost.mockImplementation(async (url: string, body?: never) => {
                if (url === BUILDS_PATH && !held) {
                    held = true;
                    await gate;
                }
                return platform(url, body);
            });
            let latest: BuildActivateApi | null = null;
            const components = [SESSION_CONNECTOR, BOUND_AGENT];
            const host = (creds: ReadonlyMap<string, number>) => <Host components={components} creds={creds} onApi={api => (latest = api)} />;
            const view = render(host(new Map([["mydb", 1]])), { wrapper: queryWrapper });
            await waitFor(() => expect(buildPosts()).toHaveLength(1));

            giveKeyInChat(CHAT_KEY);
            view.rerender(host(NO_CREDS));
            release();
            await tick(400);
            view.rerender(host(NO_CREDS));
            act(() => latest!.retry());

            await waitFor(() => expect(buildPosts()).toHaveLength(2));
            await waitFor(() => expect(storedPassword).toBe(CHAT_KEY));
        });
    });

    test("without server-side apply Try again does not create the connector a second time", async () => {
        serverApplyEnabled = false;
        const connectorCreates = () => platformPost.mock.calls.filter(([url]) => url === CONNECTORS_PATH);
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT], () => connectorCreates().length);
        giveKeyInChat(CHAT_KEY);
        build.rerender(NO_CREDS);

        act(() => build.api().retry());
        await tick();

        await waitFor(() => expect(deployPosts().length).toBeGreaterThanOrEqual(1));
        expect(connectorCreates()).toHaveLength(1);
        expect(buildPosts()).toHaveLength(0);
    });

    test("a re-save that fails deploys nothing and shows the failure", async () => {
        const build = await blockedBuild([SESSION_CONNECTOR, BOUND_AGENT]);
        giveKeyInChat(CHAT_KEY);
        build.rerender(NO_CREDS);
        platformPost.mockImplementation(async (url: string) => {
            if (url === BUILDS_PATH) throw Object.assign(new Error("build endpoint unavailable"), { status: 503 });
            return {};
        });

        act(() => build.api().retry());

        await waitFor(() => expect(build.api().failure).toMatchObject({ message: expect.stringContaining("build endpoint unavailable") }));
        await tick();
        expect(deployPosts()).toHaveLength(0);
        expect(agentDeployedWith).toEqual([]);
    });

    describe("every kind that can carry a credential given in the chat", () => {
        const GATEWAY = { filename: "events.yaml", type: "gateway", name: "events", status: "validated" };
        const REMOTE_AGENT = { filename: "remote.json", type: "a2a_agent", name: "remote", status: "validated" };

        describe("whether Try again must save before it deploys", () => {
            const at = (filename: string, version: number) => new Map([[filename, version]]);
            test.each([
                ["a connector never saved", SESSION_CONNECTOR, new Map(), at("db.json", 0), true],
                ["an entrypoint never saved", GATEWAY, new Map(), at("events.yaml", 0), true],
                ["a remote agent never saved", REMOTE_AGENT, new Map(), at("remote.json", 0), true],
                ["a connector edited after its save", SESSION_CONNECTOR, at("db.json", 0), at("db.json", 1), true],
                ["an entrypoint edited after its save", GATEWAY, at("events.yaml", 0), at("events.yaml", 3), true],
                ["a remote agent edited after its save", REMOTE_AGENT, at("remote.json", 2), at("remote.json", 3), true],
                ["a connector whose version has not loaded", SESSION_CONNECTOR, at("db.json", 0), new Map(), true],
                ["a connector unchanged since its save", SESSION_CONNECTOR, at("db.json", 1), at("db.json", 1), false],
                ["an entrypoint unchanged since its save", GATEWAY, at("events.yaml", 1), at("events.yaml", 1), false],
                ["a remote agent unchanged since its save", REMOTE_AGENT, at("remote.json", 1), at("remote.json", 1), false],
                ["a platform connector", PLATFORM_CONNECTOR, new Map(), new Map(), false],
                ["an agent", BOUND_AGENT, new Map(), at("agent.yaml", 0), false],
            ])("%s", (_kind, component, saved, current, awaiting) => {
                expect(credentialComponentAwaitingSave([component] as unknown as NonNullable<BuildManifest["components"]>, saved, current)).toBe(awaiting);
            });
        });

        test.each([
            ["an entrypoint", GATEWAY],
            ["a remote agent", REMOTE_AGENT],
        ])("%s: the build endpoint is called again once its artifact has changed, and not before", async (_kind, component) => {
            const build = await blockedBuild([component, BOUND_AGENT]);
            build.rerender(NO_CREDS);

            act(() => build.api().retry());
            await tick();
            expect(buildPosts()).toHaveLength(1);

            artifactVersions.set(String(component.filename), 1);
            build.rerender(NO_CREDS);
            act(() => build.api().retry());

            await waitFor(() => expect(buildPosts()).toHaveLength(2));
        });
    });

    describe("what a save proves the platform holds", () => {
        const components = [
            SESSION_CONNECTOR,
            { filename: "events.yaml", type: "gateway", name: "events" },
            { filename: "remote.json", type: "a2a_agent", name: "remote" },
            { filename: "theirs.json", type: "connector", name: "theirs", origin: "platform" },
            BOUND_AGENT,
        ] as unknown as NonNullable<BuildManifest["components"]>;
        const versions = new Map([
            ["db.json", 4],
            ["events.yaml", 5],
            ["remote.json", 6],
            ["theirs.json", 7],
            ["agent.yaml", 8],
        ]);

        test("a component the save wrote or found identical is recorded at the version it was sent at", () => {
            const saved = savedCredentialVersions(components, { changed: ["mydb"], matched: ["events"], declined: [] }, versions);
            expect(Object.fromEntries(saved)).toEqual({ "db.json": 4, "events.yaml": 5 });
        });

        test("a component the save accepted only in part is not recorded", () => {
            const saved = savedCredentialVersions(components, { changed: ["mydb", "remote"], matched: [], declined: ["remote"] }, versions);
            expect(Object.fromEntries(saved)).toEqual({ "db.json": 4 });
        });

        test("a component that is the platform's, or not a credential kind, is not recorded", () => {
            const saved = savedCredentialVersions(components, { changed: ["theirs", "AgentA"], matched: [], declined: [] }, versions);
            expect(saved.size).toBe(0);
        });

        test("a component whose version had not loaded is not recorded", () => {
            const saved = savedCredentialVersions(components, { changed: ["mydb"], matched: [], declined: [] }, new Map());
            expect(saved.size).toBe(0);
        });
    });

    describe("builds with no connector of their own", () => {
        test.each([
            ["an agent only", [BOUND_AGENT]],
            ["a connector that is the platform's", [PLATFORM_CONNECTOR, BOUND_AGENT]],
        ])("%s: Try again deploys without putting the build again", async (_name, components) => {
            storedPassword = CHAT_KEY;
            const build = await blockedBuild(components);
            build.rerender(NO_CREDS);

            act(() => build.api().retry());

            await waitFor(() => expect(deployPosts().length).toBeGreaterThanOrEqual(1));
            expect(buildPosts()).toHaveLength(1);
        });
    });
});
