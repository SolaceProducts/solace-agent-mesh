// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { evaluationsApi } from "@/api/evaluations/versioned";

import { StoryProvider } from "@/testing/providers/StoryProvider";

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useEvalExperiments: () => ({
            data: [
                { id: "exp-idle", name: "Idle" },
                { id: "exp-busy", name: "Busy" },
            ],
            isLoading: false,
        }),
    };
});

import { RunExperimentsDialog } from "./RunExperimentsDialog";

describe("RunExperimentsDialog", () => {
    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("a refused experiment keeps the dialog open on it while the others start", async () => {
        const user = userEvent.setup();
        const trigger = vi.spyOn(evaluationsApi, "triggerRun").mockImplementation(async id => {
            if (id === "exp-busy") throw new Error("A run of this experiment is already pending or running.");
            return { runGroupId: "group-1" };
        });
        const onOpenChange = vi.fn();
        const onRunComplete = vi.fn();
        render(
            <StoryProvider>
                <RunExperimentsDialog open onOpenChange={onOpenChange} onRunComplete={onRunComplete} />
            </StoryProvider>
        );

        await user.click(screen.getByRole("button", { expanded: false }));
        await user.click(screen.getByRole("button", { name: "Idle" }));
        await user.click(screen.getByRole("button", { name: "Busy" }));
        await user.click(screen.getByRole("button", { name: "Run" }));

        expect(await screen.findByText("Busy: A run of this experiment is already pending or running.")).toBeInTheDocument();
        await waitFor(() => expect(onRunComplete).toHaveBeenCalledTimes(1));
        expect(onOpenChange).not.toHaveBeenCalledWith(false);

        trigger.mockClear();
        await user.click(screen.getByRole("button", { name: "Run" }));
        await waitFor(() => expect(trigger).toHaveBeenCalledTimes(1));
        expect(trigger).toHaveBeenCalledWith("exp-busy", {});
    });
});
