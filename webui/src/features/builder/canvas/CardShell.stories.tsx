// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";

import { CardShell } from "./CardShell";

const meta = {
    title: "Builder/CardShell",
    component: CardShell,
    parameters: { layout: "padded" },
    decorators: [
        Story => (
            <div className="flex flex-wrap gap-8 pt-8">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof CardShell>;

export default meta;
type Story = StoryObj<typeof meta>;

const STUB = (
    <div className="px-4 py-3">
        <span className="text-sm font-semibold">MyAgent</span>
    </div>
);

const STATES: Array<React.ComponentProps<typeof CardShell> & { label: string }> = [
    { label: "Creating", generating: true, children: STUB },
    { label: "Editing", editing: true, generating: true, children: STUB },
    { label: "Building components", buildingDeps: true, children: STUB },
    { label: "Queued", queued: true, children: STUB },
    { label: "Ready", ready: true, children: STUB },
    { label: "Active", active: true, children: STUB },
    { label: "Draft", draft: true, children: STUB },
    { label: "Error", error: true, children: STUB },
    { label: "Waiting for Input", statusText: "Waiting for Input...", children: STUB },
];

export const AllStates: Story = {
    render: () => (
        <>
            {STATES.map(({ label, ...props }) => (
                <div key={label} className="flex flex-col items-center gap-1">
                    <CardShell {...props} />
                    <span className="text-xs">{label}</span>
                </div>
            ))}
        </>
    ),
    args: { children: null as never },
};
