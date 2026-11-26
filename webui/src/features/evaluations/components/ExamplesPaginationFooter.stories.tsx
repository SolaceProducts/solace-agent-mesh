// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { ExamplesPaginationFooter } from "@/features/evaluations/components/ExamplesPaginationFooter";

const meta = {
    title: "Evaluations/ExamplesPaginationFooter",
    component: ExamplesPaginationFooter,
    parameters: { layout: "padded" },
} satisfies Meta<typeof ExamplesPaginationFooter>;

export default meta;
type Story = StoryObj<typeof meta>;

export const StackedFitsTheSidePanel: Story = {
    args: { page: 1, pageSize: 5, totalPages: 3, totalCount: 12, onPageChange: () => {}, summariseSinglePage: true, layout: "stacked" },
    decorators: [Story => <div style={{ width: 308 }}>{Story()}</div>],
    play: async ({ canvasElement }) => {
        const strip = canvasElement.querySelector("nav");
        const summary = within(canvasElement).getByText(/Showing 1–5 of 12 results/);
        if (!strip) throw new Error("pagination strip did not render");

        const stripRect = strip.getBoundingClientRect();
        const summaryRect = summary.getBoundingClientRect();
        await expect(summaryRect.top).toBeGreaterThanOrEqual(stripRect.bottom - 1);
    },
};
