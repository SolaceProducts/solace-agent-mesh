// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { agentUIMode, UIModeContext } from "@/providers/contexts/UIModeContext";

import { TooltipProvider } from "@/ui/tooltip";

import { InlineProgressUpdates } from "@/features/chat/InlineProgressUpdates";

import type { ProgressUpdate } from "@/types";

const makeUpdate = (text: string, type: ProgressUpdate["type"] = "status", overrides?: Partial<ProgressUpdate>): ProgressUpdate => ({
    type,
    text,
    timestamp: Date.now(),
    ...overrides,
});

function renderUpdates(props: React.ComponentProps<typeof InlineProgressUpdates>) {
    return render(
        <TooltipProvider>
            <InlineProgressUpdates {...props} />
        </TooltipProvider>
    );
}

function renderInAgentMode(props: React.ComponentProps<typeof InlineProgressUpdates>) {
    return render(
        <UIModeContext.Provider value={agentUIMode("my-agent")}>
            <TooltipProvider>
                <InlineProgressUpdates {...props} />
            </TooltipProvider>
        </UIModeContext.Provider>
    );
}

describe("InlineProgressUpdates", () => {
    it("renders nothing when updates is empty", () => {
        const { container } = renderUpdates({ updates: [] });
        expect(container.innerHTML).toBe("");
    });

    it("renders a single status update with text", () => {
        renderUpdates({ updates: [makeUpdate("Searching...")], isActive: true });
        expect(screen.getByText("Searching...")).toBeInTheDocument();
    });

    it("drops a trailing unicode ellipsis from a finished entry when allDone (reads as done, not in-progress)", () => {
        renderUpdates({ updates: [makeUpdate("Updating agent name in config artifact\u2026")], isActive: false, allDone: true, headerless: true });
        expect(screen.getByText("Updating agent name in config artifact")).toBeInTheDocument();
        expect(screen.queryByText("Updating agent name in config artifact\u2026")).toBeNull();
    });

    it("drops a trailing three-dot ellipsis from a finished entry when allDone", () => {
        renderUpdates({ updates: [makeUpdate("Generating config...")], isActive: false, allDone: true, headerless: true });
        expect(screen.getByText("Generating config")).toBeInTheDocument();
        expect(screen.queryByText("Generating config...")).toBeNull();
    });

    it("strips ellipses from every finished step in a multi-step done timeline", () => {
        renderUpdates({ updates: [makeUpdate("Reading config\u2026"), makeUpdate("Updating name...")], isActive: false, allDone: true, headerless: true });
        expect(screen.getByText("Reading config")).toBeInTheDocument();
        expect(screen.getByText("Updating name")).toBeInTheDocument();
    });

    it("leaves text untouched when allDone is false (still in progress)", () => {
        renderUpdates({ updates: [makeUpdate("Updating\u2026")], isActive: true });
        expect(screen.getByText("Updating\u2026")).toBeInTheDocument();
    });

    it("keeps the active last step's ellipsis even when allDone, while stripping the earlier finished steps", () => {
        renderUpdates({ updates: [makeUpdate("Reading config\u2026"), makeUpdate("Updating\u2026")], isActive: true, allDone: true, headerless: true });
        expect(screen.getByText("Updating\u2026")).toBeInTheDocument();
        expect(screen.getByText("Reading config")).toBeInTheDocument();
    });

    it("only strips a trailing ellipsis, leaving a mid-text ellipsis intact", () => {
        renderUpdates({ updates: [makeUpdate("Phase\u2026 one done\u2026")], isActive: false, allDone: true, headerless: true });
        expect(screen.getByText("Phase\u2026 one done")).toBeInTheDocument();
    });

    it("shows only the latest step while streaming, then the full list once expanded", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2"), makeUpdate("Step 3")];
        renderUpdates({ updates, isActive: true });
        expect(screen.getByText("Step 3")).toBeInTheDocument();
        expect(screen.queryByText("Step 1")).not.toBeInTheDocument();
        expect(screen.queryByText("Step 2")).not.toBeInTheDocument();

        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByText("Step 1")).toBeInTheDocument();
        expect(screen.getByText("Step 2")).toBeInTheDocument();
        expect(screen.getByText("Step 3")).toBeInTheDocument();
    });

    it("deduplicates consecutive identical status updates in the expanded list", () => {
        const updates = [makeUpdate("Processing..."), makeUpdate("Processing..."), makeUpdate("Done")];
        renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getAllByText("Processing...")).toHaveLength(1);
        expect(screen.getByText("Done")).toBeInTheDocument();
    });

    it("does not deduplicate thinking type updates in the expanded list", () => {
        const updates = [makeUpdate("Thinking", "thinking", { expandableContent: "First thought", isExpandableComplete: true }), makeUpdate("Thinking", "thinking", { expandableContent: "Second thought", isExpandableComplete: true })];
        renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getAllByText("Thinking")).toHaveLength(2);
    });

    it("collapses to 'Activity Timeline' button when task completes", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2")];
        const { rerender } = renderUpdates({ updates, isActive: true });
        expect(screen.getByText("Step 2")).toBeInTheDocument();

        rerender(
            <TooltipProvider>
                <InlineProgressUpdates updates={updates} isActive={false} />
            </TooltipProvider>
        );
        expect(screen.queryByText("Step 2")).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Activity Timeline/i })).toBeInTheDocument();
    });

    it("collapses back to the live line when the expanded header is clicked mid-stream", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2")];
        renderUpdates({ updates, isActive: true });

        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByText("Step 1")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: /Activity Timeline/i }));
        expect(screen.queryByText("Step 1")).not.toBeInTheDocument();
        expect(screen.getByTitle("Show all activity steps")).toBeInTheDocument();
    });

    it("auto-collapses to the summary chip when a mid-stream expansion is still open at completion", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2")];
        const { rerender } = renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByText("Step 1")).toBeInTheDocument();

        rerender(
            <TooltipProvider>
                <InlineProgressUpdates updates={updates} isActive={false} />
            </TooltipProvider>
        );
        expect(screen.queryByText("Step 1")).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Activity Timeline/i })).toBeInTheDocument();
    });

    it("exposes the live line as a collapsed disclosure with the full step text recoverable", () => {
        const longStep = "Updating the agent configuration artifact with the newly generated tool definitions";
        renderUpdates({ updates: [makeUpdate(longStep)], isActive: true });

        const disclosure = screen.getByTitle("Show all activity steps");
        expect(disclosure).toHaveAttribute("aria-expanded", "false");
        expect(screen.getByTitle(longStep)).toBeInTheDocument();
    });

    it("keeps the active step's trailing ellipsis on the live line", () => {
        renderUpdates({ updates: [makeUpdate("Updating…")], isActive: true, allDone: true });
        expect(screen.getByText("Updating…")).toBeInTheDocument();
    });

    it("renders exactly one working indicator on the live line", () => {
        renderUpdates({ updates: [makeUpdate("Step 1")], isActive: true });
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("expands timeline when clicking 'Activity Timeline'", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2")];
        const { rerender } = renderUpdates({ updates, isActive: true });
        rerender(
            <TooltipProvider>
                <InlineProgressUpdates updates={updates} isActive={false} />
            </TooltipProvider>
        );

        fireEvent.click(screen.getByRole("button", { name: /Activity Timeline/i }));
        expect(screen.getByText("Step 1")).toBeInTheDocument();
        expect(screen.getByText("Step 2")).toBeInTheDocument();
    });

    it("renders only first and last steps when the expanded list exceeds the collapse threshold", () => {
        const updates = Array.from({ length: 12 }, (_, i) => makeUpdate(`UniqueStep${i + 1}`));
        renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByText("UniqueStep1")).toBeInTheDocument();
        expect(screen.getByText("UniqueStep12")).toBeInTheDocument();
        expect(screen.queryByText("UniqueStep5")).not.toBeInTheDocument();
    });

    it("shows all steps after clicking the 'more steps' expand button", () => {
        const updates = Array.from({ length: 12 }, (_, i) => makeUpdate(`Step ${i + 1}`));
        renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));

        const expandButton = screen.getByRole("button", { name: /more step/i });
        fireEvent.click(expandButton);

        for (let i = 1; i <= 12; i++) {
            expect(screen.getByText(`Step ${i}`)).toBeInTheDocument();
        }
    });

    it("renders thinking updates collapsed by default in the expanded timeline", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Thinking", "thinking", { expandableContent: "Deep analysis..." })];
        renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByText("Thinking")).toBeInTheDocument();
        expect(screen.queryByText("Deep analysis...")).not.toBeInTheDocument();
    });

    it("expands thinking content on click", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Thinking", "thinking", { expandableContent: "Deep analysis..." })];
        renderUpdates({ updates, isActive: true });
        fireEvent.click(screen.getByTitle("Show all activity steps"));

        fireEvent.click(screen.getByText("Thinking"));
        expect(screen.getByText("Deep analysis...")).toBeInTheDocument();
    });

    it("invokes onViewWorkflow when the workflow button is clicked while streaming", () => {
        const onViewWorkflow = vi.fn();
        renderUpdates({ updates: [makeUpdate("Processing...")], isActive: true, onViewWorkflow });

        fireEvent.click(screen.getByTestId("viewActivity"));
        expect(onViewWorkflow).toHaveBeenCalledTimes(1);
    });

    it("keeps the workflow button visible after the task completes", () => {
        const onViewWorkflow = vi.fn();
        const updates = [makeUpdate("Done")];
        const { rerender } = renderUpdates({ updates, isActive: true, onViewWorkflow });
        rerender(
            <TooltipProvider>
                <InlineProgressUpdates updates={updates} isActive={false} onViewWorkflow={onViewWorkflow} />
            </TooltipProvider>
        );
        expect(screen.getByTestId("viewActivity")).toBeInTheDocument();
    });

    it("labels the activity button with the shared tooltip", () => {
        renderUpdates({ updates: [makeUpdate("Processing...")], isActive: true, onViewWorkflow: vi.fn() });
        expect(screen.getByTestId("viewActivity")).toHaveAttribute("aria-label", "View activity details");
    });

    it("hides the activity button until hover while the task streams", () => {
        renderUpdates({ updates: [makeUpdate("Processing...")], isActive: true, onViewWorkflow: vi.fn() });

        const button = screen.getByTestId("viewActivity");
        expect(button).toHaveClass("opacity-0");
        expect(button).toHaveClass("group-hover:opacity-100");
        expect(button).toHaveClass("focus-visible:opacity-100");
    });

    it("shows the activity button statically once the task completes", () => {
        const onViewWorkflow = vi.fn();
        const updates = [makeUpdate("Done")];
        const { rerender } = renderUpdates({ updates, isActive: true, onViewWorkflow });
        rerender(
            <TooltipProvider>
                <InlineProgressUpdates updates={updates} isActive={false} onViewWorkflow={onViewWorkflow} />
            </TooltipProvider>
        );
        expect(screen.getByTestId("viewActivity")).not.toHaveClass("opacity-0");
    });

    it("shows the activity button outright in the expanded timeline even while streaming", () => {
        renderUpdates({ updates: [makeUpdate("Step 1")], isActive: true, onViewWorkflow: vi.fn() });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByTestId("viewActivity")).not.toHaveClass("opacity-0");
    });

    it("headerless mode renders only the items — no header or collapse toggle", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2")];
        renderUpdates({ updates, isActive: true, headerless: true });
        expect(screen.getByText("Step 1")).toBeInTheDocument();
        expect(screen.getByText("Step 2")).toBeInTheDocument();
        expect(screen.queryByText("Activity Timeline")).not.toBeInTheDocument();
    });

    it("agent mode still shows the live status step", () => {
        renderInAgentMode({ updates: [makeUpdate("Reading config...")], isActive: true, onViewWorkflow: vi.fn() });
        expect(screen.getByText("Reading config...")).toBeInTheDocument();
    });

    it("agent mode still expands to the full step list", () => {
        renderInAgentMode({ updates: [makeUpdate("Step 1"), makeUpdate("Step 2")], isActive: true, onViewWorkflow: vi.fn() });
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.getByText("Step 1")).toBeInTheDocument();
        expect(screen.getByText("Step 2")).toBeInTheDocument();
    });

    it("agent mode drops the activity button in every timeline state", () => {
        const updates = [makeUpdate("Step 1"), makeUpdate("Step 2")];
        const { rerender } = renderInAgentMode({ updates, isActive: true, onViewWorkflow: vi.fn() });
        expect(screen.queryByTestId("viewActivity")).not.toBeInTheDocument();

        fireEvent.click(screen.getByTitle("Show all activity steps"));
        expect(screen.queryByTestId("viewActivity")).not.toBeInTheDocument();

        rerender(
            <UIModeContext.Provider value={agentUIMode("my-agent")}>
                <TooltipProvider>
                    <InlineProgressUpdates updates={updates} isActive={false} onViewWorkflow={vi.fn()} />
                </TooltipProvider>
            </UIModeContext.Provider>
        );
        expect(screen.getByRole("button", { name: /Activity Timeline/i })).toBeInTheDocument();
        expect(screen.queryByTestId("viewActivity")).not.toBeInTheDocument();
    });

    it("an interrupted turn marks only its last step as stopped", () => {
        renderUpdates({ updates: [makeUpdate("Reading config…"), makeUpdate("Generating the agent configuration...")], isActive: false, interrupted: true, headerless: true });

        expect(screen.getByText("Generating the agent configuration (interrupted)")).toBeInTheDocument();
        expect(screen.getByText("Reading config…")).toBeInTheDocument();
    });

    it("an interrupted turn that is also marked done strips the ellipsis of earlier steps and marks the last one, in a warning colour", () => {
        const { container } = renderUpdates({ updates: [makeUpdate("Reading config\u2026"), makeUpdate("Generating the agent configuration...")], isActive: false, allDone: true, interrupted: true, headerless: true });

        expect(screen.getByText("Reading config")).toBeInTheDocument();
        expect(screen.getByText("Generating the agent configuration (interrupted)")).toBeInTheDocument();
        expect(container.querySelectorAll(".bg-\\(--warning-wMain\\)")).toHaveLength(1);
    });

    it("an interrupted turn whose last step is a thinking row labels that row too", () => {
        renderUpdates({ updates: [makeUpdate("Reading config\u2026"), makeUpdate("Thinking about the schema", "thinking")], isActive: false, interrupted: true, headerless: true });

        expect(screen.getByText("Thinking about the schema (interrupted)")).toBeInTheDocument();
    });

    it("a thinking row that is not part of an interrupted turn keeps its text", () => {
        renderUpdates({ updates: [makeUpdate("Thinking about the schema\u2026", "thinking")], isActive: false, allDone: true, headerless: true });

        expect(screen.getByText("Thinking about the schema\u2026")).toBeInTheDocument();
    });

    it("a turn that was not interrupted keeps every step as written", () => {
        renderUpdates({ updates: [makeUpdate("Generating the agent configuration...")], isActive: false, headerless: true });

        expect(screen.getByText("Generating the agent configuration...")).toBeInTheDocument();
        expect(screen.queryByText(/\(interrupted\)/)).not.toBeInTheDocument();
    });

    it("a live turn is never marked stopped", () => {
        renderUpdates({ updates: [makeUpdate("Generating the agent configuration...")], isActive: true, interrupted: true, headerless: true });

        expect(screen.queryByText(/\(interrupted\)/)).not.toBeInTheDocument();
    });

    it("headerless mode does not auto-collapse when the task completes", () => {
        const updates = [makeUpdate("Step 1")];
        const { rerender } = renderUpdates({ updates, isActive: true, headerless: true });
        rerender(
            <TooltipProvider>
                <InlineProgressUpdates updates={updates} isActive={false} headerless />
            </TooltipProvider>
        );
        expect(screen.getByText("Step 1")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Activity Timeline/i })).not.toBeInTheDocument();
    });
});
