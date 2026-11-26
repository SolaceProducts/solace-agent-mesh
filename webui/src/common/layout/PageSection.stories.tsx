// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { PageLabel } from "./PageLabel";
import { PageSection } from "./PageSection";

const meta = {
    title: "Common/Layout/PageSection",
    component: PageSection,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Vertical-stack container for grouping related items inside a page.",
            },
        },
    },
} satisfies Meta<typeof PageSection>;

export default meta;

export const Grouping = {
    render: () => (
        <>
            <PageSection>
                <PageLabel>Section Title</PageLabel>
                <div className="text-sm">Item 1</div>
                <div className="text-sm">Item 2</div>
                <div className="text-sm">Item 3</div>
            </PageSection>
        </>
    ),
    play: async () => {
        await expect(screen.getByText("Section Title")).toBeInTheDocument();
        await expect(screen.getByText("Item 1")).toBeInTheDocument();
        await expect(screen.getByText("Item 2")).toBeInTheDocument();
        await expect(screen.getByText("Item 3")).toBeInTheDocument();
    },
};
