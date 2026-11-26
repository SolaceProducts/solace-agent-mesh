// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { FormProvider, useForm } from "react-hook-form";
import { describe, expect, test } from "vitest";

import type { ConnectorSchema } from "@/types";

import { EventMeshConnectorReview } from "./EventMeshConnectorReview";
import { newEventMeshOperation } from "./eventMeshUtils";

const schemaInfo = { type: "event_mesh", name: "Event Mesh" } as unknown as ConnectorSchema;

function renderReview(values: Record<string, unknown>) {
    const Wrapper = ({ children }: { children: ReactNode }) => {
        const methods = useForm({ defaultValues: values });
        return <FormProvider {...methods}>{children}</FormProvider>;
    };
    return render(
        <Wrapper>
            <EventMeshConnectorReview connectorType="Solace Event Mesh" schemaInfo={schemaInfo} />
        </Wrapper>
    );
}

describe("EventMeshConnectorReview", () => {
    test("renders connector and broker details from form values", () => {
        renderReview({
            name: "Orders Connector",
            description: "Publishes order events",
            values: {
                broker_url: "tcps://broker.example.com",
                broker_vpn: "orders-vpn",
                broker_username: "svc-orders",
                operations: [newEventMeshOperation()],
            },
        });

        expect(screen.getByText("Connector Details")).toBeInTheDocument();
        expect(screen.getByText("Orders Connector")).toBeInTheDocument();
        expect(screen.getByText("Publishes order events")).toBeInTheDocument();
        expect(screen.getByText("Solace Event Mesh")).toBeInTheDocument();
        expect(screen.queryByText("Event Broker Connection")).toBeNull();
        expect(screen.getByText("tcps://broker.example.com")).toBeInTheDocument();
        expect(screen.getByText("orders-vpn")).toBeInTheDocument();
        expect(screen.getByText("svc-orders")).toBeInTheDocument();
    });

    test("renders one accordion per operation with a tool count", () => {
        const op1 = { ...newEventMeshOperation(0), _uid: "a", tool_name: "get_order", topic: "acme/get" };
        const op2 = { ...newEventMeshOperation(1), _uid: "b", tool_name: "cancel_order", topic: "acme/cancel" };
        renderReview({ name: "C", description: "d", values: { operations: [op1, op2] } });

        expect(screen.getByText("2 tools configured")).toBeInTheDocument();
        expect(screen.getByText("get_order")).toBeInTheDocument();
        expect(screen.getByText("cancel_order")).toBeInTheDocument();
        expect(screen.getByText("acme/get")).toBeInTheDocument();
        expect(screen.getByText("acme/cancel")).toBeInTheDocument();
    });

    test("shows the empty section when there are no operations", () => {
        renderReview({ name: "C", description: "d", values: { operations: [] } });
        expect(screen.getByText("No tools configured.")).toBeInTheDocument();
        expect(screen.queryByText(/tools? configured$/)).toBeNull();
    });

    test("uses singular tool wording for a single operation", () => {
        renderReview({ name: "C", description: "d", values: { operations: [newEventMeshOperation()] } });
        expect(screen.getByText("1 tool configured")).toBeInTheDocument();
    });
});
