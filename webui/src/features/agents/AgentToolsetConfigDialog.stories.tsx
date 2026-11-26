// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fireEvent, fn, screen, within } from "storybook/test";

import { AgentToolsetConfigDialog } from "@/features/agents/AgentToolsetConfigDialog";

import { mockToolsetFiles } from "@/testing/data";

const configSchema = mockToolsetFiles[0].configSchema!;
const toolName = mockToolsetFiles[0].toolName;

const meta = {
    title: "Agents/AgentToolsetConfigDialog",
    component: AgentToolsetConfigDialog,
    parameters: {
        docs: {
            description: {
                component: "Per-tool config dialog used on both agent edit (per-agent overrides) and toolset edit (global defaults) pages.",
            },
        },
    },
} satisfies Meta<typeof AgentToolsetConfigDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OpensWithFields: Story = {
    args: {
        toolName,
        configSchema,
        configValues: {},
        onSave: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const cog = canvas.getByRole("button", { name: /Configure/i });
        expect(cog).toBeInTheDocument();

        fireEvent.click(cog);
        const dialog = await screen.findByRole("dialog");

        await within(dialog).findByText("Tool Configuration");

        expect(within(dialog).getByText("api_key")).toBeInTheDocument();
        expect(within(dialog).getByText("endpoint_url")).toBeInTheDocument();
        expect(within(dialog).getByText("timeout")).toBeInTheDocument();
        expect(within(dialog).getByText("enable_retry")).toBeInTheDocument();

        expect(within(dialog).getByText("API key for accessing the CRM service")).toBeInTheDocument();

        const apiKeyLabel = within(dialog).getByText("api_key").closest("label");
        expect(apiKeyLabel?.textContent).toContain("*");
    },
};

export const RequiredFieldValidation: Story = {
    args: {
        toolName,
        configSchema,
        configValues: {},
        onSave: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        fireEvent.click(canvas.getByRole("button", { name: /Configure/i }));
        const dialog = await screen.findByRole("dialog");

        fireEvent.click(within(dialog).getByRole("button", { name: /Save Configuration/i }));

        await within(dialog).findByText("api_key is required");

        expect(args.onSave).not.toHaveBeenCalled();
    },
};

export const SaveWithValues: Story = {
    args: {
        toolName,
        configSchema,
        configValues: {},
        onSave: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        fireEvent.click(canvas.getByRole("button", { name: /Configure/i }));
        const dialog = await screen.findByRole("dialog");

        const passwordInputs = dialog.querySelectorAll("input[type='password']");
        fireEvent.change(passwordInputs[0], { target: { value: "sk-test-123" } });

        fireEvent.click(within(dialog).getByRole("button", { name: /Save Configuration/i }));

        expect(args.onSave).toHaveBeenCalledTimes(1);
        expect(args.onSave).toHaveBeenCalledWith(toolName, expect.objectContaining({ api_key: "sk-test-123" }));
    },
};

export const StoredSecretShowsLabel: Story = {
    args: {
        toolName,
        configSchema,
        configValues: { api_key: "previously-saved-secret" },
        onSave: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        fireEvent.click(canvas.getByRole("button", { name: /Configure/i }));
        const dialog = await screen.findByRole("dialog");

        expect(within(dialog).getByText("(stored)")).toBeInTheDocument();

        const passwordInputs = dialog.querySelectorAll("input[type='password']");
        expect((passwordInputs[0] as HTMLInputElement).value).toBe("");
    },
};

export const ToolsetDefaultDescription: Story = {
    args: {
        toolName,
        configSchema,
        configValues: {},
        onSave: fn(),
        isToolsetDefault: true,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        fireEvent.click(canvas.getByRole("button", { name: /Configure/i }));
        const dialog = await screen.findByRole("dialog");

        expect(within(dialog).getByText(/inherited by every agent/i)).toBeInTheDocument();
        expect(within(dialog).queryByText(/unique to this agent/i)).not.toBeInTheDocument();
    },
};

export const AgentSpecificDescription: Story = {
    args: {
        toolName,
        configSchema,
        configValues: {},
        onSave: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        fireEvent.click(canvas.getByRole("button", { name: /Configure/i }));
        const dialog = await screen.findByRole("dialog");

        expect(within(dialog).getByText(/unique to this agent/i)).toBeInTheDocument();
        expect(within(dialog).queryByText(/inherited by every agent/i)).not.toBeInTheDocument();
    },
};
