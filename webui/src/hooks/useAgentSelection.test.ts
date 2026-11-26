// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

describe("useAgentSelection", () => {
    let useAgentSelection: typeof import("@/hooks/useAgentSelection").useAgentSelection;
    let mockHandleNewSession: ReturnType<typeof vi.fn>;
    let mockSetSelectedAgentName: ReturnType<typeof vi.fn>;
    let mockNavigate: ReturnType<typeof vi.fn>;
    let mockMessages: { isUser: boolean }[];
    let mockSelectedAgentName: string;
    let mockPersistenceEnabled: boolean;
    let mockAgents: { name: string; isInternal?: boolean; isWorkflow?: boolean }[];
    let mockAgentSessionRoutes: Record<string, string>;
    let mockUserInfo: Record<string, unknown> | null;

    beforeEach(async () => {
        vi.resetModules();
        localStorage.clear();

        mockHandleNewSession = vi.fn();
        mockSetSelectedAgentName = vi.fn();
        mockNavigate = vi.fn();
        mockMessages = [];
        mockSelectedAgentName = "agent-a";
        mockPersistenceEnabled = false;
        mockAgents = [{ name: "agent-a" }, { name: "agent-b" }];
        mockAgentSessionRoutes = { Builder: "/builder" };
        mockUserInfo = { username: "alice" };

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                agents: mockAgents,
                messages: mockMessages,
                selectedAgentName: mockSelectedAgentName,
                setSelectedAgentName: mockSetSelectedAgentName,
                handleNewSession: mockHandleNewSession,
                navigate: mockNavigate,
                agentSessionRoutes: mockAgentSessionRoutes,
            }),
        }));

        vi.doMock("@/providers/contexts/ConfigContext", () => ({
            useConfigContext: () => ({
                persistenceEnabled: mockPersistenceEnabled,
            }),
        }));

        vi.doMock("@/providers/contexts/AuthContext", () => ({
            useAuthContext: () => ({ userInfo: mockUserInfo }),
        }));

        const mod = await import("@/hooks/useAgentSelection");
        useAgentSelection = mod.useAgentSelection;
    });

    test("switches directly when no conversation exists", () => {
        mockMessages = [];

        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-b");
        });

        expect(mockHandleNewSession).toHaveBeenCalled();
        expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-b");
        expect(result.current.switchConfirmOpen).toBe(false);
    });

    test("shows confirmation dialog when user has typed and persistence is off", () => {
        mockMessages = [{ isUser: true }];
        mockPersistenceEnabled = false;

        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-b");
        });

        expect(mockHandleNewSession).not.toHaveBeenCalled();
        expect(result.current.switchConfirmOpen).toBe(true);
    });

    test("switches directly when persistence is enabled even with conversation", () => {
        mockMessages = [{ isUser: true }];
        mockPersistenceEnabled = true;

        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-b");
        });

        expect(mockHandleNewSession).toHaveBeenCalled();
        expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-b");
        expect(result.current.switchConfirmOpen).toBe(false);
    });

    test("executeSwitch runs after confirm", () => {
        mockMessages = [{ isUser: true }];
        mockPersistenceEnabled = false;

        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-b");
        });

        expect(result.current.switchConfirmOpen).toBe(true);

        act(() => {
            result.current.confirmAgentSwitch();
        });

        expect(mockHandleNewSession).toHaveBeenCalled();
        expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-b");
        expect(result.current.switchConfirmOpen).toBe(false);
    });

    test("state resets after cancel", () => {
        mockMessages = [{ isUser: true }];
        mockPersistenceEnabled = false;

        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-b");
        });

        expect(result.current.switchConfirmOpen).toBe(true);

        act(() => {
            result.current.cancelAgentSwitch();
        });

        expect(mockHandleNewSession).not.toHaveBeenCalled();
        expect(result.current.switchConfirmOpen).toBe(false);
    });

    test("preserves project context when switching agents", () => {
        mockMessages = [];

        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-b");
        });

        expect(mockHandleNewSession).toHaveBeenCalledWith(true);
    });

    test("no-ops when selecting the same agent", () => {
        const { result } = renderHook(() => useAgentSelection());

        act(() => {
            result.current.handleAgentSelection("agent-a");
        });

        expect(mockHandleNewSession).not.toHaveBeenCalled();
        expect(result.current.switchConfirmOpen).toBe(false);
    });

    describe("last used agent persistence", () => {
        const storedFor = (user: string) => localStorage.getItem(`sam-last-used-agent:${user}`);

        test("records a deliberate pick under the acting user's key", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-b");
            });

            expect(storedFor("alice")).toBe("agent-b");
        });

        test("records only after the switch is confirmed, not when the dialog opens", () => {
            mockMessages = [{ isUser: true }];
            mockPersistenceEnabled = false;

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-b");
            });
            expect(storedFor("alice")).toBeNull();

            act(() => {
                result.current.confirmAgentSwitch();
            });
            expect(storedFor("alice")).toBe("agent-b");
        });

        test("records nothing when the switch is cancelled", () => {
            mockMessages = [{ isUser: true }];

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-b");
                result.current.cancelAgentSwitch();
            });

            expect(storedFor("alice")).toBeNull();
        });

        test("does not record an internal agent", () => {
            mockAgents = [{ name: "agent-a" }, { name: "agent-internal", isInternal: true }];

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-internal");
            });

            expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-internal");
            expect(localStorage.length).toBe(0);
        });

        test("does not record a workflow agent", () => {
            mockAgents = [{ name: "agent-a" }, { name: "agent-flow", isWorkflow: true }];

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-flow");
            });

            expect(localStorage.length).toBe(0);
        });

        test("records nothing when re-selecting the already-active agent", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-a");
            });

            expect(localStorage.length).toBe(0);
        });

        test("records nothing for an agent that is not in the mesh", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("ghost-agent");
            });

            expect(localStorage.length).toBe(0);
        });

        test("scopes the write to the acting user", () => {
            mockUserInfo = { username: "bob" };

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-b");
            });

            expect(storedFor("bob")).toBe("agent-b");
            expect(storedFor("alice")).toBeNull();
        });

        test("records under :anonymous when authorization is disabled", () => {
            mockUserInfo = null;

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.handleAgentSelection("agent-b");
            });

            expect(storedFor("anonymous")).toBe("agent-b");
        });

        test("retargetAgent keeps the conversation and still records the pick when the session's agent has left the mesh", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.retargetAgent("agent-b");
            });

            expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-b");
            expect(mockHandleNewSession).not.toHaveBeenCalled();
            expect(storedFor("alice")).toBe("agent-b");
        });

        test("retargetAgent still retargets an agent this context cannot see, but records nothing", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.retargetAgent("ghost-agent");
            });

            expect(mockSetSelectedAgentName).toHaveBeenCalledWith("ghost-agent");
            expect(localStorage.length).toBe(0);
        });

        test("retargetAgent does not record an internal agent", () => {
            mockAgents = [{ name: "agent-a" }, { name: "agent-internal", isInternal: true }];

            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.retargetAgent("agent-internal");
            });

            expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-internal");
            expect(localStorage.length).toBe(0);
        });

        test("chatWithAgent records the pick for an ordinary agent", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.chatWithAgent("agent-b");
            });

            expect(storedFor("alice")).toBe("agent-b");
        });

        test("chatWithAgent records nothing for an agent with a dedicated route", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.chatWithAgent("Builder");
            });

            expect(localStorage.length).toBe(0);
        });
    });

    describe("chatWithAgent", () => {
        test("routes an agent with a dedicated route straight to that route without selecting it in chat", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.chatWithAgent("Builder");
            });

            expect(mockNavigate).toHaveBeenCalledWith("/builder");
            expect(mockNavigate).toHaveBeenCalledTimes(1);
            expect(mockNavigate).not.toHaveBeenCalledWith("/chat");
            expect(mockHandleNewSession).not.toHaveBeenCalled();
            expect(mockSetSelectedAgentName).not.toHaveBeenCalled();
        });

        test("selects an ordinary agent and navigates to /chat", () => {
            const { result } = renderHook(() => useAgentSelection());

            act(() => {
                result.current.chatWithAgent("agent-b");
            });

            expect(mockSetSelectedAgentName).toHaveBeenCalledWith("agent-b");
            expect(mockHandleNewSession).toHaveBeenCalledWith(true);
            expect(mockNavigate).toHaveBeenCalledWith("/chat");
        });
    });
});
