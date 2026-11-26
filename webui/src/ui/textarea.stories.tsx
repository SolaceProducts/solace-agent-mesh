// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { Textarea } from "./textarea";

const labelClass = "mb-2 block font-medium";

const meta = {
    title: "UI/Textarea",
    component: Textarea,
    parameters: {
        docs: {
            description: {
                component: "The textarea component with various states",
            },
        },
    },
} satisfies Meta<typeof Textarea>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AllVariants: Story = {
    render: () => (
        <div className="flex w-100 flex-col gap-8 overflow-y-auto p-8">
            <div>
                <label className={labelClass}>Default Textarea</label>
                <Textarea placeholder="Enter text..." />
            </div>

            <div>
                <label className={labelClass}>Textarea with Value</label>
                <Textarea defaultValue="This is some longer text content that spans multiple lines and demonstrates how the textarea component handles text." />
            </div>

            <div>
                <label className={labelClass}>Disabled Textarea</label>
                <Textarea disabled placeholder="Disabled textarea" defaultValue="Cannot edit this content" />
            </div>

            <div>
                <label className={labelClass}>Readonly Textarea</label>
                <Textarea readOnly defaultValue="This is readonly content" />
            </div>

            <div>
                <label className={labelClass}>Error State (aria-invalid)</label>
                <Textarea aria-invalid={true} placeholder="This field has an error" defaultValue="Invalid input content" />
            </div>

            <div>
                <label className={labelClass}>Large Content</label>
                <Textarea
                    defaultValue="This is a longer piece of text to demonstrate scrolling behaviour.&#10;&#10;Line 2 of content.&#10;Line 3 of content.&#10;Line 4 of content.&#10;Line 5 of content.&#10;Line 6 of content."
                    className="min-h-30"
                />
            </div>
        </div>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const textareas = await canvas.findAllByRole("textbox");
        expect(textareas).toHaveLength(6);
    },
};
