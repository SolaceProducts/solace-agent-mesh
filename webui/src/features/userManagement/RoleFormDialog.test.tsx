// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useAgents } from "@/api/agents";
import { useWorkflowsQuery } from "@/api/workflows";

import type { RBACRole } from "@/types/rbac";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { RoleFormDialog } from "./RoleFormDialog";

const updateRole = vi.fn().mockResolvedValue(undefined);
const createRole = vi.fn().mockResolvedValue(undefined);

type AgentsQueryResult = ReturnType<typeof useAgents>;
type WorkflowsQueryResult = ReturnType<typeof useWorkflowsQuery>;
const asAgentsResult = (partial: Pick<AgentsQueryResult, "data" | "isLoading">) => partial as unknown as AgentsQueryResult;
const asWorkflowsResult = (partial: Pick<WorkflowsQueryResult, "data" | "isLoading">) => partial as unknown as WorkflowsQueryResult;

const defaultAgentsResult = asAgentsResult({ data: [{ id: "hr", name: "HR Assistant", description: "" }], isLoading: false });
const defaultWorkflowsResult = asWorkflowsResult({ data: [], isLoading: false });

vi.mock("@/api/rbac", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/rbac")>()),
    useCreateRoleMutation: () => ({ mutateAsync: createRole, isPending: false }),
    useUpdateRoleMutation: () => ({ mutateAsync: updateRole, isPending: false }),
}));
vi.mock("@/api/agents", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/agents")>()),
    useAgents: vi.fn(() => defaultAgentsResult),
}));
vi.mock("@/api/workflows", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/workflows")>()),
    useWorkflowsQuery: vi.fn(() => defaultWorkflowsResult),
}));

const role: RBACRole = {
    id: "r1",
    name: "Feedback Only",
    description: "",
    origin: "custom",
    scopes: ["agent:hr:read_feedback", "agent:*:read_feedback"],
    inherits: [],
    createdBy: "",
    updatedBy: "",
    createdAt: "",
    updatedAt: "",
};

describe("RoleFormDialog — Can use is always granted", () => {
    beforeEach(() => {
        updateRole.mockClear();
    });

    afterEach(() => {
        vi.mocked(useAgents).mockReturnValue(defaultAgentsResult);
        vi.mocked(useWorkflowsQuery).mockReturnValue(defaultWorkflowsResult);
    });

    test("with the flag on, saving adds invoke for every displayed card, only the wildcard where an All card already covers it", async () => {
        const user = userEvent.setup();
        renderWithProviders(<RoleFormDialog open onOpenChange={() => {}} role={role} />, { featureFlags: { feedback_management: true } });

        await user.click(await screen.findByRole("button", { name: "Save" }));

        expect(updateRole).toHaveBeenCalledTimes(1);
        const scopes = updateRole.mock.calls[0][0].changes.scopes as string[];
        expect(scopes).toEqual(expect.arrayContaining(["agent:hr:read_feedback", "agent:*:read_feedback", "agent:*:invoke"]));
        expect(scopes).not.toContain("agent:hr:invoke");
    });

    test("with the flag off, saving leaves the scopes exactly as loaded", async () => {
        const user = userEvent.setup();
        renderWithProviders(<RoleFormDialog open onOpenChange={() => {}} role={role} />);

        await user.click(await screen.findByRole("button", { name: "Save" }));

        expect(updateRole.mock.calls[0][0].changes.scopes).toEqual(["agent:hr:read_feedback", "agent:*:read_feedback"]);
    });

    test("with the flag on, opening a role that holds only a feedback grant shows the auto-added invoke scope on the Scopes tab before saving", async () => {
        const user = userEvent.setup();
        const feedbackOnlyRole: RBACRole = { ...role, scopes: ["agent:hr:read_feedback"] };
        vi.mocked(useAgents).mockReturnValue(asAgentsResult({ data: [], isLoading: false }));
        vi.mocked(useWorkflowsQuery).mockReturnValue(asWorkflowsResult({ data: [], isLoading: false }));
        renderWithProviders(<RoleFormDialog open onOpenChange={() => {}} role={feedbackOnlyRole} />, { featureFlags: { feedback_management: true } });

        await user.click(await screen.findByRole("tab", { name: "Scopes" }));

        expect(await screen.findByText("agent:hr:invoke")).toBeInTheDocument();
    });
});
