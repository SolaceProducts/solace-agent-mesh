// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { CAPABILITIES, useUserContext } from "./contexts/UserContext";
import { UserProvider } from "./UserProvider";

const mockConfigValue: Partial<ConfigContextValue> = {
    persistenceEnabled: true,
};

function wrapper({ children }: { children: React.ReactNode }) {
    return (
        <ConfigContext.Provider value={mockConfigValue as ConfigContextValue}>
            <UserProvider>{children}</UserProvider>
        </ConfigContext.Provider>
    );
}

function mockUser(scopes: string[]) {
    return { username: "u", authenticated: true, authMethod: "oidc", scopes };
}

function CapabilitiesLoadedProbe() {
    const { capabilitiesLoaded } = useUserContext();
    return <div data-testid="capabilitiesLoaded">{String(capabilitiesLoaded)}</div>;
}

describe("UserProvider — capabilities derived from /api/v1/user scopes", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });
    afterEach(() => {
        apiGetSpy.mockRestore();
    });

    it("fetches /api/v1/user (not the capabilities echo)", async () => {
        apiGetSpy.mockResolvedValue(mockUser([]));
        renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(apiGetSpy).toHaveBeenCalledOnce());
        expect(apiGetSpy.mock.calls[0][0]).toBe("/api/v1/user");
    });

    it("derives a per-verb capability from its exact backend scope", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["agent_builder:_:create"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.createAgents)).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.deleteAgents)).toBe(false);
    });

    it("distinguishes model_config verbs (create vs update)", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["model_config:*:update"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.updateModelConfig)).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.createModelConfig)).toBe(false);
        expect(result.current.userHasCapability(CAPABILITIES.readModelConfig)).toBe(false);
    });

    it("grants assignBudgets from budget:*:assign only", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["budget:*:assign"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.assignBudgets)).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.updateBudgets)).toBe(false);
    });

    it("does not grant assignBudgets from budget:*:update", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["budget:*:update"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.updateBudgets)).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.assignBudgets)).toBe(false);
    });

    it("grants readModelConfig from model_config:*:read (UI gates Models on read)", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["model_config:*:read"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.readModelConfig)).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.createModelConfig)).toBe(false);
    });

    it("honours wildcard grants via the matcher", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["agent_builder:*:read"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.readAgents)).toBe(true));
    });

    it("grants readArtifacts unconditionally (backend has no artifact-read gate)", async () => {
        apiGetSpy.mockResolvedValue(mockUser([]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.readArtifacts)).toBe(true));
    });

    it("grants readFeedback from agent:*:read_feedback", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["agent:*:read_feedback"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.readFeedback)).toBe(true));
    });

    it("denies readFeedback without a read_feedback scope", async () => {
        apiGetSpy.mockResolvedValue(mockUser([]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.capabilitiesLoaded).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.readFeedback)).toBe(false);
    });

    it("grants readFeedback from a per-subject agent:<id>:read_feedback grant", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["agent:agent-123:read_feedback"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.userHasCapability(CAPABILITIES.readFeedback)).toBe(true));
    });

    it("fails closed when the user fetch fails in persistence mode", async () => {
        apiGetSpy.mockRejectedValue(new Error("network down"));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.capabilitiesLoaded).toBe(true));
        expect(result.current.userHasCapability(CAPABILITIES.readAgents)).toBe(false);
        expect(result.current.userHasCapability(CAPABILITIES.readArtifacts)).toBe(false);
    });

    it("aborts an in-flight scope fetch on unmount so a superseded run cannot overwrite it", async () => {
        let signal: AbortSignal | undefined;
        apiGetSpy.mockImplementation(((_endpoint: string, options?: { signal?: AbortSignal }) => {
            signal = options?.signal;
            return new Promise(() => {});
        }) as unknown as typeof api.webui.get);

        const { unmount } = render(<div />, { wrapper });

        await waitFor(() => expect(signal).toBeDefined());
        expect(signal?.aborted).toBe(false);

        unmount();
        expect(signal?.aborted).toBe(true);
    });

    it("tells the user their permissions failed to load rather than silently hiding everything", async () => {
        apiGetSpy.mockRejectedValue(new Error("network down"));
        render(<div />, { wrapper });

        expect(await screen.findByText("Couldn't load your permissions")).toBeInTheDocument();
        expect(screen.getByText(/refresh the browser/i)).toBeInTheDocument();
    });

    it("shows no error dialog when scopes load", async () => {
        apiGetSpy.mockResolvedValue(mockUser([]));
        render(<CapabilitiesLoadedProbe />, { wrapper });

        await waitFor(() => expect(screen.getByTestId("capabilitiesLoaded")).toHaveTextContent("true"));
        expect(screen.queryByText("Couldn't load your permissions")).not.toBeInTheDocument();
    });
});

describe("UserProvider — hasScope and isSuperAdmin", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });
    afterEach(() => {
        apiGetSpy.mockRestore();
    });

    it("hasScope reflects the fetched scope list with wildcard matching", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["rbac:_:read"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.hasScope("rbac:_:read")).toBe(true));
        expect(result.current.hasScope("rbac:_:create")).toBe(false);
    });

    it("exposes the gateway username the task rows are keyed by", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["*"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.username).toBe("u"));
    });

    it.each([["*:*:*"], ["*"]])("isSuperAdmin is true for the universal wildcard %s", async (scope: string) => {
        apiGetSpy.mockResolvedValue(mockUser([scope]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.isSuperAdmin).toBe(true));
    });

    it("isSuperAdmin is false for a plain rbac reader", async () => {
        apiGetSpy.mockResolvedValue(mockUser(["rbac:_:read"]));
        const { result } = renderHook(() => useUserContext(), { wrapper });
        await waitFor(() => expect(result.current.capabilitiesLoaded).toBe(true));
        expect(result.current.isSuperAdmin).toBe(false);
    });
});

describe("UserProvider — capabilities gate (no empty-state flash)", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });
    afterEach(() => {
        apiGetSpy.mockRestore();
    });

    it("withholds capability-gated UI until scopes resolve", async () => {
        let resolveUser!: (value: ReturnType<typeof mockUser>) => void;
        apiGetSpy.mockReturnValue(new Promise(resolve => (resolveUser = resolve)) as never);

        function Gate() {
            const { capabilitiesLoaded } = useUserContext();
            return <div>{capabilitiesLoaded ? "agents-page" : "app-loading"}</div>;
        }

        render(
            <ConfigContext.Provider value={mockConfigValue as ConfigContextValue}>
                <UserProvider>
                    <Gate />
                </UserProvider>
            </ConfigContext.Provider>
        );

        expect(screen.getByText("app-loading")).toBeTruthy();
        expect(screen.queryByText("agents-page")).toBeNull();

        resolveUser(mockUser([]));
        await waitFor(() => expect(screen.getByText("agents-page")).toBeTruthy());
    });
});

describe("UserProvider — non-persistence mode", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });
    afterEach(() => {
        apiGetSpy.mockRestore();
    });

    function nonPersistenceWrapper({ children }: { children: React.ReactNode }) {
        return (
            <ConfigContext.Provider value={{ persistenceEnabled: false } as ConfigContextValue}>
                <UserProvider>{children}</UserProvider>
            </ConfigContext.Provider>
        );
    }

    it("uses the fixed non-persistence capability set without fetching", async () => {
        const { result } = renderHook(() => useUserContext(), { wrapper: nonPersistenceWrapper });
        await waitFor(() => expect(result.current.capabilitiesLoaded).toBe(true));
        expect(apiGetSpy).not.toHaveBeenCalled();
        expect(result.current.userHasCapability(CAPABILITIES.readAgents)).toBe(true);
        expect(result.current.userHasCapability(CAPABILITIES.readArtifacts)).toBe(true);
        expect(result.current.userHasCapability(CAPABILITIES.createAgents)).toBe(false);
        expect(result.current.isSuperAdmin).toBe(false);
    });
});
