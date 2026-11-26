// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { Connector } from "@/types";

import { ConnectorView } from "./ConnectorView";

vi.mock("@/providers/contexts", async importActual => {
    const actual = await importActual<typeof import("@/providers/contexts")>();
    return {
        ...actual,
        useChatContext: () => ({
            getConnectorSchema: () => ({ name: "Slack" }),
            navigate: vi.fn(),
        }),
    };
});

vi.mock("@/features/agents/hooks", () => ({
    useConnectorTools: () => ({ tools: [], totalCount: 0, isLoading: false }),
}));

vi.mock("./ConnectorViewValues", () => ({ ConnectorViewValues: () => null }));
vi.mock("@/common", () => ({ Metadata: () => null }));
vi.mock("@/common/layout", async importActual => {
    const actual = await importActual<typeof import("@/common/layout")>();
    return {
        ...actual,
        PageContentWrapper: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
        PageSection: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    };
});

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

describe("ConnectorView description rendering", () => {
    test("renders a plain description as-is", () => {
        render(<ConnectorView connector={makeConnector({ description: "Simple description" })} />);
        expect(screen.getByText("Simple description")).toBeInTheDocument();
    });

    test("collapses newlines in description to spaces", () => {
        render(<ConnectorView connector={makeConnector({ description: "Line one\nLine two\r\nLine three" })} />);
        expect(screen.getByText("Line one Line two Line three")).toBeInTheDocument();
    });

    test("collapses multiple consecutive newlines to a single space", () => {
        render(<ConnectorView connector={makeConnector({ description: "First\n\n\nSecond" })} />);
        expect(screen.getByText("First Second")).toBeInTheDocument();
    });

    test("exposes the type and description values by test id", () => {
        render(<ConnectorView connector={makeConnector({ description: "Simple description" })} />);
        expect(screen.getByTestId("connector-review-connector_type")).toHaveTextContent("Slack");
        expect(screen.getByTestId("connector-review-description")).toHaveTextContent("Simple description");
    });

    test("passes undefined description through to ConnectorDisplayField when absent", () => {
        render(<ConnectorView connector={makeConnector({ description: undefined as unknown as string })} />);
        const descriptionValue = screen.getByText("—");
        expect(descriptionValue).toHaveClass("text-(--secondary-text-wMain)");
    });
});
