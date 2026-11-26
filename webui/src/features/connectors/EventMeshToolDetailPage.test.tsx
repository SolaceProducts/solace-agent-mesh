// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Connector } from "@/types";

import { EventMeshToolDetailPage } from "./EventMeshToolDetailPage";
import { type EventMeshOperation, newEventMeshOperation } from "./eventMeshUtils";

const navigate = vi.fn();
let loaderData: { connectorId: string; toolName: string } = { connectorId: "c1", toolName: "get_order" };
let connectorsState: { data: Connector[]; isLoading: boolean } = { data: [], isLoading: false };

vi.mock("@/providers/contexts", async importActual => {
    const actual = await importActual<typeof import("@/providers/contexts")>();
    return {
        ...actual,
        useChatContext: () => ({ navigate, useLoaderData: () => loaderData }),
    };
});

vi.mock("@/api/connectors", async importActual => {
    const actual = await importActual<typeof import("@/api/connectors")>();
    return {
        ...actual,
        useConnectors: () => connectorsState,
    };
});

function makeOperation(overrides: Partial<EventMeshOperation> = {}): EventMeshOperation {
    return { ...newEventMeshOperation(), tool_name: "get_order", tool_description: "Look up an order by id", topic: "acme/orders/v1/get", ...overrides };
}

function makeConnector(operations: EventMeshOperation[], overrides: Partial<Connector> = {}): Connector {
    return {
        id: "c1",
        name: "Orders Connector",
        description: "Publishes order events",
        type: "event_mesh",
        subtype: "" as Connector["subtype"],
        values: { operations: operations as unknown as Connector["values"]["operations"] } as unknown as Connector["values"],
        ...overrides,
    } as Connector;
}

beforeEach(() => {
    navigate.mockReset();
    loaderData = { connectorId: "c1", toolName: "get_order" };
    connectorsState = { data: [], isLoading: false };
});

describe("EventMeshToolDetailPage", () => {
    test("shows a loading state while connectors are loading", () => {
        connectorsState = { data: [], isLoading: true };
        render(<EventMeshToolDetailPage />);
        expect(screen.getByText("Loading Tool...")).toBeInTheDocument();
    });

    test("shows a not-found state and navigates back to connectors when the connector is missing", async () => {
        connectorsState = { data: [], isLoading: false };
        render(<EventMeshToolDetailPage />);

        expect(screen.getByText("Connector Not Found")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Go To Connectors" }));
        expect(navigate).toHaveBeenCalledWith("/connectors");
    });

    test("shows a tool-not-found state and navigates back to the connector", async () => {
        loaderData = { connectorId: "c1", toolName: "missing_tool" };
        connectorsState = { data: [makeConnector([makeOperation()])], isLoading: false };
        render(<EventMeshToolDetailPage />);

        expect(screen.getByText("Tool Not Found")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Back to Connector" }));
        expect(navigate).toHaveBeenCalledWith("/connectors/c1");
    });

    test("renders the tool details for a matching operation", () => {
        connectorsState = { data: [makeConnector([makeOperation()])], isLoading: false };
        render(<EventMeshToolDetailPage />);

        expect(screen.getAllByText("get_order").length).toBeGreaterThan(0);
        expect(screen.getByText("Look up an order by id")).toBeInTheDocument();
        expect(screen.getByText("Message Routing and Delivery")).toBeInTheDocument();
        expect(screen.getByText("acme/orders/v1/get")).toBeInTheDocument();
        expect(screen.getByText("Request-Reply (Wait for response)")).toBeInTheDocument();
        expect(screen.getByText("Advanced Settings")).toBeInTheDocument();
    });

    test("shows publish-subscribe response mode when the operation does not wait for a response", () => {
        connectorsState = { data: [makeConnector([makeOperation({ wait_for_response: false })])], isLoading: false };
        render(<EventMeshToolDetailPage />);
        expect(screen.getByText("Publish-Subscribe (No wait)")).toBeInTheDocument();
    });

    test("renders the empty schema message when there are no input properties", () => {
        connectorsState = { data: [makeConnector([makeOperation()])], isLoading: false };
        render(<EventMeshToolDetailPage />);
        expect(screen.getByText("No input schema defined for this tool")).toBeInTheDocument();
        expect(screen.queryByRole("tab", { name: "Table View" })).toBeNull();
    });

    test("renders the schema table and toggles to JSON view when properties exist", async () => {
        const operation = makeOperation({
            input_schema: {
                type: "object",
                properties: { action: { type: "string", description: "the action" } },
                required: ["action"],
            },
        });
        connectorsState = { data: [makeConnector([operation])], isLoading: false };
        render(<EventMeshToolDetailPage />);

        expect(screen.getByText("action")).toBeInTheDocument();
        expect(screen.getByText("the action")).toBeInTheDocument();
        expect(screen.getAllByText("Required").length).toBeGreaterThanOrEqual(1);
        expect(screen.queryByText(/"x-solace-payload-path"|"properties"/)).toBeNull();

        await userEvent.click(screen.getByRole("tab", { name: "JSON View" }));
        expect(screen.getByText(/"properties"/)).toBeInTheDocument();
    });

    test("clicking the Close button navigates back to the connector", async () => {
        connectorsState = { data: [makeConnector([makeOperation()])], isLoading: false };
        render(<EventMeshToolDetailPage />);
        await userEvent.click(screen.getByTestId("closeButton"));
        expect(navigate).toHaveBeenCalledWith("/connectors/c1");
    });

    test("returns no operation when the connector is not an event_mesh type", () => {
        connectorsState = { data: [makeConnector([makeOperation()], { type: "knowledge_base" as Connector["type"] })], isLoading: false };
        render(<EventMeshToolDetailPage />);
        expect(screen.getByText("Tool Not Found")).toBeInTheDocument();
    });

    test("decodes a URL-encoded tool name from the loader before matching", () => {
        loaderData = { connectorId: "c1", toolName: encodeURIComponent("get order") };
        connectorsState = { data: [makeConnector([makeOperation({ tool_name: "get order" })])], isLoading: false };
        render(<EventMeshToolDetailPage />);
        expect(screen.getByText("Look up an order by id")).toBeInTheDocument();
    });
});
