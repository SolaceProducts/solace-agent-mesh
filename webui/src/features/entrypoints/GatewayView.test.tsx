// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { Gateway, GatewaySchema } from "@/types";

import { GatewayView } from "./GatewayView";

vi.mock("./hooks", async importOriginal => {
    const actual = await importOriginal<typeof import("./hooks")>();
    return {
        ...actual,
        useAgentOptions: () => [],
        useWorkflowOptions: () => ({ options: [], isError: false }),
        useSystemUserOptions: () => ({ options: [], isError: false }),
    };
});

const baseGateway: Gateway = {
    id: "gw-1",
    name: "Test Gateway",
    description: "A test gateway",
    type: "mcp",
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
    syncStatus: "in_sync",
    values: {},
    createdBy: "user@example.com",
    updatedBy: "user@example.com",
    createdAt: "2024-01-01T00:00:00.000+00:00",
    updatedAt: "2024-01-01T00:00:00.000+00:00",
};

const baseSchema: GatewaySchema = {
    type: "mcp",
    name: "MCP Gateway",
    description: "",
    schema: {},
    yamlTemplatePath: "",
    requiredEnvVars: [],
};

const baseNetworkingDetails = {
    dnsName: "",
    port: 0,
    backendPort: 0,
    url: "/gw/test-gw",
    apiPaths: [],
};

describe("NetworkingDetailsContent (via GatewayView)", () => {
    test("renders a resolved full URL for a non-teams gateway", () => {
        render(<GatewayView gateway={baseGateway} schemaInfo={baseSchema} networkingInfo={{ networkingDetails: baseNetworkingDetails, isLoading: false, error: null }} />);
        expect(screen.getByText(`${window.location.origin}/gw/test-gw`)).toBeInTheDocument();
    });

    test("renders the Teams webhook URL section for a teams gateway", () => {
        const gateway = { ...baseGateway, type: "teams" };
        const networkingDetails = { ...baseNetworkingDetails, url: "/gw/teams-gw" };
        render(<GatewayView gateway={gateway} schemaInfo={baseSchema} networkingInfo={{ networkingDetails, isLoading: false, error: null }} />);
        expect(screen.getByText(`${window.location.origin}/gw/teams-gw/api/messages`)).toBeInTheDocument();
    });

    test("renders an error banner when networking has an error", () => {
        render(<GatewayView gateway={baseGateway} schemaInfo={baseSchema} networkingInfo={{ networkingDetails: null, isLoading: false, error: "Failed to load" }} />);
        expect(screen.getByText("Failed to load")).toBeInTheDocument();
    });

    test("renders a loading indicator while networking is loading", () => {
        render(<GatewayView gateway={baseGateway} schemaInfo={baseSchema} networkingInfo={{ networkingDetails: null, isLoading: true, error: null }} />);
        expect(screen.getByText("Loading...")).toBeInTheDocument();
    });
});

describe("configuration fields (via GatewayView)", () => {
    test("lists the fields in the order the edit form shows them, not alphabetically by key", () => {
        const schemaInfo: GatewaySchema = {
            ...baseSchema,
            type: "slack",
            schema: {
                app_token: { label: "App-Level Token", type: "password", required: true, order: 3 },
                bot_token: { label: "Bot Token", type: "password", required: true, order: 2 },
                default_agent_name: { label: "Default Agent", type: "string", required: false, order: 1 },
            },
        };
        render(<GatewayView gateway={{ ...baseGateway, type: "slack" }} schemaInfo={schemaInfo} networkingInfo={{ networkingDetails: baseNetworkingDetails, isLoading: false, error: null }} />);

        const [agent, bot, app] = ["Default Agent", "Bot Token", "App-Level Token"].map(label => screen.getByText(label));
        expect(agent.compareDocumentPosition(bot) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(bot.compareDocumentPosition(app) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });
});
