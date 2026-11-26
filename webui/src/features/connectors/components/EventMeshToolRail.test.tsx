// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { type EventMeshOperation, newEventMeshOperation } from "../eventMeshUtils";

import { EventMeshToolRail } from "./EventMeshToolRail";

function makeOperation(overrides: Partial<EventMeshOperation>): EventMeshOperation {
    return { ...newEventMeshOperation(), ...overrides };
}

function renderRail(props: Partial<React.ComponentProps<typeof EventMeshToolRail>> = {}) {
    const operations = props.operations ?? [makeOperation({ _uid: "u1", tool_name: "get_order", topic: "acme/orders/v1/get" }), makeOperation({ _uid: "u2", tool_name: "cancel_order", topic: "acme/orders/v1/cancel" })];
    const handlers = {
        onSearchChange: vi.fn(),
        onCollapsedChange: vi.fn(),
        onSelect: vi.fn(),
        onAdd: vi.fn(),
        onDuplicate: vi.fn(),
        onMove: vi.fn(),
        onRequestDelete: vi.fn(),
    };
    render(<EventMeshToolRail operations={operations} activeIndex={0} search="" collapsed={false} invalidUids={new Set()} {...handlers} {...props} />);
    return { operations, ...handlers };
}

describe("EventMeshToolRail", () => {
    test("renders tool names and topics", () => {
        renderRail();
        expect(screen.getByText("get_order")).toBeInTheDocument();
        expect(screen.getByText("cancel_order")).toBeInTheDocument();
        expect(screen.getByText("acme/orders/v1/get")).toBeInTheDocument();
        expect(screen.getByText("acme/orders/v1/cancel")).toBeInTheDocument();
    });

    test("falls back to a placeholder name and topic when empty", () => {
        renderRail({ operations: [makeOperation({ _uid: "u1", tool_name: "", topic: "" })] });
        expect(screen.getByText("Untitled tool 1")).toBeInTheDocument();
        expect(screen.getByText("Insert topic address")).toBeInTheDocument();
    });

    test("clicking a row calls onSelect with its index", async () => {
        const { onSelect } = renderRail();
        await userEvent.click(screen.getByText("cancel_order"));
        expect(onSelect).toHaveBeenCalledWith(1);
    });

    test("clicking Create New Tool calls onAdd", async () => {
        const { onAdd } = renderRail();
        await userEvent.click(screen.getByRole("button", { name: "Create New Tool" }));
        expect(onAdd).toHaveBeenCalledTimes(1);
    });

    test("typing in the search box calls onSearchChange", async () => {
        const { onSearchChange } = renderRail();
        await userEvent.type(screen.getByPlaceholderText("Search"), "x");
        expect(onSearchChange).toHaveBeenCalledWith("x");
    });

    test("search value filters the visible rows by name or topic", () => {
        renderRail({ search: "cancel" });
        expect(screen.getByText("cancel_order")).toBeInTheDocument();
        expect(screen.queryByText("get_order")).toBeNull();
    });

    test("shows an empty-state message when the search matches nothing", () => {
        renderRail({ search: "zzz-nomatch" });
        expect(screen.getByText("No tools match your search.")).toBeInTheDocument();
        expect(screen.queryByText("get_order")).toBeNull();
    });

    test("renders the warning icon only for operations whose uid is invalid", () => {
        renderRail({ invalidUids: new Set(["u2"]) });
        const warnings = screen.getAllByLabelText("This tool has validation errors");
        expect(warnings).toHaveLength(1);
    });

    test("collapsing calls onCollapsedChange(true)", async () => {
        const { onCollapsedChange } = renderRail();
        await userEvent.click(screen.getByRole("button", { name: "Collapse tools panel" }));
        expect(onCollapsedChange).toHaveBeenCalledWith(true);
    });

    test("in collapsed mode only the expand affordance is shown", async () => {
        const { onCollapsedChange } = renderRail({ collapsed: true });
        expect(screen.queryByText("get_order")).toBeNull();
        expect(screen.queryByRole("button", { name: "Create New Tool" })).toBeNull();
        await userEvent.click(screen.getByRole("button", { name: "Expand tools panel" }));
        expect(onCollapsedChange).toHaveBeenCalledWith(false);
    });

    test("row action menu invokes duplicate, move and delete callbacks", async () => {
        const { onDuplicate, onMove, onRequestDelete } = renderRail();

        await userEvent.click(screen.getByRole("button", { name: "Actions for get_order" }));
        await userEvent.click(await screen.findByText("Duplicate"));
        expect(onDuplicate).toHaveBeenCalledWith(0);

        await userEvent.click(screen.getByRole("button", { name: "Actions for cancel_order" }));
        await userEvent.click(await screen.findByText("Move up"));
        expect(onMove).toHaveBeenCalledWith(1, -1);

        await userEvent.click(screen.getByRole("button", { name: "Actions for cancel_order" }));
        await userEvent.click(await screen.findByText("Delete"));
        expect(onRequestDelete).toHaveBeenCalledWith(1);
    });
});
