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

import { GatewaysView } from "./GatewaysView";

expect.extend(matchers);

const gateways: Gateway[] = [
    {
        id: "gw-consume",
        name: "Trades Consumer",
        description: "",
        type: "event_mesh",
        values: { event_rules: [{ name: "trades", existingQueueName: "preprov/q/trades" }] },
    },
    {
        id: "gw-normal",
        name: "Normal Entrypoint",
        description: "",
        type: "event_mesh",
        values: { event_rules: [{ name: "r1", subscriptions: [{ topic: "a/b" }] }] },
    },
];

vi.mock("@/api/gateways", async importActual => ({
    ...(await importActual<typeof import("@/api/gateways")>()),
    useGateways: () => ({ data: gateways, isLoading: false, error: null, refetch: vi.fn() }),
}));

describe("GatewaysView UI-notice badge", () => {
    afterEach(() => vi.clearAllMocks());

    test("renders the UI-notice badge only on rows whose entrypoint carries an existingQueueName rule", () => {
        renderWithProviders(<GatewaysView />);

        expect(screen.getByTestId("gatewayUiNoticeBadge-gw-consume")).toBeInTheDocument();
        expect(screen.queryByTestId("gatewayUiNoticeBadge-gw-normal")).not.toBeInTheDocument();
    });
});
