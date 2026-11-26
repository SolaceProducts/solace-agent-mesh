// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";

import { TruncatedText } from "./truncated-text";

const TRIGGER_SLOT = "[data-slot='tooltip-trigger']";
const LONG_TEXT = "WeatherToolset__get_current_weather";
const SHORT_TEXT = "short";

const meta = {
    title: "UI/TruncatedText",
    component: TruncatedText,
    parameters: { layout: "padded" },
    args: { text: LONG_TEXT },
} satisfies Meta<typeof TruncatedText>;

export default meta;
type Story = StoryObj<typeof meta>;

function Box({ children }: { children: React.ReactNode }) {
    return (
        <div className="flex items-center gap-2 overflow-hidden rounded border p-2 text-sm" style={{ width: 120 }} data-testid="box">
            {children}
        </div>
    );
}

export const WiresTooltipWhenTruncated: Story = {
    render: () => (
        <Box>
            <TruncatedText text={LONG_TEXT} />
        </Box>
    ),
    play: async ({ canvasElement }) => {
        const trigger = await waitFor(
            () => {
                const el = canvasElement.querySelector<HTMLElement>(TRIGGER_SLOT);
                expect(el).not.toBeNull();
                return el as HTMLElement;
            },
            { timeout: 5000 }
        );
        expect(trigger.scrollWidth).toBeGreaterThan(trigger.clientWidth);
        expect(trigger.textContent).toBe(LONG_TEXT);
    },
};

export const NoTooltipWhenNotTruncated: Story = {
    render: () => (
        <Box>
            <TruncatedText text={SHORT_TEXT} />
        </Box>
    ),
    play: async ({ canvasElement }) => {
        const span = canvasElement.querySelector("span") as HTMLElement;
        expect(span.scrollWidth).toBeLessThanOrEqual(span.clientWidth);
        await new Promise(r => setTimeout(r, 500));
        expect(canvasElement.querySelector(TRIGGER_SLOT)).toBeNull();
    },
};

export const DisplayDiffersFromTooltip: Story = {
    render: () => (
        <Box>
            <TruncatedText text={LONG_TEXT}>Weather · get current weather (long humanized label)</TruncatedText>
        </Box>
    ),
    play: async ({ canvasElement }) => {
        const trigger = await waitFor(
            () => {
                const el = canvasElement.querySelector<HTMLElement>(TRIGGER_SLOT);
                expect(el).not.toBeNull();
                return el as HTMLElement;
            },
            { timeout: 5000 }
        );
        expect(trigger.textContent).toContain("humanized label");
        expect(trigger.textContent).not.toBe(LONG_TEXT);
    },
};
