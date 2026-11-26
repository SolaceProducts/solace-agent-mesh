// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockWorkflows } from "@/testing/data/agentCardInfos";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { WorkflowList } from "./WorkflowList";
import meta, { Default } from "./WorkflowList.stories";

function LocationProbe() {
    const location = useLocation();
    return <div data-testid="location">{location.pathname}</div>;
}

const setMobile = (mobile: boolean) => {
    Object.defineProperty(window, "innerWidth", { value: mobile ? 375 : 1280, configurable: true });
    window.matchMedia = vi
        .fn()
        .mockImplementation((q: string) => ({ matches: mobile && q.includes("max-width"), media: q, onchange: null, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() }));
};

describe("WorkflowList", () => {
    describe("Search and Filtering", () => {
        test("filters workflows by search term", async () => {
            const DefaultStory = composeStory(Default, meta);
            render(<DefaultStory />);
            const user = userEvent.setup();

            const searchInput = await screen.findByPlaceholderText("Filter by name");
            await user.type(searchInput, "Complete");

            expect(await screen.findByText("Complete Order Workflow")).toBeInTheDocument();
            expect(screen.queryByText("SimpleLoopWorkflow")).not.toBeInTheDocument();
        });

        test("shows empty state for non-matching search", async () => {
            const DefaultStory = composeStory(Default, meta);
            render(<DefaultStory />);
            const user = userEvent.setup();

            const searchInput = await screen.findByPlaceholderText("Filter by name");
            await user.type(searchInput, "NONEXISTENT123");

            expect(await screen.findByText("No workflows found")).toBeInTheDocument();
            expect(screen.queryByRole("table", { name: "Workflows" })).not.toBeInTheDocument();
        });

        test("clears search filter and restores all workflows", async () => {
            const DefaultStory = composeStory(Default, meta);
            render(<DefaultStory />);
            const user = userEvent.setup();

            const table = await screen.findByRole("table", { name: "Workflows" });
            const initialRows = within(table).getAllByRole("row");
            const initialCount = initialRows.length - 1;

            const searchInput = await screen.findByPlaceholderText("Filter by name");
            await user.type(searchInput, "NONEXISTENT");

            expect(screen.queryByRole("table", { name: "Workflows" })).not.toBeInTheDocument();

            const clearButton = await screen.findByRole("button", { name: "Clear Filter" });
            await user.click(clearButton);

            const restoredTable = await screen.findByRole("table", { name: "Workflows" });
            const restoredRows = within(restoredTable).getAllByRole("row");
            expect(restoredRows.length - 1).toBe(initialCount);
        });

        test("search is case-insensitive", async () => {
            const DefaultStory = composeStory(Default, meta);
            render(<DefaultStory />);
            const user = userEvent.setup();

            const searchInput = await screen.findByPlaceholderText("Filter by name");
            await user.type(searchInput, "complete");

            expect(await screen.findByText("Complete Order Workflow")).toBeInTheDocument();
        });
    });

    describe("Opening a workflow", () => {
        const idMap = Object.fromEntries(mockWorkflows.map(w => [w.name, `id-${w.name}`]));
        const renderList = (props: { canDelete?: boolean; workflowIdByName?: Record<string, string>; initialPath?: string } = {}) =>
            render(
                <MemoryRouter initialEntries={[props.initialPath ?? "/workflows"]}>
                    <StoryProvider>
                        <WorkflowList workflows={mockWorkflows} canDelete={props.canDelete} workflowIdByName={props.workflowIdByName} />
                        <LocationProbe />
                    </StoryProvider>
                </MemoryRouter>
            );

        test("selecting a workflow's name opens its side panel and stays on the page", async () => {
            renderList({ workflowIdByName: idMap });

            await userEvent.click(screen.getByTestId("workflow-name-CompleteOrderWorkflow"));

            expect(await screen.findByText("Workflow Details")).toBeInTheDocument();
            expect(screen.getByTestId("location")).toHaveTextContent("/workflows");
        });

        test("?id= opens that workflow's side panel", async () => {
            renderList({ workflowIdByName: idMap, initialPath: "/workflows?id=id-CompleteOrderWorkflow" });

            const panel = await screen.findByRole("complementary", { name: "Workflow details panel" });
            expect(within(panel).getByText("Complete Order Workflow")).toBeInTheDocument();
        });

        test("every row's menu offers Open Workflow, which opens its diagram", async () => {
            renderList({ canDelete: false, workflowIdByName: idMap });
            expect(screen.getAllByRole("button", { name: /more/i })).toHaveLength(mockWorkflows.length);

            await userEvent.click(screen.getAllByRole("button", { name: /more/i })[0]);
            await userEvent.click(await screen.findByText("Open Workflow"));

            expect(screen.getByTestId("location")).toHaveTextContent(/^\/agent-mesh\/workflows\//);
        });

        test("Delete is offered only to a user who can delete, on a row with an id", async () => {
            renderList({ canDelete: false, workflowIdByName: idMap });
            await userEvent.click(screen.getAllByRole("button", { name: /more/i })[0]);
            expect(await screen.findByText("Open Workflow")).toBeInTheDocument();
            expect(screen.queryByText("Delete")).toBeNull();
        });
    });

    describe("Mobile layout", () => {
        beforeEach(() => setMobile(true));
        afterEach(() => vi.restoreAllMocks());

        test("renders workflows as stacked rows (not a table) on mobile", async () => {
            const DefaultStory = composeStory(Default, meta);
            render(<DefaultStory />);

            const row = await screen.findByRole("button", { name: /Complete Order Workflow/i });
            expect(row).toBeInTheDocument();
            expect(screen.queryByRole("table", { name: "Workflows" })).not.toBeInTheDocument();
            expect(within(row).getByText(/Running/)).toBeInTheDocument();
        });
    });
});
