// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, screen, within } from "storybook/test";

import { SAFE_TEXT_DESCRIPTION } from "@/common/form";

import { withDefaults } from "@/testing/helpers";

import { AgentCreateFromPromptDialog, MAX_AGENT_DESCRIPTION_LENGTH } from "./AgentCreateFromPromptDialog";

const meta = {
    title: "Agents/AgentCreateFromPromptDialog",
    component: AgentCreateFromPromptDialog,
    parameters: {
        docs: {
            description: {
                component: "The dialog component that allows users to create an agent from a prompt using AI generation.",
            },
        },
    },
} satisfies Meta<typeof AgentCreateFromPromptDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();

        await within(dialog).findByText("Create Agent");
        await within(dialog).findByTestId("agent-prompt");
        await within(dialog).findByTestId("generateAgent");
        await within(dialog).findByTestId("createManually");
        await within(dialog).findByTestId("cancelAgent");
    },
};

export const ValidationTooShort: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog)
            .findByTestId("agent-prompt")
            .then(input => {
                fireEvent.change(input, { target: { value: "Too short" } });
            });
        await within(dialog)
            .findByTestId("generateAgent")
            .then(button => {
                fireEvent.click(button);
            });
        await screen.findByText("Value must be at least 10 characters");
    },
};

export const ValidationTooLong: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog)
            .findByTestId("agent-prompt")
            .then(input => {
                fireEvent.change(input, { target: { value: "A".repeat(1001) } });
            });
        await within(dialog)
            .findByTestId("generateAgent")
            .then(button => {
                fireEvent.click(button);
            });
        await screen.findByText(`Value must be ${MAX_AGENT_DESCRIPTION_LENGTH} characters or less`);
    },
};

export const ValidationControlCharacters: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog)
            .findByTestId("agent-prompt")
            .then(input => {
                fireEvent.change(input, { target: { value: "An agent that can\x08 help with tasks" } });
            });
        await within(dialog)
            .findByTestId("generateAgent")
            .then(button => {
                fireEvent.click(button);
            });
        await screen.findByText(SAFE_TEXT_DESCRIPTION);
    },
};

export const WithPrefillPrompt: Story = {
    args: {
        isOpen: true,
        prompt: "An agent that helps users analyze financial data and create reports",
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const promptInput = await within(dialog).findByTestId("agent-prompt");

        expect(promptInput).toHaveValue("An agent that helps users analyze financial data and create reports");
    },
};

export const RegenerateMode: Story = {
    args: {
        isOpen: true,
        isRegenerating: true,
        prompt: "An agent that helps with customer support",
        onCreate: () => {},
        onClose: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");

        await within(dialog).findByText("Regenerate Agent");
        await within(dialog).findByText(/Regenerating may not keep any of the existing agent/);

        expect(within(dialog).queryByTestId("createManually")).toBeNull();

        const generateButton = await within(dialog).findByTestId("generateAgent");
        expect(generateButton).toHaveTextContent("Regenerate");
    },
};

export const CharacterCount: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        const promptInput = await within(dialog).findByTestId("agent-prompt");

        fireEvent.change(promptInput, { target: { value: "An agent that helps with data analysis" } });

        await within(dialog).findByText("38/1000");
    },
};

export const CreateManuallyButton: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");

        const createManuallyButton = await within(dialog).findByTestId("createManually");
        expect(createManuallyButton).toBeEnabled();
        expect(createManuallyButton).toHaveTextContent("Create Manually");
    },
};

const VALID_PROMPT = "An agent that helps users analyze financial data, generate reports, and track budget spending across departments";

const submitValidPrompt = async () => {
    const dialog = await screen.findByRole("dialog");
    const promptInput = await within(dialog).findByTestId("agent-prompt");
    fireEvent.change(promptInput, { target: { value: VALID_PROMPT } });
    const generateButton = await within(dialog).findByTestId("generateAgent");
    fireEvent.click(generateButton);
    return dialog;
};

export const ErrorAuthenticationFailure: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/internal/platform/aiAssistant/generateAgent", () => {
                    return HttpResponse.json({ message: "The LLM service rejected the authentication credentials. Please contact an administrator to verify the API key or authentication configuration." }, { status: 503 });
                })
            ),
        },
    },
    play: async () => {
        const dialog = await submitValidPrompt();

        const banner = await within(dialog).findByText(/LLM service rejected the authentication credentials/);
        expect(banner).toBeInTheDocument();

        const promptInput = await within(dialog).findByTestId("agent-prompt");
        expect(promptInput).toHaveValue(VALID_PROMPT);

        const generateButton = await within(dialog).findByTestId("generateAgent");
        expect(generateButton).not.toBeDisabled();
    },
};

export const ErrorRateLimit: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/internal/platform/aiAssistant/generateAgent", () => {
                    return HttpResponse.json({ message: "The LLM service rate limit has been exceeded. Please wait a moment and try again. If this persists, contact an administrator to review rate limits or upgrade the plan." }, { status: 503 });
                })
            ),
        },
    },
    play: async () => {
        const dialog = await submitValidPrompt();
        await within(dialog).findByText(/rate limit has been exceeded/);
    },
};

export const ErrorConnectionFailure: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/internal/platform/aiAssistant/generateAgent", () => {
                    return HttpResponse.json(
                        { message: "Unable to connect to the LLM service. This may be due to a network issue or incorrect endpoint configuration. Please contact an administrator to verify the connection settings." },
                        { status: 503 }
                    );
                })
            ),
        },
    },
    play: async () => {
        const dialog = await submitValidPrompt();
        await within(dialog).findByText(/Unable to connect to the LLM service/);
    },
};

export const ErrorGenericServerError: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/internal/platform/aiAssistant/generateAgent", () => {
                    return HttpResponse.json({ message: "An error occurred while communicating with the LLM service. Please try again. If the problem persists, contact an administrator." }, { status: 503 });
                })
            ),
        },
    },
    play: async () => {
        const dialog = await submitValidPrompt();
        await within(dialog).findByText(/error occurred while communicating with the LLM service/);
    },
};

export const ErrorBannerDismissible: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/internal/platform/aiAssistant/generateAgent", () => {
                    return HttpResponse.json({ message: "Service is unavailable." }, { status: 503 });
                })
            ),
        },
    },
    play: async () => {
        const dialog = await submitValidPrompt();

        await within(dialog).findByText("Service is unavailable.");

        const dismissButton = within(dialog).getByRole("button", { name: /dismiss/i });
        fireEvent.click(dismissButton);

        expect(within(dialog).queryByText("Service is unavailable.")).toBeNull();
    },
};

export const ErrorInsufficientDescription: Story = {
    args: {
        isOpen: true,
        onCreate: () => {},
        onClose: () => {},
        onSkip: () => {},
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.post("*/api/internal/platform/aiAssistant/generateAgent", () => {
                    return HttpResponse.json({ message: "Please provide a clear and sufficiently detailed description to ensure proper agent generation." }, { status: 422 });
                })
            ),
        },
    },
    play: async () => {
        const dialog = await submitValidPrompt();
        await within(dialog).findByText(/provide a clear and sufficiently detailed description/);
    },
};
