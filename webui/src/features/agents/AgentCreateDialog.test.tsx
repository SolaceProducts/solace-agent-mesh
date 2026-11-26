// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { AgentCreateDialog } from "./AgentCreateDialog";

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

function validationErrors(): Record<string, unknown>[] {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === "form:validation_error").map(call => call[1] as Record<string, unknown>);
}

describe("AgentCreateDialog reports where users get stuck", () => {
    test("submitting an empty form reports the field and the rule, not what was typed", async () => {
        const user = userEvent.setup();
        const onCreate = vi.fn();
        render(<AgentCreateDialog isOpen onClose={() => {}} onCreate={onCreate} />);

        await user.type(screen.getByTestId("agentDescriptionInput"), "a secret internal project");
        await user.click(screen.getByRole("button", { name: "Continue" }));

        expect(onCreate).not.toHaveBeenCalled();
        expect(validationErrors()).toContainEqual({ form: "agent", field: "name", errorType: "too_small", path: "/" });

        const serialized = JSON.stringify(validationErrors());
        expect(serialized).not.toContain("secret internal project");
        expect(serialized).not.toContain("required");
    });

    test("a valid submission reports nothing", async () => {
        const user = userEvent.setup();
        const onCreate = vi.fn();
        render(<AgentCreateDialog isOpen onClose={() => {}} onCreate={onCreate} />);

        await user.type(screen.getByTestId("agentNameInput"), "SupportBot");
        await user.type(screen.getByTestId("agentDescriptionInput"), "Answers support questions for the team.");
        await user.click(screen.getByRole("button", { name: "Continue" }));

        expect(onCreate).toHaveBeenCalled();
        expect(validationErrors()).toHaveLength(0);
    });
});
