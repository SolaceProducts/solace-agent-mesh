// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor } from "storybook/test";

import { Button } from "./button";
import { RichTooltip, RichTooltipContent, RichTooltipProvider, RichTooltipTrigger } from "./rich-tooltip";

const CONTENT_SLOT = "[data-slot='rich-tooltip-content']";
const OPEN_STATES = new Set(["instant-open", "delayed-open"]);

function findOpenRichTooltipContent(doc: Document): HTMLElement | null {
    const cards = doc.querySelectorAll<HTMLElement>(CONTENT_SLOT);
    for (const card of cards) {
        const state = card.dataset.state;
        if (state && OPEN_STATES.has(state)) return card;
    }
    return null;
}

const meta = {
    title: "UI/RichTooltip",
    component: RichTooltip,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Flow-based tooltip primitive for arbitrary content.",
            },
        },
    },
} satisfies Meta<typeof RichTooltip>;

export default meta;
type Story = StoryObj<typeof meta>;

const TRIGGER_LABEL = "Hover me";
const CONTENT_TEXT = "Rich tooltip content";

function TooltipHarness() {
    return (
        <RichTooltipProvider>
            <RichTooltip>
                <RichTooltipTrigger asChild>
                    <Button data-testid="tooltipTrigger" variant="ghost" className="w-50">
                        {TRIGGER_LABEL}
                    </Button>
                </RichTooltipTrigger>
                <RichTooltipContent side="bottom">{CONTENT_TEXT}</RichTooltipContent>
            </RichTooltip>
        </RichTooltipProvider>
    );
}

export const OpensOnHover: Story = {
    render: () => <TooltipHarness />,
    play: async ({ canvasElement }) => {
        const doc = canvasElement.ownerDocument;
        const trigger = canvasElement.querySelector("[data-testid='tooltipTrigger']") as HTMLElement;

        await userEvent.hover(trigger);

        await waitFor(
            () => {
                const card = findOpenRichTooltipContent(doc);
                expect(card).not.toBeNull();
                expect(card?.textContent).toContain(CONTENT_TEXT);
            },
            { timeout: 2000 }
        );
    },
};

export const ClicksDoNotCloseTooltip: Story = {
    render: () => <TooltipHarness />,
    play: async ({ canvasElement }) => {
        const doc = canvasElement.ownerDocument;
        const trigger = canvasElement.querySelector("[data-testid='tooltipTrigger']") as HTMLElement;

        await userEvent.hover(trigger);
        await waitFor(
            () => {
                expect(findOpenRichTooltipContent(doc)).not.toBeNull();
            },
            { timeout: 2000 }
        );

        await userEvent.click(trigger);

        const POSITIVE_WINDOW_MS = 250;
        const POLL_INTERVAL_MS = 25;
        const deadline = Date.now() + POSITIVE_WINDOW_MS;
        while (Date.now() < deadline) {
            expect(findOpenRichTooltipContent(doc)).not.toBeNull();
            await new Promise(r => setTimeout(r, POLL_INTERVAL_MS));
        }
    },
};
