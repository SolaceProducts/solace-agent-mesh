// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { Input } from "./input";

const labelClass = "mb-2 block font-medium";

const meta = {
    title: "UI/Input",
    component: Input,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "The input component with various states",
            },
        },
    },
} satisfies Meta<typeof Input>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    render: () => (
        <div className="flex w-100 flex-col gap-8">
            <div>
                <label className={labelClass}>Default Input</label>
                <Input placeholder="Enter text..." />
            </div>

            <div>
                <label className={labelClass}>Input with Value</label>
                <Input defaultValue="This is some text" />
            </div>

            <div>
                <label className={labelClass}>Disabled Input</label>
                <Input disabled placeholder="Disabled input" defaultValue="Cannot edit this" />
            </div>

            <div>
                <label className={labelClass}>Readonly Input</label>
                <Input readOnly defaultValue="This is readonly" />
            </div>

            <div>
                <label className={labelClass}>Error State (aria-invalid)</label>
                <Input aria-invalid={true} placeholder="This field has an error" defaultValue="Invalid input" />
            </div>
        </div>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const inputs = await canvas.findAllByRole("textbox");
        expect(inputs).toHaveLength(5);
    },
};
