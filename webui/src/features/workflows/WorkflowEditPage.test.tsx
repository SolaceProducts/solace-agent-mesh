// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { workflowsApi } from "@/api/workflows/versioned";

import type { Workflow } from "@/types/workflow";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { WorkflowEditPage } from "./WorkflowEditPage";

const mockNavigate = vi.fn();
vi.mock("react-router-dom", async importOriginal => {
    const actual = await importOriginal<typeof import("react-router-dom")>();
    return { ...actual, useNavigate: () => mockNavigate };
});

const mockEditInBuilder = vi.fn();
vi.mock("./useEditWorkflowInBuilder", () => ({ useEditWorkflowInBuilder: () => mockEditInBuilder }));

const workflow = {
    id: "wf-1",
    name: "My Workflow",
    displayName: "My Workflow",
    description: "Top level description here",
    appConfig: { nodes: [{ id: "n1", type: "agent" }], description: "Config description here", version: "1.0.0" },
    deploymentStatus: "not_deployed",
    runtimeStatus: undefined,
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
};

const nestedWorkflow = {
    id: "wf-2",
    name: "Nested Workflow",
    displayName: "Nested Workflow",
    description: "Top level description here",
    appConfig: { tools: [], toolsets: [], workflow: { nodes: [{ id: "n1", type: "agent" }], description: "Nested config description", version: "1.0.0" } },
    deploymentStatus: "not_deployed",
    runtimeStatus: undefined,
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
};

function mockList(workflows: Record<string, unknown>[] = [workflow]) {
    return vi.spyOn(workflowsApi, "getAll").mockResolvedValue(workflows as unknown as Workflow[]);
}

function renderPage(chatContextValues = {}, userContextValues = {}, workflowId = "wf-1") {
    const router = createMemoryRouter(
        [
            {
                path: "/workflows/:id/edit",
                element: (
                    <StoryProvider chatContextValues={chatContextValues} userContextValues={userContextValues}>
                        <WorkflowEditPage />
                    </StoryProvider>
                ),
            },
            { path: "/workflows", element: <div data-testid="workflows-landing" /> },
        ],
        { initialEntries: [`/workflows/${workflowId}/edit`] }
    );
    return render(<RouterProvider router={router} />);
}

describe("WorkflowEditPage", () => {
    beforeEach(() => {
        mockEditInBuilder.mockClear();
        mockNavigate.mockClear();
    });
    afterEach(() => vi.restoreAllMocks());

    test("hydrates name, description (from appConfig) and version", async () => {
        mockList();
        renderPage();

        const name = (await screen.findByTestId("workflow-name")) as HTMLInputElement;
        await waitFor(() => expect(name.value).toBe("My Workflow"));
        expect((screen.getByTestId("workflow-description") as HTMLTextAreaElement).value).toBe("Config description here");
        expect((screen.getByTestId("workflow-version") as HTMLInputElement).value).toBe("1.0.0");
    });

    test("hydrates description/version from a nested (config-apply-authored) appConfig", async () => {
        mockList([nestedWorkflow]);
        renderPage({}, {}, "wf-2");

        const name = (await screen.findByTestId("workflow-name")) as HTMLInputElement;
        await waitFor(() => expect(name.value).toBe("Nested Workflow"));
        expect((screen.getByTestId("workflow-description") as HTMLTextAreaElement).value).toBe("Nested config description");
        expect((screen.getByTestId("workflow-version") as HTMLInputElement).value).toBe("1.0.0");
    });

    test("blocks save when the name is too short", async () => {
        mockList();
        const putSpy = vi.spyOn(workflowsApi, "update");
        renderPage();
        const user = userEvent.setup();

        const name = (await screen.findByTestId("workflow-name")) as HTMLInputElement;
        await waitFor(() => expect(name.value).toBe("My Workflow"));
        await user.clear(name);
        await user.type(name, "ab");
        await user.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText("Name must be at least 3 characters")).toBeInTheDocument();
        expect(putSpy).not.toHaveBeenCalled();
    });

    test("saves via PUT with version merged into appConfig, then navigates", async () => {
        mockList();
        const putSpy = vi.spyOn(workflowsApi, "update");
        putSpy.mockResolvedValue(workflow as unknown as Workflow);
        const notify = vi.fn();
        renderPage({ addNotification: notify });
        const user = userEvent.setup();

        const version = (await screen.findByTestId("workflow-version")) as HTMLInputElement;
        await waitFor(() => expect(version.value).toBe("1.0.0"));
        await user.clear(version);
        await user.type(version, "2.0.0");
        await user.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(putSpy).toHaveBeenCalledTimes(1));
        const [id, body] = putSpy.mock.calls[0];
        expect(id).toBe("wf-1");
        expect(body).toEqual({
            name: "My Workflow",
            description: "Config description here",
            appConfig: { nodes: [{ id: "n1", type: "agent" }], description: "Config description here", version: "2.0.0" },
        });
        await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith("/workflows"));
        expect(notify).toHaveBeenCalledWith("Workflow updated successfully.", "success");
    });

    test("saves a nested (config-apply-authored) workflow into appConfig.workflow, where workflowBody() reads from, preserving sibling tools/toolsets", async () => {
        mockList([nestedWorkflow]);
        const putSpy = vi.spyOn(workflowsApi, "update");
        putSpy.mockResolvedValue(nestedWorkflow as unknown as Workflow);
        renderPage({}, {}, "wf-2");
        const user = userEvent.setup();

        const version = (await screen.findByTestId("workflow-version")) as HTMLInputElement;
        await waitFor(() => expect(version.value).toBe("1.0.0"));
        await user.clear(version);
        await user.type(version, "2.0.0");
        await user.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(putSpy).toHaveBeenCalledTimes(1));
        const [, body] = putSpy.mock.calls[0];
        expect(body).toEqual({
            name: "Nested Workflow",
            description: "Nested config description",
            appConfig: { tools: [], toolsets: [], workflow: { nodes: [{ id: "n1", type: "agent" }], description: "Nested config description", version: "2.0.0" } },
        });
    });

    test("surfaces an error dialog and does NOT navigate/notify when the save fails", async () => {
        mockList();
        const putSpy = vi.spyOn(workflowsApi, "update");
        putSpy.mockRejectedValue(new Error("boom"));
        mockNavigate.mockClear();
        const notify = vi.fn();
        renderPage({ addNotification: notify });
        const user = userEvent.setup();

        await waitFor(() => expect((screen.getByTestId("workflow-name") as HTMLInputElement).value).toBe("My Workflow"));
        await user.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(putSpy).toHaveBeenCalled());
        expect(await screen.findByText("Workflow Update Failed")).toBeInTheDocument();
        expect(screen.getByText("boom")).toBeInTheDocument();
        expect(mockNavigate).not.toHaveBeenCalledWith("/workflows");
        expect(notify).not.toHaveBeenCalled();
    });

    test("blocks save on a too-short description and on an over-long version", async () => {
        mockList();
        const putSpy = vi.spyOn(workflowsApi, "update");
        renderPage();
        const user = userEvent.setup();

        const desc = (await screen.findByTestId("workflow-description")) as HTMLTextAreaElement;
        await waitFor(() => expect(desc.value).toBe("Config description here"));
        await user.clear(desc);
        await user.type(desc, "short");
        await user.click(screen.getByRole("button", { name: "Save" }));
        expect(await screen.findByText("Description must be at least 10 characters")).toBeInTheDocument();
        expect(putSpy).not.toHaveBeenCalled();

        await user.clear(desc);
        await user.type(desc, "A perfectly valid description");
        const version = screen.getByTestId("workflow-version") as HTMLInputElement;
        await user.clear(version);
        await user.type(version, "a".repeat(65));
        await user.click(screen.getByRole("button", { name: "Save" }));
        expect(await screen.findByText("Version must be 64 characters or less")).toBeInTheDocument();
        expect(putSpy).not.toHaveBeenCalled();
    });

    test("redirects to /workflows without rendering the form when the user lacks createAgents", async () => {
        mockList();
        renderPage({}, { userHasCapability: () => false });
        expect(await screen.findByTestId("workflows-landing")).toBeInTheDocument();
        expect(screen.queryByTestId("workflow-name")).toBeNull();
    });

    test("Edit with AI prompts to discard unsaved changes, only seeding on confirm", async () => {
        mockList();
        renderPage();
        const user = userEvent.setup();
        const name = (await screen.findByTestId("workflow-name")) as HTMLInputElement;
        await waitFor(() => expect(name.value).toBe("My Workflow"));

        await user.type(name, " edited");
        await user.click(screen.getByRole("button", { name: /edit with ai/i }));

        expect(await screen.findByText("Unsaved Changes Will Be Discarded")).toBeInTheDocument();
        expect(mockEditInBuilder).not.toHaveBeenCalled();

        await user.click(screen.getByRole("button", { name: /leave & discard changes/i }));
        await waitFor(() => expect(mockEditInBuilder).toHaveBeenCalledTimes(1));
    });

    test("Edit with AI cancel keeps the user on the form without seeding", async () => {
        mockList();
        renderPage();
        const user = userEvent.setup();
        const name = (await screen.findByTestId("workflow-name")) as HTMLInputElement;
        await waitFor(() => expect(name.value).toBe("My Workflow"));

        await user.type(name, " edited");
        await user.click(screen.getByRole("button", { name: /edit with ai/i }));
        await user.click(await screen.findByRole("button", { name: /return to form/i }));

        expect(mockEditInBuilder).not.toHaveBeenCalled();
        expect(screen.getByTestId("workflow-name")).toBeInTheDocument();
    });

    test("Edit with AI seeds immediately when the form is pristine (no prompt)", async () => {
        mockList();
        renderPage();
        const user = userEvent.setup();
        await waitFor(() => expect((screen.getByTestId("workflow-name") as HTMLInputElement).value).toBe("My Workflow"));

        await user.click(screen.getByRole("button", { name: /edit with ai/i }));
        expect(screen.queryByText("Unsaved Changes Will Be Discarded")).toBeNull();
        await waitFor(() => expect(mockEditInBuilder).toHaveBeenCalledTimes(1));
    });
});
