// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { FeedbackSummaryRow } from "@/api/feedback";

import { FeedbackKpiRow } from "./FeedbackKpiRow";

const row = (over: Partial<FeedbackSummaryRow> = {}): FeedbackSummaryRow => ({
    totalActivities: 12406,
    positiveFeedback: 145,
    negativeFeedback: 14,
    negativeRatio: 0.00113,
    trend: [],
    ...over,
});

describe("FeedbackKpiRow", () => {
    it("renders the three metrics with grouped counts and a three-decimal rate", () => {
        render(<FeedbackKpiRow row={row()} />);

        expect(screen.getByText("12,406")).toBeInTheDocument();
        expect(screen.getByText("145")).toBeInTheDocument();
        expect(screen.getByText("14")).toBeInTheDocument();
        expect(screen.getByText("(0.113%)")).toBeInTheDocument();
    });

    it("sets each card's label lighter than the number it labels", () => {
        render(<FeedbackKpiRow row={row()} />);

        expect(screen.getByText("12,406").className).toContain("font-semibold");
        expect(screen.getByText("Total Activities").className).not.toContain("font-semibold");
        expect(screen.getByText("Total Activities").className).toContain("font-medium");
    });

    it("renders zero negatives as a real zero, distinct from having no data", () => {
        render(<FeedbackKpiRow row={row({ negativeFeedback: 0, negativeRatio: 0 })} />);

        expect(screen.getByText("0")).toBeInTheDocument();
        expect(screen.getByText("(0.000%)")).toBeInTheDocument();
        expect(screen.queryByText("No data yet")).not.toBeInTheDocument();
    });

    it("renders the no-data state when nothing happened in the window", () => {
        render(<FeedbackKpiRow row={row({ totalActivities: 0, positiveFeedback: 0, negativeFeedback: 0, negativeRatio: 0 })} />);

        expect(screen.getAllByText("—")).toHaveLength(3);
        expect(screen.getAllByText("No data yet")).toHaveLength(3);
    });

    it("renders the no-data state when the response carries no rows", () => {
        render(<FeedbackKpiRow row={undefined} />);

        expect(screen.getAllByText("—")).toHaveLength(3);
    });

    it("shows a loading state instead of zeroes while the first request is in flight", () => {
        render(<FeedbackKpiRow isLoading row={undefined} />);

        expect(screen.getAllByText("—")).toHaveLength(3);
        expect(screen.queryByText("No data yet")).not.toBeInTheDocument();
    });

    it("surfaces an error in place of the numbers", () => {
        render(<FeedbackKpiRow error={new Error("boom")} row={undefined} />);

        expect(screen.getByText(/couldn't load/i)).toBeInTheDocument();
    });

    it("marks the grid busy while a new window's numbers are in flight", () => {
        const { container } = render(<FeedbackKpiRow row={row()} isFetching />);

        expect(container.querySelector('[aria-busy="true"]')).toBeInTheDocument();
    });

    it("does not mark the grid busy once the request has settled", () => {
        const { container } = render(<FeedbackKpiRow row={row()} />);

        expect(container.querySelector('[aria-busy="true"]')).not.toBeInTheDocument();
    });

    it("explains the negative ratio's denominator against total activity, not just rated activity", async () => {
        const user = userEvent.setup();
        render(<FeedbackKpiRow row={row()} />);

        const helpButtons = screen.getAllByRole("button", { name: "Help" });
        await user.hover(helpButtons[1]);

        await waitFor(() => expect(screen.getByText("Share of all activity rated negative — out of 12,406 total, not out of 159 rated.")).toBeInTheDocument());
    });
});
