// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import type { UsageSavings, UsageSelf } from "@/api/usage";

import { agentUIMode } from "@/providers/contexts";

import { SettingsDialog } from "@/features/settings/SettingsDialog";

import { usageGoldens } from "@/testing/data/usage";
import { disableWailsRuntime, enableWailsRuntime, withDefaults } from "@/testing/helpers";

type SpeechOverrides = {
    speechToText?: boolean;
    textToSpeech?: boolean;
    sttProvider?: "browser" | "openai" | "azure";
    engineSTT?: "browser" | "external";
    ttsProvider?: "browser" | "gemini" | "azure" | "polly";
    engineTTS?: "browser" | "external";
    voice?: string;
};

const speechSettings = (overrides: SpeechOverrides) => ({
    settings: {
        speechToText: false,
        engineSTT: "browser" as const,
        sttProvider: "browser" as const,
        languageSTT: "en-US",
        textToSpeech: true,
        engineTTS: "browser" as const,
        ttsProvider: "browser" as const,
        voice: "",
        playbackRate: 1,
        automaticPlayback: false,
        cacheTTS: false,
        cloudBrowserVoices: false,
        conversationMode: false,
        ...overrides,
    },
});

const speechVoicesHandler = http.get("*/api/v1/speech/voices*", () => {
    return HttpResponse.json({ voices: ["en-US-Standard-A", "en-US-Standard-B"] });
});

const consentHandler = http.get("*/api/v1/productAnalytics/consent", () => {
    return HttpResponse.json({ state: "granted" });
});

const handlers = [
    http.get("*/api/v1/speech/config", () => {
        return HttpResponse.json({
            sttExternal: true,
            ttsExternal: true,
            sttProviders: { openai: true, azure: true },
            ttsProviders: { gemini: true, azure: true, polly: true },
        });
    }),
    http.get("*/api/v2/platform/version", () => {
        return HttpResponse.json({
            data: {
                products: [{ key: "solace-agent-mesh", name: "Solace Agent Mesh", version: "1.0.0" }],
                minClientVersion: null,
                apiVersion: null,
            },
        });
    }),
    speechVoicesHandler,
];

const selfRequests: URL[] = [];
const serveSelf = http.get("*/api/v2/platform/userUsage", ({ request }) => {
    selfRequests.push(new URL(request.url));
    const { id, ...self } = usageGoldens.self as UsageSelf;
    return HttpResponse.json({ data: { ...self, userId: id } });
});
const serveSelfSavings = http.get("*/api/v2/platform/userUsage/savings", () =>
    HttpResponse.json({ data: { ...usageGoldens.savings, scope: "self", avoidedInputTokens: 412_000, avoidedCost: "29.4000000000", calls: 96, unpricedCalls: 0 } satisfies UsageSavings })
);

const meta = {
    title: "Navigation/SettingsDialog",
    component: SettingsDialog,
    render: () => <SettingsDialog open={true} onOpenChange={() => {}} />,
    parameters: {
        docs: {
            description: {
                component: "A settings dialog component for configuring application settings",
            },
        },
        msw: { handlers: withDefaults(...handlers) },
        configContext: {
            persistenceEnabled: false,
            configUseAuthorization: false,
            speechToTextEnabled: false,
            textToSpeechEnabled: false,
        },
        authContext: {
            userInfo: { username: "Story Username With a Very Long Name" },
        },
    },
} satisfies Meta<typeof SettingsDialog>;

export default meta;

export const Default = {
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByRole("button", { name: "General" });
        await dialogContent.findByRole("button", { name: "About" });
        expect(dialogContent.queryByRole("button", { name: "Profile" })).toBeNull();
        expect(dialogContent.queryByText("Speech-to-Text")).toBeNull();
        expect(dialogContent.queryByText("Text-to-Speech")).toBeNull();
    },
};

export const About = {
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        const about = await dialogContent.findByRole("button", { name: "About" });
        await userEvent.click(about);
        await dialogContent.findByText("Application Versions");
    },
};

export const AboutError = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/version", () => HttpResponse.json({ message: "Version service is unavailable.", errorId: "test-version-error" }, { status: 503 })),
                speechVoicesHandler
            ),
        },
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        const about = await dialogContent.findByRole("button", { name: "About" });
        await userEvent.click(about);
        await dialogContent.findByText(/Error loading application information/);
    },
};

export const Support = {
    beforeEach: () => {
        enableWailsRuntime();
        return () => disableWailsRuntime();
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await userEvent.click(await dialogContent.findByRole("button", { name: "Support" }));

        await dialogContent.findByText("Customer Support");
        await dialogContent.findByText("Diagnostics");
        await dialogContent.findByRole("button", { name: "Open Diagnostics Folder" });
        await dialogContent.findByRole("button", { name: "Download Diagnostics Zip" });
        expect(await dialogContent.findByRole("link", { name: /Contact Solace/ })).toHaveAttribute("href", "https://solace.com/support/");
    },
};

const profileUser = { username: "Carol C. Crawford ", email: "carol@example.com", roles: ["sam_admin", "sam_manager"] };

export const Profile = {
    parameters: {
        authContext: { useAuthorization: true, userInfo: profileUser },
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await userEvent.click(await dialogContent.findByRole("button", { name: "Profile" }));

        await dialogContent.findByText(profileUser.username.trim());
        await dialogContent.findByText(profileUser.email);
        for (const role of profileUser.roles) {
            await dialogContent.findByText(role);
        }
    },
};

const longIdentityUser = {
    username: "a-very-long-employee-identifier-issued-by-the-corporate-directory-service with spaces",
    email: "a.very.long.email.address.for.testing.wrapping.behavior@example-corporation.com",
    roles: ["sam_admin", "sam_manager", "agent_builder_admin", "workflow_builder_editor", "connector_admin", "toolset_editor", "skill_admin", "model_config_admin", "evaluation_admin", "gateway_admin", "rbac_viewer", "artifact_reader"],
};

export const All = {
    beforeEach: () => {
        enableWailsRuntime();
        return () => disableWailsRuntime();
    },
    parameters: {
        authContext: { useAuthorization: true, userInfo: longIdentityUser },
        configContext: { productAnalyticsEnabled: true },
        featureContext: { notify_service: true },
        msw: { handlers: withDefaults(serveSelf, ...handlers, consentHandler) },
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);

        for (const tab of ["General", "Profile", "Usage", "Notifications", "Privacy", "Support", "About"]) {
            await dialogContent.findByRole("button", { name: tab });
        }

        await userEvent.click(await dialogContent.findByRole("button", { name: "Profile" }));
        await dialogContent.findByText(longIdentityUser.username);
        await dialogContent.findByText(longIdentityUser.email);
        for (const role of longIdentityUser.roles) {
            await dialogContent.findByText(role);
        }
    },
};

export const Privacy = {
    parameters: {
        configContext: { productAnalyticsEnabled: true },
        msw: { handlers: withDefaults(...handlers, consentHandler) },
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await userEvent.click(await dialogContent.findByRole("button", { name: "Privacy" }));
        await dialogContent.findByLabelText("Usage analytics");
    },
};

export const General = {
    parameters: {
        configContext: {
            persistenceEnabled: false,
            configUseAuthorization: true,
            speechToTextEnabled: true,
            textToSpeechEnabled: true,
        },
        featureContext: {
            logout: true,
        },
        audioSettingsContext: speechSettings({
            speechToText: true,
            textToSpeech: true,
            ttsProvider: "browser",
            engineTTS: "browser",
        }),
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByRole("button", { name: "General" });
        await dialogContent.findByText("Speech-to-Text");
        await dialogContent.findByText("Text-to-Speech");
        await dialogContent.findByText("Speech-to-Text Provider");
        await dialogContent.findByText("Text-to-Speech Provider");
        expect(dialogContent.queryByText("Voice")).toBeNull();
    },
};

export const TTSEnabled = {
    parameters: {
        configContext: {
            persistenceEnabled: false,
            configUseAuthorization: true,
            speechToTextEnabled: true,
            textToSpeechEnabled: true,
        },
        audioSettingsContext: speechSettings({
            speechToText: false,
            ttsProvider: "gemini",
            engineTTS: "external",
            voice: "en-US-Standard-A",
            textToSpeech: true,
        }),
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByRole("button", { name: "General" });
        await dialogContent.findByText("Speech-to-Text");
        await dialogContent.findByText("Text-to-Speech");
        expect(dialogContent.queryByText("Speech-to-Text Provider")).toBeNull();
        await dialogContent.findByText("Text-to-Speech Provider");
        await dialogContent.findByText("Playback Speed");
        await dialogContent.findByText("Voice");
        expect(dialogContent.queryByText(/External TTS is not configured/)).toBeNull();
        const comboboxes = await dialogContent.findAllByRole("combobox");
        const voiceTrigger = comboboxes[1];
        await waitFor(() => expect(voiceTrigger).not.toBeDisabled());
        await userEvent.click(voiceTrigger);
        const options = await screen.findAllByRole("option");
        expect(options).toHaveLength(2);
    },
};

export const AgentMode = {
    parameters: {
        configContext: {
            persistenceEnabled: false,
            configUseAuthorization: true,
            speechToTextEnabled: true,
            textToSpeechEnabled: true,
        },
        audioSettingsContext: speechSettings({ speechToText: true, textToSpeech: true }),
        uiMode: agentUIMode("OrchestratorAgent"),
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByRole("button", { name: "General" });
        expect(dialogContent.queryByText("Speech-to-Text")).toBeNull();
        expect(dialogContent.queryByText("Text-to-Speech")).toBeNull();
    },
};

export const SpeechErrors = {
    parameters: {
        configContext: {
            persistenceEnabled: false,
            configUseAuthorization: true,
            speechToTextEnabled: true,
            textToSpeechEnabled: true,
        },
        featureContext: {
            logout: true,
        },
        audioSettingsContext: speechSettings({
            speechToText: true,
            textToSpeech: true,
            sttProvider: "openai",
            engineSTT: "external",
            ttsProvider: "gemini",
            engineTTS: "external",
        }),
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/speech/config", () => {
                    return HttpResponse.json({
                        sttExternal: false,
                        ttsExternal: false,
                        sttProviders: { openai: true, azure: true },
                        ttsProviders: { gemini: true, azure: true, polly: true },
                    });
                }),
                speechVoicesHandler
            ),
        },
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const dialogContent = within(dialog);
        await dialogContent.findByRole("button", { name: "General" });
        await dialogContent.findByText(/External STT is not configured/);
        await dialogContent.findByText(/External TTS is not configured/);
    },
};

export const Usage = {
    parameters: { msw: { handlers: withDefaults(serveSelf, serveSelfSavings, ...handlers) } },
    beforeEach: () => {
        selfRequests.length = 0;
    },
    play: async () => {
        const dialog = within(await within(document.body).findByRole("dialog"));
        await userEvent.click(await dialog.findByRole("button", { name: "Usage" }));
        const panel = within(await dialog.findByTestId("usageSettings"));
        expect(within(panel.getByRole("region", { name: "Cost Versus Current Budget" })).getByText("$412.30")).toBeInTheDocument();
        expect(selfRequests.map(url => url.search)).toEqual(["?period=this_month"]);
    },
};

const notificationCategories = [
    { category: "scheduled_task", label: "Scheduled task results", group: "Agents and Tasks", description: "" },
    { category: "agent_message", label: "Messages from agents and workflows", group: "Agents and Tasks", description: "Sent when an agent or workflow finishes or finds something to report, without an open chat." },
    {
        category: "hil",
        label: "Tool approvals and agent input requests",
        group: "Requests and Sharing",
        description: "If a request expires, the agent continues without an answer, a tool call that waits for approval does not run, and a workflow step that waits for approval fails.",
    },
    { category: "chat_share", label: "Chats shared with me", group: "Requests and Sharing", description: "" },
];

const notificationChannel = (channelType: string) => ({ channelType, destinationKey: "", destination: "" });

const notificationCells = (channelType: string) => notificationCategories.map(c => ({ category: c.category, channelType, destinationKey: "", enabled: true, locked: channelType === "web" && c.category === "hil" }));

const notificationPagination = { pageNumber: 1, pageSize: 50, totalPages: 1, count: 2, nextPage: null };

const notificationHandlers = (enabled: boolean) => [
    http.get("*/api/v1/notify/preferences/matrix", () =>
        HttpResponse.json(
            enabled
                ? { categories: notificationCategories, channels: ["web", "slack", "teams"].map(notificationChannel), cells: ["web", "slack", "teams"].flatMap(notificationCells) }
                : { categories: notificationCategories, channels: [notificationChannel("web")], cells: notificationCells("web") }
        )
    ),
    http.get("*/api/v1/notify/channels", () =>
        HttpResponse.json({
            data: ["slack", "teams"].map(channelType => ({ channelType, confirmed: enabled, pending: false, locked: false })),
            meta: { pagination: notificationPagination },
        })
    ),
];

export const NotificationsNotEnabled = {
    render: () => <SettingsDialog open={true} onOpenChange={() => {}} initialSection="notifications" />,
    parameters: {
        featureContext: { notify_service: true },
        msw: { handlers: withDefaults(...handlers, ...notificationHandlers(false)) },
    },
    play: async () => {
        const dialog = within(await within(document.body).findByRole("dialog"));
        await dialog.findByRole("region", { name: "Agents and Tasks" });
        expect(dialog.queryByRole("tablist")).toBeNull();
        expect(dialog.getByRole("switch", { name: "Tool approvals and agent input requests: In-App" })).toBeDisabled();
        await dialog.findByRole("button", { name: "Enable Slack" });
        await dialog.findByRole("button", { name: "Enable Teams" });
    },
};

let slackConfirmed = false;
export const NotificationsEnableSucceeded = {
    render: () => <SettingsDialog open={true} onOpenChange={() => {}} initialSection="notifications" />,
    parameters: {
        featureContext: { notify_service: true },
        msw: {
            handlers: withDefaults(
                ...handlers,
                http.get("*/api/v1/notify/preferences/matrix", () => HttpResponse.json({ categories: notificationCategories, channels: [notificationChannel("web")], cells: notificationCells("web") })),
                http.get("*/api/v1/notify/channels", () =>
                    HttpResponse.json({
                        data: [
                            { channelType: "slack", confirmed: slackConfirmed, pending: false, locked: false },
                            { channelType: "teams", confirmed: false, pending: false, locked: false },
                        ],
                        meta: { pagination: notificationPagination },
                    })
                ),
                http.post("*/api/v1/notify/channels/slack/confirm", () => {
                    slackConfirmed = true;
                    return HttpResponse.json({ channelType: "slack", confirmed: true, pending: false, locked: false, confirmedAt: "2026-10-28T00:00:00.000Z" });
                })
            ),
        },
    },
    beforeEach: () => {
        slackConfirmed = false;
    },
    play: async () => {
        const dialog = within(await within(document.body).findByRole("dialog"));
        await userEvent.click(await dialog.findByRole("button", { name: "Enable Slack" }));
        const note = await dialog.findByText("Slack is enabled. Agent Mesh may send a short message there. If notifications do not arrive, check that the Agent Mesh application can message this user.");
        expect(within(note.closest("[data-testid='messageBanner']") as HTMLElement).getByRole("button", { name: "Dismiss" })).toBeInTheDocument();
    },
};

export const NotificationsEnableFailed = {
    render: () => <SettingsDialog open={true} onOpenChange={() => {}} initialSection="notifications" />,
    parameters: {
        featureContext: { notify_service: true },
        msw: {
            handlers: withDefaults(
                ...handlers,
                ...notificationHandlers(false),
                http.post("*/api/v1/notify/channels/teams/confirm", () =>
                    HttpResponse.json(
                        {
                            message: "Unable to enable notifications because Agent Mesh has not received a message from this user in Microsoft Teams. Send a message to the Agent Mesh application in Microsoft Teams first.",
                            errorId: "story-not-seen",
                        },
                        { status: 400 }
                    )
                )
            ),
        },
    },
    play: async () => {
        const dialog = within(await within(document.body).findByRole("dialog"));
        await userEvent.click(await dialog.findByRole("button", { name: "Enable Teams" }));
        const message = await dialog.findByText(/has not received a message from this user in Microsoft Teams/);
        expect(within(message.closest("[data-testid='messageBanner']") as HTMLElement).getByRole("button", { name: "Dismiss" })).toBeInTheDocument();
    },
};

export const NotificationsEnabled = {
    render: () => <SettingsDialog open={true} onOpenChange={() => {}} initialSection="notifications" />,
    parameters: {
        featureContext: { notify_service: true },
        msw: { handlers: withDefaults(...handlers, ...notificationHandlers(true)) },
    },
    play: async () => {
        const dialog = within(await within(document.body).findByRole("dialog"));
        await dialog.findByRole("tab", { name: "In-App, 4 of 4 on" });
        await userEvent.click(await dialog.findByRole("tab", { name: "Slack, 4 of 4 on" }));
        await dialog.findByRole("switch", { name: "Scheduled task results: Slack" });
        await dialog.findByRole("button", { name: "Slack actions" });
        await dialog.findByRole("button", { name: "Teams actions" });
    },
};
