// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

describe("useBuilderPageState", () => {
    let useBuilderPageState: typeof import("./useBuilderPageState").useBuilderPageState;

    let mockManifest: BuildManifest | null;
    let mockComponentData: Map<string, ComponentCanvasData>;
    let mockConfigArtifacts: Array<{ filename: string; mimeType: string; accumulatedContent?: string }>;
    let mockArtifactsBySession: Record<string, Array<{ filename: string; mimeType: string; accumulatedContent?: string }>>;
    let mockConfigYaml: string | null;
    let mockSessionId: string;
    let mockDirectFetch: Record<string, string | null>;
    let directFetchCalls: Array<[string, unknown]>;
    let wrapper: (props: { children: ReactNode }) => ReturnType<typeof createElement>;
    let client: QueryClient;
    let mockUploadArtifactFile: ReturnType<typeof vi.fn>;
    let builderKeys: typeof import("@/api/builder").builderKeys;

    const dataFor = (name: string, overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData => ({
        name,
        filename: `${name}.yaml`,
        type: "agent",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...overrides,
    });

    beforeAll(async () => {
        await import("./useBuilderPageState");
    }, 60_000);

    beforeEach(async () => {
        vi.resetModules();

        mockManifest = null;
        mockComponentData = new Map();
        mockConfigArtifacts = [];
        mockArtifactsBySession = {};
        mockConfigYaml = null;
        mockSessionId = "s1";
        mockDirectFetch = {};
        directFetchCalls = [];

        client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
        mockUploadArtifactFile = vi.fn().mockResolvedValue({ uri: "artifact://x", sessionId: "s1" });

        vi.doMock("./hooks/useBuilderConfig", () => ({
            useBuilderConfig: () => ({
                configYaml: mockConfigYaml,
                configType: mockConfigYaml ? "agent" : "unknown",
                artifactInfo: null,
                isLoading: false,
            }),
        }));

        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: mockManifest, isLoading: false }),
            SAM_BUILD_MANIFEST_MIME_TYPE: "application/vnd.sam-build-manifest+yaml",
        }));

        vi.doMock("./hooks/useBuilderCanvasData", () => ({
            useBuilderCanvasData: () => ({
                componentData: mockComponentData,
                manifest: mockManifest,
                generatingFilename: null,
                generatingFilenames: new Set(),
                isGeneratingConfigs: false,
                hasPendingUserInput: false,
            }),
        }));

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                downloadAndResolveArtifact: () => Promise.resolve(null),
                sessionId: mockSessionId,
                uploadArtifactFile: mockUploadArtifactFile,
            }),
        }));

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: mockArtifactsBySession[mockSessionId] ?? mockConfigArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("@/api/builder", async importOriginal => {
            const actual = await importOriginal<typeof import("@/api/builder")>();
            return {
                ...actual,
                getManifestContent: (_sid: string, filename: string, version?: unknown) => {
                    directFetchCalls.push([filename, version]);
                    return Promise.resolve(mockDirectFetch[filename] ?? null);
                },
                getManifestContentAt: (_sid: string, filename: string, version: number) => {
                    directFetchCalls.push([filename, version]);
                    const content = mockDirectFetch[filename];
                    return content == null ? Promise.reject(Object.assign(new Error("not found"), { status: 404 })) : Promise.resolve(content);
                },
            };
        });

        const mod = await import("./useBuilderPageState");
        useBuilderPageState = mod.useBuilderPageState;
        builderKeys = (await import("@/api/builder")).builderKeys;
    });

    test("defaults rootName to 'New Build' when no manifest or config is present", () => {
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        expect(result.current.rootName).toBe("New Build");
    });

    test("handleConnectorUpdated uploads the edited config and invalidates its content query", async () => {
        mockComponentData = new Map([["JiraConnector", dataFor("JiraConnector", { filename: "jira.json", type: "connector" })]]);
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.setSelectedNodeId("JiraConnector"));

        await act(async () => {
            result.current.handleConnectorUpdated(JSON.stringify({ name: "JiraConnector", type: "api", values: { password: "s3cret" } }));
        });

        await waitFor(() => {
            expect(mockUploadArtifactFile).toHaveBeenCalledTimes(1);
            expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: [...builderKeys.manifests(), "content", "s1", "jira.json"] });
        });
    });

    test("fetchConfigByFilename falls back to a direct fetch when the config is absent from the artifact list", async () => {
        mockConfigArtifacts = [];
        mockDirectFetch = { "spanish_translator.yaml": "type: agent\nname: SpanishTranslator\n" };
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });

        const yaml = await result.current.fetchConfigByFilename("spanish_translator.yaml");

        expect(yaml).toContain("SpanishTranslator");
    });

    test.each([
        ["a config in the artifact list", [{ filename: "a.yaml", mimeType: "application/vnd.sam-agent-config+yaml", accumulatedContent: "streaming" }]],
        ["a config the list leaves out", []],
    ])("fetchConfigByFilename reads exactly the version asked for, and never a download in flight: %s", async (_label, artifacts) => {
        mockConfigArtifacts = artifacts;
        mockDirectFetch = { "a.yaml": "name: pinned" };
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });

        const yaml = await result.current.fetchConfigByFilename("a.yaml", { skipAccumulated: true, version: 4 });

        expect(yaml).toBe("name: pinned");
        expect(directFetchCalls).toEqual([["a.yaml", 4]]);
    });

    test("fetchConfigByFilename throws an infrastructure error, not the latest version and not a config the agent should fix, when the version asked for cannot be read", async () => {
        mockConfigArtifacts = [{ filename: "a.yaml", mimeType: "application/vnd.sam-agent-config+yaml", accumulatedContent: "streaming" }];
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });

        const failure = await result.current.fetchConfigByFilename("a.yaml", { version: 4 }).catch((err: unknown) => err);

        expect(failure).toMatchObject({ status: 503 });
        expect(directFetchCalls).toEqual([["a.yaml", 4]]);
    });

    test("configCount survives the artifact list dropping the configs it counted", async () => {
        mockConfigArtifacts = [{ filename: "a.yaml", mimeType: "application/vnd.sam-agent-config+yaml" }];
        const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });
        expect(result.current.configCount).toBe(1);

        mockConfigArtifacts = [];
        act(() => rerender());

        expect(result.current.configCount).toBe(1);
    });

    test("configCount does not leak across a session switch", async () => {
        mockArtifactsBySession = {
            s1: [{ filename: "a.yaml", mimeType: "application/vnd.sam-agent-config+yaml" }],
            s2: [],
        };
        const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });
        expect(result.current.configCount).toBe(1);

        mockSessionId = "s2";
        act(() => rerender());

        expect(result.current.configCount).toBe(0);
    });

    test("configCount counts the new session's own configs", async () => {
        mockArtifactsBySession = {
            s1: [{ filename: "a.yaml", mimeType: "application/vnd.sam-agent-config+yaml" }],
            s2: [
                { filename: "b.yaml", mimeType: "application/vnd.sam-agent-config+yaml" },
                { filename: "c.yaml", mimeType: "application/vnd.sam-agent-config+yaml" },
            ],
        };
        const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });

        mockSessionId = "s2";
        act(() => rerender());

        expect(result.current.configCount).toBe(2);
    });

    test("derives rootName from manifest.display_name when available", async () => {
        mockManifest = { name: "raw-name", display_name: "Pretty Build", components: [] };
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        await waitFor(() => expect(result.current.rootName).toBe("Pretty Build"));
    });

    test("starts in fullscreen mode and auto-transitions to split when content appears", async () => {
        const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });
        expect(result.current.chatPanelMode).toBe("fullscreen");

        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }],
        };
        rerender();

        await waitFor(() => expect(result.current.chatPanelMode).toBe("split"));
    });

    test("setChatPanelMode updates the mode", () => {
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.setChatPanelMode("floating"));
        expect(result.current.chatPanelMode).toBe("floating");
    });

    test("handleNodeDoubleClick resolves wfp:<workflow>:<name> prefixes", () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "MyAgent" }],
        };
        mockComponentData = new Map([["MyAgent", dataFor("MyAgent", { yamlText: "apps: []" })]]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("wfp:Pipeline:MyAgent"));
        expect(result.current.selectedNodeId).toBe("MyAgent");
        expect(result.current.detailDialogOpen).toBe(true);
    });

    test("handleNodeDoubleClick maps wf:<workflow>:__start__ to the workflow itself", () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "wf.yaml", type: "workflow", name: "Pipeline" }],
        };
        mockComponentData = new Map([["Pipeline", dataFor("Pipeline", { type: "workflow", yamlText: "apps: []" })]]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("wf:Pipeline:__start__"));
        expect(result.current.selectedNodeId).toBe("Pipeline");
    });

    test("handleNodeDoubleClick resolves wf:<workflow>:<nodeId> via the manifest node list", () => {
        mockManifest = {
            name: "b",
            components: [
                {
                    filename: "wf.yaml",
                    type: "workflow",
                    name: "Pipeline",
                    nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }],
                },
                { filename: "worker.yaml", type: "agent", name: "Worker" },
            ],
        };
        mockComponentData = new Map([["Worker", dataFor("Worker", { yamlText: "apps: []" })]]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("wf:Pipeline:step1"));
        expect(result.current.selectedNodeId).toBe("Worker");
    });

    test("handleNodeDoubleClick reads the full per-node config from a FLAT-shape workflow YAML (app_config.nodes)", () => {
        mockManifest = {
            name: "b",
            components: [
                {
                    filename: "wf.yaml",
                    type: "workflow",
                    name: "Pipeline",
                    nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }],
                },
                { filename: "worker.yaml", type: "agent", name: "Worker" },
            ],
        };
        const workflowYaml = `apps:
  - app_config:
      nodes:
        - id: step1
          type: agent
          agent_name: Worker
          instruction: Full per-node instruction only present in the YAML.
`;
        mockComponentData = new Map([
            ["Pipeline", dataFor("Pipeline", { type: "workflow", yamlText: workflowYaml })],
            ["Worker", dataFor("Worker", { yamlText: "apps: []" })],
        ]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("wf:Pipeline:step1"));

        expect(result.current.selectedNodeId).toBe("Worker");
        expect(result.current.selectedWorkflowRole?.node?.instruction).toBe("Full per-node instruction only present in the YAML.");
    });

    test("handleNodeDoubleClick reads the full per-node config from a NESTED-shape workflow YAML (app_config.workflow.nodes)", () => {
        mockManifest = {
            name: "b",
            components: [
                {
                    filename: "wf.yaml",
                    type: "workflow",
                    name: "Pipeline",
                    nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }],
                },
                { filename: "worker.yaml", type: "agent", name: "Worker" },
            ],
        };
        const workflowYaml = `apps:
  - app_config:
      tools: []
      workflow:
        nodes:
          - id: step1
            type: agent
            agent_name: Worker
            instruction: Full per-node instruction only present in the nested YAML.
`;
        mockComponentData = new Map([
            ["Pipeline", dataFor("Pipeline", { type: "workflow", yamlText: workflowYaml })],
            ["Worker", dataFor("Worker", { yamlText: "apps: []" })],
        ]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("wf:Pipeline:step1"));

        expect(result.current.selectedNodeId).toBe("Worker");
        expect(result.current.selectedWorkflowRole?.node?.instruction).toBe("Full per-node instruction only present in the nested YAML.");
    });

    test("handleNodeDoubleClick ignores column header and bg nodes (__header_*, etc.)", () => {
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("__header_agent__"));
        expect(result.current.selectedNodeId).toBeNull();
        expect(result.current.detailDialogOpen).toBe(false);
    });

    test("handleNodeDoubleClick does nothing when a session draft has no yaml content", () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A" }],
        };
        mockComponentData = new Map([["A", dataFor("A", { yamlText: null })]]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("A"));
        expect(result.current.selectedNodeId).toBeNull();
    });

    test("handleNodeDoubleClick opens an existing (platform-origin) agent with no yaml content", () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "Existing", origin: "platform" }],
        };
        mockComponentData = new Map([["Existing", dataFor("Existing", { yamlText: null, origin: "platform" })]]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("Existing"));
        expect(result.current.selectedNodeId).toBe("Existing");
        expect(result.current.detailDialogOpen).toBe(true);
    });

    test("handleNodeDoubleClick does NOT open a platform non-agent with no yaml content", () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "c.yaml", type: "connector", name: "ExistingConnector", origin: "platform" }],
        };
        mockComponentData = new Map([["ExistingConnector", dataFor("ExistingConnector", { type: "connector", yamlText: null, origin: "platform" })]]);

        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.handleNodeDoubleClick("ExistingConnector"));
        expect(result.current.selectedNodeId).toBeNull();
        expect(result.current.detailDialogOpen).toBe(false);
    });

    test("isApplyingChanges defaults to false and is flipped by setIsApplyingChanges", () => {
        const { result } = renderHook(() => useBuilderPageState(), { wrapper });
        expect(result.current.isApplyingChanges).toBe(false);
        act(() => result.current.setIsApplyingChanges(true));
        expect(result.current.isApplyingChanges).toBe(true);
    });

    test("isApplyingChanges resets to false on session change", async () => {
        const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });
        act(() => result.current.setIsApplyingChanges(true));
        expect(result.current.isApplyingChanges).toBe(true);

        mockSessionId = "s2";
        rerender();

        await waitFor(() => expect(result.current.isApplyingChanges).toBe(false));
    });

    describe("buildInterrupted, the one verdict the canvas reads", () => {
        const stoppedMidBuild = () =>
            new Map([
                ["Done", dataFor("Done", { status: "validated" })],
                ["Todo", dataFor("Todo", { status: "draft" })],
            ]);
        const allDraft = () =>
            new Map([
                ["A", dataFor("A", { status: "draft" })],
                ["B", dataFor("B", { status: "draft" })],
            ]);
        const allDone = () =>
            new Map([
                ["A", dataFor("A", { status: "validated" })],
                ["B", dataFor("B", { status: "deployed" })],
            ]);

        test.each([
            { name: "a plan or a build stopped before anything validated, no host verdict", data: allDraft, host: false, expected: false },
            { name: "the host reports a build stopped before anything validated", data: allDraft, host: true, expected: true },
            { name: "a build stopped mid-way, no host verdict: component state alone", data: stoppedMidBuild, host: false, expected: true },
            { name: "a build stopped mid-way with the host verdict", data: stoppedMidBuild, host: true, expected: true },
            { name: "every component finished, no host verdict", data: allDone, host: false, expected: false },
            { name: "the host reports the verdict (cleared when every component finished is the host's own rule)", data: allDone, host: true, expected: true },
            { name: "no components yet", data: () => new Map<string, ComponentCanvasData>(), host: false, expected: false },
        ])("$name", ({ data, host, expected }) => {
            mockComponentData = data();
            const { result } = renderHook(() => useBuilderPageState(), { wrapper });
            act(() => result.current.setHostBuildInterrupted(host));
            expect(result.current.buildInterrupted).toBe(expected);
        });

        test("the host verdict clears when the panel withdraws it", () => {
            mockComponentData = allDraft();
            const { result } = renderHook(() => useBuilderPageState(), { wrapper });
            act(() => result.current.setHostBuildInterrupted(true));
            expect(result.current.buildInterrupted).toBe(true);
            act(() => result.current.setHostBuildInterrupted(false));
            expect(result.current.buildInterrupted).toBe(false);
        });

        test("the host verdict resets on session change, so one build's interruption never paints the next", async () => {
            mockComponentData = allDraft();
            const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });
            act(() => result.current.setHostBuildInterrupted(true));
            expect(result.current.buildInterrupted).toBe(true);

            mockSessionId = "s2";
            rerender();

            await waitFor(() => expect(result.current.buildInterrupted).toBe(false));
        });
    });

    describe("pendingAutoCreate (the approved plan's save + deploy)", () => {
        beforeEach(() => {
            sessionStorage.clear();
            window.location.hash = "";
        });

        async function reloadedPageState() {
            window.location.hash = "#/builder";
            const approved = sessionStorage.getItem("sam_builder_pending_autocreate");
            if (approved !== null) sessionStorage.setItem("sam_builder_active_session", approved);
            vi.resetModules();
            const { useBuilderPageState: reloaded } = await import("./useBuilderPageState");
            return reloaded;
        }

        test("is off by default and on once the plan is approved", () => {
            const { result } = renderHook(() => useBuilderPageState(), { wrapper });
            expect(result.current.pendingAutoCreate).toBe(false);

            act(() => result.current.setPendingAutoCreate(true));

            expect(result.current.pendingAutoCreate).toBe(true);
        });

        test("survives a reload of the page", async () => {
            const first = renderHook(() => useBuilderPageState(), { wrapper });
            act(() => first.result.current.setPendingAutoCreate(true));

            const reloadedHook = await reloadedPageState();
            const reloaded = renderHook(() => reloadedHook(), { wrapper });

            expect(reloaded.result.current.pendingAutoCreate).toBe(true);
        });

        test("is off in another session inside the page, and is not brought back by returning", () => {
            const { result, rerender } = renderHook(() => useBuilderPageState(), { wrapper });
            act(() => result.current.setPendingAutoCreate(true));

            mockSessionId = "new-session";
            rerender();
            expect(result.current.pendingAutoCreate).toBe(false);

            mockSessionId = "s1";
            rerender();
            expect(result.current.pendingAutoCreate).toBe(false);
        });

        test("completing the attempt clears it for good", async () => {
            const first = renderHook(() => useBuilderPageState(), { wrapper });
            act(() => first.result.current.setPendingAutoCreate(true));
            act(() => first.result.current.setPendingAutoCreate(false));

            const reloadedHook = await reloadedPageState();
            const reloaded = renderHook(() => reloadedHook(), { wrapper });

            expect(reloaded.result.current.pendingAutoCreate).toBe(false);
        });
    });
});
