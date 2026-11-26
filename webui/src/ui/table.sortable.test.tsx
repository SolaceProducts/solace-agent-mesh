// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { SortableTableHead } from "./table";

function renderInTable(ui: React.ReactNode) {
    return render(
        <table>
            <thead>
                <tr>{ui}</tr>
            </thead>
        </table>
    );
}

const baseProps = {
    column: "name",
    currentSortKey: "other",
    sortDir: "asc" as const,
    onSort: vi.fn(),
};

beforeEach(() => {
    vi.clearAllMocks();
});

describe("SortableTableHead", () => {
    test("renders the column label", () => {
        renderInTable(<SortableTableHead {...baseProps}>Name</SortableTableHead>);
        expect(screen.getByText("Name")).toBeInTheDocument();
    });

    test("renders a clickable button", () => {
        renderInTable(<SortableTableHead {...baseProps}>Name</SortableTableHead>);
        expect(screen.getByRole("button")).toBeInTheDocument();
    });

    test("renders as a columnheader cell", () => {
        renderInTable(<SortableTableHead {...baseProps}>Name</SortableTableHead>);
        expect(screen.getByRole("columnheader")).toBeInTheDocument();
    });

    test("calls onSort with the column key when clicked", () => {
        const onSort = vi.fn();
        renderInTable(
            <SortableTableHead {...baseProps} onSort={onSort}>
                Name
            </SortableTableHead>
        );
        fireEvent.click(screen.getByRole("button"));
        expect(onSort).toHaveBeenCalledWith("name");
        expect(onSort).toHaveBeenCalledTimes(1);
    });

    test("passes the correct column key even when children differ from column", () => {
        const onSort = vi.fn();
        renderInTable(
            <SortableTableHead {...baseProps} column="modelName" onSort={onSort}>
                Model
            </SortableTableHead>
        );
        fireEvent.click(screen.getByRole("button"));
        expect(onSort).toHaveBeenCalledWith("modelName");
    });

    test("cancels the sort button's own horizontal padding so the label starts where body cells do", () => {
        renderInTable(<SortableTableHead {...baseProps}>Name</SortableTableHead>);

        const className = screen.getByRole("button").className;
        const paddingX = /(?:^|\s)px-([\d.]+)/.exec(className)?.[1];
        const offset = /(?:^|\s)-ml-([\d.]+)/.exec(className)?.[1];

        expect(paddingX).toBeDefined();
        expect(offset).toBe(paddingX);
    });

    test("adds no tooltip behaviour to a heading that was given no explanation", async () => {
        renderInTable(<SortableTableHead {...baseProps}>Name</SortableTableHead>);

        await userEvent.hover(screen.getByRole("button", { name: "Name" }));

        await expect(screen.findByRole("tooltip", {}, { timeout: 1500 })).rejects.toThrow();
    });

    test("explains the column on hovering its heading", async () => {
        renderInTable(
            <SortableTableHead {...baseProps} headerTooltip="Ratings divided by activity.">
                Name
            </SortableTableHead>
        );

        await userEvent.hover(screen.getByRole("button", { name: "Name" }));

        expect(await screen.findByRole("tooltip")).toHaveTextContent("Ratings divided by activity.");
    });

    test("keeps the heading named after its column rather than its tooltip, and still sorting", async () => {
        const onSort = vi.fn();
        renderInTable(
            <SortableTableHead {...baseProps} onSort={onSort} headerTooltip="Ratings divided by activity.">
                Name
            </SortableTableHead>
        );

        const heading = screen.getByRole("button", { name: "Name" });
        await userEvent.hover(heading);
        await screen.findByRole("tooltip");

        expect(screen.getByRole("button", { name: "Name" })).toBe(heading);
        fireEvent.click(heading);
        expect(onSort).toHaveBeenCalledWith("name");
    });

    test("forwards className to the th element", () => {
        renderInTable(
            <SortableTableHead {...baseProps} className="custom-class">
                Name
            </SortableTableHead>
        );
        expect(screen.getByRole("columnheader").className).toContain("custom-class");
    });
});
