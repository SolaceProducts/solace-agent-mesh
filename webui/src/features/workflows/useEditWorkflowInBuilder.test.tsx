// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { agentsApi } from "@/api/agents";
import { workflowsApi } from "@/api/workflows";

import type { AgentCardInfo } from "@/types";

import { useEditWorkflowInBuilder } from "./useEditWorkflowInBuilder";

describe("useEditWorkflowInBuilder", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        sessionStorage.clear();
    });

    test("reads workflows and agents through the API objects", async () => {
        const workflows = vi.spyOn(workflowsApi, "getAll").mockResolvedValue([]);
        const agents = vi.spyOn(agentsApi, "getAll").mockResolvedValue([]);
        const wrapper = ({ children }: { children: ReactNode }) => <MemoryRouter>{children}</MemoryRouter>;

        const { result } = renderHook(() => useEditWorkflowInBuilder(), { wrapper });
        await act(() => result.current({ name: "orders" } as AgentCardInfo));

        expect(workflows).toHaveBeenCalled();
        expect(agents).toHaveBeenCalled();
    });
});
