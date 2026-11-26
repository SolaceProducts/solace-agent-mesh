// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

function buildQueryWrapper() {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("builderSessionName (pure)", () => {
    let builderSessionName: typeof import("./useBuilderSessions").builderSessionName;

    beforeEach(async () => {
        vi.resetModules();
        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({ sessionId: null, handleSwitchSession: () => {}, handleNewSession: () => {} }),
        }));
        vi.doMock("@/api/client", () => ({
            api: { webui: { get: () => Promise.resolve({ data: [], meta: { pagination: { nextPage: null } } }), delete: () => Promise.resolve() } },
        }));
        vi.doMock("@/utils/file", () => ({
            getArtifactContent: () => Promise.resolve({ content: null }),
        }));
        const mod = await import("./useBuilderSessions");
        builderSessionName = mod.builderSessionName;
    });

    test("prefers manifest display_name when set", () => {
        const session = { id: "1", name: "New Chat" } as unknown as Parameters<typeof builderSessionName>[0];
        expect(builderSessionName(session, { name: "foo", display_name: "Pretty Name", components: [] })).toBe("Pretty Name");
    });

    test("falls back to manifest name when display_name is absent", () => {
        const session = { id: "1", name: "New Chat" } as unknown as Parameters<typeof builderSessionName>[0];
        expect(builderSessionName(session, { name: "foo-build", components: [] })).toBe("foo-build");
    });

    test("returns the session name when there is no manifest", () => {
        const session = { id: "1", name: "My Session" } as unknown as Parameters<typeof builderSessionName>[0];
        expect(builderSessionName(session, null)).toBe("My Session");
    });

    test("returns 'New Build' for the default 'New Chat' session name when manifest is missing", () => {
        const session = { id: "1", name: "New Chat" } as unknown as Parameters<typeof builderSessionName>[0];
        expect(builderSessionName(session, null)).toBe("New Build");
    });

    test("returns 'New Build' when both session.name and manifest are missing", () => {
        const session = { id: "1", name: "" } as unknown as Parameters<typeof builderSessionName>[0];
        expect(builderSessionName(session, undefined)).toBe("New Build");
    });
});

describe("useBuilderSessions (hook)", () => {
    let useBuilderSessions: typeof import("./useBuilderSessions").useBuilderSessions;
    let apiGet: ((...args: unknown[]) => unknown) & ReturnType<typeof vi.fn>;
    let apiDelete: ((...args: unknown[]) => unknown) & ReturnType<typeof vi.fn>;
    let handleNewSession: ReturnType<typeof vi.fn>;

    beforeEach(async () => {
        vi.resetModules();
        apiGet = vi.fn() as unknown as typeof apiGet;
        apiDelete = vi.fn() as unknown as typeof apiDelete;
        handleNewSession = vi.fn();

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                sessionId: "current-sess",
                handleSwitchSession: vi.fn(),
                handleNewSession,
            }),
        }));
        vi.doMock("@/api/client", () => ({
            api: {
                webui: {
                    get: (...args: unknown[]) => apiGet(...args),
                    delete: (...args: unknown[]) => apiDelete(...args),
                },
            },
        }));
        vi.doMock("@/utils/file", () => ({
            getArtifactContent: () => Promise.resolve({ content: null }),
        }));

        const mod = await import("./useBuilderSessions");
        useBuilderSessions = mod.useBuilderSessions;
    });

    test("does not fetch sessions when active is false", () => {
        apiGet.mockResolvedValue({ data: [], meta: { pagination: { nextPage: null } } });
        renderHook(() => useBuilderSessions({ active: false }), { wrapper: buildQueryWrapper() });
        expect(apiGet).not.toHaveBeenCalled();
    });

    const routeApiGet = (sessions: { id: string; agentId?: string; name?: string }[]) => {
        apiGet.mockImplementation((url: string) => {
            if (url.startsWith("/api/v1/sessions")) {
                const isFirstPage = url.includes("pageNumber=1");
                return Promise.resolve({
                    data: isFirstPage ? sessions : [],
                    meta: { pagination: { nextPage: null } },
                });
            }
            if (url.startsWith("/api/v1/artifacts/")) {
                return Promise.resolve([]);
            }
            return Promise.resolve(null);
        });
    };

    test("fetches sessions and filters to the Builder agent when active", async () => {
        routeApiGet([
            { id: "s1", agentId: "Builder", name: "b1" },
            { id: "s2", agentId: "SomeOtherAgent", name: "other" },
            { id: "s3", agentId: "Builder", name: "b2" },
        ]);

        const { result } = renderHook(() => useBuilderSessions({ active: true }), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(result.current.isLoading).toBe(false));

        expect(result.current.sessions.map(s => s.session.id)).toEqual(["s1", "s3"]);
    });

    test("serverFilter=true includes agent filter in the request and skips client filtering", async () => {
        routeApiGet([
            { id: "s1", agentId: "anything", name: "a" },
            { id: "s2", agentId: "anything-else", name: "b" },
        ]);

        const { result } = renderHook(() => useBuilderSessions({ active: true, serverFilter: true }), { wrapper: buildQueryWrapper() });

        await waitFor(() => expect(result.current.isLoading).toBe(false));

        const sessionsCalls = apiGet.mock.calls.map(c => c[0] as string).filter(url => url.startsWith("/api/v1/sessions"));
        expect(sessionsCalls.some(url => url.includes("includeAgentIds=Builder"))).toBe(true);
        expect(result.current.sessions).toHaveLength(2);
    });

    test("handleDeleteSession removes the session and starts a new one when deleting the current", async () => {
        routeApiGet([{ id: "current-sess", agentId: "Builder", name: "a" }]);
        apiDelete.mockResolvedValue(undefined);

        const { result } = renderHook(() => useBuilderSessions({ active: true }), { wrapper: buildQueryWrapper() });
        await waitFor(() => expect(result.current.sessions).toHaveLength(1));

        await result.current.handleDeleteSession(result.current.sessions[0]);

        expect(apiDelete).toHaveBeenCalledWith("/api/v1/sessions/current-sess");
        expect(handleNewSession).toHaveBeenCalled();
    });
});
