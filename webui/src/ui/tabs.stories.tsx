// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs";

const meta = {
    title: "UI/Tabs",
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    'Radix-based tab headers. `variant="pill"` (default) renders a bordered segmented control (active filled with `--primary-w20`), matching the Agents page deployment toggle; `variant="underline"` renders a divider row with a brand-colored active underline.',
            },
        },
    },
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

function Demo({ variant }: { variant?: "pill" | "underline" }) {
    return (
        <div className="max-w-xl">
            <Tabs variant={variant} defaultValue="features" className="w-full">
                <TabsList>
                    <TabsTrigger value="features" title="Features">
                        Features
                    </TabsTrigger>
                    <TabsTrigger value="components" title="Components">
                        Components
                    </TabsTrigger>
                    <TabsTrigger value="advanced" title="Advanced">
                        Advanced
                    </TabsTrigger>
                </TabsList>
                <TabsContent value="features" className="pt-4 text-sm text-(--secondary-text-wMain)">
                    Features panel content.
                </TabsContent>
                <TabsContent value="components" className="pt-4 text-sm text-(--secondary-text-wMain)">
                    Components panel content.
                </TabsContent>
                <TabsContent value="advanced" className="pt-4 text-sm text-(--secondary-text-wMain)">
                    Advanced panel content.
                </TabsContent>
            </Tabs>
        </div>
    );
}

async function switchTabs(canvasElement: HTMLElement) {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Features panel content.")).toBeInTheDocument();

    await userEvent.click(canvas.getByRole("tab", { name: "Components" }));
    await expect(canvas.getByRole("tab", { name: "Components" })).toHaveAttribute("data-state", "active");
    await expect(canvas.getByText("Components panel content.")).toBeInTheDocument();
}

export const Pill: Story = {
    render: () => <Demo variant="pill" />,
    play: async ({ canvasElement }) => switchTabs(canvasElement),
};

export const Underline: Story = {
    render: () => <Demo variant="underline" />,
    play: async ({ canvasElement }) => switchTabs(canvasElement),
};
