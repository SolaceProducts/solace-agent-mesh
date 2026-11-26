// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { Connector } from "@/types";

import { ConnectorSidePanel } from "./ConnectorSidePanel";

vi.mock("@/providers/contexts", async importActual => {
    const actual = await importActual<typeof import("@/providers/contexts")>();
    return {
        ...actual,
        useChatContext: () => ({
            getConnectorSchema: () => ({ name: "Slack" }),
            handleViewConnector: vi.fn(),
        }),
    };
});

vi.mock("./ConnectorViewValues", () => ({ ConnectorViewValues: () => null }));
vi.mock("@/common", () => ({ Metadata: () => null }));

function makeConnector(overrides: Partial<Connector> = {}): Connector {
    return {
        id: "c1",
        name: "My Connector",
        type: "slack",
        subtype: "" as Connector["subtype"],
        description: undefined,
        agentCount: 0,
        values: {} as Connector["values"],
        ...overrides,
    } as Connector;
}

describe("ConnectorSidePanel description rendering", () => {
    test("renders a plain description as-is", () => {
        render(<ConnectorSidePanel connector={makeConnector({ description: "Simple description" })} />);
        expect(screen.getByText("Simple description")).toBeInTheDocument();
    });

    test("collapses newlines in description to spaces", () => {
        render(<ConnectorSidePanel connector={makeConnector({ description: "Line one\nLine two\r\nLine three" })} />);
        expect(screen.getByText("Line one Line two Line three")).toBeInTheDocument();
    });

    test("collapses multiple consecutive newlines to a single space", () => {
        render(<ConnectorSidePanel connector={makeConnector({ description: "First\n\n\nSecond" })} />);
        expect(screen.getByText("First Second")).toBeInTheDocument();
    });

    test("renders N/A when description is absent", () => {
        render(<ConnectorSidePanel connector={makeConnector({ description: undefined as unknown as string })} />);
        expect(screen.getByText("N/A")).toBeInTheDocument();
    });

    test("renders Description label", () => {
        render(<ConnectorSidePanel connector={makeConnector({ description: "hello" })} />);
        expect(screen.getByText("Description")).toBeInTheDocument();
    });
});
