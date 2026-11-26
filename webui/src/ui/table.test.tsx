// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
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

    test("forwards className to the th element", () => {
        renderInTable(
            <SortableTableHead {...baseProps} className="custom-class">
                Name
            </SortableTableHead>
        );
        expect(screen.getByRole("columnheader").className).toContain("custom-class");
    });
});
