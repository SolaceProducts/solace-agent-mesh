// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { Agent } from "@/types";

import { useConnectorRuntimeToolNames } from "./useConnectorRuntimeToolNames";

const useAgentsMock = vi.fn();
vi.mock("@/api/agents", () => ({
    useAgents: () => useAgentsMock(),
}));

const agent = (toolRegistrations: Agent["toolRegistrations"]): Agent => ({ toolRegistrations }) as unknown as Agent;

describe("useConnectorRuntimeToolNames", () => {
    test("maps each connector id to its running agent's ready tool names", () => {
        useAgentsMock.mockReturnValue({
            data: [
                agent([{ toolType: "openapi", connectorId: "conn-api", name: "expense", status: "ready", toolCount: 2, toolNames: ["whoami", "list_my_expenses"] }]),
                agent([{ toolType: "mcp", connectorId: "conn-mcp", name: "atlassian", status: "ready", toolCount: 1, toolNames: ["getJiraIssue"] }]),
            ],
        });

        const { result } = renderHook(() => useConnectorRuntimeToolNames());

        expect(result.current.get("conn-api")).toEqual(["whoami", "list_my_expenses"]);
        expect(result.current.get("conn-mcp")).toEqual(["getJiraIssue"]);
    });

    test("ignores failed registrations and entries without names", () => {
        useAgentsMock.mockReturnValue({
            data: [
                agent([
                    { toolType: "openapi", connectorId: "conn-failed", name: "x", status: "failed", toolCount: 0, toolNames: undefined },
                    { toolType: "openapi", connectorId: "conn-empty", name: "y", status: "ready", toolCount: 0, toolNames: [] },
                ]),
            ],
        });

        const { result } = renderHook(() => useConnectorRuntimeToolNames());

        expect(result.current.has("conn-failed")).toBe(false);
        expect(result.current.has("conn-empty")).toBe(false);
    });

    test("returns an empty map when no agents are loaded", () => {
        useAgentsMock.mockReturnValue({ data: undefined });
        const { result } = renderHook(() => useConnectorRuntimeToolNames());
        expect(result.current.size).toBe(0);
    });
});
