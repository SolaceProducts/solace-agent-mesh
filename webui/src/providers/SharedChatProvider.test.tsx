// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { artifactKeys } from "@/api/artifacts";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import type { ArtifactInfo, RAGSearchResult } from "@/types";

const mockGetSharedArtifactContent = vi.fn();

describe("SharedChatProvider", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let SharedChatProvider: React.ComponentType<any>;
    let useChatContext: () => ChatContextValue;
    let MockProjectProvider: typeof import("@/testing/providers/MockProjectProvider").MockProjectProvider;
    let queryClient: QueryClient;

    beforeEach(async () => {
        vi.resetModules();
        mockGetSharedArtifactContent.mockReset();
        queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

        vi.doMock("@/api/share", () => ({
            getSharedArtifactContent: mockGetSharedArtifactContent,
            getSharedSessionView: vi.fn(),
            createShareLink: vi.fn(),
            updateShareLink: vi.fn(),
            deleteShareLink: vi.fn(),
            downloadSharedArtifact: vi.fn(),
            forkSharedChat: vi.fn(),
            useSharedSessionView: vi.fn().mockReturnValue({ data: null, isLoading: false, error: null }),
            useForkSharedChat: vi.fn().mockReturnValue({ mutateAsync: vi.fn(), isPending: false }),
            shareKeys: {},
        }));

        const providerMod = await import("@/providers/SharedChatProvider");
        SharedChatProvider = providerMod.SharedChatProvider;

        const contextMod = await import("@/providers/contexts");
        useChatContext = contextMod.useChatContext;

        const mockProjectMod = await import("@/testing/providers/MockProjectProvider");
        MockProjectProvider = mockProjectMod.MockProjectProvider;
    });

    function makeArtifact(overrides: Partial<ArtifactInfo> = {}): ArtifactInfo {
        return {
            filename: "test.txt",
            mimeType: "text/plain",
            size: 100,
            lastModified: "2025-01-01T00:00:00Z",
            ...overrides,
        };
    }

    function renderWithProvider(props: { artifacts?: ArtifactInfo[]; ragData?: RAGSearchResult[]; sessionId?: string; shareId?: string }) {
        let capturedContext: ChatContextValue | null = null;

        function ContextReader() {
            const ctx = useChatContext();
            capturedContext = ctx;
            return React.createElement("div", { "data-testid": "child" }, "Context loaded");
        }

        const sessionId = props.sessionId || "session-1";
        const result = render(
            React.createElement(
                QueryClientProvider,
                { client: queryClient },
                React.createElement(MockProjectProvider, {
                    mockValues: {},
                    children: React.createElement(
                        SharedChatProvider,
                        {
                            artifacts: props.artifacts || [],
                            ragData: props.ragData,
                            sessionId,
                            shareId: props.shareId || "share-1",
                        },
                        React.createElement(ContextReader)
                    ),
                })
            )
        );

        const getArtifacts = (): ArtifactInfo[] => queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId)) ?? [];
        return { ...result, getContext: () => capturedContext!, getArtifacts };
    }

    test("renders children and provides context", () => {
        renderWithProvider({});
        expect(screen.getByTestId("child")).toBeInTheDocument();
    });

    test("provides artifacts via the session artifact cache", () => {
        const artifacts = [makeArtifact({ filename: "a.txt" }), makeArtifact({ filename: "b.txt" })];
        const { getArtifacts } = renderWithProvider({ artifacts });

        const cached = getArtifacts();
        expect(cached).toHaveLength(2);
        expect(cached[0].filename).toBe("a.txt");
        expect(cached[1].filename).toBe("b.txt");
    });

    test("marks artifacts with needsEmbedResolution", () => {
        const artifacts = [makeArtifact({ filename: "a.txt" })];
        const { getArtifacts } = renderWithProvider({ artifacts });
        expect(getArtifacts()[0].needsEmbedResolution).toBe(true);
    });

    test("isCollaborativeSession is true", () => {
        const { getContext } = renderWithProvider({});
        expect(getContext().isCollaborativeSession).toBe(true);
    });

    test("isSharedSession is true (read-only flag distinct from isCollaborativeSession)", () => {
        const { getContext } = renderWithProvider({});
        expect(getContext().isSharedSession).toBe(true);
    });

    test("ragData is passed through correctly", () => {
        const ragData: RAGSearchResult[] = [{ query: "test", searchType: "file_search", timestamp: new Date().toISOString(), sources: [] }];
        const { getContext } = renderWithProvider({ ragData });
        expect(getContext().ragData).toEqual(ragData);
    });

    test("ragEnabled is true when ragData has items", () => {
        const ragData: RAGSearchResult[] = [{ query: "test", searchType: "file_search", timestamp: new Date().toISOString(), sources: [] }];
        const { getContext } = renderWithProvider({ ragData });
        expect(getContext().ragEnabled).toBe(true);
    });

    test("ragEnabled is false when ragData is empty", () => {
        const { getContext } = renderWithProvider({ ragData: [] });
        expect(getContext().ragEnabled).toBe(false);
    });

    test("downloadAndResolveArtifact fetches content for shared artifacts", async () => {
        mockGetSharedArtifactContent.mockResolvedValue({ content: "file contents", mimeType: "text/plain" });

        const artifacts = [makeArtifact({ filename: "test.txt", size: 42 })];
        const { getContext } = renderWithProvider({ artifacts, shareId: "share-abc" });

        let result: unknown;
        await act(async () => {
            result = await getContext().downloadAndResolveArtifact("test.txt");
        });

        expect(mockGetSharedArtifactContent).toHaveBeenCalledWith("share-abc", "test.txt");
        expect(result).toEqual(
            expect.objectContaining({
                name: "test.txt",
                mimeType: "text/plain",
                content: "file contents",
                size: 42,
            })
        );
    });

    test("downloadAndResolveArtifact returns null for unknown artifacts", async () => {
        const artifacts = [makeArtifact({ filename: "test.txt" })];
        const { getContext } = renderWithProvider({ artifacts });

        let result: unknown;
        await act(async () => {
            result = await getContext().downloadAndResolveArtifact("nonexistent.txt");
        });

        expect(result).toBeNull();
    });

    test("downloadAndResolveArtifact prevents duplicate downloads", async () => {
        mockGetSharedArtifactContent.mockResolvedValue({ content: "data", mimeType: "text/plain" });

        const artifacts = [makeArtifact({ filename: "test.txt" })];
        const { getContext } = renderWithProvider({ artifacts, shareId: "share-abc" });

        await act(async () => {
            await getContext().downloadAndResolveArtifact("test.txt");
        });

        let secondResult: unknown;
        await act(async () => {
            secondResult = await getContext().downloadAndResolveArtifact("test.txt");
        });

        expect(mockGetSharedArtifactContent).toHaveBeenCalledTimes(1);
        expect(secondResult).toBeNull();
    });

    test("sessionId is passed through", () => {
        const { getContext } = renderWithProvider({ sessionId: "my-session" });
        expect(getContext().sessionId).toBe("my-session");
    });

    test("messages is empty array", () => {
        const { getContext } = renderWithProvider({});
        expect(getContext().messages).toEqual([]);
    });

    test("isResponding is false", () => {
        const { getContext } = renderWithProvider({});
        expect(getContext().isResponding).toBe(false);
    });
});
