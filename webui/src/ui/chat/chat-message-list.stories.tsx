// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";

import { ChatMessageList } from "./chat-message-list";

const ROW_HEIGHT = 40;
const PANE_HEIGHT = 300;
const AT_BOTTOM_TOLERANCE = 20;

const meta = {
    title: "UI/ChatMessageList",
    component: ChatMessageList,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "The scrolling message list shared by every chat surface. It follows the bottom while auto-scroll is on, including content that renders after its own scroll to the bottom has started.",
            },
        },
    },
} satisfies Meta<typeof ChatMessageList>;

export default meta;
type Story = StoryObj<typeof meta>;

function Rows({ count }: Readonly<{ count: number }>) {
    return Array.from({ length: count }, (_, i) => (
        <div key={i} data-row style={{ height: ROW_HEIGHT }}>
            Row {i + 1}
        </div>
    ));
}

function GrowsDuringFirstScroll({ initialRows, addedRows }: Readonly<{ initialRows: number; addedRows: number }>) {
    const [rows, setRows] = useState(initialRows);
    useEffect(() => {
        const scroller = document.querySelector("[data-list-pane] .overflow-y-auto");
        if (!scroller) return;
        const grow = () => setRows(initialRows + addedRows);
        scroller.addEventListener("scroll", grow, { once: true });
        return () => scroller.removeEventListener("scroll", grow);
    }, [initialRows, addedRows]);
    return <Rows count={rows} />;
}

function distanceToBottom(scroller: Element) {
    return scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop;
}

function findScroller(canvasElement: HTMLElement) {
    const scroller = canvasElement.querySelector("[data-list-pane] .overflow-y-auto");
    if (!scroller) throw new Error("scroll container not found");
    return scroller;
}

export const FollowsContentThatGrowsDuringItsFirstScroll: Story = {
    render: () => (
        <div data-list-pane style={{ height: PANE_HEIGHT }}>
            <ChatMessageList>
                <GrowsDuringFirstScroll initialRows={12} addedRows={14} />
            </ChatMessageList>
        </div>
    ),
    play: async ({ canvasElement }) => {
        const scroller = findScroller(canvasElement);

        await waitFor(() => expect(canvasElement.querySelectorAll("[data-row]").length).toBe(26));
        await waitFor(() => expect(distanceToBottom(scroller)).toBeLessThanOrEqual(AT_BOTTOM_TOLERANCE), { timeout: 4000 });
    },
};

export const LeavesAReaderWhoScrolledUpWhereTheyAre: Story = {
    render: () => (
        <div data-list-pane style={{ height: PANE_HEIGHT }}>
            <ChatMessageList>
                <GrowsDuringFirstScroll initialRows={12} addedRows={14} />
            </ChatMessageList>
        </div>
    ),
    play: async ({ canvasElement }) => {
        const scroller = findScroller(canvasElement);
        await waitFor(() => expect(distanceToBottom(scroller)).toBeLessThanOrEqual(AT_BOTTOM_TOLERANCE), { timeout: 4000 });
        await new Promise(resolve => setTimeout(resolve, 700));

        scroller.scrollTop = 120;
        await new Promise(resolve => setTimeout(resolve, 700));
        const readerPosition = scroller.scrollTop;

        const addRow = document.createElement("div");
        addRow.style.height = `${ROW_HEIGHT * 6}px`;
        scroller.firstElementChild?.appendChild(addRow);
        await new Promise(resolve => setTimeout(resolve, 900));

        await expect(Math.abs(scroller.scrollTop - readerPosition)).toBeLessThanOrEqual(AT_BOTTOM_TOLERANCE);
        await expect(distanceToBottom(scroller)).toBeGreaterThan(AT_BOTTOM_TOLERANCE);
    },
};
