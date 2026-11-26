// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { defaultPromptGroups, languagePromptGroup } from "@/testing/data/prompts";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { PromptsPage } from "./PromptsPage";

const handlers = withDefaults(
    http.get("*/api/v1/prompts/groups/all", () => {
        return HttpResponse.json(paginatedResponse(defaultPromptGroups));
    })
);

const meta = {
    title: "Prompts/PromptsPage",
    component: PromptsPage,
    parameters: {
        docs: {
            description: {
                component: "The main chat page component that displays the chat interface, side panels, and handles user interactions.",
            },
        },
    },
} satisfies Meta<typeof PromptsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: { handlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Create New Prompt");
    },
};

export const WithPromptOpen: Story = {
    parameters: {
        msw: { handlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const prompt = await canvas.findByTestId(languagePromptGroup.id);
        prompt.click();
        await canvas.findByTestId("startNewChatButton");
    },
};

export const WithSearchTerm: Story = {
    parameters: {
        msw: { handlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const search = await canvas.findByTestId("promptSearchInput");
        await userEvent.type(search, "language");

        const promptGroup = await canvas.findByTestId(languagePromptGroup.id);
        expect(promptGroup).toBeVisible();
    },
};

export const WithSearchTermNoResults: Story = {
    parameters: {
        msw: { handlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const search = await canvas.findByTestId("promptSearchInput");
        await userEvent.type(search, "asdf");

        expect(canvas.findByTestId("startNewChatButton", {}, { timeout: 1000 })).rejects.toThrowError();
    },
};

export const WithTagSelected: Story = {
    parameters: {
        msw: { handlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const tags = await canvas.findByTestId("promptTags");
        tags.click();
        const communicationFilter = await canvas.findByTestId(`category-checkbox-${languagePromptGroup.category}`);
        communicationFilter.click();
        expect(communicationFilter).toBeChecked();

        const clearFiltersButton = await canvas.findByTestId("clearFiltersButton");
        expect(clearFiltersButton).toBeVisible();

        const promptGroup = await canvas.findByTestId(languagePromptGroup.id);
        expect(promptGroup).toBeVisible();
    },
};

export const NoPrompts: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/prompts/groups/all", () => {
                    return HttpResponse.json([]);
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("Build with AI");
    },
};
