// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { MessageFE, VisualizedTask } from "@/types";

import { FlowChartDetails } from "./FlowChartDetails";

const STATUS_TEXT = "Checking existing platform components";

const makeTask = (overrides: Partial<VisualizedTask> = {}): VisualizedTask => ({ taskId: "t1", status: "working", currentStatusText: STATUS_TEXT, initialRequestText: "translate docs", ...overrides }) as unknown as VisualizedTask;

const agentMsg = (isComplete: boolean, taskId = "t1"): MessageFE => ({ role: "agent", isUser: false, taskId, isComplete, parts: [] }) as unknown as MessageFE;
const statusBubble = (): MessageFE => ({ role: "agent", isUser: false, isStatusBubble: true, isComplete: false, parts: [] }) as unknown as MessageFE;

function renderDetails(task: VisualizedTask, messages: MessageFE[]) {
    return render(
        <StoryProvider chatContextValues={{ messages }}>
            <FlowChartDetails task={task} />
        </StoryProvider>
    );
}

describe("FlowChartDetails — status header", () => {
    test("a live task (an incomplete message for the task) spins the last status", () => {
        const { container } = renderDetails(makeTask(), [agentMsg(false)]);
        expect(screen.getByText(STATUS_TEXT)).toBeInTheDocument();
        expect(container.querySelector(".animate-spin")).not.toBeNull();
    });

    test("a restored task (all messages complete, no status bubble) shows the last status statically — no spinner", () => {
        const { container } = renderDetails(makeTask(), [agentMsg(true)]);
        expect(screen.getByText(STATUS_TEXT)).toBeInTheDocument();
        expect(container.querySelector(".animate-spin")).toBeNull();
    });

    test("a transient status bubble keeps the task live (spins) even with no incomplete task message", () => {
        const { container } = renderDetails(makeTask(), [agentMsg(true), statusBubble()]);
        expect(container.querySelector(".animate-spin")).not.toBeNull();
    });

    test("an incomplete message for a DIFFERENT task does not spin this task", () => {
        const { container } = renderDetails(makeTask({ taskId: "t1" }), [agentMsg(false, "t2")]);
        expect(screen.getByText(STATUS_TEXT)).toBeInTheDocument();
        expect(container.querySelector(".animate-spin")).toBeNull();
    });

    test("a restored working task with no status text falls back to an 'Ended' badge (no spinner)", () => {
        const { container } = renderDetails(makeTask({ status: "working", currentStatusText: undefined }), [agentMsg(true)]);
        expect(screen.getByText("Ended")).toBeInTheDocument();
        expect(container.querySelector(".animate-spin")).toBeNull();
    });

    test("a completed task shows the terminal badge (unchanged)", () => {
        renderDetails(makeTask({ status: "completed", currentStatusText: undefined }), [agentMsg(true)]);
        expect(screen.getByText("Completed")).toBeInTheDocument();
    });
});
