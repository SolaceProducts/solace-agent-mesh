// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { anthropicModelConfig, mockModelConfigs } from "@/testing/data/models";
import { savedModelV2, toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { getPaginatedMeta, paginatedResponse, withDefaults } from "@/testing/helpers";

import { ModelEditPage } from "./ModelEditPage";

const supportedModels = [
    { id: "claude-3-5-sonnet", label: "Claude 3.5 Sonnet" },
    { id: "claude-3-opus", label: "Claude 3 Opus" },
    { id: "claude-3-haiku", label: "Claude 3 Haiku" },
];

const supportedModelsHandler = http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => HttpResponse.json(paginatedResponse(supportedModels.map(toSupportedModelV2(String(params.provider))))));

const createNewModelHandlers = [
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.json({
            data: mockModelConfigs.map(toModelV2),
            meta: getPaginatedMeta(mockModelConfigs.length),
        });
    }),
    supportedModelsHandler,
    http.post("*/api/v2/platform/models", async ({ request }) => HttpResponse.json({ data: savedModelV2("new-model", await request.json()) }, { status: 201 })),
];

const editModelHandlers = [
    http.get("*/api/v2/platform/models", () => {
        return HttpResponse.json({
            data: [toModelV2(anthropicModelConfig)],
            meta: getPaginatedMeta(1),
        });
    }),
    http.get("*/api/v2/platform/models/:id", ({ params }) => {
        if (params.id === anthropicModelConfig.id) {
            return HttpResponse.json({ data: toModelV2(anthropicModelConfig) });
        }
        return HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
    }),
    supportedModelsHandler,
    http.patch("*/api/v2/platform/models/:id", async ({ request }) => HttpResponse.json({ data: savedModelV2(anthropicModelConfig.id, await request.json(), { ...anthropicModelConfig, updatedBy: "test-user" }) })),
];

const pricedAnthropicModelConfig = { ...anthropicModelConfig, pricing: { input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" } };

const pricedModelHandlers = [
    http.get("*/api/v2/platform/models", () => HttpResponse.json({ data: [toModelV2(pricedAnthropicModelConfig)], meta: getPaginatedMeta(1) })),
    http.get(`*/api/v2/platform/models/${anthropicModelConfig.id}`, () => HttpResponse.json({ data: toModelV2(pricedAnthropicModelConfig) })),
];

const loadingHandlers = [
    http.get("*/api/v2/platform/models/:id", async () => {
        await delay("infinite");
        return HttpResponse.json({});
    }),
];

const testConnectionSuccessHandler = http.put("*/api/v2/platform/models/testConnection", () => {
    return HttpResponse.json({ data: { success: true, message: "Connection successful. Model responded with: OK", errorClass: null } });
});

const testConnectionFailureHandler = http.put("*/api/v2/platform/models/testConnection", () => {
    return HttpResponse.json({ data: { success: false, message: "Authentication failed: Invalid API key provided", errorClass: "auth_credentials_invalid" } });
});

const notFoundHandlers = [
    http.get("*/api/v2/platform/models/:id", () => {
        return HttpResponse.json({ message: "Model not found", errorId: "test-model-not-found" }, { status: 404 });
    }),
];

const meta = {
    title: "Models/ModelEditPage",
    component: ModelEditPage,
    parameters: {
        docs: {
            description: {
                component: "Form for creating or editing model configurations with provider selection, authentication setup, and advanced parameters.",
            },
        },
    },
} satisfies Meta<typeof ModelEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CreateNewModel: Story = {
    parameters: {
        msw: { handlers: withDefaults(...createNewModelHandlers) },
        routerValues: {
            initialPath: "/models/new/edit",
            routePath: "/models/new/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const createModelTexts = await canvas.findAllByText("Add Model");
        expect(createModelTexts.length).toBeGreaterThanOrEqual(1);

        await expect(await canvas.findByText("Display Name")).toBeInTheDocument();
        await expect(await canvas.findByText("Description")).toBeInTheDocument();
        await expect(await canvas.findByText("Model Provider")).toBeInTheDocument();

        const addButton = await canvas.findByRole("button", { name: /Add/i });
        expect(addButton).not.toBeDisabled();
    },
};

const decisionNodesCapabilityHandler = http.get("*/api/v1/config/capabilities", () =>
    HttpResponse.json({
        data: [{ key: "workflow_decision_node", name: "Workflow Decision Nodes", description: "", status: "early_access", platform: "darwin", hasEnvOverride: false }],
        meta: getPaginatedMeta(1),
    })
);

export const CreateModelChoosesTypeFirst: Story = {
    parameters: {
        featureContext: { workflow_decision_node: true },
        msw: { handlers: withDefaults(decisionNodesCapabilityHandler, ...createNewModelHandlers) },
        routerValues: {
            initialPath: "/models/new/edit",
            routePath: "/models/new/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const chatType = await canvas.findByRole("radio", { name: /Chat \(LLM\) model/ });
        const decisionType = canvas.getByRole("radio", { name: /Decision model/ });
        expect(chatType).toBeChecked();
        expect(await within(decisionType.closest("label") as HTMLElement).findByText("EARLY ACCESS")).toBeInTheDocument();

        await userEvent.click(decisionType);
        expect(decisionType).toBeChecked();
        await userEvent.click(canvas.getAllByRole("combobox")[0]);
        expect(await within(document.body).findByText("OpenAI")).toBeInTheDocument();
        await userEvent.click(await within(document.body).findByText("TypeSafe"));
        await waitFor(() => expect(canvasElement.querySelector('input[name="apiBase"]')?.getAttribute("placeholder")).toBe("https://api.typesafe.ai"));
    },
};

export const CreateAnthropicModel: Story = {
    parameters: {
        msw: { handlers: withDefaults(...createNewModelHandlers) },
        routerValues: {
            initialPath: "/models/new/edit",
            routePath: "/models/new/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const aliasInput = canvasElement.querySelector('input[name="alias"]') as HTMLInputElement;
        expect(aliasInput).toBeTruthy();
        await userEvent.clear(aliasInput);
        await userEvent.type(aliasInput, "my-planning-model");

        const descInput = canvasElement.querySelector('textarea[name="description"]') as HTMLTextAreaElement;
        expect(descInput).toBeTruthy();
        await userEvent.clear(descInput);
        await userEvent.type(descInput, "Custom planning model for complex reasoning tasks");

        const providerComboboxes = await canvas.findAllByRole("combobox");
        const providerCombobox = providerComboboxes[0];
        await userEvent.click(providerCombobox);

        const anthropicOption = await screen.findByText("Anthropic");
        await userEvent.click(anthropicOption);

        const apiKeyLabels = await canvas.findAllByText("API Key");
        expect(apiKeyLabels.length).toBeGreaterThan(0);

        const apiKeyField = canvasElement.querySelector('input[name="apiKey"]') as HTMLInputElement;
        expect(apiKeyField).toBeTruthy();
        await userEvent.type(apiKeyField, "sk-ant-test-key-123456");

        const modelNameCombo = await canvas.findByPlaceholderText(/Type or select a model/i);
        await userEvent.click(modelNameCombo);
        await expect(await screen.findByText("claude-3-5-sonnet")).toBeInTheDocument();
    },
};

export const EditExistingModel: Story = {
    parameters: {
        msw: { handlers: withDefaults(...pricedModelHandlers, ...editModelHandlers) },
        routerValues: {
            initialPath: `/models/${anthropicModelConfig.id}/edit`,
            routePath: "/models/:id/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editTexts = await canvas.findAllByText("Edit anthropic-model");
        expect(editTexts.length).toBeGreaterThanOrEqual(1);

        const aliasInput = canvasElement.querySelector('input[name="alias"]') as HTMLInputElement;
        expect(aliasInput).toBeTruthy();
        expect(aliasInput.value).toBe("anthropic-model");

        const descInput = canvasElement.querySelector('textarea[name="description"]') as HTMLTextAreaElement;
        expect(descInput).toBeTruthy();
        expect(descInput.value).toContain("planning model");

        const providerElement = await canvas.findByDisplayValue("Anthropic");
        expect(providerElement).toBeInTheDocument();

        const modelNameInput = (await canvas.findByDisplayValue(/claude/i)) as HTMLInputElement;
        expect(modelNameInput.value).toBe("claude-3-5-sonnet");

        const advancedSummary = await canvas.findByText("Advanced Settings");
        expect(advancedSummary).toBeInTheDocument();

        expect(canvas.getByRole("button", { name: "Max Input Tokens help" })).toBeInTheDocument();
    },
};

export const EditModelLoading: Story = {
    parameters: {
        msw: { handlers: withDefaults(...loadingHandlers) },
        routerValues: {
            initialPath: "/models/some-uuid/edit",
            routePath: "/models/:id/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await expect(await canvas.findByText("Loading Model...")).toBeInTheDocument();
    },
};

export const EditModelNotFound: Story = {
    parameters: {
        msw: { handlers: withDefaults(...notFoundHandlers) },
        routerValues: {
            initialPath: "/models/nonexistent-uuid/edit",
            routePath: "/models/:id/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await expect(await canvas.findByText(/Error loading model:/)).toBeInTheDocument();

        const goToButton = await canvas.findByRole("button", { name: /Go To Models/i });
        expect(goToButton).toBeInTheDocument();
    },
};

export const EditModelWithAdvancedParams: Story = {
    parameters: {
        msw: { handlers: withDefaults(...editModelHandlers) },
        routerValues: {
            initialPath: `/models/${anthropicModelConfig.id}/edit`,
            routePath: "/models/:id/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editTexts = await canvas.findAllByText("Edit anthropic-model");
        expect(editTexts.length).toBeGreaterThanOrEqual(1);

        const advancedSummary = await canvas.findByText("Advanced Settings");
        const accordionTrigger = advancedSummary.closest("button");
        expect(accordionTrigger).toHaveAttribute("data-state", "closed");

        await userEvent.click(advancedSummary);

        expect(canvasElement.querySelector('input[name="temperature"]')).toBeNull();

        const maxTokensInput = canvasElement.querySelector('input[name="max_tokens"]') as HTMLInputElement;
        expect(maxTokensInput).toBeTruthy();
        expect(maxTokensInput.value).toBe("4096");

        await expect(await canvas.findByText("Custom Parameters")).toBeInTheDocument();
        const customKeys = Array.from(canvasElement.querySelectorAll<HTMLInputElement>('input[aria-label^="Key"]')).map(el => el.value);
        expect(customKeys).toContain("temperature");

        const newPairButton = await canvas.findByRole("button", { name: /New Pair/i });
        expect(newPairButton).not.toBeDisabled();
        await userEvent.click(newPairButton);

        const keyInput = await canvas.findByLabelText("Key 1");
        await userEvent.type(keyInput, "my_param");

        const saveButton = await canvas.findByRole("button", { name: /Save/i });
        await userEvent.click(saveButton);
    },
};

export const CreateCustomProviderModel: Story = {
    parameters: {
        msw: { handlers: withDefaults(...createNewModelHandlers) },
        routerValues: {
            initialPath: "/models/new/edit",
            routePath: "/models/new/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const aliasInput = canvasElement.querySelector('input[name="alias"]') as HTMLInputElement;
        expect(aliasInput).toBeTruthy();
        await userEvent.type(aliasInput, "custom-llm");

        const descInput = canvasElement.querySelector('textarea[name="description"]') as HTMLTextAreaElement;
        expect(descInput).toBeTruthy();
        await userEvent.type(descInput, "Custom OpenAI-compatible provider");

        const providerComboboxes = await canvas.findAllByRole("combobox");
        const providerCombobox = providerComboboxes[0];
        await userEvent.click(providerCombobox);

        const customOption = await screen.findByText("Custom");
        await userEvent.click(customOption);

        const apiBaseInput = canvasElement.querySelector('input[name="apiBase"]') as HTMLInputElement;
        expect(apiBaseInput).toBeTruthy();

        await userEvent.type(apiBaseInput, "https://my-api.example.com");
    },
};

export const CreateAzureModel: Story = {
    parameters: {
        msw: { handlers: withDefaults(...createNewModelHandlers) },
        routerValues: {
            initialPath: "/models/new/edit",
            routePath: "/models/new/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const providerComboboxes = await canvas.findAllByRole("combobox");
        await userEvent.click(providerComboboxes[0]);

        const azureOption = await screen.findByText("Microsoft Foundry");
        await userEvent.click(azureOption);

        await canvas.findByText("Deployment Name");
        expect(canvas.queryByText("Model Name")).not.toBeInTheDocument();
        expect(await canvas.findByPlaceholderText(/Type or select a deployment/i)).toBeInTheDocument();
    },
};

export const TestConnectionSuccess: Story = {
    parameters: {
        msw: { handlers: withDefaults(...editModelHandlers, testConnectionSuccessHandler) },
        routerValues: {
            initialPath: `/models/${anthropicModelConfig.id}/edit`,
            routePath: "/models/:id/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Edit anthropic-model");

        const testButton = await canvas.findByTestId("test-connection-button");
        expect(testButton).toBeInTheDocument();
        expect(testButton).not.toBeDisabled();

        await userEvent.click(testButton);

        const banner = await canvas.findByText(/Connection test successful/i);
        expect(banner).toBeInTheDocument();
    },
};

export const TestConnectionFailure: Story = {
    parameters: {
        msw: { handlers: withDefaults(...editModelHandlers, testConnectionFailureHandler) },
        routerValues: {
            initialPath: `/models/${anthropicModelConfig.id}/edit`,
            routePath: "/models/:id/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Edit anthropic-model");

        const testButton = await canvas.findByTestId("test-connection-button");
        await userEvent.click(testButton);

        const banner = await canvas.findByText(/Authentication failed/i);
        expect(banner).toBeInTheDocument();
    },
};

export const TestConnectionButtonDisabled: Story = {
    parameters: {
        msw: { handlers: withDefaults(...createNewModelHandlers) },
        routerValues: {
            initialPath: "/models/new/edit",
            routePath: "/models/new/edit",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Add Model");

        const providerComboboxes = await canvas.findAllByRole("combobox");
        await userEvent.click(providerComboboxes[0]);
        const anthropicOption = await screen.findByText("Anthropic");
        await userEvent.click(anthropicOption);

        const testButton = await canvas.findByTestId("test-connection-button");
        expect(testButton).toBeDisabled();
    },
};
