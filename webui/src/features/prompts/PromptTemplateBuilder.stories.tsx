// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { withDefaults } from "@/testing/helpers";

import { PromptTemplateBuilder } from "./PromptTemplateBuilder";

const handlers = withDefaults(
    http.get("*/api/v1/prompts/chat/init", () => {
        return HttpResponse.json({
            message: "Hi! I'll help you create an effective prompt template. What kind of task or interaction would you like this prompt to handle?",
        });
    }),
    http.post("*/api/v1/prompts/chat", () => {
        return HttpResponse.json({
            message: "I understand you want to create a prompt template. Let me help you with that.",
            template_updates: {},
            ready_to_save: false,
        });
    })
);

const meta = {
    title: "Prompts/PromptTemplateBuilder",
    component: PromptTemplateBuilder,
    parameters: {
        docs: {
            description: {
                component: "The component for templating and building custom prompts",
            },
        },
        msw: { handlers },
    },
} satisfies Meta<typeof PromptTemplateBuilder>;

export default meta;
type Story = StoryObj<typeof PromptTemplateBuilder>;

export const Default: Story = {
    args: {},
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const editManually = await canvas.findByTestId("editManuallyButton");
        expect(editManually).toBeVisible();
    },
};

export const AIAssistedModeValidationErrors: Story = {
    args: {},
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const editManually = await canvas.findByTestId("editManuallyButton");
        expect(editManually).toBeVisible();

        const createButton = await canvas.findByTestId("createPromptButton");
        createButton.click();
    },
};

export const ManualMode: Story = {
    args: { initialMode: "manual" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const buildWithAI = await canvas.findByTestId("buildWithAIButton");
        expect(buildWithAI).toBeVisible();
    },
};

export const ManualModeValidationErrors: Story = {
    args: { initialMode: "manual" },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const buildWithAI = await canvas.findByTestId("buildWithAIButton");
        expect(buildWithAI).toBeVisible();

        const createButton = await canvas.findByTestId("createPromptButton");
        createButton.click();
    },
};

export const EditingAsEditor: Story = {
    args: {
        initialMode: "manual",
        isEditing: true,
        editingGroup: {
            id: "shared-prompt",
            name: "Shared Prompt",
            description: undefined,
            category: undefined,
            command: "shared-shortcut",
            userId: "owner@example.com",
            authorName: "Owner",
            productionPromptId: undefined,
            accessLevel: "resource_editor",
            isShared: true,
            isPinned: false,
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
        },
    },
    parameters: { configContext: { identityServiceType: "okta" }, featureContext: { prompt_sharing: true } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const commandInput = await canvas.findByLabelText(/chat shortcut/i);
        expect(commandInput).toBeDisabled();
        expect(await canvas.findByText(/only the prompt owner can change the chat shortcut/i)).toBeInTheDocument();
    },
};

export const EditingAsOwner: Story = {
    args: {
        initialMode: "manual",
        isEditing: true,
        editingGroup: {
            id: "own-prompt",
            name: "Own Prompt",
            description: undefined,
            category: undefined,
            command: "my-shortcut",
            userId: "owner@example.com",
            authorName: "Owner",
            productionPromptId: undefined,
            accessLevel: "owner",
            isShared: false,
            isPinned: false,
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
        },
    },
    parameters: { configContext: { identityServiceType: "okta" }, featureContext: { prompt_sharing: true } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const commandInput = await canvas.findByLabelText(/chat shortcut/i);
        expect(commandInput).not.toBeDisabled();
        expect(canvas.queryByText(/only the prompt owner can change/i)).not.toBeInTheDocument();
        expect(await canvas.findByTestId("createPromptButton")).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: /save new version/i })).not.toBeInTheDocument();
    },
};

export const EditingWithVersionHistory: Story = {
    args: EditingAsOwner.args,
    parameters: { configContext: { identityServiceType: "okta" }, featureContext: { prompt_sharing: true, prompt_version_history: true } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("button", { name: /save new version/i })).toBeInTheDocument();
        expect(await canvas.findByTestId("createPromptButton")).toBeInTheDocument();
    },
};

export const EditingModeDefaultsToAIAssisted: Story = {
    args: {
        isEditing: true,
        editingGroup: {
            id: "test-prompt-id",
            name: "Test Prompt",
            description: "A test prompt for editing",
            category: "Development",
            command: "test",
            userId: "test-user-id",
            authorName: "Test User",
            productionPromptId: "test-production-prompt-id",
            isShared: false,
            isPinned: false,
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            productionPrompt: {
                id: "test-production-prompt-id",
                promptText: "This is a test prompt with {{Variable1}}",
                groupId: "test-prompt-id",
                userId: "test-user-id",
                version: 1,
                name: "Test Prompt",
                description: "A test prompt for editing",
                category: "Development",
                command: "test",
                createdAt: new Date().toISOString(),
                updatedAt: new Date().toISOString(),
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const editManually = await canvas.findByTestId("editManuallyButton");
        expect(editManually).toBeVisible();

        const buildWithAI = canvas.queryByTestId("buildWithAIButton");
        expect(buildWithAI).not.toBeInTheDocument();
    },
};
