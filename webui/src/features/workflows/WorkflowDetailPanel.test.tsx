// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import meta, { Default } from "./WorkflowDetailPanel.stories";

describe("WorkflowDetailPanel", () => {
    describe("Close Button", () => {
        test("calls onClose when close button is clicked", async () => {
            const mockOnClose = vi.fn();
            const Story = composeStory(
                {
                    ...Default,
                    args: {
                        ...Default.args,
                        onClose: mockOnClose,
                    },
                },
                meta
            );
            render(<Story />);
            const user = userEvent.setup();

            const panel = await screen.findByRole("complementary", { name: "Workflow details panel" });
            const closeButton = within(panel).getByRole("button", { name: "Close" });

            await user.click(closeButton);

            expect(mockOnClose).toHaveBeenCalledTimes(1);
        });
    });
});
