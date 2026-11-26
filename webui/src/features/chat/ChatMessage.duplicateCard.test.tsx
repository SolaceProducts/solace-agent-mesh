// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { ChatMessage } from "./ChatMessage";

function agentMessageWith(parts: MessageFE["parts"]): MessageFE {
    return {
        role: "agent",
        isUser: false,
        taskId: "11111111-1111-1111-1111-111111111111",
        isComplete: true,
        parts,
        metadata: { messageId: "m1" },
    } as MessageFE;
}

const inlineCard = { kind: "artifact", name: "memo.md", status: "completed", file: { name: "memo.md", uri: "artifact://s/memo.md" } };
const returnedFile = { kind: "file", file: { name: "memo.md", mimeType: "text/markdown", uri: "artifact://s/memo.md" } };

describe("ChatMessage file shown once", () => {
    it("draws one card when a file saved inline is also returned", () => {
        renderWithProviders(<ChatMessage message={agentMessageWith([inlineCard] as MessageFE["parts"])} isLastWithTaskId />);
        const onlyCard = screen.queryAllByText("memo.md").length;

        renderWithProviders(<ChatMessage message={agentMessageWith([inlineCard, { kind: "text", text: "I saved the memo." }, returnedFile] as MessageFE["parts"])} isLastWithTaskId />);

        expect(onlyCard).toBeGreaterThan(0);
        expect(screen.queryAllByText("memo.md").length).toBe(onlyCard * 2);
    });

    it("still draws a returned file that has no card", () => {
        renderWithProviders(<ChatMessage message={agentMessageWith([{ kind: "text", text: "Here it is." }, returnedFile] as MessageFE["parts"])} isLastWithTaskId />);

        expect(screen.queryAllByText("memo.md").length).toBeGreaterThan(0);
    });
});
