// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DASHBOARD_DATE_PRESETS, presetRange } from "./dashboardFilters";
import { FeedbackDashboardFilters } from "./FeedbackDashboardFilters";

function renderFilters(over: Partial<React.ComponentProps<typeof FeedbackDashboardFilters>> = {}) {
    const props = {
        subjectKey: "all",
        subjectChoices: [
            { key: "agent:a1", label: "Procurement Intake Router" },
            { key: "workflow:w1", label: "Budget Approval Workflow" },
        ],
        onSubjectChange: vi.fn(),
        range: presetRange("90d"),
        datePresets: DASHBOARD_DATE_PRESETS,
        onRangeChange: vi.fn(),
        minDate: "2026-07-15",
        maxDate: "2026-10-13",
        hasActiveFilters: false,
        onResetFilters: vi.fn(),
        ...over,
    };
    render(<FeedbackDashboardFilters {...props} />);
    return props;
}

describe("FeedbackDashboardFilters", () => {
    it("offers Ask AI and calls back when it is pressed", async () => {
        const onAskAi = vi.fn();
        renderFilters({ onAskAi });

        await userEvent.click(screen.getByRole("button", { name: /ask ai/i }));

        expect(onAskAi).toHaveBeenCalledOnce();
    });

    it("leaves the field labels at the shared Label weight rather than bolding them", () => {
        renderFilters();

        ["Agent / Workflow", "Date Range"].forEach(text => {
            const label = screen.getByText(text);
            expect(label.className).not.toContain("font-semibold");
            expect(label.className).toContain("font-medium");
        });
    });

    it("omits Ask AI entirely when no handler is supplied", () => {
        renderFilters();

        expect(screen.queryByRole("button", { name: /ask ai/i })).not.toBeInTheDocument();
    });

    it("marks each subject choice with its type", async () => {
        renderFilters();

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        const options = await screen.findAllByRole("option");
        const byText = (text: string) => options.find(o => o.textContent === text)!;

        expect(byText("Procurement Intake Router").querySelector(".lucide-bot")).toBeInTheDocument();
        expect(byText("Budget Approval Workflow").querySelector(".lucide-workflow")).toBeInTheDocument();
        const all = byText("All Agents & Workflows");
        expect(all.querySelector(".lucide-bot")).not.toBeInTheDocument();
        expect(all.querySelector(".lucide-workflow")).not.toBeInTheDocument();
    });

    it("reports the chosen subject", async () => {
        const props = renderFilters();

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        await userEvent.click(await screen.findByText("Procurement Intake Router"));

        expect(props.onSubjectChange).toHaveBeenCalledWith("agent:a1");
    });

    it("reports the chosen date preset", async () => {
        const props = renderFilters();

        await userEvent.click(screen.getByRole("button", { name: /date range/i }));
        await userEvent.click(await screen.findByText("Last 7 days"));

        expect(props.onRangeChange).toHaveBeenCalledWith(presetRange("7d"));
    });

    it("offers the four presets plus a custom range, and still no unbounded option", async () => {
        renderFilters();

        await userEvent.click(screen.getByRole("button", { name: /date range/i }));

        expect(await screen.findByText("Last 24 hours")).toBeInTheDocument();
        expect(screen.getByText("Last 30 days")).toBeInTheDocument();
        expect(screen.getByText(/custom range/i)).toBeInTheDocument();
        expect(screen.queryByText(/all time/i)).not.toBeInTheDocument();
    });

    it("hides Reset at the default and reports the click once a filter differs", async () => {
        renderFilters();
        expect(screen.queryByRole("button", { name: /reset/i })).not.toBeInTheDocument();

        const props = renderFilters({ hasActiveFilters: true });
        await userEvent.click(screen.getByRole("button", { name: /reset/i }));

        expect(props.onResetFilters).toHaveBeenCalled();
    });

    it("renders with no subject options and still offers All", async () => {
        renderFilters({ subjectChoices: [] });

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));

        expect(await screen.findByRole("option", { name: "All Agents & Workflows" })).toBeInTheDocument();
    });

    it("says so when the subject list is truncated", async () => {
        renderFilters({ subjectChoicesTruncated: true });

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));

        expect(await screen.findByText(/first 100/i)).toBeInTheDocument();
    });

    it("says so when the subject list failed to load", async () => {
        renderFilters({ optionsError: true });

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));

        expect(await screen.findByText(/couldn't load the subject list/i)).toBeInTheDocument();
    });
});
