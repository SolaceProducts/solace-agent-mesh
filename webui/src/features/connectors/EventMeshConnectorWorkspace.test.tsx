// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { FormProvider, useForm, useFormContext } from "react-hook-form";
import { describe, expect, test, vi } from "vitest";

import type { ConnectorSchema } from "@/types";

import { EventMeshConnectorWorkspace } from "./EventMeshConnectorWorkspace";
import { type EventMeshOperation, newEventMeshOperation } from "./eventMeshUtils";

const schemaInfo = { type: "event_mesh", name: "Event Mesh" } as unknown as ConnectorSchema;

function makeOperation(overrides: Partial<EventMeshOperation> = {}): EventMeshOperation {
    return { ...newEventMeshOperation(), ...overrides };
}

interface RenderOptions {
    values?: Record<string, unknown>;
    validationError?: string | null;
    onClearValidationError?: () => void;
    schema?: ConnectorSchema;
}

function OperationsProbe() {
    const { watch } = useFormContext();
    const operations = (watch("values.operations") as EventMeshOperation[] | undefined) ?? [];
    return <div data-testid="op-count">{operations.length}</div>;
}

function renderWorkspace({ values = { values: { operations: [] } }, validationError, onClearValidationError, schema = schemaInfo, showValidation = true }: RenderOptions & { showValidation?: boolean } = {}) {
    const Wrapper = ({ children }: { children: ReactNode }) => {
        const methods = useForm({ defaultValues: values });
        return <FormProvider {...methods}>{children}</FormProvider>;
    };
    return render(
        <Wrapper>
            <EventMeshConnectorWorkspace schemaInfo={schema} validationError={validationError} onClearValidationError={onClearValidationError} showValidation={showValidation} />
            <OperationsProbe />
        </Wrapper>
    );
}

describe("EventMeshConnectorWorkspace", () => {
    test("shows the empty section when there are no operations", () => {
        renderWorkspace({ values: { values: { operations: [] } } });
        expect(screen.getByText("No tools yet.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Create New Tool" })).toBeInTheDocument();
    });

    test("clicking Create New Tool from the empty state adds an operation and shows the editor", async () => {
        const onClear = vi.fn();
        renderWorkspace({ values: { values: { operations: [] } }, onClearValidationError: onClear });

        expect(screen.getByTestId("op-count")).toHaveTextContent("0");
        await userEvent.click(screen.getByRole("button", { name: "Create New Tool" }));

        expect(screen.getByTestId("op-count")).toHaveTextContent("1");
        expect(onClear).toHaveBeenCalled();
        expect(screen.getByText("Tool Definition")).toBeInTheDocument();
        expect(screen.getByPlaceholderText("get_order_status")).toHaveValue("");
    });

    test("renders rail rows, editor, and info panel when operations exist", () => {
        renderWorkspace({
            values: {
                values: {
                    operations: [makeOperation({ _uid: "u1", tool_name: "get_order", topic: "acme/get" }), makeOperation({ _uid: "u2", tool_name: "cancel_order", topic: "acme/cancel" })],
                },
            },
        });

        expect(screen.getByText("get_order")).toBeInTheDocument();
        expect(screen.getByText("cancel_order")).toBeInTheDocument();
        expect(screen.getByDisplayValue("get_order")).toBeInTheDocument();
        expect(screen.getByText("Define the messages an AI agent can publish")).toBeInTheDocument();
    });

    test("renders the validation error and info warning banners", () => {
        const warnSchema = { type: "event_mesh", name: "Event Mesh", warning: "Schema out of date" } as unknown as ConnectorSchema;
        renderWorkspace({
            values: { values: { operations: [makeOperation({ _uid: "u1", tool_name: "get_order" })] } },
            validationError: "Add at least one tool",
            schema: warnSchema,
        });

        expect(screen.getByText("Add at least one tool")).toBeInTheDocument();
        expect(screen.getByText("Schema out of date")).toBeInTheDocument();
    });

    test("selecting a rail row switches the editor to that operation", async () => {
        renderWorkspace({
            values: {
                values: {
                    operations: [makeOperation({ _uid: "u1", tool_name: "get_order", topic: "acme/get" }), makeOperation({ _uid: "u2", tool_name: "cancel_order", topic: "acme/cancel" })],
                },
            },
        });

        expect(screen.getByDisplayValue("get_order")).toBeInTheDocument();
        await userEvent.click(screen.getByText("cancel_order"));
        expect(screen.getByDisplayValue("cancel_order")).toBeInTheDocument();
    });

    test("adding a tool via the rail appends and selects the new operation", async () => {
        renderWorkspace({
            values: { values: { operations: [makeOperation({ _uid: "u1", tool_name: "get_order" })] } },
        });

        expect(screen.getByTestId("op-count")).toHaveTextContent("1");
        await userEvent.click(screen.getByRole("button", { name: "Create New Tool" }));

        expect(screen.getByTestId("op-count")).toHaveTextContent("2");
        expect(screen.getByPlaceholderText("get_order_status")).toHaveValue("");
    });

    test("duplicating a tool inserts a _copy operation and selects it", async () => {
        renderWorkspace({
            values: { values: { operations: [makeOperation({ _uid: "u1", tool_name: "get_order", topic: "acme/get" })] } },
        });

        await userEvent.click(screen.getByRole("button", { name: "Actions for get_order" }));
        await userEvent.click(await screen.findByText("Duplicate"));

        expect(screen.getByTestId("op-count")).toHaveTextContent("2");
        expect(screen.getByDisplayValue("get_order_copy")).toBeInTheDocument();
    });

    test("moving a tool up reorders the operations", async () => {
        renderWorkspace({
            values: {
                values: {
                    operations: [makeOperation({ _uid: "u1", tool_name: "get_order" }), makeOperation({ _uid: "u2", tool_name: "cancel_order" })],
                },
            },
        });

        await userEvent.click(screen.getByRole("button", { name: "Actions for cancel_order" }));
        await userEvent.click(await screen.findByText("Move up"));

        expect(screen.getByDisplayValue("cancel_order")).toBeInTheDocument();
    });

    test("requesting delete opens the confirmation dialog and confirming removes the tool", async () => {
        renderWorkspace({
            values: {
                values: {
                    operations: [makeOperation({ _uid: "u1", tool_name: "get_order" }), makeOperation({ _uid: "u2", tool_name: "cancel_order" })],
                },
            },
        });

        await userEvent.click(screen.getByRole("button", { name: "Actions for get_order" }));
        await userEvent.click(await screen.findByText("Delete"));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Remove tool?")).toBeInTheDocument();

        await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

        expect(screen.getByTestId("op-count")).toHaveTextContent("1");
        expect(screen.queryByText("get_order")).toBeNull();
    });

    test("editing a field in the tool form patches the operation in form state", async () => {
        renderWorkspace({
            values: { values: { operations: [makeOperation({ _uid: "u1", tool_name: "get_order" })] } },
        });

        const nameInput = screen.getByDisplayValue("get_order");
        await userEvent.type(nameInput, "s");
        expect(screen.getByDisplayValue("get_orders")).toBeInTheDocument();
    });

    test("marks a rail row with a validation warning when its operation is invalid", () => {
        renderWorkspace({
            values: {
                values: {
                    operations: [makeOperation({ _uid: "u1", tool_name: "Invalid Name" })],
                },
            },
        });
        expect(screen.getByLabelText("This tool has validation errors")).toBeInTheDocument();
    });
});
