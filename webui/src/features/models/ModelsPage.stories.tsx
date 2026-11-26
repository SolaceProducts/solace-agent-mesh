// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { mockModelConfigs, mockModelConfigsWithUnconfiguredDefaults } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { ModelsPage } from "./ModelsPage";

const successHandlers = withDefaults(
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.json({ data: mockModelConfigs.map(toModelV2), meta: getPaginatedMeta(mockModelConfigs.length) });
    })
);

const loadingHandlers = withDefaults(
    http.get("*/api/v2/platform/models", async () => {
        await delay("infinite");
        return HttpResponse.json({ data: [], meta: getPaginatedMeta(0) });
    })
);

const emptyHandlers = withDefaults(
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.json({ data: [], meta: getPaginatedMeta(0) });
    })
);

const errorHandlers = withDefaults(
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.error();
    })
);

const meta = {
    title: "Models/ModelsPage",
    component: ModelsPage,
    parameters: {
        docs: {
            description: {
                component: "Model configurations list view with pagination and filtering.",
            },
        },
    },
} satisfies Meta<typeof ModelsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: { handlers: successHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Planning");
        await canvas.findByText("General");
        await canvas.findByText("Image Gen");

        expect(canvas.getByText("Name")).toBeInTheDocument();
        expect(canvas.getByText("Model")).toBeInTheDocument();
        expect(canvas.getByText("Model Provider")).toBeInTheDocument();

        expect(canvas.queryByRole("navigation", { name: /pagination/i })).not.toBeInTheDocument();

        const defaultBadges = await canvas.findAllByText("Default");
        expect(defaultBadges.length).toBe(2);

        const generalRow = canvas.getByRole("row", { name: /General/i });
        const planningRow = canvas.getByRole("row", { name: /Planning/i });

        const generalBadge = within(generalRow).getByText("Default");
        const planningBadge = within(planningRow).getByText("Default");

        expect(generalBadge).toBeInTheDocument();
        expect(planningBadge).toBeInTheDocument();

        const addModelButton = canvas.getByRole("button", { name: /Add Model/i });
        expect(addModelButton).toBeInTheDocument();
    },
};

export const Loading: Story = {
    parameters: {
        msw: { handlers: loadingHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Loading Models...");
        expect(canvas.getByText("Loading Models...")).toBeInTheDocument();
    },
};

export const Empty: Story = {
    parameters: {
        msw: { handlers: emptyHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Match AI Models to Your Team's Workflows");
        expect(canvas.getByText("Match AI Models to Your Team's Workflows")).toBeInTheDocument();
    },
};

export const Error: Story = {
    parameters: {
        msw: { handlers: errorHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText(/Error loading models/);
        expect(canvas.getByText(/Error loading models/)).toBeInTheDocument();
    },
};

export const Sorting: Story = {
    parameters: {
        msw: { handlers: successHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Aws");

        const rows = canvas.getAllByRole("row");
        expect(within(rows[1]).getByText("Aws")).toBeInTheDocument();
        expect(within(rows.at(-1)!).getByText("Vertex")).toBeInTheDocument();

        expect(canvas.getByRole("button", { name: "Name" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Model" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Model Provider" })).toBeInTheDocument();

        await userEvent.click(canvas.getByRole("button", { name: "Name" }));

        await canvas.findByText("Vertex");
        const rowsDesc = canvas.getAllByRole("row");
        expect(within(rowsDesc[1]).getByText("Vertex")).toBeInTheDocument();
        expect(within(rowsDesc.at(-1)!).getByText("Aws")).toBeInTheDocument();

        await userEvent.click(canvas.getByRole("button", { name: "Name" }));
        await canvas.findByText("Aws");
        const rowsAsc = canvas.getAllByRole("row");
        expect(within(rowsAsc[1]).getByText("Aws")).toBeInTheDocument();
    },
};

export const WithPagination: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models", () => {
                    const data = Array.from({ length: 45 }, (_, i) => ({
                        id: `model-${i}`,
                        alias: `model-${i}`,
                        provider: i % 3 === 0 ? "anthropic" : i % 3 === 1 ? "openai" : "vertex_ai",
                        modelName: `test-model-${i}`,
                        apiBase: "https://api.example.com",
                        authType: "apikey",
                        authConfig: { type: "apikey" },
                        modelParams: {},
                        maxInputTokens: null,
                        description: `Test model ${i}`,
                        createdBy: "system",
                        updatedBy: "system",
                        createdAt: new Date().toISOString(),
                        updatedAt: new Date().toISOString(),
                    }));
                    return HttpResponse.json({
                        data: data.map(toModelV2),
                        meta: getPaginatedMeta(data.length),
                    });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Model-0");
        await canvas.findByText("Model-19");
        expect(canvas.getByText("Model-0")).toBeInTheDocument();

        const paginationNav = canvas.getByRole("navigation", { name: /pagination/i });
        expect(paginationNav).toBeInTheDocument();
    },
};

export const UnconfiguredDefaults: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models", () => {
                    return HttpResponse.json({ data: mockModelConfigsWithUnconfiguredDefaults.map(toModelV2), meta: getPaginatedMeta(mockModelConfigsWithUnconfiguredDefaults.length) });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("General");
        await canvas.findByText("Planning");

        const badges = canvas.getAllByText("Default");
        expect(badges.length).toBeGreaterThanOrEqual(2);

        const notConfiguredTexts = canvas.getAllByText("Not configured");
        expect(notConfiguredTexts.length).toBeGreaterThanOrEqual(2);

        const warningIcons = canvasElement.querySelectorAll(".text-\\(--warning-wMain\\)");
        expect(warningIcons.length).toBe(2);

        expect(canvas.getByText("Image Gen")).toBeInTheDocument();
        expect(canvas.getByText("dall-e-3")).toBeInTheDocument();
    },
};

const pricedModelConfigs = mockModelConfigs.map((model, index) => (index < 2 ? { ...model, pricing: { input: "3", output: "15", cacheRead: index === 0 ? "0.3" : null, cacheWrite: index === 0 ? "3.75" : null } } : model));

export const WithPricing: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models", () => {
                    return HttpResponse.json({ data: pricedModelConfigs.map(toModelV2), meta: getPaginatedMeta(pricedModelConfigs.length) });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("General");
        expect(canvas.getByRole("columnheader", { name: "Input" })).toBeInTheDocument();
    },
};
