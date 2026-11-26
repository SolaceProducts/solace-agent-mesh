// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { ArtifactPanel } from "./ArtifactPanel";

function agentMessageWith(parts: MessageFE["parts"]): MessageFE {
    return { role: "agent", isUser: false, taskId: "task-1", isComplete: true, parts, metadata: { messageId: "m1" } } as MessageFE;
}

describe("ArtifactPanel working-file reveal after the listing drops __working", () => {
    it("still reports a hidden working file known only from the chat", () => {
        const messages = [agentMessageWith([{ kind: "artifact", name: "deck-spec.yaml", status: "completed", tags: ["__working"] }] as MessageFE["parts"])];

        renderWithProviders(<ArtifactPanel />, { chatContextValues: { messages } });

        expect(screen.getByText("1 working file hidden")).toBeInTheDocument();
    });

    it("counts each hidden filename once across messages", () => {
        const spec = { kind: "artifact", name: "deck-spec.yaml", status: "completed", tags: ["__working"] };
        const messages = [agentMessageWith([spec] as MessageFE["parts"]), agentMessageWith([spec, { ...spec, name: "outline.md" }] as MessageFE["parts"])];

        renderWithProviders(<ArtifactPanel />, { chatContextValues: { messages } });

        expect(screen.getByText("2 working files hidden")).toBeInTheDocument();
    });

    it("counts a working file the model returned with «artifact_return»", () => {
        const messages = [
            agentMessageWith([
                { kind: "file", file: { name: "solace-overview.pptx", uri: "artifact://s/solace-overview.pptx" } },
                { kind: "file", file: { name: "deck-spec.yaml", uri: "artifact://s/deck-spec.yaml" }, metadata: { tags: ["__working"] } },
            ] as MessageFE["parts"]),
        ];

        renderWithProviders(<ArtifactPanel />, { chatContextValues: { messages } });

        expect(screen.getByText("1 working file hidden")).toBeInTheDocument();
    });

    it("reports nothing hidden when the chat's cards are untagged", () => {
        const messages = [agentMessageWith([{ kind: "artifact", name: "deck.pptx", status: "completed" }] as MessageFE["parts"])];

        renderWithProviders(<ArtifactPanel />, { chatContextValues: { messages } });

        expect(screen.queryByText(/working files? hidden/)).not.toBeInTheDocument();
    });
});
