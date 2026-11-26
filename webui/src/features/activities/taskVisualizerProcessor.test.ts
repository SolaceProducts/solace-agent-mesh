// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import {
    createCompletedResponseEvent,
    createEvent,
    createFailedResponseEvent,
    createRequestEvent,
    createSignalEvent,
    createStatusUpdateEvent,
    createTask,
    offsetTime,
    pendingAuthToolCallMonitoredTasks,
    pendingAuthToolCallTask,
    reportToolCallStartEvents,
    reportToolResultEvent,
    reportToolSignInPlaceholderEvents,
} from "@/testing/data";
import type { A2AEventSSEPayload, TaskFE } from "@/types";

import { processTaskForVisualization } from "./taskVisualizerProcessor";

describe("processTaskForVisualization", () => {
    test("returns null when parentTaskObject is null", () => {
        const result = processTaskForVisualization([], {}, null);
        expect(result).toBeNull();
    });

    test("returns empty steps when task has no events", () => {
        const task = createTask({ events: [] });
        const result = processTaskForVisualization([], { "task-1": task }, task);
        expect(result).not.toBeNull();
        expect(result!.steps).toEqual([]);
        expect(result!.status).toBe("working");
    });

    test("produces USER_REQUEST step from a request event", () => {
        const requestEvent = createRequestEvent("What is the weather?");
        const task = createTask({ events: [requestEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        const userRequestSteps = result!.steps.filter(s => s.type === "USER_REQUEST");
        expect(userRequestSteps).toHaveLength(1);
        expect(userRequestSteps[0].data.text).toBe("What is the weather?");
        expect(userRequestSteps[0].source).toBe("User");
    });

    test("produces TASK_COMPLETED step from a completed response", () => {
        const requestEvent = createRequestEvent("Hello");
        const responseEvent = createCompletedResponseEvent("Hi there!");
        const task = createTask({ events: [requestEvent, responseEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        expect(result!.status).toBe("completed");

        const completedSteps = result!.steps.filter(s => s.type === "TASK_COMPLETED");
        expect(completedSteps).toHaveLength(1);
        expect(completedSteps[0].source).toBe("Orchestrator");
    });

    test("produces TASK_FAILED step from a failed response", () => {
        const requestEvent = createRequestEvent("Do something impossible");
        const responseEvent = createFailedResponseEvent("Something went wrong");
        const task = createTask({ events: [requestEvent, responseEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        expect(result!.status).toBe("failed");

        const failedSteps = result!.steps.filter(s => s.type === "TASK_FAILED");
        expect(failedSteps).toHaveLength(1);
    });

    test("ends a task stopped by a JSON-RPC error response, keeping the error's message, code and data", () => {
        const requestEvent = createRequestEvent("Summarise this", "2024-01-01T00:00:00.000Z");
        const statusEvent = createStatusUpdateEvent("Thinking...", "Orchestrator", "2024-01-01T00:00:01.000Z");
        const budgetStop = createEvent({
            direction: "error_response",
            timestamp: "2024-01-01T00:00:02.000Z",
            full_payload: {
                error: {
                    code: -32603,
                    message: "This request was stopped because your usage budget is used up until 2024-02-01.",
                    data: { llm_error_category: "budget_exhausted" },
                },
            },
        });
        const task = createTask({ events: [requestEvent, statusEvent, budgetStop] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result!.status).toBe("failed");
        expect(result!.endTime).toBe("2024-01-01T00:00:02.000Z");
        const failedSteps = result!.steps.filter(s => s.type === "TASK_FAILED");
        expect(failedSteps).toHaveLength(1);
        expect(failedSteps[0].data.errorDetails).toEqual({
            message: "This request was stopped because your usage budget is used up until 2024-02-01.",
            code: -32603,
            details: { llm_error_category: "budget_exhausted" },
        });
    });

    test("calculates duration for completed tasks", () => {
        const requestEvent = createRequestEvent("Hello", "2024-01-01T00:00:00.000Z");
        const responseEvent = createCompletedResponseEvent("Done", "Orchestrator", "2024-01-01T00:00:05.000Z");
        const task = createTask({ events: [requestEvent, responseEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        expect(result!.durationMs).toBe(5000);
        expect(result!.startTime).toBe("2024-01-01T00:00:00.000Z");
        expect(result!.endTime).toBe("2024-01-01T00:00:05.000Z");
    });

    test("status is 'working' when task has no terminal event", () => {
        const requestEvent = createRequestEvent("Hello");
        const statusEvent = createStatusUpdateEvent("Thinking...");
        const task = createTask({ events: [requestEvent, statusEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        expect(result!.status).toBe("working");
        expect(result!.endTime).toBeUndefined();
        expect(result!.durationMs).toBeUndefined();
    });

    test("status line reads 'Saving artifact' while an artifact is still streaming", () => {
        const requestEvent = createRequestEvent("Save a file");
        const progress = createSignalEvent("artifact_creation_progress", { filename: "report.md", status: "in-progress", bytes_transferred: 512 }, "Orchestrator", "2024-01-01T00:00:00.300Z");
        const task = createTask({ events: [requestEvent, progress] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result!.currentStatusText).toBe("Saving artifact: report.md (512 bytes)");
    });

    test("status line switches to 'Saved artifact' once the save completes", () => {
        const requestEvent = createRequestEvent("Save a file");
        const inProgress = createSignalEvent("artifact_creation_progress", { filename: "report.md", status: "in-progress", bytes_transferred: 512 }, "Orchestrator", "2024-01-01T00:00:00.300Z");
        const completed = createSignalEvent("artifact_creation_progress", { filename: "report.md", status: "completed", bytes_transferred: 512 }, "Orchestrator", "2024-01-01T00:00:00.600Z");
        const task = createTask({ events: [requestEvent, inProgress, completed] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result!.currentStatusText).toBe("Saved artifact: report.md");
    });

    test("status line reports a failed artifact save", () => {
        const requestEvent = createRequestEvent("Save a file");
        const failed = createSignalEvent("artifact_creation_progress", { filename: "report.md", status: "failed", bytes_transferred: 512 }, "Orchestrator", "2024-01-01T00:00:00.300Z");
        const task = createTask({ events: [requestEvent, failed] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result!.currentStatusText).toBe("Failed to save artifact: report.md");
    });

    test("status line reports a cancelled artifact save", () => {
        const requestEvent = createRequestEvent("Save a file");
        const cancelled = createSignalEvent("artifact_creation_progress", { filename: "report.md", status: "cancelled", bytes_transferred: 512 }, "Orchestrator", "2024-01-01T00:00:00.300Z");
        const task = createTask({ events: [requestEvent, cancelled] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result!.currentStatusText).toBe("Cancelled artifact: report.md");
    });

    test("aggregates streaming text into AGENT_RESPONSE_TEXT steps", () => {
        const requestEvent = createRequestEvent("Hello");
        const textEvent1 = createStatusUpdateEvent("Part 1 ", "Orchestrator", "2024-01-01T00:00:00.300Z");
        const textEvent2 = createStatusUpdateEvent("Part 2", "Orchestrator", "2024-01-01T00:00:00.600Z");
        const responseEvent = createCompletedResponseEvent("", "Orchestrator", "2024-01-01T00:00:01.000Z");

        const task = createTask({ events: [requestEvent, textEvent1, textEvent2, responseEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        const textSteps = result!.steps.filter(s => s.type === "AGENT_RESPONSE_TEXT");
        expect(textSteps.length).toBeGreaterThanOrEqual(1);
    });

    test("synthesizes a USER_REQUEST when no request event exists but initialRequestText is present", () => {
        const responseEvent = createCompletedResponseEvent("Done", "Orchestrator", "2024-01-01T00:00:01.000Z");
        const task = createTask({
            initialRequestText: "What is the weather?",
            events: [responseEvent],
        });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        const userRequestSteps = result!.steps.filter(s => s.type === "USER_REQUEST");
        expect(userRequestSteps).toHaveLength(1);
        expect(userRequestSteps[0].data.text).toBe("What is the weather?");
    });

    test("does not mutate input parentTaskObject.events when synthesizing a request event", () => {
        const responseEvent = createCompletedResponseEvent("Done", "Orchestrator", "2024-01-01T00:00:01.000Z");
        const task = createTask({
            initialRequestText: "What is the weather?",
            events: [responseEvent],
        });
        const monitoredTasks: Record<string, TaskFE> = { "task-1": task };

        const originalEvents = task.events;
        const originalLength = task.events.length;
        const originalFirstEvent = task.events[0];

        processTaskForVisualization(task.events, monitoredTasks, task);

        expect(task.events).toBe(originalEvents);
        expect(task.events.length).toBe(originalLength);
        expect(task.events[0]).toBe(originalFirstEvent);
    });

    test("anchors a top-level task that has tool events but no request event and no initialRequestText (live activity_event path)", () => {
        const startA = createSignalEvent("tool_invocation_start", { function_call_id: "fc-a", tool_name: "tool_a", tool_args: {}, parallel_group_id: "grp-1" }, "Orchestrator", "2024-01-01T00:00:00.100Z");
        const startB = createSignalEvent("tool_invocation_start", { function_call_id: "fc-b", tool_name: "tool_b", tool_args: {}, parallel_group_id: "grp-1" }, "Orchestrator", "2024-01-01T00:00:00.110Z");
        const resultA = createSignalEvent("tool_result", { function_call_id: "fc-a", tool_name: "tool_a", result_data: { ok: true } }, "Orchestrator", "2024-01-01T00:00:00.500Z");
        const resultB = createSignalEvent("tool_result", { function_call_id: "fc-b", tool_name: "tool_b", result_data: { ok: true } }, "Orchestrator", "2024-01-01T00:00:00.510Z");

        const task = createTask({ initialRequestText: "", events: [startA, startB, resultA, resultB] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        const roots = result!.steps.filter(s => s.type === "USER_REQUEST");
        expect(roots).toHaveLength(1);
        expect(roots[0].source).toBe("User");
        expect(roots[0].target).toBe("Orchestrator");
        expect(roots[0].data.text).toBe("User request");
        expect(result!.steps.filter(s => s.type === "AGENT_TOOL_INVOCATION_START")).toHaveLength(2);
    });

    test("does not add a second synthetic root when the top-level task already has a request event", () => {
        const requestEvent = createRequestEvent("Run both tools");
        const startA = createSignalEvent("tool_invocation_start", { function_call_id: "fc-a", tool_name: "tool_a", tool_args: {}, parallel_group_id: "grp-1" }, "Orchestrator", "2024-01-01T00:00:00.100Z");
        const startB = createSignalEvent("tool_invocation_start", { function_call_id: "fc-b", tool_name: "tool_b", tool_args: {}, parallel_group_id: "grp-1" }, "Orchestrator", "2024-01-01T00:00:00.110Z");
        const task = createTask({ events: [requestEvent, startA, startB] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        const roots = result!.steps.filter(s => s.type === "USER_REQUEST");
        expect(roots).toHaveLength(1);
        expect(roots[0].data.text).toBe("Run both tools");
    });

    test("does not draw a second box for a workflow tool node's tool telemetry", () => {
        const requestEvent = createRequestEvent("Run the workflow");
        const workflowMeta = { agent_name: "wf", workflow_name: "wf", node_id: "lookup_node", node_type: "tool" };
        const start = createSignalEvent("tool_invocation_start", { function_call_id: "sub-1", tool_name: "lookup", tool_args: {} }, "wf", "2024-01-01T00:00:00.100Z");
        const result = createSignalEvent("tool_result", { function_call_id: "sub-1", tool_name: "lookup", result_data: { ok: true } }, "wf", "2024-01-01T00:00:00.200Z");
        for (const event of [start, result]) {
            const payload = event.full_payload as { result: { metadata: Record<string, unknown>; status: { message: { metadata: Record<string, unknown> } } } };
            payload.result.metadata = workflowMeta;
            payload.result.status.message.metadata = workflowMeta;
        }
        const task = createTask({ events: [requestEvent, start, result] });
        const processed = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(processed).not.toBeNull();
        expect(processed!.steps.filter(s => s.type === "AGENT_TOOL_INVOCATION_START")).toHaveLength(0);
        expect(processed!.steps.filter(s => s.type === "AGENT_TOOL_EXECUTION_RESULT")).toHaveLength(0);
    });

    test("does not draw a second box for a workflow tool node forwarded to its orchestrator", () => {
        const forwarded = (attribution: Record<string, unknown>, type: string, ts: string) =>
            createEvent({
                direction: "status-update",
                task_id: "task-A",
                timestamp: ts,
                source_entity: "Orchestrator",
                full_payload: {
                    result: {
                        taskId: "task-A",
                        status: {
                            state: "working",
                            message: {
                                metadata: { agent_name: "Orchestrator", function_call_id: "call-7", forwarded_from_peer: "wf", original_peer_event_taskId: "wf-task", peer_task_chain: ["wf-task"], ...attribution },
                                parts: [{ kind: "data", data: { type, function_call_id: "sub-1", tool_name: "lookup", tool_args: {}, result_data: { ok: true } } }],
                            },
                        },
                    },
                },
            });
        const toolSteps = (attribution: Record<string, unknown>) => {
            const events = [
                createRequestEvent("run it", "2024-01-01T00:00:00.000Z", "task-A", "Orchestrator"),
                forwarded(attribution, "tool_invocation_start", "2024-01-01T00:00:01.000Z"),
                forwarded(attribution, "tool_result", "2024-01-01T00:00:02.000Z"),
            ];
            const task = createTask({ taskId: "task-A", events });
            const processed = processTaskForVisualization(task.events, { "task-A": task }, task);
            return processed!.steps.filter(s => s.owningTaskId === "wf-task" && (s.type === "AGENT_TOOL_INVOCATION_START" || s.type === "AGENT_TOOL_EXECUTION_RESULT"));
        };

        expect(toolSteps({ workflow_name: "wf", node_id: "lookup_node", node_type: "tool", sub_task_id: "sub-1" })).toHaveLength(0);
        expect(toolSteps({})).toHaveLength(2);
    });

    test("puts a workflow tool node's arguments and result on the node's own steps", () => {
        const workflowMeta = { agent_name: "wf", workflow_name: "wf", node_id: "lookup_node", node_type: "tool" };
        const withWorkflowMeta = (event: ReturnType<typeof createSignalEvent>) => {
            const payload = event.full_payload as { result: { metadata: Record<string, unknown>; status: { message: { metadata: Record<string, unknown> } } } };
            payload.result.metadata = workflowMeta;
            payload.result.status.message.metadata = workflowMeta;
            return event;
        };
        const events = [
            createRequestEvent("Run the workflow"),
            createSignalEvent("workflow_node_execution_start", { node_id: "lookup_node", node_type: "tool", sub_task_id: "sub-1", tool_name: "lookup", depends_on: [] }, "wf", "2024-01-01T00:00:00.050Z"),
            withWorkflowMeta(createSignalEvent("tool_invocation_start", { function_call_id: "sub-1", tool_name: "lookup", tool_args: { repo: "sam", number: 4969 } }, "wf", "2024-01-01T00:00:00.100Z")),
            withWorkflowMeta(createSignalEvent("tool_result", { function_call_id: "sub-1", tool_name: "lookup", result_data: { next: "assess" } }, "wf", "2024-01-01T00:00:00.200Z")),
            createSignalEvent("workflow_node_execution_result", { node_id: "lookup_node", status: "success" }, "wf", "2024-01-01T00:00:00.300Z"),
        ];
        const task = createTask({ events });
        const processed = processTaskForVisualization(task.events, { "task-1": task }, task);

        const start = processed!.steps.find(s => s.type === "WORKFLOW_NODE_EXECUTION_START");
        const result = processed!.steps.find(s => s.type === "WORKFLOW_NODE_EXECUTION_RESULT");
        expect(start?.data.workflowNodeExecutionStart?.toolArguments).toEqual({ repo: "sam", number: 4969 });
        expect(result?.data.workflowNodeExecutionResult?.toolResultData).toEqual({ next: "assess" });
        expect(processed!.steps.filter(s => s.type === "AGENT_TOOL_INVOCATION_START" || s.type === "AGENT_TOOL_EXECUTION_RESULT")).toHaveLength(0);
    });

    test("still draws an agent's own tool calls", () => {
        const start = createSignalEvent("tool_invocation_start", { function_call_id: "fc-1", tool_name: "lookup", tool_args: {} }, "Orchestrator", "2024-01-01T00:00:00.100Z");
        const task = createTask({ events: [createRequestEvent("Look it up"), start] });
        const processed = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(processed!.steps.filter(s => s.type === "AGENT_TOOL_INVOCATION_START")).toHaveLength(1);
    });

    test("strips gateway timestamp prefix from user request text", () => {
        const requestEvent = createRequestEvent("Request received by gateway at: 2025-12-19T22:46:16.994017+00:00\nWhat is the weather?");
        const task = createTask({ events: [requestEvent] });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        const userRequestSteps = result!.steps.filter(s => s.type === "USER_REQUEST");
        expect(userRequestSteps).toHaveLength(1);
        expect(userRequestSteps[0].data.text).toBe("What is the weather?");
    });

    test("processes sub-task events with correct nesting level", () => {
        const requestEvent = createRequestEvent("Research this topic");
        const subTaskRequest = createEvent({
            direction: "request",
            timestamp: "2024-01-01T00:00:00.200Z",
            task_id: "subtask-1",
            source_entity: "Orchestrator",
            target_entity: "ResearchAgent",
            payload_summary: { method: "message/send" },
            full_payload: {
                method: "message/send",
                params: {
                    message: {
                        parts: [{ kind: "text", text: "Please research" }],
                        metadata: { parentTaskId: "task-1" },
                    },
                    metadata: { function_call_id: "fc-1" },
                },
            },
        });
        const subTaskResponse = createEvent({
            direction: "response",
            timestamp: "2024-01-01T00:00:00.800Z",
            task_id: "subtask-1",
            source_entity: "ResearchAgent",
            target_entity: "Orchestrator",
            full_payload: {
                result: {
                    status: { state: "completed", message: { parts: [{ kind: "text", text: "Research done" }] } },
                    metadata: { agent_name: "ResearchAgent" },
                },
            },
        });
        const responseEvent = createCompletedResponseEvent("Here are the results", "Orchestrator", "2024-01-01T00:00:01.000Z");

        const subTask = createTask({
            taskId: "subtask-1",
            initialRequestText: "Please research",
            events: [subTaskRequest, subTaskResponse],
            parentTaskId: "task-1",
        });
        const parentTask = createTask({
            events: [requestEvent, responseEvent],
        });

        const monitoredTasks: Record<string, TaskFE> = {
            "task-1": parentTask,
            "subtask-1": subTask,
        };

        const result = processTaskForVisualization(parentTask.events, monitoredTasks, parentTask);
        expect(result).not.toBeNull();

        const subTaskSteps = result!.steps.filter(s => s.nestingLevel > 0);
        expect(subTaskSteps.length).toBeGreaterThan(0);
    });

    test("nests a sub-task's JSON-RPC error response under the sub-task and leaves the root task working", () => {
        const requestEvent = createRequestEvent("Research this topic");
        const subTaskRequest = createEvent({
            direction: "request",
            timestamp: "2024-01-01T00:00:00.200Z",
            task_id: "subtask-1",
            source_entity: "Orchestrator",
            target_entity: "ResearchAgent",
            payload_summary: { method: "message/send" },
            full_payload: {
                method: "message/send",
                params: {
                    message: { parts: [{ kind: "text", text: "Please research" }], metadata: { parentTaskId: "task-1" } },
                    metadata: { function_call_id: "fc-1" },
                },
            },
        });
        const subTaskError = createEvent({
            direction: "error_response",
            timestamp: "2024-01-01T00:00:00.800Z",
            task_id: "subtask-1",
            source_entity: "ResearchAgent",
            target_entity: "Orchestrator",
            full_payload: { error: { code: -32603, message: "usage budget is used up", data: { llm_error_category: "budget_exhausted" } } },
        });
        const subTask = createTask({ taskId: "subtask-1", initialRequestText: "Please research", events: [subTaskRequest, subTaskError], parentTaskId: "task-1" });
        const parentTask = createTask({ events: [requestEvent] });

        const result = processTaskForVisualization(parentTask.events, { "task-1": parentTask, "subtask-1": subTask }, parentTask);

        const failedSteps = result!.steps.filter(s => s.type === "TASK_FAILED");
        expect(failedSteps).toHaveLength(1);
        expect(failedSteps[0]).toMatchObject({ owningTaskId: "subtask-1", isSubTaskStep: true });
        expect(failedSteps[0].nestingLevel).toBeGreaterThan(0);
        expect(result!.status).toBe("working");
        expect(result!.endTime).toBeUndefined();
    });

    test("resolves agent slug to friendly display name via agentNameMap", () => {
        const slug = "agent_019e9334_15a5_73bb_917e_86d2816197ac";
        const requestEvent = createRequestEvent("Hello");
        const llmInvocation = createSignalEvent("llm_invocation", { request: { model: "gpt-4" } }, slug, offsetTime(100));
        const llmResponse = createSignalEvent("llm_response", { data: {} }, slug, offsetTime(600));
        const responseEvent = createCompletedResponseEvent("Done", slug, offsetTime(1000));
        const task = createTask({ events: [requestEvent, llmInvocation, llmResponse, responseEvent] });

        const result = processTaskForVisualization(task.events, { "task-1": task }, task, { [slug]: "Weather Agent" });

        expect(result).not.toBeNull();
        const agents = Object.values(result!.performanceReport!.agents);
        expect(agents).toHaveLength(1);
        expect(agents[0].agentName).toBe(slug);
        expect(agents[0].displayName).toBe("Weather Agent");
    });

    test("falls back to the slug when no agentNameMap is provided", () => {
        const slug = "agent_019e9334_15a5_73bb_917e_86d2816197ac";
        const requestEvent = createRequestEvent("Hello");
        const llmInvocation = createSignalEvent("llm_invocation", { request: { model: "gpt-4" } }, slug, offsetTime(100));
        const llmResponse = createSignalEvent("llm_response", { data: {} }, slug, offsetTime(600));
        const task = createTask({ events: [requestEvent, llmInvocation, llmResponse] });

        const result = processTaskForVisualization(task.events, { "task-1": task }, task);

        expect(result).not.toBeNull();
        const agents = Object.values(result!.performanceReport!.agents);
        expect(agents).toHaveLength(1);
        expect(agents[0].displayName).toBe(slug);
    });

    const promptPreviewFor = (userTexts: string[]): string | undefined => {
        const slug = "agent_019e9334_15a5_73bb_917e_86d2816197ac";
        const request = { model: "gpt-4", contents: userTexts.map(text => ({ role: "user", parts: [{ text }] })) };
        const task = createTask({
            events: [createRequestEvent("Hello"), createSignalEvent("llm_invocation", { request }, slug, offsetTime(100))],
        });
        const result = processTaskForVisualization(task.events, { "task-1": task }, task);
        return result!.steps.find(s => s.data.llmCall)?.data.llmCall?.promptPreview;
    };

    test("hides the agent's turn time stamp from the prompt preview", () => {
        const preview = promptPreviewFor(["[Current time: Wednesday, 2026-09-23T10:29:41-04:00 (America/Toronto)]\nwhat day is today", "[Current time: 2026-09-23T10:29:50-04:00]\ntime?"]);
        expect(preview).toBe("what day is today\ntime?");
    });

    test("keeps text the user wrote that merely starts like a stamp", () => {
        const typed = "[Current time: meeting moved to 2026-09-21T20:47:30Z, please]\nremind me";
        expect(promptPreviewFor([typed])).toBe(typed);
    });

    test("produces identical output when called multiple times with the same input", () => {
        const requestEvent = createRequestEvent("Hello");
        const statusEvent = createStatusUpdateEvent("Thinking...");
        const responseEvent = createCompletedResponseEvent("Hi there!");
        const task = createTask({ events: [requestEvent, statusEvent, responseEvent] });
        const monitoredTasks = { "task-1": task };

        const result1 = processTaskForVisualization(task.events, monitoredTasks, task);
        const result2 = processTaskForVisualization(task.events, monitoredTasks, task);

        expect(result1!.steps.length).toBe(result2!.steps.length);
        expect(result1!.status).toBe(result2!.status);
        expect(result1!.durationMs).toBe(result2!.durationMs);
        result1!.steps.forEach((step, i) => {
            expect(step.type).toBe(result2!.steps[i].type);
            expect(step.id).toBe(result2!.steps[i].id);
        });
    });

    test("nests a 3-level peer chain (A->B->C) from peer_task_chain", () => {
        const fwd = (agent: string, opet: string, chain: string[], ts: string) =>
            createEvent({
                direction: "status-update",
                task_id: "task-A",
                timestamp: ts,
                source_entity: agent,
                full_payload: {
                    result: {
                        taskId: "task-A",
                        status: {
                            state: "working",
                            message: {
                                metadata: { agent_name: agent, forwarded_from_peer: "AgentB", original_peer_event_taskId: opet, peer_task_chain: chain },
                                parts: [{ kind: "data", data: { type: "tool_invocation_start", function_call_id: `${opet}-fc`, tool_name: "t" } }],
                            },
                        },
                    },
                },
            });

        const req = createRequestEvent("do it", "2024-01-01T00:00:00.000Z", "task-A", "Orchestrator");
        const ownerNesting = (chainC: string[]) => {
            const bEvt = fwd("AgentB", "task-B", ["task-B"], "2024-01-01T00:00:01.000Z");
            const cEvt = fwd("AgentC", "task-C", chainC, "2024-01-01T00:00:02.000Z");
            const task = createTask({ taskId: "task-A", events: [req, bEvt, cEvt] });
            const result = processTaskForVisualization(task.events, { "task-A": task }, task);
            const byOwner = (owner: string) => result!.steps.find(s => s.owningTaskId === owner && s.type === "AGENT_TOOL_INVOCATION_START");
            return { b: byOwner("task-B")?.nestingLevel, c: byOwner("task-C")?.nestingLevel };
        };

        const withChain = ownerNesting(["task-C", "task-B"]);
        expect(withChain.b).toBe(1);
        expect(withChain.c).toBe(2);

        const noChain = ownerNesting(["task-C"]);
        expect(noChain.c).toBe(1);
    });

    test("nests a peer chain regardless of forwarded-event arrival order", () => {
        const fwd = (agent: string, opet: string, chain: string[], ts: string) =>
            createEvent({
                direction: "status-update",
                task_id: "task-A",
                timestamp: ts,
                source_entity: agent,
                full_payload: {
                    result: {
                        taskId: "task-A",
                        status: {
                            state: "working",
                            message: {
                                metadata: { agent_name: agent, forwarded_from_peer: "AgentB", original_peer_event_taskId: opet, peer_task_chain: chain },
                                parts: [{ kind: "data", data: { type: "tool_invocation_start", function_call_id: `${opet}-fc`, tool_name: "t" } }],
                            },
                        },
                    },
                },
            });

        const req = createRequestEvent("do it", "2024-01-01T00:00:00.000Z", "task-A", "Orchestrator");
        const bEvt = fwd("AgentB", "task-B", ["task-B"], "2024-01-01T00:00:02.000Z");
        const cEvt = fwd("AgentC", "task-C", ["task-C", "task-B"], "2024-01-01T00:00:01.000Z");

        const task = createTask({ taskId: "task-A", events: [req, cEvt, bEvt] });
        const result = processTaskForVisualization(task.events, { "task-A": task }, task);
        const byOwner = (owner: string) => result!.steps.find(s => s.owningTaskId === owner && s.type === "AGENT_TOOL_INVOCATION_START");
        expect(byOwner("task-B")?.nestingLevel).toBe(1);
        expect(byOwner("task-C")?.nestingLevel).toBe(2);
    });
});

describe("processTaskForVisualization with a tool paused for OAuth sign-in", () => {
    const toolCallsAfter = (events: A2AEventSSEPayload[]) => {
        const task = createTask({ events });
        const result = processTaskForVisualization(events, { "task-1": task }, task)!;
        return Object.values(result.performanceReport!.agents).flatMap(a => a.toolCalls);
    };

    test("keeps both the placeholder and the final tool_result steps", () => {
        const result = processTaskForVisualization(pendingAuthToolCallTask.events, pendingAuthToolCallMonitoredTasks, pendingAuthToolCallTask)!;

        const statuses = result.steps.filter(s => s.type === "AGENT_TOOL_EXECUTION_RESULT").map(s => (s.data.toolResult?.resultData as { status?: string }).status);
        expect(statuses).toEqual(["pending", "error"]);
    });

    test("measures the tool call from the LLM's decision to its final result, not the placeholder", () => {
        const toolCalls = toolCallsAfter(pendingAuthToolCallTask.events);

        expect(toolCalls).toHaveLength(1);
        expect(toolCalls[0].toolName).toBe("get_report");
        expect(toolCalls[0].durationMs).toBe(10800);
    });

    test("leaves the tool call open while only the placeholder has arrived", () => {
        expect(toolCallsAfter([...reportToolCallStartEvents, ...reportToolSignInPlaceholderEvents])).toEqual([]);
    });

    test("closes the tool call on a tool's own result that says pending", () => {
        const toolCalls = toolCallsAfter([...reportToolCallStartEvents, reportToolResultEvent({ status: "pending", job_id: "job-7" }, offsetTime(1305))]);

        expect(toolCalls.map(c => c.durationMs)).toEqual([105]);
    });
});
