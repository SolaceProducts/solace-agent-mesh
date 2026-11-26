// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryContext, StoryFn, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, within } from "storybook/test";

import { withDefaults } from "@/testing/helpers";

import { PromptDeleteDialog } from "./PromptDeleteDialog";

const PROMPT_ID = "demo-prompt-id";

const share = (i: number, email: string) => ({
    id: `share-${i}`,
    userEmail: email,
    accessLevel: "resource_viewer",
    sharedByEmail: "owner@example.com",
    createdAt: "2026-06-01T08:00:00Z",
    updatedAt: "2026-06-01T08:00:00Z",
});

const sharesEnvelope = (data: ReturnType<typeof share>[]) => ({
    data,
    meta: { pagination: { pageNumber: 1, pageSize: data.length || 10, totalPages: 1, count: data.length, nextPage: null } },
});

const noSharesHandler = http.get("*/api/v1/prompts/groups/:id/shares", () => HttpResponse.json(sharesEnvelope([])));
const oneShareHandler = http.get("*/api/v1/prompts/groups/:id/shares", () => HttpResponse.json(sharesEnvelope([share(1, "alice@example.com")])));
const threeSharesHandler = http.get("*/api/v1/prompts/groups/:id/shares", () => HttpResponse.json(sharesEnvelope([share(1, "alice@example.com"), share(2, "bob@example.com"), share(3, "carol@example.com")])));

const meta = {
    title: "Prompts/PromptDeleteDialog",
    component: PromptDeleteDialog,
    parameters: {
        docs: {
            description: {
                component: "Confirmation dialog for deleting a prompt. When prompt sharing is available and the prompt has shares, surfaces a recipient count so the owner knows who will lose access.",
            },
        },
    },
    decorators: [
        (Story: StoryFn, context: StoryContext) => {
            const storyResult = Story(context.args, context);
            return <div style={{ height: "100vh", width: "100vw" }}>{storyResult}</div>;
        },
    ],
    args: {
        isOpen: true,
        onClose: () => alert("Deletion will be cancelled"),
        onConfirm: () => alert("Prompt will be deleted"),
        promptId: PROMPT_ID,
        promptName: "Demo Prompt",
    },
} satisfies Meta<typeof PromptDeleteDialog>;

export default meta;
type Story = StoryObj<typeof PromptDeleteDialog>;

export const Default: Story = {
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByTestId("dialogConfirmButton");
        await expect(dialogContent.queryByText(/shared with/i)).not.toBeInTheDocument();
    },
};

export const NoRecipients: Story = {
    args: { promptId: "no-recipients-prompt" },
    parameters: {
        msw: { handlers: withDefaults(noSharesHandler) },
        configContext: { identityServiceType: "okta" },
        featureContext: { prompt_sharing: true },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByText(/permanently delete the prompt/i);
        await expect(dialogContent.queryByText(/lose access/i)).not.toBeInTheDocument();
    },
};

export const SingleRecipient: Story = {
    args: { promptId: "single-recipient-prompt" },
    parameters: {
        msw: { handlers: withDefaults(oneShareHandler) },
        configContext: { identityServiceType: "okta" },
        featureContext: { prompt_sharing: true },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByText(/lose access/i);
    },
};

export const MultipleRecipients: Story = {
    args: { promptId: "multiple-recipients-prompt" },
    parameters: {
        msw: { handlers: withDefaults(threeSharesHandler) },
        configContext: { identityServiceType: "okta" },
        featureContext: { prompt_sharing: true },
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByText(/lose access/i);
    },
};
