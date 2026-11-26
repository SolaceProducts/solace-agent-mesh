// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, fn, within } from "storybook/test";

import { withDefaults } from "@/testing/helpers";

import { PromptBuilderChat } from "./PromptBuilderChat";

const initHandler = http.get("*/api/v1/prompts/chat/init", () => {
    return HttpResponse.json({
        message: "Hi! I'll help you create a prompt template. What kind of task would you like to template?",
    });
});

const successHandler = http.post("*/api/v1/prompts/chat", () => {
    return HttpResponse.json({
        message: "I'll create a code review template for you. What information changes each time?",
        template_updates: { name: "Code Review Template", category: "Development" },
        confidence: 0.8,
        ready_to_save: false,
        is_error: false,
    });
});

const authErrorHandler = http.post("*/api/v1/prompts/chat", () => {
    return HttpResponse.json({
        message: "The LLM service rejected the authentication credentials. Contact an administrator to verify the API key or authentication configuration.",
        template_updates: {},
        confidence: 0.0,
        ready_to_save: false,
        is_error: true,
    });
});

const rateLimitErrorHandler = http.post("*/api/v1/prompts/chat", () => {
    return HttpResponse.json({
        message: "The LLM service rate limit has been exceeded. Wait a moment and try again. If this persists, contact an administrator to review rate limits or adjust the plan.",
        template_updates: {},
        confidence: 0.0,
        ready_to_save: false,
        is_error: true,
    });
});

const timeoutErrorHandler = http.post("*/api/v1/prompts/chat", () => {
    return HttpResponse.json({
        message: "The request to the LLM service timed out. This may be due to high load or a complex request. Try again. If this persists, contact an administrator.",
        template_updates: {},
        confidence: 0.0,
        ready_to_save: false,
        is_error: true,
    });
});

const modelNotFoundErrorHandler = http.post("*/api/v1/prompts/chat", () => {
    return HttpResponse.json({
        message: "The configured LLM model was not found. Contact an administrator to verify the model name and provider configuration.",
        template_updates: {},
        confidence: 0.0,
        ready_to_save: false,
        is_error: true,
    });
});

const connectionErrorHandler = http.post("*/api/v1/prompts/chat", () => {
    return HttpResponse.json({
        message: "Unable to connect to the LLM service. This may be due to a network issue or incorrect endpoint configuration. Contact an administrator to verify the connection settings.",
        template_updates: {},
        confidence: 0.0,
        ready_to_save: false,
        is_error: true,
    });
});

const meta = {
    title: "Prompts/PromptBuilderChat",
    component: PromptBuilderChat,
    parameters: {
        docs: {
            description: {
                component: "AI-assisted prompt builder chat with error state styling. Error messages from the LLM service are displayed with red border, background, and an alert icon.",
            },
        },
        msw: { handlers: withDefaults(initHandler, successHandler) },
    },
    args: {
        onConfigUpdate: fn(),
        onReadyToSave: fn(),
        currentConfig: {
            name: "",
            description: "",
            category: "",
            command: "",
            promptText: "",
        },
    },
    decorators: [
        Story => (
            <div className="h-150 w-125 overflow-hidden rounded-xl border border-(--secondary-w20)">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof PromptBuilderChat>;

export default meta;
type Story = StoryObj<typeof PromptBuilderChat>;

const USER_PROMPT = "I want a template for weekly code reviews that includes the PR link, reviewer name, and summary of changes.";

async function sendMessage(canvas: ReturnType<typeof within>) {
    const textarea = await canvas.findByRole("textbox");
    fireEvent.change(textarea, { target: { value: USER_PROMPT } });

    const sendButton = canvas.getByRole("button", { name: /send message/i });
    fireEvent.click(sendButton);
}

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const heading = await canvas.findByText("AI Builder");
        expect(heading).toBeVisible();

        await canvas.findByText(/I'll help you create a prompt template/);

        await sendMessage(canvas);

        await canvas.findByText(USER_PROMPT);

        const response = await canvas.findByText(/I'll create a code review template for you/);
        expect(response).toBeVisible();

        const textarea = canvas.getByRole("textbox");
        expect(textarea).toHaveValue("");
        expect(textarea).not.toBeDisabled();
    },
};

export const AuthenticationError: Story = {
    parameters: {
        docs: {
            description: {
                story: "Shows the error state when the LLM service rejects authentication credentials. The error message is displayed with red styling and an alert icon.",
            },
        },
        msw: { handlers: withDefaults(initHandler, authErrorHandler) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await sendMessage(canvas);

        const errorMsg = await canvas.findByText(/LLM service rejected the authentication credentials/);
        expect(errorMsg).toBeVisible();

        expect(canvas.getByText(USER_PROMPT)).toBeVisible();

        const textarea = canvas.getByRole("textbox");
        expect(textarea).not.toBeDisabled();
    },
};

export const RateLimitError: Story = {
    parameters: {
        docs: {
            description: {
                story: "Shows the error state when the LLM rate limit is exceeded.",
            },
        },
        msw: { handlers: withDefaults(initHandler, rateLimitErrorHandler) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await sendMessage(canvas);

        const errorMsg = await canvas.findByText(/rate limit has been exceeded/);
        expect(errorMsg).toBeVisible();
    },
};

export const TimeoutError: Story = {
    parameters: {
        docs: {
            description: {
                story: "Shows the error state when the LLM request times out.",
            },
        },
        msw: { handlers: withDefaults(initHandler, timeoutErrorHandler) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await sendMessage(canvas);

        const errorMsg = await canvas.findByText(/request to the LLM service timed out/);
        expect(errorMsg).toBeVisible();
    },
};

export const ModelNotFoundError: Story = {
    parameters: {
        docs: {
            description: {
                story: "Shows the error state when the configured LLM model is not found.",
            },
        },
        msw: { handlers: withDefaults(initHandler, modelNotFoundErrorHandler) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await sendMessage(canvas);

        const errorMsg = await canvas.findByText(/configured LLM model was not found/);
        expect(errorMsg).toBeVisible();
    },
};

export const ConnectionError: Story = {
    parameters: {
        docs: {
            description: {
                story: "Shows the error state when the LLM service cannot be reached.",
            },
        },
        msw: { handlers: withDefaults(initHandler, connectionErrorHandler) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await sendMessage(canvas);

        const errorMsg = await canvas.findByText(/Unable to connect to the LLM service/);
        expect(errorMsg).toBeVisible();
    },
};
