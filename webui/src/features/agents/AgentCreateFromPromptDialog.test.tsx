// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import type { Agent } from "@/types";

import { AgentCreateFromPromptDialog } from "./AgentCreateFromPromptDialog";

const VALID_DESCRIPTION = "This is a valid description with enough characters to pass validation.";

function renderDialog(overrides: Partial<React.ComponentProps<typeof AgentCreateFromPromptDialog>> = {}) {
    const onCreate = vi.fn();
    const onClose = vi.fn();
    render(<AgentCreateFromPromptDialog isOpen={true} onCreate={onCreate} onClose={onClose} {...overrides} />);
    return { onCreate, onClose };
}

async function fillAndSubmit(description: string) {
    const textarea = screen.getByTestId("agent-prompt");
    fireEvent.change(textarea, { target: { value: description } });
    const submitBtn = screen.getByTestId("generateAgent");
    fireEvent.click(submitBtn);
}

describe("AgentCreateFromPromptDialog — error handling (getErrorMessage)", () => {
    let apiPostSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiPostSpy = vi.spyOn(api.platform, "post");
    });

    afterEach(() => {
        apiPostSpy.mockRestore();
    });

    it("shows Error.message when API rejects with an Error instance", async () => {
        apiPostSpy.mockRejectedValue(new Error("Service temporarily unavailable"));

        renderDialog();
        await fillAndSubmit(VALID_DESCRIPTION);

        await waitFor(() => {
            screen.getByText("Service temporarily unavailable");
        });
    });

    it("shows fallback message when API rejects with a non-Error unknown object", async () => {
        apiPostSpy.mockRejectedValue({ statusCode: 500 });

        renderDialog();
        await fillAndSubmit(VALID_DESCRIPTION);

        await waitFor(() => {
            screen.getByText("Could not generate agent. Try a new description.");
        });
    });

    it("calls onCreate and onClose on successful submission", async () => {
        const fakeAgent: Agent = { id: "agent-1", name: "Test Agent" } as Agent;
        apiPostSpy.mockResolvedValue(fakeAgent);

        const { onCreate, onClose } = renderDialog();
        await fillAndSubmit(VALID_DESCRIPTION);

        await waitFor(() => {
            expect(onCreate).toHaveBeenCalledWith(fakeAgent, VALID_DESCRIPTION);
            expect(onClose).toHaveBeenCalled();
        });
    });

    it("does not show an error message on initial render", () => {
        renderDialog();
        expect(screen.queryByText("Could not generate agent. Try a new description.")).toBeNull();
    });
});
