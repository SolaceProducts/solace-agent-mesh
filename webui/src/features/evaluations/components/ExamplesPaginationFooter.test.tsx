// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { ExamplesPaginationFooter } from "@/features/evaluations/components/ExamplesPaginationFooter";

const props = { page: 1, pageSize: 10, totalPages: 1, totalCount: 0, onPageChange: vi.fn() };

describe("ExamplesPaginationFooter", () => {
    test("renders nothing for an empty dataset", () => {
        const { container } = render(<ExamplesPaginationFooter {...props} summariseSinglePage />);
        expect(container).toBeEmptyDOMElement();
    });

    test("counts the range from the page offset", () => {
        render(<ExamplesPaginationFooter {...props} page={3} totalPages={5} totalCount={47} />);
        expect(screen.getByText(/Showing 21–30 of 47 results/)).toBeTruthy();
    });

    test("clamps the range end to the total on the last page", () => {
        render(<ExamplesPaginationFooter {...props} page={5} totalPages={5} totalCount={47} />);
        expect(screen.getByText(/Showing 41–47 of 47 results/)).toBeTruthy();
    });

    test("counts the same range in either layout", () => {
        render(<ExamplesPaginationFooter {...props} page={3} totalPages={5} totalCount={47} layout="stacked" />);
        expect(screen.getByText(/Showing 21–30 of 47 results/)).toBeTruthy();
    });

    test("summarises a single page only when asked", () => {
        const single = { ...props, totalPages: 1, totalCount: 4 };
        const { container, rerender } = render(<ExamplesPaginationFooter {...single} />);
        expect(container).toBeEmptyDOMElement();

        rerender(<ExamplesPaginationFooter {...single} summariseSinglePage />);
        expect(screen.getByText("Showing 4 results")).toBeTruthy();
    });
});
