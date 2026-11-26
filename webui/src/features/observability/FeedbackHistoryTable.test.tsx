// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { CrossUserFeedback } from "@/api/feedback";

import { FeedbackHistoryTable } from "./FeedbackHistoryTable";

const row = (over: Partial<CrossUserFeedback>): CrossUserFeedback => ({
    id: "f1",
    sessionId: "s",
    taskId: "t",
    userId: "user-123",
    rating: "up",
    comment: "helpful",
    shareTaskContent: false,
    subjectId: "a-1",
    subjectType: "agent",
    subjectName: "Agent One",
    entrypointType: "webui",
    createdAt: "2026-08-25T09:42:00.000Z",
    ...over,
});

describe("FeedbackHistoryTable", () => {
    it("marks each subject type with its own glyph and leaves Unknown unglyphed", () => {
        render(
            <FeedbackHistoryTable rows={[row({ id: "f-a", subjectType: "agent", subjectName: "Agent One" }), row({ id: "f-w", subjectType: "workflow", subjectName: "Budget Approval" }), row({ id: "f-u", subjectType: "unknown", subjectName: "" })]} />
        );

        const cellOf = (name: string) => screen.getByText(name).closest("td")!;

        expect(cellOf("Agent One").querySelector(".lucide-bot")).toBeInTheDocument();
        expect(cellOf("Budget Approval").querySelector(".lucide-workflow")).toBeInTheDocument();
        expect(cellOf("Unknown").querySelector("svg")).not.toBeInTheDocument();
    });

    it("renders the resolved subject name", () => {
        render(<FeedbackHistoryTable rows={[row({})]} />);
        expect(screen.getByText("Agent One")).toBeInTheDocument();
    });

    it("falls back to the subject id when the name is unresolved", () => {
        render(<FeedbackHistoryTable rows={[row({ subjectName: "" })]} />);
        expect(screen.getByText("a-1")).toBeInTheDocument();
    });

    it("renders an unattributable subject as Unknown", () => {
        render(<FeedbackHistoryTable rows={[row({ subjectType: "unknown", subjectId: "unknown", subjectName: "" })]} />);
        expect(screen.getByText("Unknown")).toBeInTheDocument();
    });

    it("renders the raw user id", () => {
        render(<FeedbackHistoryTable rows={[row({})]} />);
        expect(screen.getByText("user-123")).toBeInTheDocument();
    });

    it("gives the rating an accessible label rather than a bare glyph", () => {
        render(<FeedbackHistoryTable rows={[row({ rating: "down" })]} />);
        expect(screen.getByLabelText("Negative")).toBeInTheDocument();
    });

    it("renders an em-dash for a missing comment", () => {
        render(<FeedbackHistoryTable rows={[row({ comment: null })]} />);
        expect(screen.getByText("—")).toBeInTheDocument();
    });

    it("carries the year on the timestamp's hover title, not in the cell", () => {
        vi.stubEnv("TZ", "UTC");

        render(<FeedbackHistoryTable rows={[row({ createdAt: "2026-08-25T09:42:00.000Z" })]} />);

        const timestamp = screen.getByTitle(/2026/);
        expect(timestamp).toBeInTheDocument();
        expect(timestamp.textContent).not.toContain("2026");
    });
});

describe("FeedbackHistoryTable selection", () => {
    it("selects a row on click", async () => {
        const user = userEvent.setup();
        const onSelect = vi.fn();
        const target = row({ id: "f2", subjectName: "Agent Two" });
        render(<FeedbackHistoryTable rows={[row({}), target]} onSelect={onSelect} />);

        await user.click(screen.getByText("Agent Two"));
        expect(onSelect).toHaveBeenCalledWith(target);
    });

    it.each(["{Enter}", " "])("selects a focused row with %j", async key => {
        const user = userEvent.setup();
        const onSelect = vi.fn();
        const target = row({});
        render(<FeedbackHistoryTable rows={[target]} onSelect={onSelect} />);

        screen.getByText("Agent One").closest("tr")!.focus();
        await user.keyboard(key);
        expect(onSelect).toHaveBeenCalledWith(target);
    });

    it("marks only the selected row", () => {
        render(<FeedbackHistoryTable rows={[row({}), row({ id: "f2", subjectName: "Agent Two" })]} selectedId="f2" onSelect={vi.fn()} />);

        expect(screen.getByText("Agent Two").closest("tr")).toHaveAttribute("aria-selected", "true");
        expect(screen.getByText("Agent One").closest("tr")).toHaveAttribute("aria-selected", "false");
    });

    it("leaves rows non-interactive without onSelect", () => {
        render(<FeedbackHistoryTable rows={[row({})]} />);
        const tr = screen.getByText("Agent One").closest("tr")!;
        expect(tr).not.toHaveAttribute("tabindex");
        expect(tr).not.toHaveAttribute("aria-selected");
    });
    describe("bringing the selected row into view", () => {
        const rowA = row({ id: "f-a", subjectName: "Agent One", comment: "on this page" });
        const rowB = row({ id: "f-b", subjectName: "Agent Two", comment: "also here" });

        function spyOnScroll() {
            return vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(() => {});
        }

        afterEach(() => vi.restoreAllMocks());

        it("scrolls the selected row's own scroll region, not the page", () => {
            const scrollIntoView = spyOnScroll();

            render(<FeedbackHistoryTable rows={[rowA, rowB]} selectedId="f-b" onSelect={vi.fn()} />);

            expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
            expect(scrollIntoView.mock.instances[0]).toBe(screen.getByText("also here").closest("tr"));
        });

        it("scrolls once the row arrives on a later page, not only when the id changes", () => {
            const scrollIntoView = spyOnScroll();
            const { rerender } = render(<FeedbackHistoryTable rows={[rowA]} selectedId="f-b" onSelect={vi.fn()} />);

            expect(scrollIntoView).not.toHaveBeenCalled();

            rerender(<FeedbackHistoryTable rows={[rowB]} selectedId="f-b" onSelect={vi.fn()} />);

            expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
            expect(scrollIntoView.mock.instances[0]).toBe(screen.getByText("also here").closest("tr"));
        });

        it("scrolls nothing when no row is selected", () => {
            const scrollIntoView = spyOnScroll();

            render(<FeedbackHistoryTable rows={[rowA, rowB]} onSelect={vi.fn()} />);

            expect(scrollIntoView).not.toHaveBeenCalled();
        });
    });
});
