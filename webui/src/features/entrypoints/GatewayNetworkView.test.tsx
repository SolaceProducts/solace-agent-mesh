// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { GatewayNetworkView } from "./GatewayNetworkView";

expect.extend(matchers);

const googleChatDetails = {
    dnsName: "",
    port: 0,
    url: "https://sam.example.com/gw/hr-chat",
    apiPaths: [{ path: "/events", description: "Google Chat events" }],
};

describe("GatewayNetworkView for Google Chat", () => {
    test("shows the events endpoint to enter in the Chat app", () => {
        renderWithProviders(<GatewayNetworkView gatewayType="google_chat" networkingInfo={{ networkingDetails: googleChatDetails, isLoading: false, error: null }} />);

        expect(screen.getAllByText("https://sam.example.com/gw/hr-chat/events").length).toBeGreaterThan(0);
    });

    test("an unknown type gets the endpoint without another type's docs link", () => {
        renderWithProviders(<GatewayNetworkView gatewayType="carrier_pigeon" networkingInfo={{ networkingDetails: googleChatDetails, isLoading: false, error: null }} />);

        expect(screen.queryByRole("link", { name: /configuration/ })).not.toBeInTheDocument();
    });
});
