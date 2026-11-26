// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { Label } from "./label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./select";

const meta = {
    title: "UI/Select",
    component: Select,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "The select component with various states",
            },
        },
    },
} satisfies Meta<typeof Select>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AllVariants: Story = {
    render: () => (
        <div className="flex w-100 flex-col gap-8">
            <div>
                <Label>Default Select</Label>
                <Select>
                    <SelectTrigger>
                        <SelectValue placeholder="Select an option..." />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="option1">Option 1</SelectItem>
                        <SelectItem value="option2">Option 2</SelectItem>
                        <SelectItem value="option3">Option 3</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div>
                <Label>With Selected Value</Label>
                <Select defaultValue="option2">
                    <SelectTrigger>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="option1">Option 1</SelectItem>
                        <SelectItem value="option2">Option 2</SelectItem>
                        <SelectItem value="option3">Option 3</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div>
                <Label>Disabled Select</Label>
                <Select disabled defaultValue="option2">
                    <SelectTrigger>
                        <SelectValue placeholder="Cannot interact" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="option1">Option 1</SelectItem>
                        <SelectItem value="option2">Option 2</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div>
                <Label>Readonly Select</Label>
                <Select readonly defaultValue="option2">
                    <SelectTrigger readonly>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="option1">Option 1</SelectItem>
                        <SelectItem value="option2">Option 2</SelectItem>
                        <SelectItem value="option3">Option 3</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div>
                <Label>Error State (invalid prop)</Label>
                <Select defaultValue="option2">
                    <SelectTrigger invalid={true} className="w-full max-w-xs">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="option1">Option 1</SelectItem>
                        <SelectItem value="option2">Option 2</SelectItem>
                        <SelectItem value="option3">Option 3</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div>
                <Label>Constant Width Select</Label>
                <Select defaultValue="optionLong">
                    <SelectTrigger className="w-full max-w-xs">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="option1">Option 1</SelectItem>
                        <SelectItem value="option2">Option 2</SelectItem>
                        <SelectItem value="optionLong">Option 3 is too long and it will overflow the width but that's ok since this is an example.</SelectItem>
                    </SelectContent>
                </Select>
            </div>
        </div>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const selectButtons = await canvas.findAllByRole("combobox");
        expect(selectButtons).toHaveLength(6);
    },
};
