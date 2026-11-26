// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { ArtifactPart, MessageFE } from "@/types";

import { serializeChatMessage } from "./serializeChatMessage";

function makeMessage(overrides: Partial<MessageFE> = {}): MessageFE {
    return {
        role: "agent",
        isUser: false,
        parts: [],
        ...overrides,
    };
}

describe("serializeChatMessage", () => {
    it("serializes a basic user message", () => {
        const result = serializeChatMessage(
            makeMessage({
                isUser: true,
                parts: [{ kind: "text", text: "hello" }],
                metadata: { messageId: "msg-1" },
            })
        );

        expect(result.type).toBe("user");
        expect(result.text).toBe("hello");
        expect(result.id).toBe("msg-1");
    });

    it("serializes a basic agent message", () => {
        const result = serializeChatMessage(
            makeMessage({
                isUser: false,
                parts: [{ kind: "text", text: "response" }],
            })
        );

        expect(result.type).toBe("agent");
        expect(result.text).toBe("response");
    });

    it("concatenates multiple text parts", () => {
        const result = serializeChatMessage(
            makeMessage({
                parts: [
                    { kind: "text", text: "first " },
                    { kind: "text", text: "second" },
                ],
            })
        );

        expect(result.text).toBe("first second");
    });

    it("embeds artifact markers in text", () => {
        const result = serializeChatMessage(
            makeMessage({
                parts: [{ kind: "text", text: "Here is the file: " }, { kind: "artifact", status: "completed", name: "report.pdf" } as ArtifactPart],
            })
        );

        expect(result.text).toBe("Here is the file: «artifact_return:report.pdf»");
    });

    it("generates an id when messageId is missing", () => {
        const result = serializeChatMessage(makeMessage());

        expect(result.id).toMatch(/^msg-/);
    });

    it("preserves error flag", () => {
        const result = serializeChatMessage(makeMessage({ isError: true }));

        expect(result.isError).toBe(true);
    });

    it("maps uploadedFiles to name and type", () => {
        const file = new File(["content"], "doc.txt", { type: "text/plain" });
        const result = serializeChatMessage(makeMessage({ uploadedFiles: [file] }));

        expect(result.uploadedFiles).toEqual([{ name: "doc.txt", type: "text/plain" }]);
    });

    it("includes progressUpdates when present", () => {
        const updates = [{ type: "status" as const, text: "Working", timestamp: 123 }];
        const result = serializeChatMessage(makeMessage({ progressUpdates: updates }));

        expect(result.progressUpdates).toEqual(updates);
    });

    it("omits progressUpdates when empty", () => {
        const result = serializeChatMessage(makeMessage({ progressUpdates: [] }));

        expect(result).not.toHaveProperty("progressUpdates");
    });

    it("includes thinkingContent when present", () => {
        const result = serializeChatMessage(
            makeMessage({
                thinkingContent: "reasoning...",
                isThinkingComplete: true,
            })
        );

        expect(result.thinkingContent).toBe("reasoning...");
        expect(result.isThinkingComplete).toBe(true);
    });

    it("defaults isThinkingComplete to true when thinkingContent exists", () => {
        const result = serializeChatMessage(
            makeMessage({
                thinkingContent: "thinking",
            })
        );

        expect(result.isThinkingComplete).toBe(true);
    });

    it("omits thinkingContent when empty", () => {
        const result = serializeChatMessage(makeMessage({ thinkingContent: "" }));

        expect(result).not.toHaveProperty("thinkingContent");
    });

    it("preserves displayHtml and contextQuote fields", () => {
        const result = serializeChatMessage(
            makeMessage({
                displayHtml: "<b>hello</b>",
                contextQuote: "quoted text",
                contextQuoteSourceId: "task-1",
            })
        );

        expect(result.displayHtml).toBe("<b>hello</b>");
        expect(result.contextQuote).toBe("quoted text");
        expect(result.contextQuoteSourceId).toBe("task-1");
    });

    it("passes through parts array as-is", () => {
        const parts: MessageFE["parts"] = [{ kind: "text", text: "hi" }, { kind: "artifact", status: "completed", name: "f.txt" } as ArtifactPart];
        const result = serializeChatMessage(makeMessage({ parts }));

        expect(result.parts).toBe(parts);
    });

    it("persists render-intent metadata (hidden) so it survives reload", () => {
        const result = serializeChatMessage(makeMessage({ isUser: true, metadata: { messageId: "m1", hidden: true } }));
        expect(result.metadata).toEqual({ hidden: true });
    });

    it("persists chip and applyChangesCard metadata", () => {
        const chip = { emoji: "✅", label: "Plan approved" };
        const applyChangesCard = { componentName: "X", componentKind: "agent", items: [] };
        const result = serializeChatMessage(makeMessage({ isUser: true, metadata: { messageId: "m1", chip, applyChangesCard } }));
        expect(result.metadata).toEqual({ chip, applyChangesCard });
    });

    it("omits metadata when no render-intent flags are set (volatile fields not persisted)", () => {
        const result = serializeChatMessage(makeMessage({ isUser: true, metadata: { messageId: "m1", sessionId: "s1", lastProcessedEventSequence: 7 } }));
        expect(result).not.toHaveProperty("metadata");
    });

    it("persists a resolved authenticationLink so the terminal state survives reload", () => {
        const authenticationLink = { url: "https://auth.example.com", text: "Authenticate", targetAgent: "orchestrator", outcome: "authenticated" as const };
        const result = serializeChatMessage(makeMessage({ authenticationLink }));
        expect(result.authenticationLink).toEqual(authenticationLink);
    });

    it("persists a rejected authenticationLink", () => {
        const authenticationLink = { url: "https://auth.example.com", text: "Authenticate", rejected: true };
        const result = serializeChatMessage(makeMessage({ authenticationLink }));
        expect(result.authenticationLink).toEqual(authenticationLink);
    });

    it("does not persist a still-pending authenticationLink (volatile live-only prompt)", () => {
        const result = serializeChatMessage(makeMessage({ authenticationLink: { url: "https://auth.example.com", text: "Authenticate", authenticationAttempted: true } }));
        expect(result).not.toHaveProperty("authenticationLink");
    });
});
