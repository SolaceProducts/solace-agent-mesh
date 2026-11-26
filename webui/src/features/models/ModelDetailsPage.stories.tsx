// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen } from "storybook/test";

import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { ModelDetailsPage } from "./ModelDetailsPage";

const mockModelConfigs = [{ ...anthropicModelConfig, pricing: { input: "3", output: "15", cacheRead: null, cacheWrite: null } }];

const successHandlers = withDefaults(
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.json({ data: mockModelConfigs.map(toModelV2), meta: getPaginatedMeta(mockModelConfigs.length) });
    })
);

const notFoundHandlers = withDefaults(
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.json({ data: [], meta: getPaginatedMeta(0) });
    })
);

const meta = {
    title: "Models/ModelDetailsPage",
    component: ModelDetailsPage,
    parameters: {
        docs: {
            description: {
                component: "Detailed view of a single model configuration showing authentication type, parameters, and metadata.",
            },
        },
    },
} satisfies Meta<typeof ModelDetailsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WithAPIKeyAuth: Story = {
    parameters: {
        msw: { handlers: successHandlers },
        routerValues: {
            initialPath: `/models/${anthropicModelConfig.id}`,
            routePath: "/models/:id",
        },
    },
    play: async () => {
        const aliasElements = await screen.findAllByText("anthropic-model");
        await expect(aliasElements.length).toBeGreaterThan(0);

        await expect(await screen.findByText("Anthropic")).toBeInTheDocument();

        await expect(await screen.findByText("claude-3-5-sonnet")).toBeInTheDocument();
        await expect(screen.queryByText("Model Prefix")).not.toBeInTheDocument();

        await expect(await screen.findByText("Enterprise-grade planning model with prompt caching for cost optimization")).toBeInTheDocument();

        await expect(await screen.findByText("API Key")).toBeInTheDocument();

        await expect(await screen.findByText("Chat (LLM) model")).toBeInTheDocument();

        await expect(await screen.findByText("https://api.anthropic.com")).toBeInTheDocument();

        await expect(await screen.findByText("temperature:")).toBeInTheDocument();
        await expect(await screen.findByText("max_tokens:")).toBeInTheDocument();

        await expect(await screen.findByText("Created By:")).toBeInTheDocument();
        await expect(await screen.findByText("ID:")).toBeInTheDocument();
    },
};

export const DecisionModel: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models", () => {
                    return HttpResponse.json({ data: [toModelV2({ ...typesafeDecisionModelConfig, pricing: { input: "2", output: "8", cacheRead: null, cacheWrite: null } })], meta: getPaginatedMeta(1) });
                }),
                http.get("*/api/v1/config/capabilities", () => {
                    return HttpResponse.json({
                        data: [{ key: "workflow_decision_node", name: "Workflow Decision Nodes", description: "", status: "early_access", platform: "darwin", hasEnvOverride: false }],
                        meta: getPaginatedMeta(1),
                    });
                })
            ),
        },
        routerValues: {
            initialPath: `/models/${typesafeDecisionModelConfig.id}`,
            routePath: "/models/:id",
        },
    },
    play: async () => {
        await expect(await screen.findByText("TypeSafe")).toBeInTheDocument();
        await expect(await screen.findByText("Model Type")).toBeInTheDocument();
        await expect(await screen.findByText("Decision model")).toBeInTheDocument();
        await expect(await screen.findByText("EARLY ACCESS")).toBeInTheDocument();
        await expect(screen.queryByText("Model Prefix")).not.toBeInTheDocument();
    },
};

const customGatewayModel = { ...anthropicModelConfig, id: "custom-gateway", alias: "gateway-llama", provider: "custom", modelName: "openai/meta-llama/Llama-3-70b", apiBase: "https://gateway.example.com/llms/v1" };

export const CustomModelPrefix: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models", () => {
                    return HttpResponse.json({ data: [toModelV2(customGatewayModel)], meta: getPaginatedMeta(1) });
                })
            ),
        },
        routerValues: {
            initialPath: `/models/${customGatewayModel.id}`,
            routePath: "/models/:id",
        },
    },
    play: async () => {
        await expect(await screen.findByText("Model Prefix")).toBeInTheDocument();
        await expect(await screen.findByText("openai/")).toBeInTheDocument();
        await expect(await screen.findByText("meta-llama/Llama-3-70b")).toBeInTheDocument();
    },
};

const customGatewayClaudeModel = { ...customGatewayModel, id: "custom-gateway-claude", alias: "gateway-claude", modelName: "litellm_proxy/claude-sonnet-4-6" };

export const CustomModelUnroutedNamespace: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/models", () => {
                    return HttpResponse.json({ data: [toModelV2(customGatewayClaudeModel)], meta: getPaginatedMeta(1) });
                })
            ),
        },
        routerValues: {
            initialPath: `/models/${customGatewayClaudeModel.id}`,
            routePath: "/models/:id",
        },
    },
    play: async () => {
        await expect(await screen.findByText("Model Prefix")).toBeInTheDocument();
        await expect(await screen.findByText("anthropic/")).toBeInTheDocument();
        await expect(await screen.findByText("litellm_proxy/claude-sonnet-4-6")).toBeInTheDocument();
    },
};

export const NotFound: Story = {
    parameters: {
        msw: { handlers: notFoundHandlers },
        routerValues: {
            initialPath: "/models/nonexistent-uuid",
            routePath: "/models/:id",
        },
    },
    play: async () => {
        await expect(await screen.findByText("Model Not Found")).toBeInTheDocument();
        await expect(await screen.findByRole("button", { name: "Go To Models" })).toBeInTheDocument();
    },
};
