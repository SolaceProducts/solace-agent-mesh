// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";
import type { SharedTaskEvents } from "@/types/share";

import { SharedWorkflowPanel } from "@/features/share/SharedWorkflowPanel";

import type { A2AEventSSEPayload } from "@/types";

function makeSharedEvent(overrides: Partial<A2AEventSSEPayload> = {}): A2AEventSSEPayload {
    return {
        event_type: "a2a_message",
        timestamp: "2024-06-15T10:00:00.000Z",
        solace_topic: "test/topic",
        direction: "response",
        source_entity: "Orchestrator",
        target_entity: "User",
        message_id: "msg-1",
        task_id: "task-1",
        payload_summary: { method: "message/send", params_preview: undefined },
        full_payload: {},
        ...overrides,
    };
}

function makeUserRequestEvent(text: string, taskId = "task-1"): A2AEventSSEPayload {
    return makeSharedEvent({
        direction: "request",
        task_id: taskId,
        source_entity: "User",
        target_entity: "Orchestrator",
        payload_summary: { method: "message/send", params_preview: text },
        full_payload: {
            method: "message/send",
            params: { message: { parts: [{ kind: "text", text }] } },
        },
    });
}

function makeCompletedResponse(text: string, agent = "Orchestrator", taskId = "task-1"): A2AEventSSEPayload {
    return makeSharedEvent({
        direction: "response",
        task_id: taskId,
        source_entity: agent,
        target_entity: "User",
        full_payload: {
            result: {
                status: { state: "completed", message: { parts: [{ kind: "text", text }] } },
                metadata: { agent_name: agent },
            },
        },
    });
}

function renderPanel(taskEvents: Record<string, SharedTaskEvents> | null, selectedTaskId: string | null = null, onTaskSelect = vi.fn()) {
    const utils = render(
        <TooltipProvider>
            <SharedWorkflowPanel taskEvents={taskEvents} selectedTaskId={selectedTaskId} onTaskSelect={onTaskSelect} />
        </TooltipProvider>
    );
    return { ...utils, onTaskSelect };
}

describe("SharedWorkflowPanel", () => {
    test("shows 'No workflow data' when taskEvents is null", () => {
        renderPanel(null);
        expect(screen.getByText("No workflow data available for this session")).toBeInTheDocument();
    });

    test("shows 'No workflow data' when taskEvents is empty object", () => {
        renderPanel({});
        expect(screen.getByText("No workflow data available for this session")).toBeInTheDocument();
    });

    test("shows 'Unable to process' when selectedTaskId does not match any task", () => {
        const taskEvents: Record<string, SharedTaskEvents> = {
            "task-1": {
                taskId: "task-1",
                events: [makeUserRequestEvent("Hello")],
                initialRequestText: "Hello",
            },
        };
        renderPanel(taskEvents, "nonexistent-task");
        expect(screen.getByText("Unable to process workflow data")).toBeInTheDocument();
    });

    test("auto-selects root task when no task is selected", () => {
        const taskEvents: Record<string, SharedTaskEvents> = {
            "task-1": {
                taskId: "task-1",
                events: [makeUserRequestEvent("Hello")],
                initialRequestText: "Hello",
            },
        };
        const { onTaskSelect } = renderPanel(taskEvents, null);
        expect(onTaskSelect).toHaveBeenCalledWith("task-1");
    });

    test("renders initial request text and completed status when task succeeds", () => {
        const taskEvents: Record<string, SharedTaskEvents> = {
            "task-1": {
                taskId: "task-1",
                events: [makeUserRequestEvent("Hello agent"), makeCompletedResponse("Hi there!")],
                initialRequestText: "Hello agent",
            },
        };
        renderPanel(taskEvents, "task-1");
        expect(screen.getByText("Hello agent")).toBeInTheDocument();
        expect(screen.getByText("Completed")).toBeInTheDocument();
    });

    test("shows 'No request text' when initialRequestText is empty", () => {
        const taskEvents: Record<string, SharedTaskEvents> = {
            "task-1": {
                taskId: "task-1",
                events: [makeUserRequestEvent("")],
                initialRequestText: "",
            },
        };
        renderPanel(taskEvents, "task-1");
        expect(screen.getByText("No request text")).toBeInTheDocument();
    });

    test("renders snake_case backend events without crashing", () => {
        const taskEvents: Record<string, SharedTaskEvents> = {
            "task-1": {
                taskId: "task-1",
                events: [makeUserRequestEvent("Hello agent"), makeCompletedResponse("Hi there!")],
                initialRequestText: "Hello agent",
            },
        };
        renderPanel(taskEvents, "task-1");
        expect(screen.getByText("Hello agent")).toBeInTheDocument();
        expect(screen.getByText("Completed")).toBeInTheDocument();
    });

    test("tolerates events with no payload_summary", () => {
        const eventWithoutSummary = makeSharedEvent({ direction: "request", source_entity: "User" });
        delete (eventWithoutSummary as { payload_summary?: unknown }).payload_summary;
        const taskEvents: Record<string, SharedTaskEvents> = {
            "task-1": {
                taskId: "task-1",
                events: [eventWithoutSummary],
                initialRequestText: "Hi",
            },
        };
        expect(() => renderPanel(taskEvents, "task-1")).not.toThrow();
    });
});
