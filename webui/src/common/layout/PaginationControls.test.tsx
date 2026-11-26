// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { PaginationControls } from "./PaginationControls";

describe("PaginationControls", () => {
    test("returns null when totalPages is 1", () => {
        const { container } = render(<PaginationControls totalPages={1} currentPage={1} onPageChange={() => {}} />);
        expect(container.firstChild).toBeNull();
    });

    test("renders all page numbers when totalPages <= 5", () => {
        const mockOnPageChange = vi.fn();
        render(<PaginationControls totalPages={3} currentPage={1} onPageChange={mockOnPageChange} />);

        expect(screen.getByText("1")).toBeInTheDocument();
        expect(screen.getByText("2")).toBeInTheDocument();
        expect(screen.getByText("3")).toBeInTheDocument();
    });

    test("marks current page as active", () => {
        render(<PaginationControls totalPages={3} currentPage={2} onPageChange={() => {}} />);
        const page2Link = screen.getByText("2").closest("a");
        expect(page2Link).toHaveAttribute("aria-current", "page");
    });

    test("calls onPageChange when clicking a page", () => {
        const mockOnPageChange = vi.fn();
        render(<PaginationControls totalPages={3} currentPage={1} onPageChange={mockOnPageChange} />);

        fireEvent.click(screen.getByText("2"));
        expect(mockOnPageChange).toHaveBeenCalledWith(2);
    });

    test("Previous control is CSS-disabled on the first page", () => {
        const { container } = render(<PaginationControls totalPages={3} currentPage={1} onPageChange={() => {}} />);
        const previous = container.querySelector("[aria-label*='Previous' i]")!;
        expect(previous.className).toMatch(/pointer-events-none/);
    });

    test("Next control is CSS-disabled on the last page", () => {
        const { container } = render(<PaginationControls totalPages={3} currentPage={3} onPageChange={() => {}} />);
        const next = container.querySelector("[aria-label*='Next' i]")!;
        expect(next.className).toMatch(/pointer-events-none/);
    });

    test("renders an ellipsis (More pages) marker for large page counts", () => {
        render(<PaginationControls totalPages={10} currentPage={1} onPageChange={() => {}} />);
        expect(screen.getByText("1")).toBeInTheDocument();
        expect(screen.getByText("10")).toBeInTheDocument();
        expect(screen.getByText("More pages")).toBeInTheDocument();
    });
});
