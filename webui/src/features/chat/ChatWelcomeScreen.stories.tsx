// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, waitFor, within } from "storybook/test";

import { ChatWelcomeScreen } from "@/features/chat/ChatWelcomeScreen";

import { mockDiscoveredAgentCardInfos } from "@/testing/data";
import { withDefaults } from "@/testing/helpers";
import type { AgentCardInfo, AgentWelcomeConfig } from "@/types";

const baseChatContext = {
    sessionId: "mock-session-id",
    messages: [],
    isResponding: false,
    isCancelling: false,
    selectedAgentName: "Orchestrator",
};

const welcomeConfig: AgentWelcomeConfig = {
    welcome_message: "Welcome to the Weather Expert!",
    suggestions: [
        { label: "Forecast", prompt: "What's the forecast for " },
        { label: "Compare", prompt: "Compare the weather between " },
        { label: "Today", prompt: "Give me today's weather summary", auto_send: true },
    ],
};

const agentWithWelcome: AgentCardInfo = {
    ...mockDiscoveredAgentCardInfos[0],
    name: "WeatherAgent",
    displayName: "WeatherAgent",
    welcome: welcomeConfig,
};

const meta = {
    title: "Chat/ChatWelcomeScreen",
    component: ChatWelcomeScreen,
    parameters: {
        docs: {
            description: {
                component: "Empty-state screen shown before the first message. Renders a welcome heading, suggestion chips, and the chat input. Honours per-agent welcome config (message, suggestions, icon) with a default fallback.",
            },
        },
        chatContext: baseChatContext,
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/sessions/:sessionId/contextUsage", () =>
                    HttpResponse.json({
                        sessionId: "mock-session-id",
                        currentContextTokens: 0,
                        promptTokens: 0,
                        completionTokens: 0,
                        cachedTokens: 0,
                        maxInputTokens: null,
                        usagePercentage: 0,
                        model: "mock",
                        totalEvents: 0,
                        totalMessages: 0,
                        totalTasks: 0,
                        hasCompaction: false,
                    })
                )
            ),
        },
    },
    args: {
        agents: mockDiscoveredAgentCardInfos,
    },
} satisfies Meta<typeof ChatWelcomeScreen>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        configContext: { configWelcomeMessage: null },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("What should we work on?");

        await canvas.findByRole("button", { name: /Write/ });
        await canvas.findByRole("button", { name: /Learn/ });
        await canvas.findByRole("button", { name: /Code/ });
        await canvas.findByRole("button", { name: /Brainstorm/ });
        await canvas.findByRole("button", { name: /Surprise me/ });

        await canvas.findByTestId("chat-input");
    },
};

export const WithAgentWelcomeConfig: Story = {
    args: {
        agents: [...mockDiscoveredAgentCardInfos, agentWithWelcome],
        selectedAgentName: "WeatherAgent",
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Welcome to the Weather Expert!");

        await canvas.findByRole("button", { name: /Forecast/ });
        await canvas.findByRole("button", { name: /Compare/ });
        await canvas.findByRole("button", { name: /Today/ });

        expect(canvas.queryByRole("button", { name: /Brainstorm/ })).toBeNull();
    },
};

export const WithWelcomeOverride: Story = {
    args: {
        selectedAgentName: "not-yet-discovered",
        welcomeOverride: {
            welcome_message: "Spinning up your agent…",
            suggestions: [{ label: "Get started", prompt: "Help me get started" }],
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Spinning up your agent…");
        await canvas.findByRole("button", { name: /Get started/ });
    },
};

export const WithCustomIconNoSuggestions: Story = {
    args: {
        welcomeOverride: {
            welcome_message: "Custom branding",
            welcome_icon: "https://placehold.co/120x120/png",
            welcome_icon_size: "large",
            suggestions: [],
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Custom branding");

        const icon = canvasElement.querySelector('img[src="https://placehold.co/120x120/png"]');
        expect(icon).not.toBeNull();
        expect(icon).toHaveClass("h-40");

        await canvas.findByTestId("chat-input");
        expect(canvas.queryByRole("button", { name: /Surprise me/ })).toBeNull();
        expect(canvas.queryByRole("button", { name: /Write/ })).toBeNull();
    },
};

export const WithDeploymentLogo: Story = {
    parameters: {
        configContext: {
            configLogoUrl: "https://placehold.co/200x60/4338ca/ffffff?text=Deployment+Logo",
            configSmallLogoUrl: null,
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const logo = (await canvas.findByAltText("Logo")) as HTMLImageElement;
        expect(logo.src).toContain("Deployment+Logo");
        expect(canvas.queryByAltText("Solace")).toBeNull();
    },
};

export const WithSmallDeploymentLogoOnly: Story = {
    parameters: {
        configContext: {
            configLogoUrl: null,
            configSmallLogoUrl: "https://placehold.co/60x60/dc2626/ffffff?text=Small+Only",
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const logo = (await canvas.findByAltText("Logo")) as HTMLImageElement;
        expect(logo.src).toContain("Small+Only");
        expect(canvas.queryByAltText("Solace")).toBeNull();
    },
};

export const EmptyDeploymentLogoDrawsDefault: Story = {
    parameters: {
        configContext: {
            configLogoUrl: "",
            configSmallLogoUrl: "",
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.queryByAltText("Logo")).toBeNull();
        expect(await canvas.findByAltText("Solace")).toBeInTheDocument();
    },
};

export const AgentIconWinsOverDeploymentLogo: Story = {
    args: {
        welcomeOverride: {
            welcome_message: "Custom branding",
            welcome_icon: "https://placehold.co/120x120/png",
        },
    },
    parameters: {
        configContext: {
            configLogoUrl: "https://placehold.co/200x60/4338ca/ffffff?text=Deployment+Logo",
            configSmallLogoUrl: null,
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Custom branding");
        expect(canvasElement.querySelector('img[src="https://placehold.co/120x120/png"]')).not.toBeNull();
        expect(canvas.queryByAltText("Logo")).toBeNull();
        expect(canvas.queryByAltText("Solace")).toBeNull();
    },
};

export const WithDisclaimer: Story = {
    parameters: {
        chatContext: baseChatContext,
        configContext: {
            configDisclaimerText: "AI responses may be inaccurate. See our [terms of service](https://example.com/terms).",
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/AI responses may be inaccurate/);

        const link = await canvas.findByRole("link", { name: /terms of service/ });
        expect(link).toHaveAttribute("href", "https://example.com/terms");
        expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
    },
};

export const WithBulletedDisclaimer: Story = {
    parameters: {
        chatContext: baseChatContext,
        configContext: {
            configDisclaimerText: ["- AI responses may be inaccurate.", "- Do not share sensitive data.", "- See our [terms of service](https://example.com/terms)."].join("\n"),
        },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const items = await canvas.findAllByRole("listitem");
        expect(items.length).toBe(3);
        const link = await canvas.findByRole("link", { name: /terms of service/ });
        expect(link).toHaveAttribute("href", "https://example.com/terms");
    },
};

export const ChipClickFillsInput: Story = {
    args: {
        agents: [...mockDiscoveredAgentCardInfos, agentWithWelcome],
        selectedAgentName: "WeatherAgent",
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const forecastChip = await canvas.findByRole("button", { name: /Forecast/ });
        await userEvent.click(forecastChip);

        const chatInput = await canvas.findByTestId("chat-input");
        await waitFor(() => expect(chatInput).toHaveTextContent("What's the forecast for"));
    },
};
