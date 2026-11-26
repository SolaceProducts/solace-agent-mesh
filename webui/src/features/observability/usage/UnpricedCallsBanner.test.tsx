// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { UnpricedCallsBanner } from "./UnpricedCallsBanner";

describe("UnpricedCallsBanner", () => {
    test("says the cost of calls on a model with no price is left out", () => {
        const { rerender } = render(<UnpricedCallsBanner unpricedCalls={1240} totalCalls={5000} />);
        expect(screen.getByRole("alert")).toHaveTextContent("1,240 out of 5,000 calls in the selected period used a model with no price set. Their cost is not included.");
        rerender(<UnpricedCallsBanner unpricedCalls={1} totalCalls={1} />);
        expect(screen.getByRole("alert")).toHaveTextContent("1 out of 1 call in the selected period used a model with no price set. Its cost is not included.");
        expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
    });

    test("says nothing when every call was priced", () => {
        const { container } = render(<UnpricedCallsBanner unpricedCalls={0} totalCalls={10} />);
        expect(container).toBeEmptyDOMElement();
    });

    test("offers the Models page only when given a way there", async () => {
        const onGoToModels = vi.fn();
        const { rerender } = render(<UnpricedCallsBanner unpricedCalls={3} totalCalls={10} />);
        expect(screen.queryByRole("button", { name: "Go to Models" })).not.toBeInTheDocument();
        rerender(<UnpricedCallsBanner unpricedCalls={3} totalCalls={10} onGoToModels={onGoToModels} />);
        await userEvent.click(screen.getByRole("button", { name: "Go to Models" }));
        expect(onGoToModels).toHaveBeenCalledOnce();
    });
});
