// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { Button } from "./button";

const meta = {
    title: "UI/Button",
    component: Button,
    parameters: {
        layout: "padded",
        docs: {
            description: { component: "The button component" },
        },
    },
} satisfies Meta<typeof Button>;

export default meta;
type Story = StoryObj<typeof meta>;

const VARIANTS = ["default", "secondary", "outline", "link", "ghost"] as const;

export const AllVariants: Story = {
    args: { onClick: fn() },
    render: args => (
        <div className="flex max-w-62.5 flex-col gap-4">
            {VARIANTS.map(variant => (
                <Button key={variant} variant={variant} onClick={args.onClick}>
                    {variant}
                </Button>
            ))}
            <Button variant="default" disabled onClick={args.onClick}>
                disabled
            </Button>
        </div>
    ),
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);

        for (const variant of VARIANTS) {
            expect(canvas.getByRole("button", { name: variant })).not.toBeDisabled();
        }

        const disabled = canvas.getByRole("button", { name: "disabled" });
        expect(disabled).toBeDisabled();

        await userEvent.click(canvas.getByRole("button", { name: "default" }));
        expect(args.onClick).toHaveBeenCalledTimes(1);

        await userEvent.click(disabled);
        expect(args.onClick).toHaveBeenCalledTimes(1);
    },
};
