// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DatePicker } from "./date-picker";

async function openCalendar(label: string) {
    await userEvent.click(screen.getByRole("button", { name: label }));
}

describe("DatePicker bounds", () => {
    it("disables days before min and leaves the rest selectable", async () => {
        render(<DatePicker value="2026-08-15" min="2026-08-10" onChange={vi.fn()} />);

        await openCalendar("2026-08-15");

        expect(screen.getByRole("button", { name: "9" })).toBeDisabled();
        expect(screen.getByRole("button", { name: "10" })).toBeEnabled();
    });

    it("disables days after max and leaves the rest selectable", async () => {
        render(<DatePicker value="2026-08-15" max="2026-08-20" onChange={vi.fn()} />);

        await openCalendar("2026-08-15");

        expect(screen.getByRole("button", { name: "21" })).toBeDisabled();
        expect(screen.getByRole("button", { name: "20" })).toBeEnabled();
    });

    it("reports a day inside both bounds", async () => {
        const onChange = vi.fn();
        render(<DatePicker value="2026-08-15" min="2026-08-10" max="2026-08-20" onChange={onChange} />);

        await openCalendar("2026-08-15");
        await userEvent.click(screen.getByRole("button", { name: "12" }));

        expect(onChange).toHaveBeenCalledWith("2026-08-12");
    });

    it("leaves every day selectable when neither bound is given", async () => {
        render(<DatePicker value="2026-08-15" onChange={vi.fn()} />);

        await openCalendar("2026-08-15");

        expect(screen.getByRole("button", { name: "1" })).toBeEnabled();
        expect(screen.getByRole("button", { name: "28" })).toBeEnabled();
    });

    it("takes an accessible name so sibling pickers stay distinguishable", () => {
        render(
            <>
                <DatePicker value="2026-08-15" ariaLabel="Start date" onChange={vi.fn()} />
                <DatePicker value="2026-08-15" ariaLabel="End date" onChange={vi.fn()} />
            </>
        );

        expect(screen.getByRole("button", { name: "Start date" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "End date" })).toBeInTheDocument();
    });
});
