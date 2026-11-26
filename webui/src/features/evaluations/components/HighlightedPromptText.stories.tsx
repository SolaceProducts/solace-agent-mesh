// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { HighlightedPromptText } from "@/features/evaluations/components";

const meta = {
    title: "Evaluations/HighlightedPromptText",
    component: HighlightedPromptText,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Read-only renderer for evaluator criteria. Highlights only the supported template variables ({{Prompt}}, {{Response}}, {{Expected Response}}) — case-insensitive and tolerant of whitespace inside the braces.",
            },
        },
    },
} satisfies Meta<typeof HighlightedPromptText>;

export default meta;
type Story = StoryObj<typeof meta>;

const highlightedSpans = (root: HTMLElement): string[] => Array.from(root.querySelectorAll("span.bg-\\(--info-w20\\)")).map(n => n.textContent ?? "");

export const HighlightsCanonicalTokens: Story = {
    args: {
        text: "Compare {{Response}} against {{Expected Response}} for the {{Prompt}}.",
    },
    play: async ({ canvasElement }) => {
        const tokens = highlightedSpans(canvasElement);
        await expect(tokens).toEqual(["{{Response}}", "{{Expected Response}}", "{{Prompt}}"]);
    },
};

export const HighlightsMixedCaseAndPadding: Story = {
    args: {
        text: "{{response}} and {{ Response }} and {{EXPECTED RESPONSE}} all highlight.",
    },
    play: async ({ canvasElement }) => {
        const tokens = highlightedSpans(canvasElement);
        await expect(tokens).toEqual(["{{response}}", "{{ Response }}", "{{EXPECTED RESPONSE}}"]);
    },
};

export const SkipsUnsupportedTokens: Story = {
    args: {
        text: "Supported {{Response}} but {{Foo}} and {{NotResponse}} stay plain.",
    },
    play: async ({ canvasElement }) => {
        const tokens = highlightedSpans(canvasElement);
        await expect(tokens).toEqual(["{{Response}}"]);
        const canvas = within(canvasElement);
        await expect(canvas.getByText(/\{\{Foo\}\}/)).toBeInTheDocument();
        await expect(canvas.getByText(/\{\{NotResponse\}\}/)).toBeInTheDocument();
    },
};
