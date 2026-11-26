// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";

import { addComponent, COMPONENT_SCOPES, type ComponentKind, type ComponentOption, EVALUATION_SCOPES } from "./bundles";
import { DEMO_AGENTS, DEMO_WORKFLOWS, EXAMPLE_ROLE_SCOPES, LONG_AGENTS, LONG_WORKFLOWS, makeAgents } from "./fixtures";
import { ScopeBundleEditor } from "./ScopeBundleEditor";

const invokeScopes = (kind: ComponentKind, items: readonly ComponentOption[]): string[] => items.reduce<string[]>((acc, item) => addComponent(acc, kind, item.id), []);

const meta = {
    title: "User Management/ScopeBundleEditor",
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Coarse RBAC scope editor. Access tab: per-instance agent/workflow grants (plus the All-Agents/All-Workflows wildcards). Scopes tab: the raw scope list. These stories cover visual states; behavior is tested in bundles.test.ts and ScopeBundleEditor.test.tsx.",
            },
        },
    },
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

function Controlled({ initialValue = [], agents = DEMO_AGENTS, workflows = DEMO_WORKFLOWS }: { initialValue?: string[]; agents?: readonly ComponentOption[]; workflows?: readonly ComponentOption[] }) {
    const [value, setValue] = useState<string[]>(initialValue);
    return (
        <div className="max-w-2xl space-y-6">
            <ScopeBundleEditor value={value} onChange={setValue} agents={agents} workflows={workflows} />
            <div className="rounded bg-(--secondary-w10) p-3 text-xs text-(--secondary-text-wMain)">{value.length ? value.map(scope => <div key={scope}>{scope}</div>) : <div>(no scopes)</div>}</div>
        </div>
    );
}

const openAccess = (canvasElement: HTMLElement) => userEvent.click(within(canvasElement).getByRole("tab", { name: "Access" }));

export const AccessEmpty: Story = {
    render: () => <Controlled />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        const canvas = within(canvasElement);
        await expect(canvas.getByText("No agents added")).toBeInTheDocument();
        await expect(canvas.getByText("No workflows added")).toBeInTheDocument();
    },
};

export const AccessWithSelections: Story = {
    render: () => <Controlled initialValue={[...invokeScopes("agent", DEMO_AGENTS.slice(0, 2)), ...invokeScopes("workflow", DEMO_WORKFLOWS.slice(0, 1))]} />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Agents (2)")).toBeInTheDocument();
        await expect(canvas.getByText(DEMO_AGENTS[0].name)).toBeInTheDocument();
    },
};

export const AccessAllAgents: Story = {
    render: () => <Controlled initialValue={["agent:*:invoke"]} />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        await expect(within(canvasElement).getByText("Agents (All)")).toBeInTheDocument();
    },
};

export const AccessAdding: Story = {
    render: () => <Controlled />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        await userEvent.click(within(canvasElement).getByRole("button", { name: /Add Agent/ }));
        await expect(await within(document.body).findByText("All Agents")).toBeInTheDocument();
    },
};

export const ScopesTab: Story = {
    render: () => <Controlled initialValue={[...COMPONENT_SCOPES, ...EVALUATION_SCOPES.view]} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("tab", { name: "Scopes" }));
        await expect(within(canvas.getByRole("tabpanel")).getByText("agent:*:invoke")).toBeInTheDocument();
    },
};

export const EmptyScopesTab: Story = {
    render: () => <Controlled initialValue={[]} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("tab", { name: "Scopes" }));
        await expect(within(canvas.getByRole("tabpanel")).getByText(/^No scopes granted\./)).toBeInTheDocument();
    },
};

export const LongContent: Story = {
    render: () => <Controlled agents={LONG_AGENTS} workflows={LONG_WORKFLOWS} initialValue={invokeScopes("agent", LONG_AGENTS.slice(0, 1))} />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        await userEvent.click(within(canvasElement).getByRole("button", { name: /Add Agent/ }));
    },
};

const MANY_AGENTS = makeAgents(30);

export const ManyAgents: Story = {
    render: () => <Controlled agents={MANY_AGENTS} initialValue={invokeScopes("agent", MANY_AGENTS)} />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        await expect(within(canvasElement).getByText("Agents (30)")).toBeInTheDocument();
    },
};

const flagOn = { featureContext: { feedback_management: true } };

export const ExampleFeedbackReviewer: Story = {
    parameters: flagOn,
    render: () => <Controlled initialValue={[...EXAMPLE_ROLE_SCOPES.feedbackViewer]} />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        const canvas = within(canvasElement);
        await expect(canvas.getAllByRole("checkbox", { name: /Can view feedback$/ })[0]).toBeChecked();
        await expect(canvas.getAllByRole("checkbox", { name: /Can view activities$/ })[0]).not.toBeChecked();
    },
};

export const ExampleTraceReviewer: Story = {
    parameters: flagOn,
    render: () => <Controlled initialValue={[...EXAMPLE_ROLE_SCOPES.traceViewer]} />,
};

export const GeneralReviewerRole: Story = {
    parameters: flagOn,
    render: () => <Controlled initialValue={[...EXAMPLE_ROLE_SCOPES.generalReviewer]} />,
    play: async ({ canvasElement }) => {
        await openAccess(canvasElement);
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Agents (All)")).toBeInTheDocument();
        await expect(canvas.getByText("Workflows (All)")).toBeInTheDocument();
        await expect(canvas.getAllByRole("checkbox", { name: /Can view activities$/ })).toHaveLength(1);
    },
};

export const OperationsReviewerRole: Story = {
    parameters: flagOn,
    render: () => <Controlled initialValue={[...EXAMPLE_ROLE_SCOPES.operationsReviewer]} />,
};
