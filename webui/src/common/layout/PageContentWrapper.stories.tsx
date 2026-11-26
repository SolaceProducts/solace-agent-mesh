// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { PageContentWrapper } from "./PageContentWrapper";
import { PageLabel } from "./PageLabel";
import { PageSection } from "./PageSection";

const meta = {
    title: "Common/Layout/PageContentWrapper",
    component: PageContentWrapper,
    parameters: {
        docs: {
            description: {
                component: "Scrollable page content area. Holds the per-page main content beneath the page header.",
            },
        },
    },
} satisfies Meta<typeof PageContentWrapper>;

export default meta;

export const Layout = {
    render: () => (
        <div className="flex h-screen flex-col">
            <PageContentWrapper>
                <PageSection>
                    <PageLabel>Section 1</PageLabel>
                    <div className="text-sm">Content for section 1</div>
                </PageSection>
                <PageSection>
                    <PageLabel>Section 2</PageLabel>
                    <div className="text-sm">Content for section 2</div>
                </PageSection>
                <PageSection>
                    <PageLabel>Section 3</PageLabel>
                    <div className="text-sm">Content for section 3</div>
                </PageSection>
            </PageContentWrapper>
        </div>
    ),
    play: async () => {
        await expect(screen.getByText("Section 1")).toBeInTheDocument();
        await expect(screen.getByText("Section 2")).toBeInTheDocument();
        await expect(screen.getByText("Section 3")).toBeInTheDocument();
    },
};
