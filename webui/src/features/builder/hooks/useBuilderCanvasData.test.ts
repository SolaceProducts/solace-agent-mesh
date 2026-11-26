// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { SAM_A2A_AGENT_CONFIG_MIME_TYPE, SAM_AGENT_CONFIG_MIME_TYPE } from "../builderUtils";

import { deriveGeneratingFilenames, hasGenerationStarted } from "./useBuilderCanvasData";
import type { BuildManifest } from "./useBuilderManifest";

describe("useBuilderCanvasData", () => {
    let useBuilderCanvasData: typeof import("./useBuilderCanvasData").useBuilderCanvasData;

    type Artifact = {
        filename: string;
        mimeType: string;
        version?: number;
        accumulatedContent?: string;
    };

    let mockManifest: BuildManifest | null;
    let mockArtifacts: Artifact[];
    let mockRemoteAgents: unknown[];
    let mockMessages: unknown[];
    let mockIsResponding: boolean;
    let mockDownloadAndResolveArtifact: (filename: string) => Promise<unknown>;
    let mockFetchArtifactDirect: (sessionId: string, filename: string) => Promise<string | null>;
    let wrapper: (props: { children: ReactNode }) => ReturnType<typeof createElement>;

    beforeEach(async () => {
        vi.resetModules();
        mockManifest = null;
        mockArtifacts = [];
        mockRemoteAgents = [];
        mockMessages = [];
        mockIsResponding = false;
        mockDownloadAndResolveArtifact = () => Promise.resolve(null);
        mockFetchArtifactDirect = () => Promise.resolve(null);

        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);

        vi.doMock("./artifactUtils", async importOriginal => {
            const actual = await importOriginal<typeof import("./artifactUtils")>();
            return { ...actual };
        });

        vi.doMock("@/api/builder", async importOriginal => {
            const actual = await importOriginal<typeof import("@/api/builder")>();
            return { ...actual, getManifestContent: (_sessionId: string, filename: string) => mockFetchArtifactDirect("s1", filename) };
        });

        vi.doMock("./useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: mockManifest, isLoading: false }),
            SAM_BUILD_MANIFEST_MIME_TYPE: "application/vnd.sam-build-manifest+yaml",
        }));

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                downloadAndResolveArtifact: (filename: string) => mockDownloadAndResolveArtifact(filename),
                sessionId: "s1",
                isResponding: mockIsResponding,
                messages: mockMessages,
            }),
        }));

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: mockArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("@/api/remote-agents", () => ({
            useRemoteAgents: () => ({ data: mockRemoteAgents, isLoading: false }),
        }));

        const mod = await import("./useBuilderCanvasData");
        useBuilderCanvasData = mod.useBuilderCanvasData;
    });

    const agentYaml = `apps:
  - name: A
    app_config:
      agent_name: A
      instruction: ${"x".repeat(120)}
      description: An agent that does a useful task.
`;

    test("returns an empty component map and null manifest when there is no manifest", () => {
        mockManifest = null;

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        expect(result.current.manifest).toBeNull();
        expect(result.current.componentData.size).toBe(0);
        expect(result.current.isGeneratingConfigs).toBe(false);
    });

    test("builds a component entry for each manifest component", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "a.yaml", type: "agent", name: "A" },
                { filename: "b.yaml", type: "agent", name: "B" },
            ],
        };
        mockArtifacts = [
            {
                filename: "a.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: agentYaml,
            },
        ];

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.size).toBe(2));

        const a = result.current.componentData.get("A");
        const b = result.current.componentData.get("B");
        expect(a?.yamlText).toBe(agentYaml);
        expect(a?.type).toBe("agent");
        expect(b?.yamlText).toBeNull();
    });

    test("resumed session: recovers yamlText via direct fetch when the config artifact is absent from the session list", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }],
        };
        mockArtifacts = [];
        mockFetchArtifactDirect = (_sessionId, filename) => Promise.resolve(filename === "a.yaml" ? agentYaml : null);

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.get("A")?.yamlText).toBe(agentYaml));
    });

    test("resumed session: card reports isLoading while the reactive config fetch is in flight", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }],
        };
        mockArtifacts = [];
        mockFetchArtifactDirect = () => new Promise<string | null>(() => {});

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.get("A")?.isLoading).toBe(true));
        expect(result.current.componentData.get("A")?.yamlText).toBeNull();
    });

    test("resumed session: recovers a generated component whose manifest status is absent", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A" }],
        };
        mockArtifacts = [];
        mockFetchArtifactDirect = (_sessionId, filename) => Promise.resolve(filename === "a.yaml" ? agentYaml : null);

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")?.yamlText).toBe(agentYaml));
    });

    test("resumed session: a component with no retrievable config is fetched once, not in a loop", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }],
        };
        mockArtifacts = [];
        let calls = 0;
        mockFetchArtifactDirect = () => {
            calls++;
            return Promise.resolve(null);
        };

        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")).toBeTruthy());
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(result.current.componentData.get("A")?.yamlText).toBeNull();
        expect(calls).toBe(1);

        mockArtifacts = [];
        rerender();
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(calls).toBe(1);
    });

    test("does not direct-fetch a platform-origin component (it has no per-session config)", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "deployed", origin: "platform" }],
        };
        mockArtifacts = [];
        let calls = 0;
        mockFetchArtifactDirect = () => {
            calls++;
            return Promise.resolve(null);
        };

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")).toBeTruthy());
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(calls).toBe(0);
    });

    test("does not direct-fetch a draft component even when a fetch would succeed", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }],
        };
        mockArtifacts = [];
        let calls = 0;
        mockFetchArtifactDirect = () => {
            calls++;
            return Promise.resolve(agentYaml);
        };

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")).toBeTruthy());
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(calls).toBe(0);
        expect(result.current.componentData.get("A")?.yamlText).toBeNull();
    });

    test("marks actively-streaming components as isGenerating", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }],
        };
        mockArtifacts = [
            {
                filename: "a.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: "apps: []",
            },
        ];
        mockIsResponding = true;

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.isGeneratingConfigs).toBe(true));

        const generating = [...result.current.generatingFilenames];
        expect(generating).toContain("a.yaml");
        expect(result.current.generatingFilename).toBe("a.yaml");
    });

    test("marks queued components when the agent is responding and another is generating", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
            ],
        };
        mockArtifacts = [
            {
                filename: "a.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: "apps: []",
            },
        ];
        mockIsResponding = true;

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.size).toBe(2));
        const b = result.current.componentData.get("B");
        expect(b?.isQueued).toBe(true);
        expect(b?.isGenerating).toBe(false);
    });

    test("detects pending HIL input and marks the matching component as waiting", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "c.json", type: "connector", name: "JiraConn", status: "draft" }],
        };
        mockArtifacts = [];
        mockMessages = [
            {
                userInputRequest: {
                    responded: false,
                    timedOut: false,
                    surface: { dataModel: { component_name: "JiraConn" } },
                },
            },
        ];

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.hasPendingUserInput).toBe(true));

        const conn = result.current.componentData.get("JiraConn");
        expect(conn?.isWaitingForInput).toBe(true);
        expect(conn?.isGenerating).toBe(false);
        expect(conn?.isQueued).toBe(false);
    });

    test("downloadAndResolveArtifact rejection: component still appears with null yamlText", async () => {
        mockManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "Orphan" }],
        };
        mockArtifacts = [{ filename: "a.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 0 }];
        mockDownloadAndResolveArtifact = () => Promise.reject(new Error("session cache miss for __working artifact"));

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.size).toBe(1));
        const orphan = result.current.componentData.get("Orphan");
        expect(orphan).toBeDefined();
        expect(orphan?.yamlText).toBeNull();
        expect(orphan?.isGenerating).toBe(false);
    });

    test("rejection on one component does not prevent other components from rendering", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "good.yaml", type: "agent", name: "Good", origin: "session" },
                { filename: "bad.yaml", type: "agent", name: "Bad", origin: "session" },
            ],
        };
        mockArtifacts = [
            {
                filename: "good.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: agentYaml,
            },
            { filename: "bad.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 0 },
        ];
        mockDownloadAndResolveArtifact = (filename: string) => Promise.reject(new Error(`download failed for ${filename}`));

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.size).toBe(2));
        await waitFor(() => expect(result.current.componentData.get("Good")?.yamlText).toBe(agentYaml));
        expect(result.current.componentData.get("Bad")?.yamlText).toBeNull();
    });

    test("does not treat platform-origin components as queued", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "p.yaml", type: "agent", name: "Platform", origin: "platform" },
                { filename: "a.yaml", type: "agent", name: "Session", status: "draft" },
            ],
        };
        mockArtifacts = [
            {
                filename: "a.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: "apps: []",
            },
        ];
        mockIsResponding = true;

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => expect(result.current.componentData.size).toBe(2));

        const platform = result.current.componentData.get("Platform");
        expect(platform?.isQueued).toBe(false);
    });

    test("throttles streaming writes while the agent is responding", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            mockManifest = {
                name: "b",
                components: [{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }],
            };
            mockArtifacts = [
                {
                    filename: "a.yaml",
                    mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                    version: 0,
                    accumulatedContent: "apps: [partial]",
                },
            ];
            mockIsResponding = true;

            const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper });

            await waitFor(() => expect(result.current.componentData.size).toBe(1));
            expect(result.current.componentData.get("A")?.yamlText).toBeNull();

            await vi.advanceTimersByTimeAsync(300);
            rerender();

            await waitFor(() => {
                expect(result.current.componentData.get("A")?.yamlText).toBe("apps: [partial]");
            });
        } finally {
            vi.useRealTimers();
        }
    });

    test("populates dependencyProgress when an agent's connector is generating", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "draft", depends_on: ["Conn"] },
                { filename: "c.yaml", type: "connector", name: "Conn", status: "draft" },
            ],
        };
        mockArtifacts = [
            {
                filename: "c.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: "name: Conn\n",
            },
        ];
        mockIsResponding = true;

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });

        await waitFor(() => {
            expect(result.current.componentData.get("A")?.isBuildingDeps).toBe(true);
        });

        const a = result.current.componentData.get("A");
        expect(a?.dependencyProgress).toHaveLength(1);
        expect(a?.dependencyProgress[0]).toMatchObject({ name: "Conn", status: "generating" });
    });

    test("entries for unchanged components keep stable references across streaming updates", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
            ],
        };
        mockArtifacts = [
            {
                filename: "a.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: agentYaml,
            },
        ];

        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.size).toBe(2));

        const bBefore = result.current.componentData.get("B");

        mockArtifacts = [
            {
                filename: "a.yaml",
                mimeType: SAM_AGENT_CONFIG_MIME_TYPE,
                version: 0,
                accumulatedContent: agentYaml + "      # appended\n",
            },
        ];
        rerender();
        await waitFor(() => {
            const a = result.current.componentData.get("A");
            expect(a?.yamlText).toBe(agentYaml + "      # appended\n");
        });

        const bAfter = result.current.componentData.get("B");
        expect(bAfter).toBe(bBefore);
    });

    test("surfaces agentAuthConfig.type from a session a2a config artifact (a2a MIME is recognized)", async () => {
        const a2aConfig = JSON.stringify({ name: "Mult", agentUrl: "https://m/", agentCardLocation: "none", agentAuthConfig: { type: "bearer", token: "<<__SAM_REQUIRED__>>" } });
        mockManifest = {
            name: "b",
            components: [{ filename: "mult.json", type: "a2a_agent", name: "Mult", status: "validated" }],
        };
        mockArtifacts = [{ filename: "mult.json", mimeType: SAM_A2A_AGENT_CONFIG_MIME_TYPE, version: 0, accumulatedContent: a2aConfig }];

        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("Mult")?.a2aAgentMetadata?.agentAuthConfig?.type).toBe("bearer"));
    });

    test("keeps per-component auth for multiple deployed a2a agents across re-renders", async () => {
        mockManifest = {
            name: "b",
            components: [
                { filename: "s.yaml", type: "agent", name: "Streamer", status: "draft" },
                { filename: "", type: "a2a_agent", name: "AgentA", status: "deployed", origin: "platform" },
                { filename: "", type: "a2a_agent", name: "AgentB", status: "deployed", origin: "platform" },
            ],
        };
        mockArtifacts = [{ filename: "s.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 0, accumulatedContent: agentYaml }];
        mockRemoteAgents = [
            { name: "AgentA", agentUrl: "https://a/", agentCardLocation: "none", agentAuthType: "bearer" },
            { name: "AgentB", agentUrl: "https://b/", agentCardLocation: "none", agentAuthType: "apikey" },
        ];

        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.size).toBe(3));
        expect(result.current.componentData.get("AgentA")?.a2aAgentMetadata?.agentAuthConfig?.type).toBe("bearer");
        expect(result.current.componentData.get("AgentB")?.a2aAgentMetadata?.agentAuthConfig?.type).toBe("apikey");

        mockArtifacts = [{ filename: "s.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 0, accumulatedContent: agentYaml + "      # appended\n" }];
        rerender();
        await waitFor(() => expect(result.current.componentData.get("Streamer")?.yamlText).toBe(agentYaml + "      # appended\n"));
        expect(result.current.componentData.get("AgentA")?.a2aAgentMetadata?.agentAuthConfig?.type).toBe("bearer");
        expect(result.current.componentData.get("AgentB")?.a2aAgentMetadata?.agentAuthConfig?.type).toBe("apikey");
    });

    test("validated session component is finalizing while responding and the manifest isn't stamped", async () => {
        mockManifest = { name: "b", full_validation_passed: false, components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" }] };
        mockArtifacts = [];
        mockIsResponding = true;
        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")?.isBuildFinalizing).toBe(true));
    });

    test("draft session component with streamed config content is finalizing end-to-end (the backward-flip fix)", async () => {
        mockManifest = { name: "b", full_validation_passed: false, components: [{ filename: "a.yaml", type: "agent", name: "A", status: "draft", origin: "session" }] };
        mockArtifacts = [{ filename: "a.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 0, accumulatedContent: agentYaml }];
        mockIsResponding = true;
        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")?.yamlText).toBe(agentYaml));
        expect(result.current.componentData.get("A")?.status).toBe("draft");
        expect(result.current.componentData.get("A")?.isBuildFinalizing).toBe(true);
    });

    test("a component edited after the validation stamp is marked changed since validation; its sibling is not", async () => {
        mockManifest = {
            name: "b",
            full_validation_passed: true,
            full_validation_versions: { "a.yaml": 1, "b.yaml": 1 },
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" },
                { filename: "b.yaml", type: "agent", name: "B", status: "validated", origin: "session" },
            ],
        };
        mockArtifacts = [
            { filename: "a.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 2, accumulatedContent: agentYaml },
            { filename: "b.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, version: 1, accumulatedContent: agentYaml },
        ];
        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")?.changedSinceValidation).toBe(true));
        expect(result.current.componentData.get("B")?.changedSinceValidation).toBe(false);
    });

    test("NOT finalizing once the manifest is stamped full_validation_passed, even mid-turn", async () => {
        mockManifest = { name: "b", full_validation_passed: true, components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" }] };
        mockArtifacts = [];
        mockIsResponding = true;
        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")).toBeTruthy());
        expect(result.current.componentData.get("A")?.isBuildFinalizing).toBe(false);
    });

    test("NOT finalizing once the turn ends (isResponding false), even without the stamp", async () => {
        mockManifest = { name: "b", full_validation_passed: false, components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" }] };
        mockArtifacts = [];
        mockIsResponding = false;
        const { result } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.get("A")).toBeTruthy());
        expect(result.current.componentData.get("A")?.isBuildFinalizing).toBe(false);
    });

    test("every entry carries the chat context's responding state, and a flip reaches already-cached entries", async () => {
        mockManifest = {
            name: "b",
            full_validation_passed: true,
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" },
                { filename: "b.yaml", type: "agent", name: "B", status: "error", origin: "session" },
            ],
        };
        mockArtifacts = [];
        mockIsResponding = true;
        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper });
        await waitFor(() => expect(result.current.componentData.size).toBe(2));
        expect([...result.current.componentData.values()].map(e => e.isBuilderResponding)).toEqual([true, true]);

        mockIsResponding = false;
        rerender();
        await waitFor(() => expect([...result.current.componentData.values()].map(e => e.isBuilderResponding)).toEqual([false, false]));

        mockIsResponding = true;
        rerender();
        await waitFor(() => expect([...result.current.componentData.values()].map(e => e.isBuilderResponding)).toEqual([true, true]));
    });
});

describe("hasGenerationStarted", () => {
    const SID = "sess-1";
    beforeEach(() => {
        try {
            sessionStorage.clear();
        } catch {}
    });

    test("true once the plan is approved (session flag set)", () => {
        sessionStorage.setItem(`sam_builder_approved_${SID}`, "1");
        const m: BuildManifest = { name: "b", components: [{ filename: "a.yaml", type: "agent", name: "A", status: "draft", origin: "session" }] };
        expect(hasGenerationStarted(m, SID)).toBe(true);
    });

    test("false while reviewing the plan (no approval, all session components draft)", () => {
        const m: BuildManifest = {
            name: "b",
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "draft", origin: "session" },
                { filename: "p.yaml", type: "connector", name: "P", status: "deployed", origin: "platform" },
            ],
        };
        expect(hasGenerationStarted(m, SID)).toBe(false);
    });

    test("true for a restored session where a session component is already past draft", () => {
        const m: BuildManifest = { name: "b", components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" }] };
        expect(hasGenerationStarted(m, SID)).toBe(true);
    });
});

describe("deriveGeneratingFilenames", () => {
    const draftManifest: BuildManifest = {
        name: "b",
        components: [
            { filename: "a.yaml", type: "agent", name: "A", status: "draft", origin: "session" },
            { filename: "b.yaml", type: "agent", name: "B", status: "draft", origin: "session" },
        ],
    };

    test("review phase: nothing generating (drafts read as Draft) when generation has not started", () => {
        const result = deriveGeneratingFilenames([], draftManifest, true, false);
        expect(result.size).toBe(0);
    });

    test("generation phase: marks the first draft as generating (sub_task fallback) once started", () => {
        const result = deriveGeneratingFilenames([], draftManifest, true, true);
        expect([...result]).toEqual(["a.yaml"]);
    });

    test("an actively-streaming artifact (agent responding) is generating regardless of approval phase", () => {
        const artifacts = [{ filename: "a.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, accumulatedContent: "apps:\n", size: 0, lastModified: "" }];
        const result = deriveGeneratingFilenames(artifacts, draftManifest, true, false);
        expect(result.has("a.yaml")).toBe(true);
    });

    test("idle plan review: a lingering artifact does NOT read as Creating once the agent stops responding (Draft)", () => {
        const artifacts = [{ filename: "a.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, accumulatedContent: "apps:\n", size: 0, lastModified: "" }];
        expect(deriveGeneratingFilenames(artifacts, draftManifest, false, false).size).toBe(0);
        expect(deriveGeneratingFilenames(artifacts, draftManifest, false, true).size).toBe(0);
    });

    test("re-edit: a validated component mid-rewrite (agent responding) still shows Creating", () => {
        const validatedManifest: BuildManifest = {
            name: "b",
            components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated", origin: "session" }],
        };
        const artifacts = [{ filename: "a.yaml", mimeType: SAM_AGENT_CONFIG_MIME_TYPE, accumulatedContent: "apps: [rewrite]\n", size: 0, lastModified: "" }];
        expect(deriveGeneratingFilenames(artifacts, validatedManifest, true, true).has("a.yaml")).toBe(true);
    });

    test("nothing generating when the agent is idle", () => {
        expect(deriveGeneratingFilenames([], draftManifest, false, true).size).toBe(0);
    });
});
