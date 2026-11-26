// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import type { AgentCardInfoFE } from "@/types/agent";

const navigate = vi.fn();
let canEdit = true;
let capturedOnEdit: ((workflow: AgentCardInfoFE) => void) | undefined;

vi.mock("react-router-dom", () => ({
    useNavigate: () => navigate,
}));

vi.mock("@/providers/contexts", () => ({
    useUserContext: () => ({
        userHasCapability: () => canEdit,
        CAPABILITIES: { createAgents: "createAgents" },
    }),
}));

vi.mock("@/api/workflows", () => ({ workflowsApi: { v1: { getAll: () => Promise.resolve([]) } } }));
vi.mock("@/api/agents", () => ({ agentsApi: { v1: { getAll: () => Promise.resolve([]) } } }));

vi.mock("@/features/workflowVisualization/WorkflowVisualizationPage", () => ({
    WorkflowVisualizationPage: ({ onEdit }: { onEdit?: (w: AgentCardInfoFE) => void }) => {
        capturedOnEdit = onEdit;
        return (
            <button data-testid="edit" disabled={!onEdit} onClick={() => onEdit?.({ name: "wf-1", displayName: "My Workflow" } as AgentCardInfoFE)}>
                edit
            </button>
        );
    },
}));

import { WorkflowVisualizationPageWrapper } from "./WorkflowVisualizationPageWrapper";

beforeEach(() => {
    navigate.mockClear();
    canEdit = true;
    capturedOnEdit = undefined;
    sessionStorage.clear();
});

afterEach(() => vi.clearAllMocks());

describe("WorkflowVisualizationPageWrapper", () => {
    test("stashes edit context and navigates to /builder when the user can create agents", async () => {
        render(<WorkflowVisualizationPageWrapper />, { wrapper: OpenFeatureTestProvider });

        await userEvent.click(screen.getByTestId("edit"));

        await waitFor(() => expect(navigate).toHaveBeenCalledWith("/builder"));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID)).toBeTruthy();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toContain("wf-1");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT)).toBe("true");
        const chip = JSON.parse(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP) ?? "null");
        expect(chip).toMatchObject({ label: "My Workflow", kind: "workflow" });
    });

    test("does not expose an edit affordance without the create-agents capability", () => {
        canEdit = false;
        render(<WorkflowVisualizationPageWrapper />, { wrapper: OpenFeatureTestProvider });

        expect(capturedOnEdit).toBeUndefined();
        expect(screen.getByTestId("edit")).toBeDisabled();
    });
});
