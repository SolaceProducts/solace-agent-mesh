// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, vi } from "vitest";

import { isHiddenWorkingPart, partArtifactName } from "@/features/chat/artifactTags";

import type { ArtifactInfo, DataPart, MessageFE, RAGSearchResult } from "@/types";

import { deserializeChatMessages } from "./deserializeChatMessages";
import { type ChatEffect, type ChatEventInput, type ChatEventOutput, processChatEvent } from "./processChatEvent";
import { serializeChatMessage } from "./serializeChatMessage";

function makeInput(overrides: Partial<ChatEventInput> = {}): ChatEventInput {
    return {
        eventData: "{}",
        messages: [],
        ragData: [],
        artifacts: [],
        flags: {
            showThinkingContent: false,
            ragEnabled: true,
            isReplaying: false,
        },
        sessionId: "session-1",
        selectedAgentName: "agent-1",
        deepResearchQueryHistory: new Map(),
        eventSequence: 1,
        isTaskRunningInBackground: () => false,
        ...overrides,
    };
}

function statusUpdateEvent(taskId: string, parts: Array<Record<string, unknown>> = [], options: { final?: boolean; contextId?: string; messageMetadata?: Record<string, unknown> } = {}): string {
    const message: Record<string, unknown> = { role: "agent", parts };
    if (options.messageMetadata) {
        message.metadata = options.messageMetadata;
    }
    return JSON.stringify({
        jsonrpc: "2.0",
        result: {
            kind: "status-update",
            taskId,
            final: options.final ?? false,
            status: {
                state: "working",
                message,
            },
            contextId: options.contextId,
        },
    });
}

function taskCompleteEvent(taskId: string, state: "completed" | "failed" = "completed", parts: Array<Record<string, unknown>> = [], metadata?: Record<string, unknown>): string {
    return JSON.stringify({
        jsonrpc: "2.0",
        result: {
            kind: "task",
            id: taskId,
            status: {
                state,
                message: state === "failed" && parts.length > 0 ? { role: "agent", parts } : { role: "agent", parts: [] },
            },
            ...(metadata && { metadata }),
        },
    });
}

function rpcError(message: string, code: number = -32000, data?: Record<string, unknown>): string {
    return JSON.stringify({
        jsonrpc: "2.0",
        error: { code, message, ...(data && { data }) },
    });
}

function artifactUpdateEvent(): string {
    return JSON.stringify({
        jsonrpc: "2.0",
        result: { kind: "artifact-update" },
    });
}

function agentMessage(taskId: string, parts: MessageFE["parts"] = [], extra: Partial<MessageFE> = {}): MessageFE {
    return {
        role: "agent",
        isUser: false,
        taskId,
        parts,
        isComplete: false,
        ...extra,
    };
}

function userMessage(taskId?: string): MessageFE {
    return {
        role: "user",
        isUser: true,
        taskId,
        parts: [{ kind: "text", text: "hello" }],
    };
}

function effectTypes(output: ChatEventOutput): string[] {
    return output.effects.map(e => e.type);
}

describe("processChatEvent", () => {
    describe("parsing", () => {
        it("returns empty effects for invalid JSON", () => {
            const output = processChatEvent(makeInput({ eventData: "not json" }));
            expect(output.effects).toEqual([]);
            expect(output.messages).toBeUndefined();
        });

        it("returns empty effects for missing result and error", () => {
            const output = processChatEvent(makeInput({ eventData: JSON.stringify({ jsonrpc: "2.0" }) }));
            expect(output.effects).toEqual([]);
        });
    });

    describe("RPC errors", () => {
        it("creates an error message and emits stop/close/clear effects", () => {
            const output = processChatEvent(makeInput({ eventData: rpcError("Something broke") }));

            expect(output.messages).toHaveLength(1);
            expect(output.messages![0].isError).toBe(true);
            expect(output.messages![0].isWarning).toBeFalsy();
            expect(output.messages![0].parts[0]).toMatchObject({ kind: "text", text: "Error: Something broke" });
            expect(effectTypes(output)).toEqual(["stop-responding", "close-connection", "clear-task-id", "rpc-error"]);
            expect(output.effects.at(-1)).toEqual({ type: "rpc-error", payload: { message: "Something broke", replayed: false } });
        });

        it("marks an RPC error replayed while a chat loads as replayed, so it is not treated as a new refusal", () => {
            const output = processChatEvent(makeInput({ eventData: rpcError("The free model's allowance is used up. Add a model to replace it."), flags: { showThinkingContent: false, ragEnabled: true, isReplaying: true } }));

            expect(output.effects.at(-1)).toEqual({ type: "rpc-error", payload: { message: "The free model's allowance is used up. Add a model to replace it.", replayed: true } });
        });

        it("filters out status bubbles before adding error message", () => {
            const existing: MessageFE[] = [agentMessage("t1", [], { isStatusBubble: true }), userMessage("t1")];
            const output = processChatEvent(makeInput({ eventData: rpcError("fail"), messages: existing }));

            expect(output.messages).toHaveLength(2);
            expect(output.messages![0].isUser).toBe(true);
            expect(output.messages![1].isError).toBe(true);
        });

        it("flags a task timeout (-32001) as a warning while keeping error semantics, with no Error: prefix", () => {
            const output = processChatEvent(makeInput({ eventData: rpcError("The request timed out. Please try again.", -32001) }));
            const last = output.messages![output.messages!.length - 1];
            expect(last.isWarning).toBe(true);
            expect(last.isError).toBe(true);
            expect(last.parts[0]).toMatchObject({ kind: "text", text: "The request timed out. Please try again." });
        });

        it("shows a budget stop as a notice, not an Error:, while keeping error semantics", () => {
            const stopped = "This request was stopped because your usage budget is used up until 1 Feb. Contact your administrator if you need it sooner.";
            const output = processChatEvent(makeInput({ eventData: rpcError(stopped, -32603, { llm_error_category: "budget_exhausted" }) }));
            const last = output.messages![output.messages!.length - 1];
            expect(last.isWarning).toBe(true);
            expect(last.isError).toBe(true);
            expect(last.parts[0]).toMatchObject({ kind: "text", text: stopped });
        });

        it("keeps any other internal error a hard Error:", () => {
            const output = processChatEvent(makeInput({ eventData: rpcError("model unreachable", -32603, { llm_error_category: "timeout" }) }));
            const last = output.messages![output.messages!.length - 1];
            expect(last.isWarning).toBe(false);
            expect(last.parts[0]).toMatchObject({ kind: "text", text: "Error: model unreachable" });
        });

        it("marks the trailing in-progress agent message complete so its activity timeline stops spinning", () => {
            const existing: MessageFE[] = [userMessage("t1"), agentMessage("t1", [{ kind: "text", text: "working" }], { isComplete: false })];
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: existing }));
            const inProgress = output.messages!.find(m => m.taskId === "t1" && !m.isUser);
            expect(inProgress!.isComplete).toBe(true);
        });

        it("leaves already-complete and user messages untouched on RPC error", () => {
            const done = agentMessage("t1", [{ kind: "text", text: "done" }], { isComplete: true });
            const existing: MessageFE[] = [userMessage("t1"), done];
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: existing }));
            expect(output.messages!.find(m => !m.isUser && m.taskId === "t1")).toBe(done);
            expect(output.messages![0].isUser).toBe(true);
        });

        it("stamps the error message with the errored task id so feedback actions can render", () => {
            const existing: MessageFE[] = [userMessage("t1"), agentMessage("t1", [{ kind: "text", text: "working" }], { isComplete: false })];
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: existing }));
            const errorMsg = output.messages![output.messages!.length - 1];
            expect(errorMsg.isError).toBe(true);
            expect(errorMsg.isComplete).toBe(true);
            expect(errorMsg.taskId).toBe("t1");
        });

        it("recovers the task id from the user message when no agent message streamed", () => {
            const existing: MessageFE[] = [userMessage("t1")];
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: existing }));
            const errorMsg = output.messages![output.messages!.length - 1];
            expect(errorMsg.isUser).toBe(false);
            expect(errorMsg.taskId).toBe("t1");
        });

        it("stamps the errored task id on a timeout (-32001) message too", () => {
            const existing: MessageFE[] = [userMessage("t1"), agentMessage("t1", [{ kind: "text", text: "working" }], { isComplete: false })];
            const output = processChatEvent(makeInput({ eventData: rpcError("The request timed out. Please try again.", -32001), messages: existing }));
            const errorMsg = output.messages![output.messages!.length - 1];
            expect(errorMsg.isWarning).toBe(true);
            expect(errorMsg.taskId).toBe("t1");
        });

        it("picks the current turn's user-message task id over a prior completed task on fallback", () => {
            const existing: MessageFE[] = [agentMessage("t0", [{ kind: "text", text: "done" }], { isComplete: true }), userMessage("t1")];
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: existing }));
            const errorMsg = output.messages![output.messages!.length - 1];
            expect(errorMsg.taskId).toBe("t1");
        });
    });

    describe("artifact-update events", () => {
        it("emits refetch-artifacts and returns no messages", () => {
            const output = processChatEvent(makeInput({ eventData: artifactUpdateEvent() }));
            expect(effectTypes(output)).toEqual(["refetch-artifacts"]);
            expect(output.messages).toBeUndefined();
        });
    });

    describe("status-update events", () => {
        it("appends text content to a new agent message", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "text", text: "Hello world" }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.messages).toHaveLength(1);
            expect(output.messages![0].isUser).toBe(false);
            expect(output.messages![0].taskId).toBe("task-1");
            expect(output.messages![0].parts).toContainEqual(expect.objectContaining({ kind: "text", text: "Hello world" }));
        });

        it("appends text to the last agent message for the same task", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "part1" }])];
            const eventData = statusUpdateEvent("task-1", [{ kind: "text", text: " part2" }]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            expect(output.messages).toHaveLength(1);
            expect(output.messages![0].parts).toHaveLength(2);
        });

        it("does not append to a different task's message", () => {
            const existing = [agentMessage("task-other", [{ kind: "text", text: "old" }])];
            const eventData = statusUpdateEvent("task-1", [{ kind: "text", text: "new" }]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            expect(output.messages).toHaveLength(2);
        });

        it("removes a status bubble before appending content", () => {
            const existing = [agentMessage("task-1", [], { isStatusBubble: true })];
            const eventData = statusUpdateEvent("task-1", [{ kind: "text", text: "real content" }]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            expect(output.messages).toHaveLength(1);
            expect(output.messages![0].isStatusBubble).toBeFalsy();
        });
    });

    describe("gateway soft timeout warning", () => {
        const warningPart = (message?: string) => ({ kind: "data", data: { type: "gateway_timeout_warning", ...(message ? { message } : {}) } });

        it("adds a non-terminal amber warning without tearing down the stream", () => {
            const streaming = agentMessage("task-1", [{ kind: "text", text: "working" }]);
            const eventData = statusUpdateEvent("task-1", [warningPart()]);
            const output = processChatEvent(makeInput({ eventData, messages: [streaming] }));

            const warning = output.messages!.find(m => m.isWarning);
            expect(warning).toBeDefined();
            expect(warning!.isComplete).toBe(true);
            expect(warning!.parts[0]).toMatchObject({ kind: "text", text: expect.stringContaining("taking longer") });

            const stillStreaming = output.messages!.find(m => m.taskId === "task-1" && !m.isWarning);
            expect(stillStreaming!.isComplete).toBe(false);
        });

        it("uses the backend-supplied message text when present", () => {
            const eventData = statusUpdateEvent("task-1", [warningPart("Custom still-working notice")]);
            const output = processChatEvent(makeInput({ eventData }));
            expect(output.messages!.find(m => m.isWarning)!.parts[0]).toMatchObject({ text: "Custom still-working notice" });
        });

        it("dedupes repeated warnings for the same task", () => {
            const eventData = statusUpdateEvent("task-1", [warningPart()]);
            const first = processChatEvent(makeInput({ eventData }));
            const second = processChatEvent(makeInput({ eventData, messages: first.messages }));
            expect(second.messages!.filter(m => m.isWarning)).toHaveLength(1);
        });

        it("routes the agent's later output into a fresh bubble, not the warning bubble", () => {
            const warned = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [warningPart()]) }));
            const output = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [{ kind: "text", text: "here is the answer" }]), messages: warned.messages }));

            const warning = output.messages!.find(m => m.isWarning)!;
            expect(warning.parts).toHaveLength(1);
            expect(warning.parts[0]).toMatchObject({ text: expect.stringContaining("taking longer") });

            const answer = output.messages!.find(m => !m.isWarning && m.parts.some(p => p.kind === "text" && p.text === "here is the answer"));
            expect(answer).toBeDefined();
            expect(answer!.isWarning).toBeFalsy();
        });

        it("does not attach inline progress updates to the warning bubble", () => {
            const warned = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [warningPart()]) }));
            const output = processChatEvent(
                makeInput({
                    eventData: statusUpdateEvent("task-1", [{ kind: "data", data: { type: "agent_progress_update", status_text: "still crunching" } }]),
                    messages: warned.messages,
                    flags: { showThinkingContent: false, ragEnabled: true, isReplaying: false },
                })
            );
            const warning = output.messages!.find(m => m.isWarning)!;
            expect(warning.progressUpdates ?? []).toHaveLength(0);
            const progressMsg = output.messages!.find(m => !m.isWarning && (m.progressUpdates?.length ?? 0) > 0);
            expect(progressMsg).toBeDefined();
        });

        it("does not create an empty bubble from a warning-only event", () => {
            const output = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [warningPart()]) }));
            expect(output.messages).toHaveLength(1);
            expect(output.messages![0].isWarning).toBe(true);
        });
    });

    describe("task complete (final) events", () => {
        it("marks the last task message as complete", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "response" }])];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            expect(output.messages!.find(m => m.taskId === "task-1")!.isComplete).toBe(true);
        });

        it("emits final effects in the correct order", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "done" }])];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            expect(effectTypes(output)).toContain("cancel-complete");
            expect(effectTypes(output)).toContain("stop-responding");
            expect(effectTypes(output)).toContain("close-connection");
            expect(effectTypes(output)).toContain("clear-task-id");
            expect(effectTypes(output)).toContain("refetch-artifacts");
            expect(effectTypes(output)).toContain("finalize");
        });

        it("emits save-task effect with correct payload when not replaying", () => {
            const existing = [userMessage("task-1"), agentMessage("task-1", [{ kind: "text", text: "response" }])];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing, sessionId: "ses-1", selectedAgentName: "myAgent" }));

            const saveEffect = output.effects.find(e => e.type === "save-task") as Extract<ChatEffect, { type: "save-task" }>;
            expect(saveEffect).toBeDefined();
            expect(saveEffect.payload.taskId).toBe("task-1");
            expect(saveEffect.payload.selectedAgentName).toBe("myAgent");
        });

        it("does not emit save-task when replaying", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "response" }])];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(
                makeInput({
                    eventData,
                    messages: existing,
                    flags: { showThinkingContent: false, ragEnabled: true, isReplaying: true },
                })
            );

            expect(output.effects.find(e => e.type === "save-task")).toBeUndefined();
        });

        it("marks lingering in-progress artifacts as failed for the completed task", () => {
            const existing = [
                agentMessage("task-1", [
                    {
                        kind: "artifact" as const,
                        status: "in-progress" as const,
                        name: "file.txt",
                        bytesTransferred: 100,
                    },
                ]),
            ];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            expect(taskMsg!.isError).toBe(true);
            expect(taskMsg!.isComplete).toBe(true);
            const artifactPart = taskMsg!.parts.find(p => p.kind === "artifact");
            expect(artifactPart).toMatchObject({ status: "failed", name: "file.txt" });
        });

        it("creates a fallback message when final task has parts but no prior status updates", () => {
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: {
                        state: "completed",
                        message: { role: "agent", parts: [{ kind: "text", text: "final answer" }] },
                    },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            expect(taskMsg!.isComplete).toBe(true);
            expect(taskMsg!.parts).toContainEqual(expect.objectContaining({ kind: "text", text: "final answer" }));
        });

        it("marks earlier incomplete messages for the same task as complete", () => {
            const existing = [
                agentMessage("task-1", [{ kind: "text", text: "progress update 1" }], { isComplete: false }),
                agentMessage("task-1", [{ kind: "text", text: "progress update 2" }], { isComplete: false }),
                userMessage("task-1"),
                agentMessage("task-1", [{ kind: "text", text: "final response" }], { isComplete: false }),
            ];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMessages = output.messages!.filter(m => !m.isUser && m.taskId === "task-1");
            expect(taskMessages).toHaveLength(3);
            taskMessages.forEach(msg => {
                expect(msg.isComplete).toBe(true);
            });
        });

        it("does not mark messages from other tasks as complete", () => {
            const existing = [
                agentMessage("task-other", [{ kind: "text", text: "other task" }], { isComplete: false }),
                agentMessage("task-1", [{ kind: "text", text: "progress" }], { isComplete: false }),
                agentMessage("task-1", [{ kind: "text", text: "response" }], { isComplete: false }),
            ];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const otherTaskMsg = output.messages!.find(m => m.taskId === "task-other");
            expect(otherTaskMsg).toBeDefined();
            expect(otherTaskMsg!.isComplete).toBeFalsy();
        });

        it("appends gateway-resolved file parts from final task message to streamed message", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "Here are your files:" }])];
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: {
                        state: "completed",
                        message: {
                            role: "agent",
                            parts: [
                                { kind: "file", file: { name: "page1.json", mimeType: "application/json", uri: "artifact://app/user/ses-1/page1.json?version=0" } },
                                { kind: "file", file: { name: "report.csv", mimeType: "text/csv", uri: "artifact://app/user/ses-1/report.csv?version=0" } },
                                { kind: "text", text: "Here are your files:" },
                            ],
                        },
                    },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.isComplete).toBe(true);
            const fileParts = taskMsg.parts.filter(p => p.kind === "file");
            expect(fileParts).toHaveLength(2);
            expect(fileParts[0]).toMatchObject({ kind: "file", file: { name: "page1.json" } });
            expect(fileParts[1]).toMatchObject({ kind: "file", file: { name: "report.csv" } });
            const textParts = taskMsg.parts.filter(p => p.kind === "text");
            expect(textParts).toHaveLength(1);
            expect(textParts[0]).toMatchObject({ kind: "text", text: "Here are your files:" });
        });

        it("adopts the completion's final text when the streamed bubble is blank", () => {
            const existing = [agentMessage("task-1", [])];
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "Here is the full analysis." }] } },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.isComplete).toBe(true);
            const textParts = taskMsg.parts.filter(p => p.kind === "text");
            expect(textParts).toHaveLength(1);
            expect(textParts[0]).toMatchObject({ kind: "text", text: "Here is the full analysis." });
        });

        function taskCompletionWithText(text: string): string {
            return JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text }] } },
                    contextId: "ses-1",
                },
            });
        }

        it("adopts the completion's final text when the only card is a hidden __working artifact", () => {
            const hiddenSpec = { kind: "artifact" as const, status: "completed" as const, name: "deck-spec.yaml", tags: ["__working"] };
            const existing = [agentMessage("task-1", [hiddenSpec])];
            const output = processChatEvent(makeInput({ eventData: taskCompletionWithText("Your deck is ready."), messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "text")).toEqual([{ kind: "text", text: "Your deck is ready." }]);
        });

        it("adopts the completion's final text when the only file part is a hidden __working return", () => {
            const hiddenReturn = { kind: "file" as const, file: { name: "deck-spec.yaml", uri: "artifact://ses-1/deck-spec.yaml?version=1" }, metadata: { tags: ["__working"] } };
            const existing = [agentMessage("task-1", [hiddenReturn])];
            const output = processChatEvent(makeInput({ eventData: taskCompletionWithText("Your deck is ready. «artifact_return:deck-spec.yaml»"), messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const textParts = taskMsg.parts.filter(p => p.kind === "text") as { text: string }[];
            expect(textParts).toHaveLength(1);
            expect(textParts[0].text).toContain("Your deck is ready.");
            expect(textParts[0].text).not.toContain("«");
        });

        it("adopts only the text when the completion also carries the hidden __working file part", () => {
            const hiddenReturn = { kind: "file", file: { name: "deck-spec.yaml", uri: "artifact://ses-1/deck-spec.yaml?version=1" }, metadata: { tags: ["__working"] } };
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "Your deck is ready. «artifact_return:deck-spec.yaml»" }, hiddenReturn] } },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [])] }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "file")).toEqual([hiddenReturn]);
            const textParts = taskMsg.parts.filter(p => p.kind === "text") as { text: string }[];
            expect(textParts).toHaveLength(1);
            expect(textParts[0].text).toContain("Your deck is ready.");
            expect(textParts[0].text).not.toContain("«");
        });

        it("keeps adopted markers for files that never streamed as parts", () => {
            const hiddenReturn = { kind: "file" as const, file: { name: "deck-spec.yaml", uri: "artifact://ses-1/deck-spec.yaml?version=1" }, metadata: { tags: ["__working"] } };
            const existing = [agentMessage("task-1", [hiddenReturn])];
            const output = processChatEvent(makeInput({ eventData: taskCompletionWithText("Done. «artifact_return:deck-spec.yaml» «artifact_return:report.pdf»"), messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const adoptedText = (taskMsg.parts.filter(p => p.kind === "text") as { text: string }[]).map(p => p.text).join("");
            expect(adoptedText).toContain("«artifact_return:report.pdf»");
            expect(adoptedText).not.toContain("deck-spec.yaml");
        });

        it("does not adopt the completion's text when a visible artifact card already fills the bubble", () => {
            const visibleDeck = { kind: "artifact" as const, status: "completed" as const, name: "deck.pptx" };
            const existing = [agentMessage("task-1", [visibleDeck])];
            const output = processChatEvent(makeInput({ eventData: taskCompletionWithText("Your deck is ready."), messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "text")).toHaveLength(0);
        });

        it("does not adopt a whitespace-only completion (leaves the blank bubble blank, not falsely completed)", () => {
            const existing = [agentMessage("task-1", [])];
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "   " }] } },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "text")).toHaveLength(0);
        });

        it("adopts a blank task's completion text without touching a different task's visible bubble", () => {
            const existing = [agentMessage("task-A", [{ kind: "text", text: "Task A answer." }]), agentMessage("task-B", [])];
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-B",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "Task B answer." }] } },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskA = output.messages!.find(m => m.taskId === "task-A")!;
            expect(taskA.parts.filter(p => p.kind === "text")).toHaveLength(1);
            expect(taskA.parts.filter(p => p.kind === "text")[0]).toMatchObject({ text: "Task A answer." });
            const taskB = output.messages!.find(m => m.taskId === "task-B")!;
            expect(taskB.parts.filter(p => p.kind === "text")[0]).toMatchObject({ text: "Task B answer." });
        });

        it("does not duplicate the completion's text when the streamed bubble already has it", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "The streamed answer." }])];
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "The streamed answer." }] } },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const textParts = taskMsg.parts.filter(p => p.kind === "text");
            expect(textParts).toHaveLength(1);
            expect(textParts[0]).toMatchObject({ kind: "text", text: "The streamed answer." });
        });

        it("creates a message from the completion text when no task message exists", () => {
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "Standalone answer." }] } },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1" && !m.isUser)!;
            expect(taskMsg).toBeDefined();
            expect(taskMsg.parts.filter(p => p.kind === "text")[0]).toMatchObject({ kind: "text", text: "Standalone answer." });
        });

        it("creates error message for failed task", () => {
            const eventData = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: "it broke" }]);
            const output = processChatEvent(makeInput({ eventData }));

            const errorMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(errorMsg).toBeDefined();
            expect(errorMsg!.isError).toBe(true);
        });

        describe("a workflow its usage budget stopped", () => {
            const stopped = 'Workflow "Report" was stopped because your usage budget is used up until 1 Feb. Contact your administrator if you need it sooner.';
            const budgetStop = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: stopped }], { workflow_name: "Report", llm_error_category: "budget_exhausted" });

            it("leaves what the workflow streamed as it was and puts the stop in a notice of its own", () => {
                const streamed = agentMessage("task-1", [{ kind: "text", text: "Step one done." }], { isComplete: false });
                const output = processChatEvent(makeInput({ eventData: budgetStop, messages: [userMessage("task-1"), streamed] }));

                const agentMessages = output.messages!.filter(m => !m.isUser);
                expect(agentMessages).toHaveLength(2);
                expect(agentMessages[0]).toMatchObject({ isComplete: true, parts: [{ kind: "text", text: "Step one done." }] });
                expect(agentMessages[0].isError).toBeFalsy();
                expect(agentMessages[1]).toMatchObject({ isError: true, isWarning: true, isComplete: true, taskId: "task-1", parts: [{ kind: "text", text: stopped }] });
                expect(agentMessages[1].metadata?.messageId).toBeTruthy();
                expect(agentMessages[1].metadata?.messageId).not.toBe(agentMessages[0].metadata?.messageId);
            });

            it("keeps the stop a notice of its own after the session is saved and reloaded", () => {
                const streamed = agentMessage("task-1", [{ kind: "text", text: "Step one done." }], { isComplete: false, metadata: { messageId: "task-1" } });
                const output = processChatEvent(makeInput({ eventData: budgetStop, messages: [userMessage("task-1"), streamed] }));

                const saved = output.messages!.filter(m => !m.isUser).map(m => ({ ...serializeChatMessage(m), type: "agent" as const }));
                const reloaded = deserializeChatMessages({ taskId: "task-1", messageBubbles: saved, createdAt: 1000 }, "ses-1");

                expect(reloaded).toHaveLength(2);
                expect(reloaded[0]).toMatchObject({ parts: [{ kind: "text", text: "Step one done." }] });
                expect(reloaded[0].isError).toBeFalsy();
                expect(reloaded[1]).toMatchObject({ isError: true, isWarning: true, parts: [{ kind: "text", text: stopped }] });
            });

            it("shows the notice alone when the workflow streamed nothing", () => {
                const output = processChatEvent(makeInput({ eventData: budgetStop, messages: [userMessage("task-1")] }));

                const agentMessages = output.messages!.filter(m => !m.isUser);
                expect(agentMessages).toHaveLength(1);
                expect(agentMessages[0]).toMatchObject({ isError: true, isWarning: true, parts: [{ kind: "text", text: stopped }] });
            });

            it("leaves another task's in-progress message alone", () => {
                const otherTask = agentMessage("task-2", [{ kind: "text", text: "Still going." }], { isComplete: false });
                const output = processChatEvent(makeInput({ eventData: budgetStop, messages: [userMessage("task-1"), otherTask] }));

                expect(output.messages!.find(m => m.taskId === "task-2")).toMatchObject({ isComplete: false, parts: [{ kind: "text", text: "Still going." }] });
                expect(output.messages![output.messages!.length - 1]).toMatchObject({ taskId: "task-1", isWarning: true });
            });

            it("shows a fixed notice when the stop has nothing visible to show", () => {
                const streamed = agentMessage("task-1", [{ kind: "text", text: "Step one done." }], { isComplete: false });
                const silentStop = taskCompleteEvent("task-1", "failed", [], { workflow_name: "Report", llm_error_category: "budget_exhausted" });
                const output = processChatEvent(makeInput({ eventData: silentStop, messages: [userMessage("task-1"), streamed] }));

                const agentMessages = output.messages!.filter(m => !m.isUser);
                expect(agentMessages).toHaveLength(2);
                expect(agentMessages[0]).toMatchObject({ isComplete: true, parts: [{ kind: "text", text: "Step one done." }] });
                expect(agentMessages[0].isWarning).toBeFalsy();
                expect(agentMessages[1]).toMatchObject({ isWarning: true, parts: [{ kind: "text", text: "This workflow stopped because a usage budget was reached." }] });
            });

            it("treats a workflow that completed as finished, whatever its metadata says", () => {
                const streamed = agentMessage("task-1", [{ kind: "text", text: "Step one done." }], { isComplete: false });
                const completed = taskCompleteEvent("task-1", "completed", [], { workflow_name: "Report", llm_error_category: "budget_exhausted" });
                const output = processChatEvent(makeInput({ eventData: completed, messages: [userMessage("task-1"), streamed] }));

                expect(output.messages!.some(m => m.isWarning)).toBe(false);
            });

            it("keeps any other workflow failure a red error joined to what it streamed", () => {
                const streamed = agentMessage("task-1", [{ kind: "text", text: "Step one done." }], { isComplete: false });
                const failed = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: 'Workflow "Report" failed: node timed out' }], { workflow_name: "Report" });
                const output = processChatEvent(makeInput({ eventData: failed, messages: [userMessage("task-1"), streamed] }));

                const agentMessages = output.messages!.filter(m => !m.isUser);
                expect(agentMessages).toHaveLength(1);
                expect(agentMessages[0].isError).toBe(true);
                expect(agentMessages[0].isWarning).toBeFalsy();
            });
        });

        it("cleans up deep research query history on completion", () => {
            const history = new Map([["task-1", [{ query: "q", timestamp: "t", urls: [] }]]]);
            const existing = [agentMessage("task-1", [{ kind: "text", text: "done" }])];
            const eventData = taskCompleteEvent("task-1");
            const output = processChatEvent(makeInput({ eventData, messages: existing, deepResearchQueryHistory: history }));

            expect(output.deepResearchQueryHistory!.has("task-1")).toBe(false);
        });
    });

    describe("background task timestamp", () => {
        it("emits update-task-timestamp for background tasks", () => {
            const eventData = statusUpdateEvent("task-bg", [{ kind: "text", text: "working" }]);
            const output = processChatEvent(
                makeInput({
                    eventData,
                    isTaskRunningInBackground: id => id === "task-bg",
                })
            );

            const tsEffect = output.effects.find(e => e.type === "update-task-timestamp");
            expect(tsEffect).toBeDefined();
        });

        it("does not emit update-task-timestamp for foreground tasks", () => {
            const eventData = statusUpdateEvent("task-fg", [{ kind: "text", text: "working" }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.effects.find(e => e.type === "update-task-timestamp")).toBeUndefined();
        });

        it("emits unregister + dispatch for background task with no messages on final", () => {
            const eventData = taskCompleteEvent("task-bg");
            const output = processChatEvent(
                makeInput({
                    eventData,
                    isTaskRunningInBackground: id => id === "task-bg",
                })
            );

            expect(effectTypes(output)).toContain("unregister-background-task");
            expect(effectTypes(output)).toContain("dispatch-new-chat-session");
        });
    });

    describe("thinking_content", () => {
        it("returns no state changes when showThinkingContent is disabled and no other parts", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "thinking_content", content: "hmm", is_complete: false } }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.messages).toBeUndefined();
            expect(output.effects).toEqual([]);
        });

        it("creates a thinking progress update when showThinkingContent is enabled", () => {
            const existing = [agentMessage("task-1")];
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "thinking_content", content: "think", is_complete: true } }]);
            const output = processChatEvent(
                makeInput({
                    eventData,
                    messages: existing,
                    flags: { showThinkingContent: true, ragEnabled: true, isReplaying: false },
                })
            );

            expect(output.messages![0].progressUpdates).toBeDefined();
            expect(output.messages![0].progressUpdates![0].type).toBe("thinking");
        });
    });

    describe("agent_progress_update", () => {
        it("sets latestStatusText", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "agent_progress_update", status_text: "Searching..." } }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.latestStatusText).toBe("Searching...");
        });

        it("defaults status text to Processing...", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "agent_progress_update" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.latestStatusText).toBe("Processing...");
        });

        it("returns early with messages when status-only (no other parts)", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "agent_progress_update", status_text: "Working" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.latestStatusText).toBe("Working");
        });
    });

    describe("authentication_required", () => {
        it("adds an auth message with valid http URI", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com", target_agent: "OAuth Agent" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg).toBeDefined();
            expect(authMsg!.authenticationLink!.url).toBe("https://auth.example.com");
            expect(authMsg!.authenticationLink!.targetAgent).toBe("OAuth Agent");
        });

        it("does not add auth message for non-http URI", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "ftp://bad" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages?.find(m => m.authenticationLink);
            expect(authMsg).toBeUndefined();
        });

        it("defaults target_agent to Agent", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.targetAgent).toBe("Agent");
        });

        it("maps tool_display_name to serviceName", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com", tool_display_name: "Atlassian" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.serviceName).toBe("Atlassian");
        });

        it("leaves serviceName undefined when tool_display_name is absent", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.serviceName).toBeUndefined();
        });

        it("keeps the task active while parked for auth so the Cancel button stays usable", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(effectTypes(output)).not.toContain("stop-responding");
            expect(effectTypes(output)).not.toContain("clear-task-id");
            expect(effectTypes(output)).not.toContain("finalize");
        });

        it("tags the auth prompt with the SSE taskId for later resolution", () => {
            const eventData = statusUpdateEvent("task-9", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.taskId).toBe("task-9");
        });

        it("supersedes a still-pending prior prompt for the same task when a new one arrives", () => {
            const prior = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com/1", text: "Authenticate", authenticationAttempted: true },
                metadata: { messageId: "auth-old" },
            });
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com/2" } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [prior] }));

            const superseded = output.messages!.find(m => m.metadata?.messageId === "auth-old");
            expect(superseded!.authenticationLink!.outcome).toBe("failed");
            expect(output.messages!.filter(m => m.authenticationLink).length).toBe(2);
        });

        it("parses credential_key onto the card", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com", credential_key: "cred-A" } }]);
            const output = processChatEvent(makeInput({ eventData }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.credentialKey).toBe("cred-A");
        });

        it("does NOT supersede a concurrent prompt carrying a different credential_key", () => {
            const prior = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com/A", text: "Authenticate", credentialKey: "cred-A" },
                metadata: { messageId: "auth-A" },
            });
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com/B", credential_key: "cred-B" } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [prior] }));

            const cardA = output.messages!.find(m => m.metadata?.messageId === "auth-A");
            expect(cardA!.authenticationLink!.outcome).toBeUndefined();
            const cardB = output.messages!.find(m => m.authenticationLink?.credentialKey === "cred-B");
            expect(cardB).toBeDefined();
            expect(cardB!.authenticationLink!.outcome).toBeUndefined();
            expect(output.messages!.filter(m => m.authenticationLink).length).toBe(2);
        });

        it("supersedes a prior prompt with the SAME credential_key (retry)", () => {
            const prior = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com/1", text: "Authenticate", credentialKey: "cred-A", authenticationAttempted: true },
                metadata: { messageId: "auth-old" },
            });
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com/2", credential_key: "cred-A" } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [prior] }));

            const superseded = output.messages!.find(m => m.metadata?.messageId === "auth-old");
            expect(superseded!.authenticationLink!.outcome).toBe("failed");
            expect(output.messages!.filter(m => m.authenticationLink).length).toBe(2);
        });

        it("does not absorb resumed content into the auth-prompt bubble", () => {
            const authText = (t: string): string => statusUpdateEvent("task-1", [{ kind: "text", text: t }]);
            const progress = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "agent_progress_update", status_text: "Working after auth..." } }]);
            const authEvent = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_required", auth_uri: "https://auth.example.com", tool_display_name: "Atlassian" } }]);

            let messages: MessageFE[] = [];
            let seq = 1;
            for (const eventData of [authEvent, progress, authText("The answer is 42."), taskCompleteEvent("task-1", "completed", [{ kind: "text", text: "The answer is 42." }])]) {
                const out = processChatEvent(makeInput({ eventData, messages, eventSequence: seq++, flags: { showThinkingContent: false, ragEnabled: true, isReplaying: false } }));
                messages = out.messages!;
            }

            const authMsg = messages.find(m => m.authenticationLink)!;
            expect(authMsg).toBeDefined();
            expect(authMsg.parts.some(p => p.kind === "text" && (p as { text?: string }).text?.includes("42"))).toBe(false);
            expect(authMsg.progressUpdates?.length ?? 0).toBe(0);

            const answerMsg = messages.find(m => !m.isUser && !m.authenticationLink && m.parts.some(p => p.kind === "text" && (p as { text?: string }).text?.includes("42")));
            expect(answerMsg, "resumed answer must land on a non-auth bubble").toBeDefined();
            const timelineMsg = messages.find(m => !m.isUser && !m.authenticationLink && (m.progressUpdates?.length ?? 0) > 0);
            expect(timelineMsg, "resumed status timeline must land on a non-auth bubble").toBeDefined();
        });
    });

    describe("authentication_result", () => {
        function pendingAuth(taskId: string): MessageFE {
            return agentMessage(taskId, [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", authenticationAttempted: true },
                metadata: { messageId: "auth-1" },
            });
        }

        it("resolves the pending prompt to 'authenticated' on success", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_result", success: true } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("authenticated");
        });

        it("resolves the pending prompt to 'failed' on failure", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_result", success: false } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("failed");
        });

        it("does not touch a prompt for a different task", () => {
            const eventData = statusUpdateEvent("task-2", [{ kind: "data", data: { type: "authentication_result", success: true } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBeUndefined();
        });

        it("treats an absent success field as failure", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_result" } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("failed");
        });

        it("does not overwrite an already-resolved outcome", () => {
            const resolved = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", outcome: "authenticated" },
                metadata: { messageId: "auth-1" },
            });
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_result", success: false } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [resolved] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("authenticated");
        });

        it("resolves only the card matching the result's credential_key", () => {
            const cardA = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com/A", text: "Authenticate", credentialKey: "cred-A" },
                metadata: { messageId: "auth-A" },
            });
            const cardB = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com/B", text: "Authenticate", credentialKey: "cred-B" },
                metadata: { messageId: "auth-B" },
            });
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "authentication_result", success: true, credential_key: "cred-B" } }]);
            const output = processChatEvent(makeInput({ eventData, messages: [cardA, cardB] }));

            expect(output.messages!.find(m => m.metadata?.messageId === "auth-B")!.authenticationLink!.outcome).toBe("authenticated");
            expect(output.messages!.find(m => m.metadata?.messageId === "auth-A")!.authenticationLink!.outcome).toBeUndefined();
        });
    });

    describe("auth prompt resolution on task completion", () => {
        function pendingAuth(taskId: string): MessageFE {
            return agentMessage(taskId, [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", authenticationAttempted: true },
                metadata: { messageId: "auth-1" },
            });
        }

        it("resolves an unresolved prompt to 'authenticated' when the task completes", () => {
            const eventData = taskCompleteEvent("task-1", "completed");
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("authenticated");
        });

        it("resolves an unresolved prompt to 'failed' when the task fails", () => {
            const eventData = taskCompleteEvent("task-1", "failed");
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("failed");
        });

        it("leaves a rejected prompt untouched on completion", () => {
            const rejected = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", rejected: true },
                metadata: { messageId: "auth-1" },
            });
            const eventData = taskCompleteEvent("task-1", "completed");
            const output = processChatEvent(makeInput({ eventData, messages: [rejected] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBeUndefined();
            expect(authMsg!.authenticationLink!.rejected).toBe(true);
        });

        it("resolves an unresolved prompt to 'failed' when the task is canceled (not authenticated)", () => {
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: { kind: "task", id: "task-1", status: { state: "canceled", message: { role: "agent", parts: [] } } },
            });
            const output = processChatEvent(makeInput({ eventData, messages: [pendingAuth("task-1")] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("failed");
        });
    });

    describe("auth prompt resolution on RPC error", () => {
        it("resolves a pending prompt for the errored task to 'failed' rather than leaving it spinning", () => {
            const authCard = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", authenticationAttempted: true },
                metadata: { messageId: "auth-1" },
                isComplete: true,
            });
            const streaming = agentMessage("task-1", [{ kind: "text", text: "working" }], { isComplete: false });
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: [authCard, streaming] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBe("failed");
        });

        it("does not touch a rejected prompt on RPC error", () => {
            const rejected = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", rejected: true },
                metadata: { messageId: "auth-1" },
                isComplete: true,
            });
            const streaming = agentMessage("task-1", [{ kind: "text", text: "working" }], { isComplete: false });
            const output = processChatEvent(makeInput({ eventData: rpcError("boom"), messages: [rejected, streaming] }));

            const authMsg = output.messages!.find(m => m.authenticationLink);
            expect(authMsg!.authenticationLink!.outcome).toBeUndefined();
            expect(authMsg!.authenticationLink!.rejected).toBe(true);
        });

        it("does not inject an error message when the task fails after user denied auth", () => {
            const rejected = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", rejected: true },
                metadata: { messageId: "auth-1" },
                isComplete: true,
            });
            const output = processChatEvent(makeInput({ eventData: rpcError("Task cancelled"), messages: [rejected] }));

            const errorMessages = output.messages!.filter(m => m.isError);
            expect(errorMessages).toHaveLength(0);
        });
    });

    describe("auth denial suppresses task-failed error message", () => {
        it("does not inject an error chat message when the task fails after user denied auth", () => {
            const rejected = agentMessage("task-1", [{ kind: "text", text: "" }], {
                authenticationLink: { url: "https://auth.example.com", text: "Authenticate", rejected: true },
                metadata: { messageId: "auth-1" },
                isComplete: true,
            });
            const output = processChatEvent(makeInput({ eventData: taskCompleteEvent("task-1", "failed"), messages: [rejected] }));

            const errorMessages = output.messages!.filter(m => m.isError);
            expect(errorMessages).toHaveLength(0);
        });
    });

    describe("rag_info_update", () => {
        it("creates a new RAG entry", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Search Results",
                        query: "test query",
                        search_type: "deep_research",
                        sources: [{ url: "https://example.com", title: "Example", citationId: "s0r0" }],
                        is_complete: false,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].query).toBe("test query");
            expect(output.ragData![0].sources).toHaveLength(1);
        });

        it("does not update ragData when ragEnabled is false", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Results",
                        query: "q",
                        search_type: "deep_research",
                        sources: [],
                        is_complete: false,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(
                makeInput({
                    eventData,
                    flags: { showThinkingContent: false, ragEnabled: false, isReplaying: false },
                })
            );

            expect(output.ragData).toBeUndefined();
        });

        it("merges sources into existing RAG entry for same query", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "test",
                    searchType: "deep_research",
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [{ citationId: "s0r0", title: "A", sourceUrl: "https://a.com", url: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: {} }],
                    taskId: "task-1",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Updated",
                        query: "test",
                        search_type: "deep_research",
                        sources: [
                            { url: "https://a.com", title: "A" },
                            { url: "https://b.com", title: "B" },
                        ],
                        is_complete: false,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, ragData: existing }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].sources).toHaveLength(2);
        });

        it("preserves nested metadata (fetched/favicon) and sets isComplete from is_complete", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Report",
                        query: "q",
                        search_type: "deep_research",
                        sources: [
                            {
                                url: "https://a.com",
                                title: "A",
                                citationId: "research0",
                                contentPreview: "full text [Full Content Fetched]",
                                metadata: { fetched: true, favicon: "https://a.com/fav.ico", source_type: "web", type: "web_search" },
                            },
                        ],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const entry = output.ragData![0];
            expect(entry.isComplete).toBe(true);
            const source = entry.sources[0];
            expect(source.citationId).toBe("research0");
            expect(source.metadata?.fetched).toBe(true);
            expect(source.metadata?.favicon).toBe("https://a.com/fav.ico");
            expect(source.sourceType).toBe("web");
            expect(source.contentPreview).toBe("full text [Full Content Fetched]");
        });

        it("lets the final cumulative (fetched) source list win over an earlier snippet-only entry", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "q",
                    searchType: "deep_research",
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [{ citationId: "research0", title: "A", sourceUrl: "https://a.com", url: "https://a.com", contentPreview: "snippet", relevanceScore: 1, metadata: { fetched: false } }],
                    taskId: "task-1",
                    isComplete: false,
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Report",
                        query: "q",
                        search_type: "deep_research",
                        sources: [{ url: "https://a.com", title: "A", citationId: "research0", metadata: { fetched: true } }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, ragData: existing }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].isComplete).toBe(true);
            expect(output.ragData![0].sources).toHaveLength(1);
            expect(output.ragData![0].sources[0].metadata?.fetched).toBe(true);
        });

        it("stores the signal's search_type instead of assuming deep research", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Web Results",
                        query: "solace agent mesh",
                        search_type: "web_search",
                        sources: [{ url: "https://solace.com", title: "Solace", citationId: "s1r0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].searchType).toBe("web_search");
        });

        it("keeps deep_research signals on the deep_research branch", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Report",
                        query: "q",
                        search_type: "deep_research",
                        sources: [{ url: "https://a.com", title: "A", citationId: "research0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].searchType).toBe("deep_research");
        });

        it("falls back to deep_research when the signal omits search_type", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Report",
                        query: "q",
                        sources: [{ url: "https://a.com", title: "A", citationId: "research0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].searchType).toBe("deep_research");
        });

        it("keeps both entries when the same web_search query is re-issued at a later turn", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "solace agent mesh",
                    searchType: "web_search",
                    searchTurn: 0,
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [{ citationId: "s0r0", title: "A", sourceUrl: "https://a.com", url: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: {} }],
                    taskId: "task-1",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        query: "solace agent mesh",
                        search_type: "web_search",
                        search_turn: 3,
                        sources: [{ url: "https://b.com", title: "B", citationId: "s3r0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:05:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, ragData: existing }));

            expect(output.ragData).toHaveLength(2);
            const ids = output.ragData!.flatMap(r => r.sources.map(s => s.citationId));
            expect(ids).toContain("s0r0");
            expect(ids).toContain("s3r0");
        });

        it("replaces the entry when deep_research republishes its cumulative sources", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "what is solace agent mesh",
                    searchType: "deep_research",
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [{ citationId: "research0", title: "A", sourceUrl: "https://a.com", url: "https://a.com", contentPreview: "unfetched snippet", relevanceScore: 1, metadata: {} }],
                    taskId: "task-1",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        query: "what is solace agent mesh",
                        search_type: "deep_research",
                        sources: [
                            { url: "https://a.com", title: "A", citationId: "research0", contentPreview: "the full fetched page text" },
                            { url: "https://b.com", title: "B", citationId: "research1" },
                        ],
                        is_complete: true,
                        timestamp: "2024-01-01T00:05:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, ragData: existing }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].sources).toHaveLength(2);
            expect(output.ragData![0].sources[0].contentPreview).toBe("the full fetched page text");
        });

        it("warns when search_type is unrecognised", () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        query: "q",
                        search_type: "web-search",
                        sources: [{ url: "https://a.com", title: "A", citationId: "x0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData![0].searchType).toBe("deep_research");
            expect(warn).toHaveBeenCalledWith(expect.stringContaining("web-search"));
            warn.mockRestore();
        });

        it("does not warn when search_type is simply absent", () => {
            const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        query: "q",
                        sources: [{ url: "https://a.com", title: "A", citationId: "research0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            processChatEvent(makeInput({ eventData }));

            expect(warn).not.toHaveBeenCalled();
            warn.mockRestore();
        });

        it("does not merge a deep_research signal into a web_search entry for the same query", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "q",
                    searchType: "web_search",
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [{ citationId: "s1r0", title: "A", sourceUrl: "https://a.com", url: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: {} }],
                    taskId: "task-1",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Report",
                        query: "q",
                        search_type: "deep_research",
                        sources: [{ url: "https://b.com", title: "B", citationId: "research0" }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, ragData: existing }));

            expect(output.ragData).toHaveLength(2);
            expect(output.ragData!.map(r => r.searchType)).toEqual(["web_search", "deep_research"]);
            expect(output.ragData![0].sources[0].url).toBe("https://a.com");
            expect(output.ragData![1].sources[0].url).toBe("https://b.com");
        });
    });

    describe("tool_result rag_metadata", () => {
        const toolResultEvent = (ragMetadata: unknown) => statusUpdateEvent("task-1", [{ kind: "data", data: { type: "tool_result", result_data: { rag_metadata: ragMetadata } } }]);

        it("ingests a well-formed rag_metadata object (web search)", () => {
            const output = processChatEvent(
                makeInput({
                    eventData: toolResultEvent({
                        query: "q",
                        searchType: "web_search",
                        timestamp: "2024-01-01T00:00:00Z",
                        sources: [{ citationId: "s0r0", title: "A", url: "https://a.com", sourceUrl: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: {} }],
                    }),
                })
            );
            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].searchType).toBe("web_search");
        });

        it("ignores a bare RAGSource[] array so it can't append a junk entry that shadows the deep-research sources", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "q",
                    searchType: "deep_research",
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [{ citationId: "research0", title: "A", url: "https://a.com", sourceUrl: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: { fetched: true } }],
                    taskId: "task-1",
                    isComplete: true,
                },
            ];
            const output = processChatEvent(
                makeInput({
                    eventData: toolResultEvent([{ citationId: "research0", title: "A", url: "https://a.com", sourceUrl: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: { fetched: true } }]),
                    ragData: existing,
                })
            );
            expect(output.ragData).toBeUndefined();
        });

        it("does not let a same-event bare-array tool_result clobber sources a prior rag_info_update part just set", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "Report",
                        query: "q",
                        search_type: "deep_research",
                        sources: [{ url: "https://a.com", title: "A", citationId: "research0", metadata: { fetched: true } }],
                        is_complete: true,
                        timestamp: "2024-01-01T00:00:00Z",
                    },
                },
                {
                    kind: "data",
                    data: { type: "tool_result", result_data: { rag_metadata: [{ citationId: "research0", title: "A", url: "https://a.com", sourceUrl: "https://a.com", contentPreview: "", relevanceScore: 1, metadata: { fetched: true } }] } },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].sources).toHaveLength(1);
            expect(output.ragData![0].sources[0].citationId).toBe("research0");
            expect(output.ragData![0].isComplete).toBe(true);
        });
    });

    describe("deep_research_progress", () => {
        it("clears latestStatusText", () => {
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "deep_research_progress", phase: "searching", current_query: "q1", fetching_urls: [] } }]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.latestStatusText).toBeNull();
        });

        it("accumulates query history", () => {
            const history = new Map<string, Array<{ query: string; timestamp: string; urls: Array<{ url: string; title: string; favicon: string; source_type?: string }> }>>();
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "deep_research_progress",
                        phase: "searching",
                        current_query: "what is X",
                        fetching_urls: [{ url: "https://x.com", title: "X", favicon: "" }],
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, deepResearchQueryHistory: history }));

            expect(output.deepResearchQueryHistory!.get("task-1")).toHaveLength(1);
            expect(output.deepResearchQueryHistory!.get("task-1")![0].query).toBe("what is X");
        });
    });

    describe("compaction_notification", () => {
        it("clears latestStatusText", () => {
            const eventData = statusUpdateEvent("task-1", [
                { kind: "data", data: { type: "compaction_notification" } },
                { kind: "text", text: "Context compacted" },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.latestStatusText).toBeNull();
        });
    });

    describe("tool_result with redirect data", () => {
        it("extracts redirect data from tool_result and injects as DataPart", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "Here is a redirect" }])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "tool_result",
                        result_data: {
                            type: "redirect",
                            route: "/builder",
                            label: "Go to Builder",
                            context: "Build me an app",
                        },
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            const redirectPart = taskMsg!.parts.find(p => p.kind === "data" && (p as DataPart).data?.type === "redirect");
            expect(redirectPart).toBeDefined();
            expect((redirectPart as DataPart).data).toMatchObject({
                type: "redirect",
                route: "/builder",
                label: "Go to Builder",
                context: "Build me an app",
            });
        });

        it("does not inject DataPart when result_data type is not redirect", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "response" }])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "tool_result",
                        result_data: {
                            type: "other_type",
                            some_field: "value",
                        },
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            const redirectPart = taskMsg!.parts.find(p => p.kind === "data" && (p as DataPart).data?.type === "redirect");
            expect(redirectPart).toBeUndefined();
        });

        it("injects a request_build_activate signal and it SURVIVES the renderable filter onto the message (so useChatDeployTrigger can see it)", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "Deploying your build now." }])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "tool_result",
                        result_data: { type: "request_build_activate" },
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            const signalPart = taskMsg!.parts.find(p => p.kind === "data" && (p as DataPart).data?.type === "request_build_activate");
            expect(signalPart).toBeDefined();
        });

        it("creates a message for a standalone request_build_activate signal even with no prior agent text (the 'deploy it' ordering)", () => {
            const existing = [userMessage("task-1")];
            const eventData = statusUpdateEvent("task-1", [{ kind: "data", data: { type: "tool_result", result_data: { type: "request_build_activate" } } }]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const agentMsg = output.messages!.find(m => m.taskId === "task-1" && !m.isUser);
            expect(agentMsg).toBeDefined();
            expect(agentMsg!.parts.some(p => p.kind === "data" && (p as DataPart).data?.type === "request_build_activate")).toBe(true);
        });

        it("does not inject DataPart when result_data is missing", () => {
            const existing = [agentMessage("task-1", [{ kind: "text", text: "response" }])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "tool_result",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            const redirectPart = taskMsg!.parts.find(p => p.kind === "data" && (p as DataPart).data?.type === "redirect");
            expect(redirectPart).toBeUndefined();
        });
    });

    describe("tool_result with RAG metadata", () => {
        it("appends RAG data for web_search", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "tool_result",
                        result_data: {
                            rag_metadata: {
                                query: "search",
                                searchType: "web_search",
                                timestamp: "2024-01-01T00:00:00Z",
                                sources: [],
                            },
                        },
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].searchType).toBe("web_search");
        });

        it("replaces previous deep_research entries for same task", () => {
            const existing: RAGSearchResult[] = [
                {
                    query: "old",
                    searchType: "deep_research",
                    timestamp: "t",
                    sources: [],
                    taskId: "task-1",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "tool_result",
                        result_data: {
                            rag_metadata: {
                                query: "new",
                                searchType: "deep_research",
                                timestamp: "t2",
                                sources: [],
                            },
                        },
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, ragData: existing }));

            expect(output.ragData).toHaveLength(1);
            expect(output.ragData![0].query).toBe("new");
        });
    });

    describe("gateway-resolved file parts", () => {
        it("keeps a streamed file part's __working tag through ingestion", () => {
            const eventData = statusUpdateEvent("task-1", [
                { kind: "text", text: "Deck ready." },
                {
                    kind: "file",
                    file: { name: "deck-spec.yaml", uri: "artifact://ses-1/deck-spec.yaml?version=1", mimeType: "application/yaml" },
                    metadata: { tags: ["__working"] },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: [userMessage("task-1")] }));

            const taskMsg = output.messages!.find(m => !m.isUser && m.taskId === "task-1")!;
            const deckParts = taskMsg.parts.filter(p => partArtifactName(p) === "deck-spec.yaml");
            expect(deckParts).toHaveLength(1);
            expect(deckParts[0]).toMatchObject({ kind: "file", metadata: { tags: expect.arrayContaining(["__working"]) } });
            expect(isHiddenWorkingPart(deckParts[0])).toBe(true);
        });
    });

    describe("artifact_creation_progress", () => {
        it("carries __working tags onto the inline artifact card", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "deck-spec.yaml",
                        status: "in-progress",
                        bytes_transferred: 120,
                        tags: ["__working"],
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const cards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(cards).toHaveLength(1);
            expect(cards[0]).toMatchObject({ name: "deck-spec.yaml", tags: ["__working"] });
        });

        it("merges tags from a completed event onto a card saved untagged", () => {
            const untaggedCard = {
                kind: "artifact" as const,
                status: "in-progress" as const,
                name: "deck-spec.yaml",
                bytesTransferred: 120,
            };
            const existing = [agentMessage("task-1", [untaggedCard])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "deck-spec.yaml",
                        status: "completed",
                        bytes_transferred: 120,
                        mime_type: "application/yaml",
                        version: 2,
                        tags: ["__working"],
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const cards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(cards).toHaveLength(1);
            expect(cards[0]).toMatchObject({ status: "completed", tags: ["__working"] });
        });

        it("un-hides the card when the same filename is re-saved without tags", () => {
            const earlierWorkingSave = { kind: "artifact" as const, status: "completed" as const, name: "notes.md", tags: ["__working"] };
            const reSave = (status: "in-progress" | "completed") =>
                statusUpdateEvent("task-1", [
                    {
                        kind: "data",
                        data: { type: "artifact_creation_progress", filename: "notes.md", status, bytes_transferred: 50, mime_type: "text/markdown", version: 1 },
                    },
                ]);

            const streaming = processChatEvent(makeInput({ eventData: reSave("in-progress"), messages: [agentMessage("task-1", [earlierWorkingSave])] }));
            const done = processChatEvent(makeInput({ eventData: reSave("completed"), messages: streaming.messages! }));

            const card = done.messages!.find(m => m.taskId === "task-1")!.parts.find(p => p.kind === "artifact")!;
            expect(card).toMatchObject({ status: "completed", name: "notes.md" });
            expect(card).not.toHaveProperty("tags");
        });

        it("replaces rather than merges the card's tags on a completed event", () => {
            const taggedCard = { kind: "artifact" as const, status: "in-progress" as const, name: "report.md", tags: ["__working"] };
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: { type: "artifact_creation_progress", filename: "report.md", status: "completed", bytes_transferred: 50, mime_type: "text/markdown", version: 0, tags: ["reviewed"] },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [taggedCard])] }));

            const card = output.messages!.find(m => m.taskId === "task-1")!.parts.find(p => p.kind === "artifact")!;
            expect(card).toMatchObject({ tags: ["reviewed"] });
        });

        it("tags the card from an artifact_saved fallback when the completed event is lost", () => {
            const untaggedCard = {
                kind: "artifact" as const,
                status: "in-progress" as const,
                name: "build_manifest.yaml",
                bytesTransferred: 40,
            };
            const existing = [agentMessage("task-1", [untaggedCard])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_saved",
                        filename: "build_manifest.yaml",
                        version: 1,
                        mime_type: "application/vnd.sam-build-manifest+yaml",
                        size_bytes: 400,
                        tags: ["__working"],
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const cards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(cards[0]).toMatchObject({ status: "completed", tags: ["__working"] });
        });

        it("merges tags onto a card on a subsequent in-progress chunk", () => {
            const untaggedCard = {
                kind: "artifact" as const,
                status: "in-progress" as const,
                name: "deck-spec.yaml",
                bytesTransferred: 10,
            };
            const existing = [agentMessage("task-1", [untaggedCard])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "deck-spec.yaml",
                        status: "in-progress",
                        bytes_transferred: 80,
                        tags: ["__working"],
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "artifact")[0]).toMatchObject({ tags: ["__working"] });
        });

        it("takes a failed event's tags onto the card", () => {
            const untaggedCard = { kind: "artifact" as const, status: "in-progress" as const, name: "deck-spec.yaml", bytesTransferred: 20 };
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: { type: "artifact_creation_progress", filename: "deck-spec.yaml", status: "failed", bytes_transferred: 0, tags: ["__working"] },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [untaggedCard])] }));

            const card = output.messages!.find(m => m.taskId === "task-1")!.parts.find(p => p.kind === "artifact")!;
            expect(card).toMatchObject({ status: "failed", tags: ["__working"] });
        });

        it("keeps tags and description when the completion sweep finalizes another task's stuck card", () => {
            const stuckCard = { kind: "artifact" as const, status: "in-progress" as const, name: "deck-spec.yaml", description: "Slide spec", tags: ["__working"], bytesTransferred: 10 };
            const existing = [agentMessage("task-0", [stuckCard]), agentMessage("task-1", [{ kind: "text", text: "done" }])];
            const output = processChatEvent(makeInput({ eventData: taskCompleteEvent("task-1"), messages: existing }));

            const card = output.messages!.find(m => m.taskId === "task-0")!.parts.find(p => p.kind === "artifact")!;
            expect(card).toMatchObject({ status: "completed", description: "Slide spec", tags: ["__working"] });
            expect(card).not.toHaveProperty("bytesTransferred");
        });

        it("creates a new artifact entry for in-progress status", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "in-progress",
                        bytes_transferred: 500,
                        description: "A document",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            expect(output.artifacts).toHaveLength(1);
            expect(output.artifacts![0].filename).toBe("doc.pdf");
            expect(output.artifacts![0].creationStatus).toBe("in-progress");
        });

        it("keeps lastModified stable across subsequent chunks of an in-progress stream", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                    creationStatus: "in-progress",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "in-progress",
                        bytes_transferred: 500,
                        artifact_chunk: "more content",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            expect(output.artifacts![0].lastModified).toBe("2024-01-01");
        });

        it("restamps lastModified when a new creation stream starts over a persisted artifact", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "in-progress",
                        bytes_transferred: 50,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            expect(output.artifacts![0].lastModified).not.toBe("2024-01-01");
        });

        it("marks the side-panel artifact completed once the save finishes", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                    creationStatus: "in-progress",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "completed",
                        bytes_transferred: 1000,
                        mime_type: "application/pdf",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            expect(output.artifacts![0].creationStatus).toBe("completed");
        });

        it("marks the side-panel artifact failed when the save fails", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                    creationStatus: "in-progress",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "failed",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            expect(output.artifacts![0].creationStatus).toBe("failed");
        });

        it("emits refetch-artifacts on completed artifact", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                    description: "test",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "completed",
                        bytes_transferred: 1000,
                        mime_type: "application/pdf",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            expect(effectTypes(output)).toContain("refetch-artifacts");
        });

        it("does not emit finalize-artifacts on a mid-task completed artifact", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                    description: "test",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "completed",
                        bytes_transferred: 1000,
                        mime_type: "application/pdf",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            const types = effectTypes(output);
            expect(types).toContain("refetch-artifacts");
            expect(types).not.toContain("finalize-artifacts");
        });

        it("emits finalize-artifacts before refetch-artifacts on the final event, because the refetch reads the finalized cache", () => {
            const output = processChatEvent(makeInput({ eventData: taskCompleteEvent("task-1") }));

            const types = effectTypes(output);
            expect(types).toContain("finalize-artifacts");
            expect(types.indexOf("finalize-artifacts")).toBeLessThan(types.indexOf("refetch-artifacts"));
        });

        it("emits auto-download-artifact for completed displayed artifact", () => {
            const existing: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                    isDisplayed: true,
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "completed",
                        bytes_transferred: 1000,
                        mime_type: "application/pdf",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existing }));

            expect(effectTypes(output)).toContain("auto-download-artifact");
        });

        it("removes artifact from the side panel on cancelled status", () => {
            const existingArtifacts: ArtifactInfo[] = [
                {
                    filename: "doc.pdf",
                    mimeType: "application/pdf",
                    size: 100,
                    lastModified: "2024-01-01",
                },
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "doc.pdf",
                        status: "cancelled",
                        bytes_transferred: 0,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, artifacts: existingArtifacts }));

            expect(output.artifacts).toHaveLength(0);
        });

        it("pushes a streaming artifact card on in-progress (fenced-block save_artifact)", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "report.md",
                        status: "in-progress",
                        bytes_transferred: 500,
                        description: "Streaming markdown report",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const artifactCards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(artifactCards).toHaveLength(1);
            expect(artifactCards[0]).toMatchObject({
                kind: "artifact",
                status: "in-progress",
                name: "report.md",
                bytesTransferred: 500,
                description: "Streaming markdown report",
            });
            expect(output.artifacts!.find(a => a.filename === "report.md")).toBeDefined();
        });

        it("transitions the streaming card to completed on the matching completed event", () => {
            const existing = [
                agentMessage("task-1", [
                    {
                        kind: "artifact" as const,
                        status: "in-progress" as const,
                        name: "report.md",
                        bytesTransferred: 800,
                    },
                ]),
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "report.md",
                        status: "completed",
                        bytes_transferred: 1024,
                        mime_type: "text/markdown",
                        version: 0,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const artifactCards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(artifactCards).toHaveLength(1);
            expect(artifactCards[0]).toMatchObject({
                kind: "artifact",
                status: "completed",
                name: "report.md",
                file: expect.objectContaining({ name: "report.md", mimeType: "text/markdown" }),
            });
        });

        it("does NOT add a chat card for a tool-saved completed artifact (no prior in-progress)", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "tool_output.json",
                        status: "completed",
                        bytes_transferred: 1000,
                        mime_type: "application/json",
                        description: "tool output",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages?.find(m => m.taskId === "task-1");
            const artifactCards = taskMsg?.parts.filter(p => p.kind === "artifact") ?? [];
            expect(artifactCards).toHaveLength(0);
            expect(output.artifacts!.find(a => a.filename === "tool_output.json")).toBeDefined();
            expect(effectTypes(output)).toContain("refetch-artifacts");
        });

        it("transitions the streaming card to failed on a matching failed event", () => {
            const existing = [
                agentMessage("task-1", [
                    {
                        kind: "artifact" as const,
                        status: "in-progress" as const,
                        name: "report.md",
                        bytesTransferred: 100,
                    },
                ]),
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "report.md",
                        status: "failed",
                        bytes_transferred: 100,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const artifactCards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(artifactCards).toHaveLength(1);
            expect(artifactCards[0]).toMatchObject({ kind: "artifact", status: "failed", name: "report.md" });
        });

        it("does NOT add a chat card for a failed event with no prior in-progress card", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "scratch.json",
                        status: "failed",
                        bytes_transferred: 0,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages?.find(m => m.taskId === "task-1");
            expect(taskMsg?.isError).toBeFalsy();
            expect(taskMsg?.parts.filter(p => p.kind === "artifact") ?? []).toHaveLength(0);
        });

        it("does NOT add a chat card when the event is forwarded from a peer sub-task", () => {
            const eventData = statusUpdateEvent(
                "task-1",
                [
                    {
                        kind: "data",
                        data: {
                            type: "artifact_creation_progress",
                            filename: "subtask.md",
                            status: "in-progress",
                            bytes_transferred: 200,
                            description: "from a sub-agent",
                        },
                    },
                ],
                { messageMetadata: { forwarded_from_peer: "ChildAgent" } }
            );
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages?.find(m => m.taskId === "task-1");
            const artifactCards = taskMsg?.parts.filter(p => p.kind === "artifact") ?? [];
            expect(artifactCards).toHaveLength(0);
            expect(output.artifacts!.find(a => a.filename === "subtask.md")).toBeDefined();
        });

        it("forwarded-from-peer completed leaves the parent's in-progress card untouched and still updates the side panel", () => {
            const parentCard = {
                kind: "artifact" as const,
                status: "in-progress" as const,
                name: "subtask.md",
                bytesTransferred: 50,
            };
            const existingMessages = [agentMessage("task-1", [parentCard])];
            const existingArtifacts: ArtifactInfo[] = [
                {
                    filename: "subtask.md",
                    mimeType: "text/markdown",
                    size: 50,
                    lastModified: "2024-01-01",
                    description: "in-flight",
                },
            ];
            const eventData = statusUpdateEvent(
                "task-1",
                [
                    {
                        kind: "data",
                        data: {
                            type: "artifact_creation_progress",
                            filename: "subtask.md",
                            status: "completed",
                            bytes_transferred: 1024,
                            mime_type: "text/markdown",
                            version: 1,
                        },
                    },
                ],
                { messageMetadata: { forwarded_from_peer: "ChildAgent" } }
            );
            const output = processChatEvent(makeInput({ eventData, messages: existingMessages, artifacts: existingArtifacts }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const cards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(cards).toHaveLength(1);
            expect(cards[0]).toMatchObject({ status: "in-progress", name: "subtask.md", bytesTransferred: 50 });
            expect(effectTypes(output)).toContain("refetch-artifacts");
            expect(output.artifacts!.find(a => a.filename === "subtask.md")?.size).toBe(1024);
        });

        it("cancelled removes the prior in-progress card and leaves no rolled-back text part when none was supplied", () => {
            const existing = [
                agentMessage("task-1", [
                    { kind: "text" as const, text: "intro" },
                    {
                        kind: "artifact" as const,
                        status: "in-progress" as const,
                        name: "report.md",
                        bytesTransferred: 200,
                    },
                ]),
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "report.md",
                        status: "cancelled",
                        bytes_transferred: 0,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "artifact")).toHaveLength(0);
            const textParts = taskMsg.parts.filter(p => p.kind === "text");
            expect(textParts).toHaveLength(1);
            expect(textParts[0]).toMatchObject({ kind: "text", text: "intro" });
            expect(output.artifacts!.find(a => a.filename === "report.md")).toBeUndefined();
        });

        it("cancelled with rolled_back_text removes the card and appends the rolled-back text to the agent message", () => {
            const existing = [
                agentMessage("task-1", [
                    { kind: "text" as const, text: "Working on it…" },
                    {
                        kind: "artifact" as const,
                        status: "in-progress" as const,
                        name: "report.md",
                        bytesTransferred: 200,
                    },
                ]),
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "report.md",
                        status: "cancelled",
                        bytes_transferred: 0,
                        rolled_back_text: "I was about to save report.md but you cancelled.",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            expect(taskMsg.parts.filter(p => p.kind === "artifact")).toHaveLength(0);
            const textParts = taskMsg.parts.filter(p => p.kind === "text");
            expect(textParts).toHaveLength(2);
            expect(textParts[0]).toMatchObject({ kind: "text", text: "Working on it…" });
            expect(textParts[1]).toMatchObject({ kind: "text", text: "I was about to save report.md but you cancelled." });
            expect(output.artifacts!.find(a => a.filename === "report.md")).toBeUndefined();
        });

        it("artifact_saved finalizes an in-progress card and refetches artifacts from REST when the completed event is missing", () => {
            const inProgressCard = {
                kind: "artifact" as const,
                status: "in-progress" as const,
                name: "report.md",
                bytesTransferred: 800,
            };
            const existing = [agentMessage("task-1", [inProgressCard])];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_saved",
                        filename: "report.md",
                        version: 0,
                        mime_type: "text/markdown",
                        size_bytes: 1446,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const cards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(cards).toHaveLength(1);
            expect(cards[0]).toMatchObject({
                kind: "artifact",
                status: "completed",
                name: "report.md",
                file: expect.objectContaining({ name: "report.md", mimeType: "text/markdown" }),
            });
            expect(effectTypes(output)).toContain("refetch-artifacts");
        });

        it("artifact_saved is a no-op when no in-progress card exists (tool-saved path)", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_saved",
                        filename: "tool_output.json",
                        version: 0,
                        mime_type: "application/json",
                        size_bytes: 256,
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages?.find(m => m.taskId === "task-1");
            const cards = taskMsg?.parts.filter(p => p.kind === "artifact") ?? [];
            expect(cards).toHaveLength(0);
            expect(effectTypes(output)).not.toContain("refetch-artifacts");
        });

        it("artifact_saved leaves the parent's in-progress card alone when forwarded from a peer sub-task", () => {
            const parentCard = {
                kind: "artifact" as const,
                status: "in-progress" as const,
                name: "shared.md",
                bytesTransferred: 50,
            };
            const existing = [agentMessage("task-1", [parentCard])];
            const eventData = statusUpdateEvent(
                "task-1",
                [
                    {
                        kind: "data",
                        data: {
                            type: "artifact_saved",
                            filename: "shared.md",
                            version: 1,
                            mime_type: "text/markdown",
                            size_bytes: 999,
                        },
                    },
                ],
                { messageMetadata: { forwarded_from_peer: "ChildAgent" } }
            );
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const taskMsg = output.messages!.find(m => m.taskId === "task-1")!;
            const cards = taskMsg.parts.filter(p => p.kind === "artifact");
            expect(cards).toHaveLength(1);
            expect(cards[0]).toMatchObject({ status: "in-progress", name: "shared.md", bytesTransferred: 50 });
            expect(effectTypes(output)).not.toContain("refetch-artifacts");
        });

        it("renders FilePart from gateway-resolved «artifact_return» as a chat card", () => {
            const eventData = JSON.stringify({
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: "task-1",
                    status: {
                        state: "completed",
                        message: {
                            role: "agent",
                            parts: [
                                { kind: "text", text: "Here is your report." },
                                { kind: "file", file: { name: "report.csv", mimeType: "text/csv", uri: "artifact://app/user/sess/report.csv?version=1" } },
                            ],
                        },
                    },
                    contextId: "ses-1",
                },
            });
            const output = processChatEvent(makeInput({ eventData }));

            const taskMsg = output.messages?.find(m => m.taskId === "task-1");
            expect(taskMsg).toBeDefined();
            const fileParts = taskMsg!.parts.filter(p => p.kind === "file");
            expect(fileParts).toHaveLength(1);
            expect(fileParts[0]).toMatchObject({ kind: "file", file: expect.objectContaining({ name: "report.csv" }) });
        });
    });

    describe("user_input_request events", () => {
        it("creates a HIL message with correct userInputRequest shape", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "user_input_request",
                        request_id: "req-42",
                        surface: "chat",
                        expires_at: "2026-04-15T12:00:00Z",
                        source: "ask_user_question",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, sessionId: "session-1", selectedAgentName: "agent-1" }));

            const hilMsg = output.messages!.find(m => m.userInputRequest);
            expect(hilMsg).toBeDefined();
            expect(hilMsg!.role).toBe("agent");
            expect(hilMsg!.isUser).toBe(false);
            expect(hilMsg!.isComplete).toBe(false);
            expect(hilMsg!.taskId).toBe("task-1");
            expect(hilMsg!.metadata).toEqual({ messageId: "hil-req-42" });
            expect(hilMsg!.userInputRequest).toMatchObject({
                requestId: "req-42",
                expiresAt: "2026-04-15T12:00:00Z",
                source: "ask_user_question",
                surface: "chat",
                taskId: "task-1",
                agentName: "agent-1",
            });
        });

        it("uses tool_approval source when source is tool_approval", () => {
            const eventData = statusUpdateEvent("task-2", [
                {
                    kind: "data",
                    data: {
                        type: "user_input_request",
                        request_id: "req-99",
                        surface: "chat",
                        source: "tool_approval",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const hilMsg = output.messages!.find(m => m.userInputRequest);
            expect(hilMsg).toBeDefined();
            expect(hilMsg!.userInputRequest!.source).toBe("tool_approval");
        });

        it("does not append a duplicate when the same request_id is processed twice", () => {
            const event = (taskId: string) =>
                statusUpdateEvent(taskId, [
                    {
                        kind: "data",
                        data: {
                            type: "user_input_request",
                            request_id: "req-dup",
                            surface: "chat",
                            source: "ask_user_question",
                        },
                    },
                ]);

            const first = processChatEvent(makeInput({ eventData: event("task-1") }));
            const second = processChatEvent(makeInput({ eventData: event("task-1"), messages: first.messages }));

            const hilMsgs = second.messages!.filter(m => m.userInputRequest?.requestId === "req-dup");
            expect(hilMsgs).toHaveLength(1);
        });

        it("supersedes a prior still-pending HIL prompt when a new question arrives (even cross-task)", () => {
            const q = (taskId: string, requestId: string) => statusUpdateEvent(taskId, [{ kind: "data", data: { type: "user_input_request", request_id: requestId, surface: "chat", source: "ask_user_question" } }]);
            const first = processChatEvent(makeInput({ eventData: q("task-1", "req-A") }));
            const second = processChatEvent(makeInput({ eventData: q("task-2", "req-B"), messages: first.messages }));

            const a = second.messages!.find(m => m.userInputRequest?.requestId === "req-A");
            const b = second.messages!.find(m => m.userInputRequest?.requestId === "req-B");
            expect(a!.userInputRequest!.cancelled).toBe(true);
            expect(b!.userInputRequest!.cancelled).toBeFalsy();
        });

        it("closes a still-pending HIL prompt when its task reaches a terminal state (Stop/cancel)", () => {
            const withHil = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [{ kind: "data", data: { type: "user_input_request", request_id: "req-C", surface: "chat", source: "ask_user_question" } }]) }));
            expect(withHil.messages!.find(m => m.userInputRequest?.requestId === "req-C")!.userInputRequest!.cancelled).toBeFalsy();

            const done = processChatEvent(makeInput({ eventData: taskCompleteEvent("task-1", "failed"), messages: withHil.messages }));
            expect(done.messages!.find(m => m.userInputRequest?.requestId === "req-C")!.userInputRequest!.cancelled).toBe(true);
        });

        it("terminal-close is scoped to the ending task — another task's pending HIL is untouched", () => {
            const t1 = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [{ kind: "data", data: { type: "user_input_request", request_id: "req-T1", surface: "chat", source: "ask_user_question" } }]) }));
            const t2 = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-2", [{ kind: "data", data: { type: "user_input_request", request_id: "req-T2", surface: "chat", source: "ask_user_question" } }]), messages: t1.messages }));
            const cleared = t2.messages!.map(m => (m.userInputRequest?.requestId === "req-T1" ? { ...m, userInputRequest: { ...m.userInputRequest!, cancelled: false } } : m));

            const done = processChatEvent(makeInput({ eventData: taskCompleteEvent("task-2", "failed"), messages: cleared }));
            expect(done.messages!.find(m => m.userInputRequest?.requestId === "req-T2")!.userInputRequest!.cancelled).toBe(true);
            expect(done.messages!.find(m => m.userInputRequest?.requestId === "req-T1")!.userInputRequest!.cancelled).toBeFalsy();
        });

        it("supersede applies to a pending tool_approval prompt too (source-agnostic)", () => {
            const first = processChatEvent(makeInput({ eventData: statusUpdateEvent("task-1", [{ kind: "data", data: { type: "user_input_request", request_id: "req-appr", surface: "chat", source: "tool_approval" } }]) }));
            const second = processChatEvent(
                makeInput({ eventData: statusUpdateEvent("task-1", [{ kind: "data", data: { type: "user_input_request", request_id: "req-next", surface: "chat", source: "ask_user_question" } }]), messages: first.messages })
            );
            expect(second.messages!.find(m => m.userInputRequest?.requestId === "req-appr")!.userInputRequest!.cancelled).toBe(true);
        });

        it("does not append a duplicate when a deserialized bubble already carries the requestId", () => {
            const existing: MessageFE[] = [
                agentMessage("task-1", [{ kind: "text", text: "" }], {
                    userInputRequest: {
                        requestId: "req-7",
                        expiresAt: "",
                        source: "ask_user_question",
                        surface: { surfaceId: "s", catalogId: "c", components: [], dataModel: {} },
                        taskId: "task-1",
                        agentName: "agent-1",
                    },
                    metadata: { messageId: "bubble-abc" },
                }),
            ];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "user_input_request",
                        request_id: "req-7",
                        surface: "chat",
                        source: "ask_user_question",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData, messages: existing }));

            const hilMsgs = output.messages!.filter(m => m.userInputRequest?.requestId === "req-7");
            expect(hilMsgs).toHaveLength(1);
            expect(hilMsgs[0].metadata?.messageId).toBe("bubble-abc");
        });

        it("does not create a message when request_id is missing", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "user_input_request",
                        surface: "chat",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const hilMsg = output.messages?.find(m => m.userInputRequest);
            expect(hilMsg).toBeUndefined();
        });

        it("does not create a message when surface is missing", () => {
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "user_input_request",
                        request_id: "req-1",
                    },
                },
            ]);
            const output = processChatEvent(makeInput({ eventData }));

            const hilMsg = output.messages?.find(m => m.userInputRequest);
            expect(hilMsg).toBeUndefined();
        });
    });

    describe("input immutability", () => {
        it("does not mutate the input messages array", () => {
            const original: MessageFE[] = [agentMessage("task-1", [{ kind: "text", text: "old" }])];
            const frozen = [...original];
            const eventData = statusUpdateEvent("task-1", [{ kind: "text", text: "new" }]);
            processChatEvent(makeInput({ eventData, messages: original }));

            expect(original).toEqual(frozen);
        });

        it("does not mutate the input ragData array", () => {
            const original: RAGSearchResult[] = [];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "rag_info_update",
                        title: "T",
                        query: "q",
                        search_type: "deep_research",
                        sources: [],
                        is_complete: false,
                        timestamp: "t",
                    },
                },
            ]);
            processChatEvent(makeInput({ eventData, ragData: original }));

            expect(original).toHaveLength(0);
        });

        it("does not mutate the input artifacts array", () => {
            const original: ArtifactInfo[] = [];
            const eventData = statusUpdateEvent("task-1", [
                {
                    kind: "data",
                    data: {
                        type: "artifact_creation_progress",
                        filename: "f.txt",
                        status: "in-progress",
                        bytes_transferred: 10,
                        description: "test",
                    },
                },
            ]);
            processChatEvent(makeInput({ eventData, artifacts: original }));

            expect(original).toHaveLength(0);
        });
    });
});

describe("finalize carries the task outcome", () => {
    function finalizeEffect(output: ReturnType<typeof processChatEvent>) {
        return output.effects?.find(e => e.type === "finalize") as { type: "finalize"; payload: { failed: boolean; failureText: string; replayed: boolean } } | undefined;
    }

    it("marks a failed task as failed", () => {
        const eventData = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: "429 rate limit exceeded" }]);
        const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [{ kind: "text", text: "" }])] }));

        expect(finalizeEffect(output)?.payload.failed).toBe(true);
    });

    it("marks a completed task as not failed", () => {
        const eventData = taskCompleteEvent("task-1");
        const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [{ kind: "text", text: "done" }])] }));

        expect(finalizeEffect(output)?.payload.failed).toBe(false);
    });

    it("carries the agent's failure text for bucketing", () => {
        const eventData = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: "429 rate limit exceeded" }]);
        const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [{ kind: "text", text: "" }])] }));

        expect(finalizeEffect(output)?.payload.failureText).toContain("rate limit");
    });

    it("marks a failure that arrives live as not replayed", () => {
        const eventData = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: "The free model's allowance is used up. Add a model to replace it." }]);
        const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [{ kind: "text", text: "" }])] }));

        expect(finalizeEffect(output)?.payload.replayed).toBe(false);
    });

    it("marks a failure replayed while a chat loads as replayed, so it is not treated as a new refusal", () => {
        const eventData = taskCompleteEvent("task-1", "failed", [{ kind: "text", text: "The free model's allowance is used up. Add a model to replace it." }]);
        const output = processChatEvent(makeInput({ eventData, messages: [agentMessage("task-1", [{ kind: "text", text: "" }])], flags: { showThinkingContent: false, ragEnabled: true, isReplaying: true } }));

        expect(finalizeEffect(output)?.payload.replayed).toBe(true);
    });
});
