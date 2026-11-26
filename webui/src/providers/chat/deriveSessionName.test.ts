// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { MessageFE, Part } from "@/types";

import { deriveSessionNameFromFirstMessage } from "./deriveSessionName";

const userMsg = (parts: Part[]): MessageFE =>
    ({
        role: "user",
        parts,
        isUser: true,
        metadata: { messageId: "m1", sessionId: "", lastProcessedEventSequence: 0 },
    }) as MessageFE;

const text = (s: string): Part => ({ kind: "text", text: s });

const file = (name: string): File => new File(["x"], name, { type: "text/plain" });

describe("deriveSessionNameFromFirstMessage", () => {
    it("returns 'New Chat' when auto-title generation is enabled", () => {
        expect(
            deriveSessionNameFromFirstMessage({
                userMessage: userMsg([text("hello")]),
                files: [],
                autoTitleEnabled: true,
            })
        ).toBe("New Chat");
    });

    it("uses the joined text parts as the name when auto-title is disabled", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("hello"), text("world")]),
            files: [],
            autoTitleEnabled: false,
        });
        expect(result).toBe("hello world");
    });

    it("converts internal mention format @[Name](id) → @Name", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("hi @[Alice](u:1) and @[Bob](u:2)")]),
            files: [],
            autoTitleEnabled: false,
        });
        expect(result).toBe("hi @Alice and @Bob");
    });

    it("strips backend <user_id:...> markers (complete form)", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("hello <user_id:abc-123> world")]),
            files: [],
            autoTitleEnabled: false,
        });
        expect(result).toBe("hello  world");
    });

    it("strips backend <user_id:...> markers (truncated form)", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("hello <user_id:abc-123 cut off")]),
            files: [],
            autoTitleEnabled: false,
        });
        expect(result).toBe("hello");
    });

    it("truncates names longer than 100 characters with an ellipsis", () => {
        const long = "x".repeat(150);
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text(long)]),
            files: [],
            autoTitleEnabled: false,
        });
        expect(result.endsWith("...")).toBe(true);
        expect(result.length).toBe(103);
    });

    it("uses the single file name as fallback when there's no text", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("")]),
            files: [file("report.pdf")],
            autoTitleEnabled: false,
        });
        expect(result).toBe("report.pdf");
    });

    it("uses '<first> +N more' fallback for multiple files", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("")]),
            files: [file("a.txt"), file("b.txt"), file("c.txt")],
            autoTitleEnabled: false,
        });
        expect(result).toBe("a.txt +2 more");
    });

    it("returns 'New Chat' when there's no text and no files", () => {
        const result = deriveSessionNameFromFirstMessage({
            userMessage: userMsg([text("   ")]),
            files: [],
            autoTitleEnabled: false,
        });
        expect(result).toBe("New Chat");
    });
});
