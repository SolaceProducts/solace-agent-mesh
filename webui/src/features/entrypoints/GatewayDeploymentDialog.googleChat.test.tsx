// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Gateway } from "@/types";

import { GatewayDeploymentDialog } from "./GatewayDeploymentDialog";

expect.extend(matchers);

const WEBHOOK_COPY = "Ensure the webhook URL is configured in Google Chat before deploying.";

const gateway: Gateway = {
    id: "gw-1",
    slug: "hr-assistant",
    name: "HR Assistant",
    description: "Answers HR questions in Google Chat.",
    type: "google_chat",
    deploymentStatus: "not_deployed",
    values: { delivery: "https", endpoint_url: "" },
};

const renderDialog = (overrides: Partial<Gateway>, action: "deploy" | "update" = "deploy") =>
    renderWithProviders(<GatewayDeploymentDialog open gatewayName="HR Assistant" gateway={{ ...gateway, ...overrides }} action={action} onConfirm={vi.fn()} onClose={vi.fn()} />, {
        configContextValues: { googleChatEndpointBase: "https://sam.example.com" },
    });

describe("GatewayDeploymentDialog for Google Chat", () => {
    afterEach(() => vi.clearAllMocks());

    test("a first deploy over https shows the derived webhook URL to configure", async () => {
        renderDialog({});
        expect(await screen.findByText(WEBHOOK_COPY)).toBeInTheDocument();
        expect(screen.getByText("https://sam.example.com/gw/hr-assistant/events")).toBeInTheDocument();
    });

    test("a first deploy shows a stored endpoint URL override instead of the derived one", async () => {
        renderDialog({ values: { delivery: "https", endpoint_url: "https://tunnel.example.com/gw/hr-assistant/events" } });
        expect(await screen.findByText("https://tunnel.example.com/gw/hr-assistant/events")).toBeInTheDocument();
    });

    test.each([
        { when: "it pulls events over Pub/Sub", overrides: { values: { delivery: "pubsub" } }, action: "deploy" as const },
        { when: "it is a redeploy", overrides: { deploymentStatus: "deployed" as const }, action: "update" as const },
        { when: "a failed deploy is retried", overrides: { deploymentStatus: "deploy_failed" as const }, action: "deploy" as const },
    ])("no webhook URL is asked for when $when", async ({ overrides, action }) => {
        renderDialog(overrides, action);
        expect(await screen.findByRole("dialog")).toBeInTheDocument();
        expect(screen.queryByText(WEBHOOK_COPY)).not.toBeInTheDocument();
    });
});
