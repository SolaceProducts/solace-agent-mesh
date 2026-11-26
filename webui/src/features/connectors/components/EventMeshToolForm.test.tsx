// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { type EventMeshOperation, newEventMeshOperation } from "../eventMeshUtils";

import { EventMeshToolForm } from "./EventMeshToolForm";

function makeOperation(overrides: Partial<EventMeshOperation> = {}): EventMeshOperation {
    return { ...newEventMeshOperation(), tool_name: "get_order", tool_description: "Look up an order", topic: "acme/orders/v1/get", ...overrides };
}

describe("EventMeshToolForm", () => {
    test("renders the operation's current values", () => {
        render(<EventMeshToolForm operation={makeOperation()} onChange={vi.fn()} />);
        expect(screen.getByDisplayValue("get_order")).toBeInTheDocument();
        expect(screen.getByDisplayValue("Look up an order")).toBeInTheDocument();
        expect(screen.getByDisplayValue("acme/orders/v1/get")).toBeInTheDocument();
        expect(screen.getByLabelText("Tool input JSON Schema")).toHaveValue(JSON.stringify(makeOperation().input_schema, null, 2));
    });

    test("typing in the tool name input calls onChange with the patch", async () => {
        const onChange = vi.fn();
        render(<EventMeshToolForm operation={makeOperation()} onChange={onChange} />);
        await userEvent.type(screen.getByDisplayValue("get_order"), "s");
        expect(onChange).toHaveBeenCalledWith({ tool_name: "get_orders" });
    });

    test("editing the topic input calls onChange with the topic patch", async () => {
        const onChange = vi.fn();
        render(<EventMeshToolForm operation={makeOperation()} onChange={onChange} />);
        await userEvent.type(screen.getByDisplayValue("acme/orders/v1/get"), "x");
        expect(onChange).toHaveBeenCalledWith({ topic: "acme/orders/v1/getx" });
    });

    test("editing the tool description calls onChange with the description patch", async () => {
        const onChange = vi.fn();
        render(<EventMeshToolForm operation={makeOperation()} onChange={onChange} />);
        await userEvent.type(screen.getByDisplayValue("Look up an order"), "!");
        expect(onChange).toHaveBeenCalledWith({ tool_description: "Look up an order!" });
    });

    test("valid schema JSON calls onChange with a parsed input_schema and shows no error", async () => {
        const onChange = vi.fn();
        render(<EventMeshToolForm operation={makeOperation({ input_schema: { type: "object", properties: {} } })} onChange={onChange} />);

        const textarea = screen.getByLabelText("Tool input JSON Schema");
        await userEvent.clear(textarea);
        await userEvent.paste('{"type":"object","properties":{"action":{"type":"string"}}}');

        const lastCall = onChange.mock.calls.at(-1)?.[0];
        expect(lastCall).toHaveProperty("input_schema");
        expect(lastCall.input_schema.properties.action["x-solace-payload-path"]).toBe("action");
        expect(screen.queryByText(/Invalid input|Expected/i)).toBeNull();
    });

    test("invalid schema JSON surfaces an error and does not emit input_schema", async () => {
        const onChange = vi.fn();
        render(<EventMeshToolForm operation={makeOperation({ input_schema: { type: "object", properties: {} } })} onChange={onChange} />);

        const textarea = screen.getByLabelText("Tool input JSON Schema");
        await userEvent.clear(textarea);
        await userEvent.click(textarea);
        await userEvent.paste("{not valid json");

        expect(await screen.findByText(/in JSON at position|Unexpected (token|end)|Expected property name/i)).toBeInTheDocument();
        expect(onChange.mock.calls.every(([patch]) => !("input_schema" in patch))).toBe(true);
    });

    test("renders field validation errors inline, not in an aggregated warning banner", () => {
        render(<EventMeshToolForm operation={makeOperation({ tool_description: "short", topic: "" })} onChange={vi.fn()} showValidation />);

        expect(screen.getByText("Description must be at least 10 characters")).toBeInTheDocument();
        expect(screen.getByText("Topic address is required")).toBeInTheDocument();
        expect(screen.getByDisplayValue("short")).toHaveAttribute("aria-invalid", "true");
        expect(screen.queryByText(/Description must be at least 10 characters.+Topic address is required/)).toBeNull();
    });

    test("hides field errors until validation is requested (page-1 behaviour)", () => {
        render(<EventMeshToolForm operation={makeOperation({ tool_description: "short", topic: "" })} onChange={vi.fn()} />);
        expect(screen.queryByText("Description must be at least 10 characters")).toBeNull();
        expect(screen.queryByText("Topic address is required")).toBeNull();
    });

    test("surfaces a duplicate tool-name error inline when flagged", () => {
        render(<EventMeshToolForm operation={makeOperation()} onChange={vi.fn()} hasDuplicateName showValidation />);
        expect(screen.getByText("Tool names must be unique")).toBeInTheDocument();
        expect(screen.getByDisplayValue("get_order")).toHaveAttribute("aria-invalid", "true");
    });

    test("renders the section headings and advanced settings trigger", () => {
        render(<EventMeshToolForm operation={makeOperation()} onChange={vi.fn()} />);
        expect(screen.getByText("Tool Definition")).toBeInTheDocument();
        expect(screen.getByText("Message Routing and Delivery")).toBeInTheDocument();
        expect(screen.getByText("Advanced Settings")).toBeInTheDocument();
    });

    test("expanding Advanced Settings reveals request-reply fields when waiting for a response", async () => {
        render(<EventMeshToolForm operation={makeOperation({ wait_for_response: true })} onChange={vi.fn()} />);
        await userEvent.click(screen.getByText("Advanced Settings"));
        expect(await screen.findByText("Timeout")).toBeInTheDocument();
        expect(screen.getByText("Reply Delivery")).toBeInTheDocument();
        expect(screen.getByText("Payload Schema")).toBeInTheDocument();
        expect(screen.getByText("Delivery Mode")).toBeInTheDocument();
    });

    test("hides timeout and reply delivery when not waiting for a response", async () => {
        render(<EventMeshToolForm operation={makeOperation({ wait_for_response: false })} onChange={vi.fn()} />);
        await userEvent.click(screen.getByText("Advanced Settings"));
        expect(await screen.findByText("Payload Schema")).toBeInTheDocument();
        expect(screen.queryByText("Timeout")).toBeNull();
        expect(screen.queryByText("Reply Delivery")).toBeNull();
    });

    test("clicking Import from File opens the hidden file picker", async () => {
        const { container } = render(<EventMeshToolForm operation={makeOperation()} onChange={vi.fn()} />);
        const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement;
        const clickSpy = vi.spyOn(fileInput, "click");
        await userEvent.click(screen.getByRole("button", { name: /Import from File/i }));
        expect(clickSpy).toHaveBeenCalled();
    });
});
