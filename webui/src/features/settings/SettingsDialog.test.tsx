// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { notificationKeys } from "@/api/notifications";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import { disableWailsRuntime, enableWailsRuntime } from "@/testing/helpers/wailsRuntime";

import { SettingsDialog } from "./SettingsDialog";

vi.mock("./GeneralSettings", () => ({ GeneralSettings: () => <div>General settings panel</div> }));
vi.mock("./UsageSettings", () => ({ UsageSettings: () => <div>Usage panel</div> }));
vi.mock("@/features/settings/AboutProduct", () => ({ AboutProduct: () => <div>About panel</div> }));
vi.mock("@/features/settings/SupportSettings", () => ({ SupportSettings: () => <div>Support panel</div> }));

const mockGet = vi.fn();
const mockPut = vi.fn();
const mockPost = vi.fn();
vi.mock("@/api/client", () => ({ api: { webui: { get: (...a: unknown[]) => mockGet(...a), put: (...a: unknown[]) => mockPut(...a), post: (...a: unknown[]) => mockPost(...a) } } }));

beforeEach(() => {
    mockGet.mockReset();
    mockPut.mockReset();
    mockPost.mockReset();
    mockGet.mockImplementation((url: string) =>
        Promise.resolve(
            url.endsWith("/preferences/matrix")
                ? {
                      categories: [{ category: "scheduled_task", label: "Scheduled task results", group: "Agents and Tasks", description: "" }],
                      channels: [
                          { channelType: "web", destinationKey: "", destination: "" },
                          { channelType: "webhook", destinationKey: "wh_1", destination: "hooks.example.com" },
                      ],
                      cells: [
                          { category: "scheduled_task", channelType: "web", destinationKey: "", enabled: true, locked: false },
                          { category: "scheduled_task", channelType: "webhook", destinationKey: "wh_1", enabled: true, locked: false },
                      ],
                  }
                : { data: [], meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 0, count: 0, nextPage: null } } }
        )
    );
});

afterEach(() => vi.clearAllMocks());

describe("SettingsDialog — Notifications tab", () => {
    it("shows a Notifications tab when notify_service is on with the notification types and the external applications", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} />, { featureFlags: { notify_service: true }, configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: "Notifications" }));
        expect(await screen.findByRole("region", { name: "Agents and Tasks" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { level: 3, name: /Notifications\s*EARLY ACCESS/ })).toBeInTheDocument();
        expect(screen.getByText("External Application Notifications")).toBeInTheDocument();
        expect(await screen.findByRole("tab", { name: "Webhook (hooks.example.com), 1 of 1 on" })).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Scheduled task results: In-App" })).toBeChecked();
    });

    it("opens directly on the Notifications tab via initialSection", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} initialSection="notifications" />, { featureFlags: { notify_service: true }, configContextValues: {} });
        expect(await screen.findByRole("region", { name: "Agents and Tasks" })).toBeInTheDocument();
    });

    it("hides the Notifications tab when notify_service is off", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} />, { featureFlags: { notify_service: false }, configContextValues: {} });
        expect(await screen.findByText("General settings panel")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Notifications" })).not.toBeInTheDocument();
    });

    it("adds an application to the channel switcher once it is enabled and takes it out when it is disabled", async () => {
        const category = { category: "scheduled_task", label: "Scheduled task results", group: "Agents and Tasks", description: "" };
        const cell = (channelType: string) => ({ category: "scheduled_task", channelType, destinationKey: "", enabled: true, locked: false });
        const web = { channelType: "web", destinationKey: "", destination: "" };
        const slack = { channelType: "slack", destinationKey: "", destination: "" };
        const webOnly = { categories: [category], channels: [web], cells: [cell("web")] };
        const withSlack = { categories: [category], channels: [web, slack], cells: [cell("web"), cell("slack")] };
        let currentMatrix = webOnly;
        let slackOn = false;
        const pagination = { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null };
        mockGet.mockImplementation((url: string) => Promise.resolve(url.endsWith("/preferences/matrix") ? currentMatrix : { data: [{ channelType: "slack", confirmed: slackOn, pending: false, locked: false }], meta: { pagination } }));
        mockPost.mockImplementation((url: string) => {
            slackOn = url.endsWith("/confirm");
            currentMatrix = slackOn ? withSlack : webOnly;
            return Promise.resolve({ channelType: "slack", confirmed: slackOn, pending: false, locked: false });
        });
        queryClient.removeQueries({ queryKey: notificationKeys.all });

        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} initialSection="notifications" />, { featureFlags: { notify_service: true }, configContextValues: {} });

        expect(await screen.findByRole("switch", { name: "Scheduled task results: In-App" })).toBeInTheDocument();
        expect(screen.queryByRole("tablist")).not.toBeInTheDocument();

        fireEvent.click(await screen.findByRole("button", { name: "Enable Slack" }));
        expect(await screen.findByRole("tab", { name: "Slack, 1 of 1 on" })).toBeInTheDocument();

        await userEvent.setup().click(screen.getByRole("button", { name: "Slack actions" }));
        await userEvent.setup().click(await screen.findByRole("menuitem", { name: "Disable" }));
        await waitFor(() => expect(screen.queryByRole("tablist")).not.toBeInTheDocument());
        expect(screen.getByRole("switch", { name: "Scheduled task results: In-App" })).toBeInTheDocument();
    });
});

describe("SettingsDialog — Support tab", () => {
    afterEach(() => disableWailsRuntime());

    it("shows the tab in the desktop app and renders the panel", async () => {
        enableWailsRuntime();
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} />, { configContextValues: {} });

        fireEvent.click(await screen.findByRole("button", { name: "Support" }));
        expect(await screen.findByText("Support panel")).toBeInTheDocument();
    });

    it("hides the tab in the browser", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} />, { configContextValues: {} });

        expect(await screen.findByText("General settings panel")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Support" })).not.toBeInTheDocument();
    });

    it("falls back to General when a browser caller opens it on Support", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} initialSection="support" />, { configContextValues: {} });

        expect(await screen.findByText("General settings panel")).toBeInTheDocument();
        expect(screen.queryByText("Support panel")).not.toBeInTheDocument();
    });
});

describe("SettingsDialog — Privacy tab", () => {
    it("shows a Privacy tab when this build carries product analytics and renders the telemetry controls", async () => {
        mockGet.mockResolvedValue({ state: "granted" });
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} />, { configContextValues: { productAnalyticsEnabled: true } });

        fireEvent.click(await screen.findByRole("button", { name: "Privacy" }));

        expect(await screen.findByLabelText("Usage analytics")).toBeInTheDocument();
    });

    it("falls back to General when a caller opens it on Privacy in a build with no collection path", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} initialSection="privacy" />, { configContextValues: { productAnalyticsEnabled: false } });

        expect(await screen.findByText("General settings panel")).toBeInTheDocument();
        expect(screen.queryByLabelText("Usage analytics")).not.toBeInTheDocument();
    });

    it("hides the Privacy tab in a build with no collection path", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} />, { configContextValues: { productAnalyticsEnabled: false } });

        expect(await screen.findByText("General settings panel")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Privacy" })).not.toBeInTheDocument();
    });
});

describe("SettingsDialog — Usage tab", () => {
    it("opens on the Usage tab when asked", async () => {
        renderWithProviders(<SettingsDialog open onOpenChange={() => {}} initialSection="usage" />, { configContextValues: {} });

        expect(await screen.findByText("Usage panel")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Usage" })).toBeInTheDocument();
    });
});
