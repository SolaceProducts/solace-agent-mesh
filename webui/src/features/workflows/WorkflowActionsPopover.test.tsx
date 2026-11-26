// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { MorePopoverButton } from "@/common";
import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { WorkflowActionsPopover } from "./WorkflowActionsPopover";

const mockNavigate = vi.fn();
vi.mock("react-router-dom", async importOriginal => {
    const actual = await importOriginal<typeof import("react-router-dom")>();
    return { ...actual, useNavigate: () => mockNavigate };
});

function renderPopover(props: { canDelete: boolean; canEdit?: boolean; canViewAccess?: boolean; workflowId?: string | undefined; addNotification?: () => void }) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={props.addNotification ? { addNotification: props.addNotification } : {}}>
                <WorkflowActionsPopover workflowId={"workflowId" in props ? props.workflowId : "wf-1"} workflowName="My Workflow" canDelete={props.canDelete} canEdit={props.canEdit} canViewAccess={props.canViewAccess}>
                    {MorePopoverButton}
                </WorkflowActionsPopover>
            </StoryProvider>
        </MemoryRouter>
    );
}

async function confirmDelete(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: /more/i }));
    await user.click(await screen.findByText("Delete"));
    await user.type(await screen.findByRole("textbox"), "DELETE");
    await user.click(screen.getByRole("button", { name: "Delete" }));
}

describe("WorkflowActionsPopover", () => {
    afterEach(() => vi.clearAllMocks());

    test("renders nothing when the user can neither edit nor delete", () => {
        const { container } = renderPopover({ canDelete: false });
        expect(container).toBeEmptyDOMElement();
    });

    test("renders nothing when the workflow id is missing", () => {
        const { container } = renderPopover({ canDelete: true, canEdit: true, workflowId: undefined });
        expect(container).toBeEmptyDOMElement();
    });

    test("opens the Delete menu, then the confirmation dialog", async () => {
        renderPopover({ canDelete: true });
        const user = userEvent.setup();

        await user.click(screen.getByRole("button", { name: /more/i }));
        await user.click(await screen.findByText("Delete"));

        expect(await screen.findByText("Confirm Workflow Deletion")).toBeInTheDocument();
        expect(screen.getByText("My Workflow")).toBeInTheDocument();
    });

    describe("edit action", () => {
        test("is hidden when the user cannot edit", async () => {
            renderPopover({ canDelete: true, canEdit: false });
            await userEvent.setup().click(screen.getByRole("button", { name: /more/i }));
            expect(screen.queryByText("Edit")).not.toBeInTheDocument();
        });

        test("navigates to the edit page when clicked", async () => {
            renderPopover({ canDelete: false, canEdit: true });
            const user = userEvent.setup();

            await user.click(screen.getByRole("button", { name: /more/i }));
            await user.click(await screen.findByText("Edit"));

            expect(mockNavigate).toHaveBeenCalledWith("/workflows/wf-1/edit");
        });
    });

    describe("view access action", () => {
        test("is hidden when canViewAccess is false", async () => {
            renderPopover({ canDelete: false, canEdit: true });
            await userEvent.setup().click(screen.getByRole("button", { name: /more/i }));
            expect(screen.queryByText("View Access")).not.toBeInTheDocument();
        });

        test("shows View Access and opens the access dialog", async () => {
            const getSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.platform, "get");
            getSpy.mockResolvedValue(new Response(JSON.stringify({ data: [], meta: getPaginatedMeta(0) }), { headers: { "Content-Type": "application/json" } }));
            renderPopover({ canDelete: false, canEdit: true, canViewAccess: true });
            const user = userEvent.setup();

            await user.click(screen.getByRole("button", { name: /more/i }));
            await user.click(await screen.findByText("View Access"));

            expect(await screen.findByText("User Access")).toBeInTheDocument();
            await waitFor(() => expect(getSpy).toHaveBeenCalledWith(expect.stringContaining("/api/v2/platform/workflows/wf-1/access"), expect.anything()));
        });
    });

    describe("confirm flow", () => {
        afterEach(() => vi.restoreAllMocks());

        test("deletes by id and toasts success", async () => {
            const deleteSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.platform, "delete");
            deleteSpy.mockResolvedValue(new Response(null, { status: 204 }));
            const notify = vi.fn();
            renderPopover({ canDelete: true, addNotification: notify });
            await confirmDelete(userEvent.setup());

            await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("/api/v2/platform/workflows/wf-1", expect.anything()));
            await waitFor(() => expect(notify).toHaveBeenCalledWith("Workflow deleted successfully."));
        });

        test("surfaces a persistent error dialog when the delete fails", async () => {
            const deleteSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.platform, "delete");
            deleteSpy.mockRejectedValue(new Error("boom"));
            renderPopover({ canDelete: true });
            await confirmDelete(userEvent.setup());

            expect(await screen.findByText("Workflow Deletion Failed")).toBeInTheDocument();
            expect(screen.getByText("boom")).toBeInTheDocument();
        });
    });
});
