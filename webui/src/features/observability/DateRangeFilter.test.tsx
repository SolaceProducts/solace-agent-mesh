// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DASHBOARD_DATE_PRESETS, presetRange } from "./dashboardFilters";
import { DateRangeFilter } from "./DateRangeFilter";

function renderFilter(over: Partial<React.ComponentProps<typeof DateRangeFilter>> = {}) {
    const props = {
        value: presetRange("90d"),
        onChange: vi.fn(),
        presets: DASHBOARD_DATE_PRESETS,
        minDate: "2026-07-15",
        maxDate: "2026-10-13",
        ...over,
    };
    render(<DateRangeFilter {...props} />);
    return props;
}

const openMenu = () => userEvent.click(screen.getByRole("button", { name: /date range/i }));

describe("DateRangeFilter presets", () => {
    it("offers every preset it is given and reports the chosen one", async () => {
        const props = renderFilter();

        await openMenu();
        expect(screen.getByText("Last 24 hours")).toBeInTheDocument();
        await userEvent.click(screen.getByText("Last 7 days"));

        expect(props.onChange).toHaveBeenCalledWith(presetRange("7d"));
    });

    it("names the active preset on the trigger", () => {
        renderFilter({ value: presetRange("30d") });

        expect(screen.getByRole("button", { name: /date range/i })).toHaveTextContent("Last 30 days");
    });

    it("marks only the active preset as current", async () => {
        renderFilter({ value: presetRange("30d") });

        await openMenu();

        expect(screen.getByRole("button", { name: "Last 30 days" })).toHaveAttribute("aria-current", "true");
        DASHBOARD_DATE_PRESETS.filter(preset => preset.value !== "30d").forEach(preset => {
            expect(screen.getByRole("button", { name: preset.label })).not.toHaveAttribute("aria-current");
        });
        expect(screen.getByRole("button", { name: /custom range/i })).not.toHaveAttribute("aria-current");
    });

    it("marks the custom row, and no preset, as current while an explicit range is active", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-08-19", to: "2026-09-17" } });

        await openMenu();

        expect(screen.getByRole("button", { name: /custom range/i })).toHaveAttribute("aria-current", "true");
        DASHBOARD_DATE_PRESETS.forEach(preset => {
            expect(screen.getByRole("button", { name: preset.label })).not.toHaveAttribute("aria-current");
        });
    });

    it("offers only the presets it is given, so a retention-narrowed list stays narrowed", async () => {
        renderFilter({ presets: DASHBOARD_DATE_PRESETS.slice(0, 2) });

        await openMenu();

        expect(screen.getByText("Last 7 days")).toBeInTheDocument();
        expect(screen.queryByText("Last 90 days")).not.toBeInTheDocument();
    });
});

describe("DateRangeFilter custom range", () => {
    it("reads an active range back as dates rather than the word Custom", () => {
        renderFilter({ value: { kind: "custom", from: "2026-08-19", to: "2026-09-17" } });

        const trigger = screen.getByRole("button", { name: /date range/i });
        expect(trigger).toHaveTextContent("Aug 19, 2026");
        expect(trigger).toHaveTextContent("Sep 17, 2026");
        expect(trigger).not.toHaveTextContent(/custom/i);
    });

    it("refuses a day past the retention floor on both calendars", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-07-20", to: "2026-07-25" } });

        await openMenu();

        for (const field of ["Start date", "End date"]) {
            await userEvent.click(screen.getByRole("button", { name: field }));
            expect(screen.getByRole("button", { name: "14" })).toBeDisabled();
            expect(screen.getByRole("button", { name: "15" })).toBeEnabled();
            await userEvent.keyboard("{Escape}");
        }
    });

    it("leaves every day selectable when retention is unconfigured", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-07-20", to: "2026-07-25" }, minDate: undefined });

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: "Start date" }));

        expect(screen.getByRole("button", { name: "14" })).toBeEnabled();
        expect(screen.getByRole("button", { name: "1" })).toBeEnabled();
    });

    it("reports the edited draft rather than the range it started from", async () => {
        const props = renderFilter({ value: { kind: "custom", from: "2026-08-19", to: "2026-09-17" } });

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: "Start date" }));
        await userEvent.click(screen.getByRole("button", { name: "5" }));
        await userEvent.click(screen.getByRole("button", { name: /apply/i }));

        expect(props.onChange).toHaveBeenCalledWith({ kind: "custom", from: "2026-08-05", to: "2026-09-17" });
    });

    it("opens with the panel already showing when a range is active", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-08-19", to: "2026-09-17" } });

        await openMenu();

        expect(screen.getByRole("button", { name: /apply/i })).toBeInTheDocument();
    });

    it("keeps the menu open while a day is chosen in the calendar", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-08-19", to: "2026-09-17" } });

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: "Start date" }));
        await userEvent.click(screen.getByRole("button", { name: "5" }));

        expect(screen.getByRole("button", { name: /apply/i })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Start date" })).toHaveTextContent("2026-08-05");
    });
});

describe("DateRangeFilter draft handling", () => {
    it("forgets an empty Apply once a preset closes the menu", async () => {
        renderFilter();

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: /custom range/i }));
        await userEvent.click(screen.getByRole("button", { name: /apply/i }));
        expect(screen.getByText("Pick a start and end date.")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Last 7 days" }));

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: /custom range/i }));
        expect(screen.queryByText("Pick a start and end date.")).not.toBeInTheDocument();
    });

    it("keeps an in-progress draft when the parent re-renders with an equal range", async () => {
        const props = {
            onChange: vi.fn(),
            presets: DASHBOARD_DATE_PRESETS,
            minDate: "2026-07-15",
            maxDate: "2026-10-13",
        };
        const { rerender } = render(<DateRangeFilter {...props} value={{ kind: "custom", from: "2026-08-19", to: "2026-09-17" }} />);

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: "Start date" }));
        await userEvent.click(screen.getByRole("button", { name: "5" }));
        expect(screen.getByRole("button", { name: "Start date" })).toHaveTextContent("2026-08-05");

        rerender(<DateRangeFilter {...props} value={{ kind: "custom", from: "2026-08-19", to: "2026-09-17" }} />);

        expect(screen.getByRole("button", { name: "Start date" })).toHaveTextContent("2026-08-05");
    });

    it("takes a genuinely different applied range into its draft fields", async () => {
        const props = {
            onChange: vi.fn(),
            presets: DASHBOARD_DATE_PRESETS,
            minDate: "2026-07-15",
            maxDate: "2026-10-13",
        };
        const { rerender } = render(<DateRangeFilter {...props} value={{ kind: "custom", from: "2026-08-19", to: "2026-09-17" }} />);

        rerender(<DateRangeFilter {...props} value={{ kind: "custom", from: "2026-08-01", to: "2026-08-31" }} />);
        await openMenu();

        expect(screen.getByRole("button", { name: "Start date" })).toHaveTextContent("2026-08-01");
        expect(screen.getByRole("button", { name: "End date" })).toHaveTextContent("2026-08-31");
    });
});

describe("DateRangeFilter refuses a range it cannot send", () => {
    it("says to pick both dates only once Apply is chosen without them, and applies nothing", async () => {
        const { onChange } = renderFilter();

        await openMenu();
        await userEvent.click(screen.getByText(/custom range/i));
        expect(screen.queryByText(/pick a start and end date/i)).not.toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: /apply/i }));
        expect(screen.getByRole("button", { name: /apply/i })).toHaveAccessibleDescription(/pick a start and end date/i);
        expect(onChange).not.toHaveBeenCalled();
    });

    it("ties the reason to the disabled Apply, and drops it once the range is valid", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-09-17", to: "2026-09-17" } });

        await openMenu();
        expect(screen.getByRole("button", { name: /apply/i })).not.toHaveAccessibleDescription();

        await userEvent.click(screen.getByRole("button", { name: "End date" }));
        await userEvent.click(screen.getByRole("button", { name: "5" }));

        expect(screen.getByRole("button", { name: /apply/i })).toHaveAccessibleDescription(/start date must come before/i);
    });

    it("disables Apply and says why when the bounds are reversed", async () => {
        renderFilter({ value: { kind: "custom", from: "2026-09-17", to: "2026-09-17" } });

        await openMenu();
        await userEvent.click(screen.getByRole("button", { name: "End date" }));
        await userEvent.click(screen.getByRole("button", { name: "5" }));

        expect(screen.getByRole("button", { name: /apply/i })).toBeDisabled();
        expect(screen.getByText(/start date must come before/i)).toBeInTheDocument();
    });

    it.each([
        ["start", 0],
        ["end", 1],
    ])("refuses to apply when only the %s date is cleared", async (_bound, clearIndex) => {
        const { onChange } = renderFilter({ value: { kind: "custom", from: "2026-08-19", to: "2026-09-17" } });

        await openMenu();
        await userEvent.click(screen.getAllByRole("button", { name: "Clear date" })[clearIndex]);
        await userEvent.click(screen.getByRole("button", { name: /apply/i }));

        expect(screen.getByText(/pick a start and end date/i)).toBeInTheDocument();
        expect(onChange).not.toHaveBeenCalled();
    });

    it("allows a range of exactly 366 days", async () => {
        renderFilter({ value: { kind: "custom", from: "2025-09-17", to: "2026-09-17" }, minDate: undefined });

        await openMenu();

        expect(screen.getByRole("button", { name: /apply/i })).toBeEnabled();
        expect(screen.queryByText(/at most 366 days/i)).not.toBeInTheDocument();
    });

    it("refuses a range of 367 days", async () => {
        renderFilter({ value: { kind: "custom", from: "2025-09-16", to: "2026-09-17" }, minDate: undefined });

        await openMenu();

        expect(screen.getByRole("button", { name: /apply/i })).toBeDisabled();
        expect(screen.getByText(/at most 366 days/i)).toBeInTheDocument();
    });

    it("disables Apply and says why when the span far exceeds the server's ceiling", async () => {
        renderFilter({ value: { kind: "custom", from: "2025-01-01", to: "2026-09-17" }, minDate: undefined });

        await openMenu();

        expect(screen.getByRole("button", { name: /apply/i })).toBeDisabled();
        expect(screen.getByText(/at most 366 days/i)).toBeInTheDocument();
    });
});
