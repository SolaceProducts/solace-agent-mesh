// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { ChatMessage } from "./ChatMessage";

function userChipMessage(chip: Record<string, unknown>): MessageFE {
    return {
        role: "user",
        isUser: true,
        isComplete: true,
        parts: [{ kind: "text", text: "Load the existing deployed workflow ..." }],
        metadata: { messageId: "m1", chip },
    } as unknown as MessageFE;
}

describe("ChatMessage — workflow chip", () => {
    it('renders a kind:"workflow" chip as an attachment tile (in a <code> label), not the plain pill, and only once', () => {
        renderWithProviders(<ChatMessage message={userChipMessage({ label: "My Workflow", kind: "workflow" })} />);

        const labels = screen.getAllByText("My Workflow");
        expect(labels).toHaveLength(1);
        expect(labels[0].closest("code")).not.toBeNull();
        expect(screen.queryByText(/Load the existing deployed workflow/)).toBeNull();
    });

    it("still renders a chip WITHOUT a kind as the plain emoji+label pill (no <code> tile)", () => {
        renderWithProviders(<ChatMessage message={userChipMessage({ emoji: "✅", label: "Plan approved" })} />);

        const label = screen.getByText("Plan approved");
        expect(label.closest("code")).toBeNull();
    });
});
