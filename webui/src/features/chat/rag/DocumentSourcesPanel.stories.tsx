// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";

import { DocumentSourcesPanel } from "@/features/chat/rag/DocumentSourcesPanel";

import { documentSearchRagData, documentWithNoPageMetadata, multipleDocumentsOverlappingPages, singleDocumentSinglePage } from "@/testing/data/ragSearchResults";

const meta = {
    title: "Chat/DocumentSourcesPanel",
    component: DocumentSourcesPanel,
    parameters: {
        docs: {
            description: {
                component: "Document Sources Panel component for displaying document citations grouped by filename and page. Displays in the side panel when document_search results are present.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l">
                <Story />
            </div>
        ),
    ],
    args: {
        enabled: true,
    },
} satisfies Meta<typeof DocumentSourcesPanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        ragData: documentSearchRagData,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const header = await canvas.findByText(/3 Documents/);
        expect(header).toBeInTheDocument();
        expect(header).toHaveTextContent("6 Citations");

        expect(await canvas.findByText("annual_summary_2024.pdf")).toBeInTheDocument();
        expect(await canvas.findByText("market_analysis_report.txt")).toBeInTheDocument();
        expect(await canvas.findByText("quarterly_report_q4_2024.pdf")).toBeInTheDocument();
    },
};

export const SingleDocument: Story = {
    args: {
        ragData: singleDocumentSinglePage,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const header = await canvas.findByText(/1 Document/);
        expect(header).toBeInTheDocument();
        expect(header).toHaveTextContent("1 Citation");

        expect(await canvas.findByText("product_specs.pdf")).toBeInTheDocument();
    },
};

export const NoPageMetadata: Story = {
    args: {
        ragData: documentWithNoPageMetadata,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const header = await canvas.findByText(/1 Document/);
        expect(header).toBeInTheDocument();
    },
};

export const OverlappingPages: Story = {
    args: {
        ragData: multipleDocumentsOverlappingPages,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const header = await canvas.findByText(/2 Documents/);
        expect(header).toBeInTheDocument();
        expect(header).toHaveTextContent("4 Citations");
    },
};

export const ExpandedAccordion: Story = {
    args: {
        ragData: documentSearchRagData,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const firstSource = await canvas.findByText("annual_summary_2024.pdf");
        await userEvent.click(firstSource);

        const pageCitations = await canvas.findAllByText(/Page \d+/);
        expect(pageCitations.length).toBeGreaterThan(0);
    },
};

export const Disabled: Story = {
    args: {
        ragData: documentSearchRagData,
        enabled: false,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Document Sources")).toBeInTheDocument();
        expect(await canvas.findByText("Source visibility is disabled in settings")).toBeInTheDocument();
    },
};

export const EmptyState: Story = {
    args: {
        ragData: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Document Sources")).toBeInTheDocument();
        expect(await canvas.findByText("No document sources available yet")).toBeInTheDocument();
    },
};

export const NullData: Story = {
    args: {
        ragData: null,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Document Sources")).toBeInTheDocument();
        expect(await canvas.findByText("No document sources available yet")).toBeInTheDocument();
    },
};
