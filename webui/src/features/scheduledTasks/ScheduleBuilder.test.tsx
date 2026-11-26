// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ScheduleBuilder } from "@/features/scheduledTasks/ScheduleBuilder";

function renderBuilder(props: Partial<React.ComponentProps<typeof ScheduleBuilder>> = {}) {
    const defaultProps = {
        value: "0 9 * * *",
        onChange: vi.fn(),
        ...props,
    };
    return { ...render(<ScheduleBuilder {...defaultProps} />), onChange: defaultProps.onChange };
}

describe("ScheduleBuilder", () => {
    it("calls onChange on initial mount with the canonical cron", () => {
        const onChange = vi.fn();
        renderBuilder({ value: "0 9 * * *", onChange });
        expect(onChange).toHaveBeenCalledWith("0 9 * * *");
    });

    it("falls back to custom mode for non-representable cron and preserves the original", () => {
        renderBuilder({ value: "0 0 1 1 *" });

        const cronInput = screen.getByPlaceholderText("0 9 * * *");
        expect(cronInput).toHaveValue("0 0 1 1 *");
    });

    it("custom mode validates cron — invalid cron shows error", () => {
        renderBuilder({ value: "0 0 1 1 *" });

        const cronInput = screen.getByPlaceholderText("0 9 * * *");

        fireEvent.change(cronInput, { target: { value: "* *" } });
        expect(screen.getByText(/Cron expression must have exactly 5 fields/)).toBeInTheDocument();

        fireEvent.change(cronInput, { target: { value: "0 9 * * 1" } });
        expect(screen.queryByText(/Cron expression must have exactly 5 fields/)).not.toBeInTheDocument();
    });

    it("custom mode rejects out-of-range cron values", () => {
        renderBuilder({ value: "0 0 1 1 *" });
        const cronInput = screen.getByPlaceholderText("0 9 * * *");

        fireEvent.change(cronInput, { target: { value: "0 25 * * *" } });
        expect(screen.getByText(/Invalid hour field/)).toBeInTheDocument();
    });

    it("custom mode accepts complex cron grammar (ranges, steps, lists)", () => {
        renderBuilder({ value: "0 0 1 1 *" });
        const cronInput = screen.getByPlaceholderText("0 9 * * *");

        fireEvent.change(cronInput, { target: { value: "0 9-17/2 * * 1-5" } });
        expect(screen.queryByText(/Invalid/)).not.toBeInTheDocument();

        fireEvent.change(cronInput, { target: { value: "0 9 * * 1-3,5" } });
        expect(screen.queryByText(/Invalid/)).not.toBeInTheDocument();

        fireEvent.change(cronInput, { target: { value: "*/15,30 * * * *" } });
        expect(screen.queryByText(/Invalid/)).not.toBeInTheDocument();
    });

    it("custom mode shows the friendly preview for a valid cron", () => {
        renderBuilder({ value: "0 0 1 1 *" });
        const cronInput = screen.getByPlaceholderText("0 9 * * *");

        fireEvent.change(cronInput, { target: { value: "0 14 * * 1" } });

        expect(screen.getByText(/Every Monday at 2:00 PM/)).toBeInTheDocument();
    });

    it("emits cron updates through onChange when the cron input changes", () => {
        const onChange = vi.fn();
        renderBuilder({ value: "0 0 1 1 *", onChange });
        const cronInput = screen.getByPlaceholderText("0 9 * * *");

        onChange.mockClear();
        fireEvent.change(cronInput, { target: { value: "0 14 * * 1" } });

        expect(onChange).toHaveBeenCalledWith("0 14 * * 1");
    });
});
