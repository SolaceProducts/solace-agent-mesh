// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { MessageFE, Part, RAGSearchResult } from "@/types";

import { buildSaveTaskPayload } from "./buildSaveTaskPayload";

const text = (s: string): Part => ({ kind: "text", text: s });

const userMsg = (parts: Part[], overrides: Partial<MessageFE> = {}): MessageFE =>
    ({
        role: "user",
        parts,
        isUser: true,
        metadata: { messageId: "m1", sessionId: "", lastProcessedEventSequence: 0 },
        ...overrides,
    }) as MessageFE;

const agentMsg = (parts: Part[], overrides: Partial<MessageFE> = {}): MessageFE =>
    ({
        role: "agent",
        parts,
        isUser: false,
        metadata: { sessionId: "", lastProcessedEventSequence: 0 },
        ...overrides,
    }) as MessageFE;

describe("buildSaveTaskPayload", () => {
    it("returns null when taskMessages is empty", () => {
        expect(buildSaveTaskPayload({ taskId: "t", taskMessages: [] })).toBeNull();
    });

    it("derives userMessage by joining text parts of the user-role message", () => {
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("hello "), text("world")]), agentMsg([text("reply")])],
        });
        expect(result?.userMessage).toBe("hello world");
    });

    it("excludes non-text parts from the user message text", () => {
        const fileLike = { kind: "file", file: { name: "x.pdf" } } as unknown as Part;
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("hi"), fileLike])],
        });
        expect(result?.userMessage).toBe("hi");
    });

    it("marks taskStatus as 'error' when any message has isError", () => {
        const errMsg = agentMsg([text("bad")], { isError: true } as Partial<MessageFE>);
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("q")]), errMsg],
        });
        expect(result?.taskMetadata.status).toBe("error");
    });

    it("marks taskStatus as 'completed' when no message has isError", () => {
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("q")]), agentMsg([text("ok")])],
        });
        expect(result?.taskMetadata.status).toBe("completed");
    });

    it("omits ragData from metadata when the array is empty", () => {
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("q")])],
            ragData: [],
        });
        expect(result?.taskMetadata.ragData).toBeUndefined();
    });

    it("includes ragData in metadata when non-empty", () => {
        const ragData: RAGSearchResult[] = [{ content: "snippet", source: "doc.txt", score: 0.9 } as unknown as RAGSearchResult];
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("q")])],
            ragData,
        });
        expect(result?.taskMetadata.ragData).toEqual(ragData);
    });

    it("passes through agentName onto taskMetadata", () => {
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("q")])],
            agentName: "Solver",
        });
        expect(result?.taskMetadata.agentName).toBe("Solver");
    });

    it("serializes each taskMessage into the messageBubbles array (one per input)", () => {
        const result = buildSaveTaskPayload({
            taskId: "t1",
            taskMessages: [userMsg([text("a")]), agentMsg([text("b")])],
        });
        expect(result?.messageBubbles).toHaveLength(2);
    });
});
