// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent } from "storybook/test";

import { toModelDependentV2 } from "@/testing/data/modelsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { ModelDeleteDialog } from "./ModelDeleteDialog";

const meta = {
    title: "Models/ModelDeleteDialog",
    component: ModelDeleteDialog,
    parameters: {
        docs: {
            description: {
                component: "Delete dialog for models that shows the agents and workflows a delete will un-deploy, when attempting to delete a model configuration.",
            },
        },
    },
} satisfies Meta<typeof ModelDeleteDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

async function assertDeleteConfirmation(modelAlias: string) {
    const deleteButton = await screen.findByRole("button", { name: /^delete$/i });
    expect(deleteButton).toBeDisabled();

    const input = await screen.findByRole("textbox");
    await userEvent.clear(input);
    await userEvent.type(input, modelAlias);
    expect(deleteButton).not.toBeDisabled();
}

export const NoAffectedAgents: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-unused",
        modelAlias: "my-gpt-4",
        isLoading: false,
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models/:modelId/dependents", () => {
                    return HttpResponse.json(paginatedResponse([]));
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByText(/code-based agents/i);
        await screen.findByText(/confirm the deletion/i);
        await assertDeleteConfirmation(args.modelAlias);
    },
};

export const WithFewAffectedAgents: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-gpt-4",
        modelAlias: "my-gpt-4",
        isLoading: false,
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models/:modelId/dependents", () => {
                    return HttpResponse.json(
                        paginatedResponse(
                            [
                                { id: "agent-1", name: "Data Processing Pipeline", type: "agent", deploymentStatus: "not_deployed" },
                                { id: "agent-2", name: "Customer Support Bot", type: "agent", deploymentStatus: "not_deployed" },
                                { id: "agent-3", name: "Report Generator", type: "agent", deploymentStatus: "not_deployed" },
                                { id: "agent-4", name: "Email Assistant", type: "agent", deploymentStatus: "not_deployed" },
                            ].map(toModelDependentV2)
                        )
                    );
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByText(/currently being used by/i);
        await screen.findByText("Affected Agents (4)");

        await screen.findByText("Data Processing Pipeline");
        await screen.findByText("Customer Support Bot");
        await screen.findByText("Report Generator");
        await screen.findByText("Email Assistant");

        const accordionButton = screen.queryByRole("button", { name: /affected agents/i });
        expect(accordionButton).toBeNull();

        await assertDeleteConfirmation(args.modelAlias);
    },
};

export const WithManyAffectedAgents: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-claude-opus",
        modelAlias: "my-claude-opus",
        isLoading: false,
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models/:modelId/dependents", () => {
                    return HttpResponse.json(
                        paginatedResponse(
                            Array.from({ length: 15 }, (_, i) => ({
                                id: `agent-${i + 1}`,
                                name: `Agent ${i + 1} - ${["Sales", "Support", "Analysis", "Processing", "Generation"][i % 5]}`,
                                type: "agent",
                                deploymentStatus: "not_deployed",
                            })).map(toModelDependentV2)
                        )
                    );
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByText(/currently being used by/i);

        const accordionButton = await screen.findByRole("button", { name: /affected agents \(15\)/i });
        expect(screen.queryByText("Agent 1 - Sales")).toBeNull();

        await userEvent.click(accordionButton);
        await screen.findByText("Agent 1 - Sales");
        await screen.findByText("Agent 6 - Sales");

        await assertDeleteConfirmation(args.modelAlias);
    },
};

export const WithWorkflowDependents: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-decider",
        modelAlias: "decider",
        isLoading: false,
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models/:modelId/dependents", () => {
                    return HttpResponse.json(
                        paginatedResponse(
                            [
                                { id: "wf-1", name: "Invoice Router", type: "workflow", deploymentStatus: "deployed" },
                                { id: "wf-2", name: "Ticket Triage", type: "workflow", deploymentStatus: "not_deployed" },
                            ].map(toModelDependentV2)
                        )
                    );
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByText("Affected Workflows (2)");
        await screen.findByText(/Deleting this model will un-deploy any workflows that are currently deployed/i);
        await screen.findByText("Invoice Router");
        await screen.findByText("Ticket Triage");

        expect(screen.queryByText(/affected agents/i)).toBeNull();
        expect(await screen.findAllByText("Will be undeployed")).toHaveLength(1);

        const deleteButton = await screen.findByRole("button", { name: /^delete$/i });
        expect(deleteButton).toBeDisabled();
        await userEvent.type(await screen.findByRole("textbox"), args.modelAlias);
        expect(deleteButton).toBeEnabled();
    },
};

export const WithAgentAndWorkflowDependents: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-shared",
        modelAlias: "shared-model",
        isLoading: false,
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models/:modelId/dependents", () => {
                    return HttpResponse.json(
                        paginatedResponse(
                            [
                                { id: "agent-1", name: "Customer Support Bot", type: "agent", deploymentStatus: "deployed" },
                                { id: "agent-2", name: "Report Generator", type: "agent", deploymentStatus: "deployed" },
                                { id: "wf-1", name: "Invoice Router", type: "workflow", deploymentStatus: "deployed" },
                            ].map(toModelDependentV2)
                        )
                    );
                })
            ),
        },
    },
    play: async ({ args }) => {
        await screen.findByText("Affected Workflows (1)");
        await screen.findByText("Affected Agents (2)");
        await screen.findByText("Invoice Router");
        await screen.findByText("Customer Support Bot");

        await userEvent.type(await screen.findByRole("textbox"), args.modelAlias);
        expect(await screen.findByRole("button", { name: /^delete$/i })).toBeEnabled();
    },
};

export const CheckingDependents: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        onConfirm: async () => {},
        modelId: "model-checking",
        modelAlias: "my-model",
        isLoading: false,
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models/:modelId/dependents", async () => {
                    await new Promise(resolve => setTimeout(resolve, 2000));
                    return HttpResponse.json(paginatedResponse([{ id: "agent-1", name: "My Agent", type: "agent", deploymentStatus: "not_deployed" }].map(toModelDependentV2)));
                })
            ),
        },
    },
    play: async () => {
        await screen.findByText(/checking what uses this model/i);

        expect(screen.queryByRole("textbox")).toBeNull();

        const deleteButton = await screen.findByRole("button", { name: /^delete$/i });
        expect(deleteButton).toBeDisabled();
    },
};
