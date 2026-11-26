// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { EvalStatusBadge } from "./EvalStatusBadge";

const meta = {
    title: "Evaluations/EvalStatusBadge",
    component: EvalStatusBadge,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component: "Compact status pill rendered next to a run name. Each enum value picks its own colour token and decides whether to pulse.",
            },
        },
    },
} satisfies Meta<typeof EvalStatusBadge>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Pending: Story = {
    args: { status: "pending" },
    play: async () => {
        await expect(screen.getByText("Pending")).toBeInTheDocument();
    },
};

export const Running: Story = {
    args: { status: "running" },
    play: async ({ canvasElement }) => {
        const badge = canvasElement.querySelector('[data-testid="evalStatusBadge"][data-status="running"]');
        await expect(badge?.querySelector(".animate-pulse")).toBeTruthy();
    },
};

export const Completed: Story = {
    args: { status: "completed" },
    play: async () => {
        await expect(screen.getByText("Completed")).toBeInTheDocument();
    },
};

export const CompletedWithWarnings: Story = {
    args: { status: "completed_with_warnings" },
    play: async () => {
        await expect(screen.getByText("Completed (Warnings)")).toBeInTheDocument();
    },
};

export const Failed: Story = {
    args: { status: "failed" },
    play: async () => {
        await expect(screen.getByText("Failed")).toBeInTheDocument();
    },
};

export const Cancelled: Story = {
    args: { status: "cancelled" },
    play: async () => {
        await expect(screen.getByText("Cancelled")).toBeInTheDocument();
    },
};
