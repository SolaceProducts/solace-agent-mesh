// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { GrantCard } from "./GrantCard";

const none = { invoke: false, feedbackRead: false, traceRead: false };

describe("GrantCard", () => {
    test("renders the three grants with Can use locked on", () => {
        renderWithProviders(<GrantCard label="HR Assistant" description="Answers HR questions." showTraces grants={{ ...none, invoke: true }} onGrantChange={() => {}} onRemove={() => {}} />);

        expect(screen.getByText("HR Assistant")).toBeInTheDocument();
        expect(screen.getByText("Answers HR questions.")).toBeInTheDocument();
        const use = screen.getByRole("checkbox", { name: "HR Assistant: Can use" });
        expect(use).toBeChecked();
        expect(use).toBeDisabled();
        expect(screen.getByText("— always granted")).toBeInTheDocument();
        expect(screen.getByRole("checkbox", { name: "HR Assistant: Can view feedback" })).not.toBeChecked();
        expect(screen.getByRole("checkbox", { name: "HR Assistant: Can view activities" })).not.toBeChecked();
    });

    test("traces is editable on its own, without feedback", async () => {
        const user = userEvent.setup();
        const onGrantChange = vi.fn();
        renderWithProviders(<GrantCard label="A" showTraces grants={none} onGrantChange={onGrantChange} onRemove={() => {}} />);
        const traces = screen.getByRole("checkbox", { name: "A: Can view activities" });
        expect(traces).toBeEnabled();

        await user.click(traces);
        expect(onGrantChange).toHaveBeenLastCalledWith("traceRead", true);
    });

    test("clicking a box reports the grant and the new state", async () => {
        const user = userEvent.setup();
        const onGrantChange = vi.fn();
        renderWithProviders(<GrantCard label="A" showTraces grants={{ invoke: false, feedbackRead: true, traceRead: false }} onGrantChange={onGrantChange} onRemove={() => {}} />);

        await user.click(screen.getByRole("checkbox", { name: "A: Can view activities" }));
        expect(onGrantChange).toHaveBeenLastCalledWith("traceRead", true);

        await user.click(screen.getByRole("checkbox", { name: "A: Can view feedback" }));
        expect(onGrantChange).toHaveBeenLastCalledWith("feedbackRead", false);
    });

    test("clicking the label text toggles its box", async () => {
        const user = userEvent.setup();
        const onGrantChange = vi.fn();
        renderWithProviders(<GrantCard label="A" showTraces grants={none} onGrantChange={onGrantChange} onRemove={() => {}} />);

        await user.click(screen.getByText("Can view feedback"));
        expect(onGrantChange).toHaveBeenLastCalledWith("feedbackRead", true);
    });

    test("the All card shows only the coverage subtext", () => {
        renderWithProviders(<GrantCard label="All Agents" allOf={{ noun: "agent" }} showTraces grants={none} onGrantChange={() => {}} onRemove={() => {}} />);
        expect(screen.getByText("Every agent, including ones created later.")).toBeInTheDocument();
    });

    test("disabled mode disables every control and hides remove", () => {
        renderWithProviders(<GrantCard label="A" showTraces grants={{ invoke: true, feedbackRead: true, traceRead: true }} onGrantChange={() => {}} onRemove={() => {}} disabled />);
        for (const name of ["A: Can use", "A: Can view feedback", "A: Can view activities"]) {
            expect(screen.getByRole("checkbox", { name })).toBeDisabled();
        }
        expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
    });

    test("remove calls onRemove", async () => {
        const user = userEvent.setup();
        const onRemove = vi.fn();
        renderWithProviders(<GrantCard label="A" showTraces grants={none} onGrantChange={() => {}} onRemove={onRemove} />);
        await user.click(screen.getByRole("button", { name: "Remove" }));
        expect(onRemove).toHaveBeenCalledTimes(1);
    });

    test("a workflow card renders Can use and Can view feedback only", () => {
        renderWithProviders(<GrantCard label="Onboarding" showTraces={false} grants={{ invoke: true, feedbackRead: false, traceRead: false }} onGrantChange={vi.fn()} onRemove={vi.fn()} />);
        expect(screen.getByLabelText("Onboarding: Can use")).toBeInTheDocument();
        expect(screen.getByLabelText("Onboarding: Can view feedback")).toBeInTheDocument();
        expect(screen.queryByLabelText("Onboarding: Can view activities")).not.toBeInTheDocument();
    });

    test("read-only mode shows Can use from the actual grant, with no promise note", () => {
        renderWithProviders(<GrantCard label="HR Agent" showTraces grants={{ invoke: false, feedbackRead: true, traceRead: false }} onGrantChange={vi.fn()} onRemove={vi.fn()} disabled />);
        expect(screen.getByLabelText("HR Agent: Can use")).not.toBeChecked();
        expect(screen.queryByText(/always granted/)).not.toBeInTheDocument();
    });

    test("editable mode keeps the always-granted promise", () => {
        renderWithProviders(<GrantCard label="HR Agent" showTraces grants={{ invoke: false, feedbackRead: true, traceRead: false }} onGrantChange={vi.fn()} onRemove={vi.fn()} />);
        expect(screen.getByLabelText("HR Agent: Can use")).toBeChecked();
        expect(screen.getByText(/always granted/)).toBeInTheDocument();
    });
});
