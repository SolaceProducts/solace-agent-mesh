// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Gateway, GatewaySchema } from "@/types";

import { GatewaysView } from "./GatewaysView";

const nonWebhookSchemas: GatewaySchema[] = [{ type: "slack", name: "Slack", description: "Slack gateway", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

expect.extend(matchers);

vi.mock("@/api/gateways", async importActual => ({
    ...(await importActual<typeof import("@/api/gateways")>()),
    useGateways: () => ({
        data: [
            {
                id: "gw-webhook",
                name: "Webhook Gateway",
                description: "A gateway whose type is feature-gated and absent from the schema list",
                type: "webhook",
                values: {},
            } satisfies Gateway,
        ],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
    }),
}));

describe("GatewaysView schema-less gateway visibility", () => {
    test("gateway with a type absent from all schemas still appears in the list", () => {
        renderWithProviders(<GatewaysView />, { chatContextValues: { gatewaySchemas: nonWebhookSchemas } });

        expect(screen.getByText("Webhook Gateway")).toBeInTheDocument();
        expect(screen.getByText("Webhook")).toBeInTheDocument();
    });
});
