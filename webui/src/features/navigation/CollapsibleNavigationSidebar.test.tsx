// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test } from "vitest";

import meta, { Collapsed, Expanded, HiddenItems, WithSubmenus } from "./CollapsibleNavigationSidebar.stories";

const getToggleButton = () => {
    const chevron = document.querySelector(".lucide-chevron-left, .lucide-chevron-right");
    return chevron?.closest("button") as HTMLButtonElement;
};

beforeEach(() => {
    sessionStorage.clear();
});

describe("CollapsibleNavigationSidebar", () => {
    test("collapses when the toggle button is clicked", async () => {
        const user = userEvent.setup();
        const Story = composeStory(Expanded, meta);
        render(<Story />);

        const sidebar = document.querySelector(".navigation-sidebar")!;
        expect(sidebar).toHaveClass("w-64");

        const toggleButton = getToggleButton();
        expect(toggleButton).toBeInTheDocument();
        await user.click(toggleButton);

        expect(sidebar).toHaveClass("w-20");
    });

    test("expands when the toggle button is clicked", async () => {
        const user = userEvent.setup();
        const Story = composeStory(Collapsed, meta);
        render(<Story isCollapsed={undefined} defaultCollapsed={true} />);

        const sidebar = document.querySelector(".navigation-sidebar")!;
        expect(sidebar).toHaveClass("w-20");

        const toggleButton = getToggleButton();
        expect(toggleButton).toBeInTheDocument();
        await user.click(toggleButton);

        expect(sidebar).toHaveClass("w-64");
    });

    test("nav items with routes render as links", async () => {
        const Story = composeStory(Expanded, meta);
        render(<Story />);

        const projectsLink = screen.getByRole("link", { name: /projects/i });
        expect(projectsLink).toHaveAttribute("href", "/projects");

        const agentsLink = screen.getByRole("link", { name: /agents/i });
        expect(agentsLink).toHaveAttribute("href", "/agent-mesh");
    });

    test("bottom items without routes render as buttons", async () => {
        const Story = composeStory(Expanded, meta);
        render(<Story />);

        const userAccountButton = screen.getByRole("button", { name: /user account/i });
        expect(userAccountButton).toBeInTheDocument();
        expect(userAccountButton.tagName).toBe("BUTTON");
    });

    test("child items with tooltip show tooltip on hover", async () => {
        const user = userEvent.setup();
        const Story = composeStory(WithSubmenus, meta);
        render(<Story />);

        const promptsLink = screen.getByRole("link", { name: /prompts/i });
        expect(promptsLink).toBeInTheDocument();

        await user.hover(promptsLink);

        await waitFor(
            () => {
                const tooltipElements = screen.getAllByText("Experimental Feature");
                expect(tooltipElements.length).toBeGreaterThan(0);
            },
            { timeout: 2000 }
        );

        expect(screen.getByRole("tooltip")).toHaveTextContent("Experimental Feature");
    });

    test("child items with tooltip do not render badge in nav", async () => {
        const Story = composeStory(WithSubmenus, meta);
        render(<Story />);

        expect(screen.getByText("Prompts")).toBeInTheDocument();
        expect(screen.getByText("Artifacts")).toBeInTheDocument();

        expect(screen.queryByText("EXPERIMENTAL")).not.toBeInTheDocument();
    });

    test("items marked hidden:true are filtered out of the rendered sidebar", () => {
        const Story = composeStory(HiddenItems, meta);
        render(<Story />);

        expect(screen.getByText("Projects")).toBeInTheDocument();
        expect(screen.getByText("User Account")).toBeInTheDocument();
        expect(screen.queryByText("Agents")).not.toBeInTheDocument();
        expect(screen.queryByText("Log Out")).not.toBeInTheDocument();
        expect(screen.getByText("Prompts")).toBeInTheDocument();
        expect(screen.queryByText("Artifacts")).not.toBeInTheDocument();
    });

    test("collapsed top-level nav items show their label tooltip on hover", async () => {
        const user = userEvent.setup();
        const Story = composeStory(Collapsed, meta);
        render(<Story />);

        const projectsLink = screen.getByRole("link", { name: /projects/i });
        await user.hover(projectsLink);

        await waitFor(
            () => {
                expect(screen.getAllByText("Projects").length).toBeGreaterThan(1);
            },
            { timeout: 2000 }
        );
        expect(screen.getByRole("tooltip")).toHaveTextContent("Projects");
    });
});
