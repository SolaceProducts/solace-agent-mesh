// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import { AgentNotDeployedDialog, type AgentNotDeployedDialogFlow } from "./AgentNotDeployedDialog";

expect.extend(matchers);

interface RenderOptions {
    agentName?: string;
    agentId?: string;
    flow?: AgentNotDeployedDialogFlow;
    targetType?: "agent" | "workflow";
}

function LocationProbe() {
    const location = useLocation();
    return <div data-testid="location">{`${location.pathname}${location.search}`}</div>;
}

function renderDialog(options: RenderOptions = {}) {
    const onOpenChange = vi.fn();
    const { agentName = "Customer Support Bot", flow = "create" } = options;
    const agentId = "agentId" in options ? options.agentId : "agent-uuid-123";

    const result = render(
        <MemoryRouter>
            <AgentNotDeployedDialog open={true} onOpenChange={onOpenChange} agentName={agentName} agentId={agentId} flow={flow} targetType={options.targetType} />
            <LocationProbe />
        </MemoryRouter>
    );

    return { onOpenChange, ...result };
}

describe("AgentNotDeployedDialog for a workflow", () => {
    test("create flow names the workflow and says the experiment was saved", () => {
        renderDialog({ agentName: "Order Flow", agentId: undefined, flow: "create", targetType: "workflow" });

        expect(screen.getByText(/has been saved, but it cannot be run because the workflow/)).toBeInTheDocument();
        expect(screen.getByText("Order Flow")).toBeInTheDocument();
        expect(screen.getByText("Deploy the workflow, then run the experiment from the Experiment Lab.")).toBeInTheDocument();
    });

    test("Go to Workflows closes the dialog and opens the workflows page", async () => {
        const { onOpenChange } = renderDialog({ agentName: "Order Flow", agentId: undefined, flow: "run", targetType: "workflow" });

        await userEvent.click(screen.getByRole("button", { name: "Go to Workflows" }));

        expect(onOpenChange).toHaveBeenCalledWith(false);
        await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/workflows"));
        expect(screen.queryByRole("button", { name: "Go to Agent" })).not.toBeInTheDocument();
    });
});

describe("AgentNotDeployedDialog", () => {
    describe("error framing", () => {
        test("create flow: tells the user the experiment was saved but cannot run", () => {
            renderDialog({ agentName: "My Test Agent", flow: "create" });

            expect(screen.getByText("Unable to Run Experiment")).toBeInTheDocument();
            expect(screen.getByText(/has been saved/i)).toBeInTheDocument();
            expect(screen.getByText(/cannot be run/i)).toBeInTheDocument();
        });

        test("run flow: tells the user the experiment cannot run (does NOT claim it was just saved)", () => {
            renderDialog({ agentName: "My Test Agent", flow: "run" });

            expect(screen.queryByText(/has been saved/i)).not.toBeInTheDocument();
            expect(screen.getByText(/cannot be run/i)).toBeInTheDocument();
        });
    });

    describe("actions", () => {
        test("Close calls onOpenChange(false)", async () => {
            const { onOpenChange } = renderDialog();

            await userEvent.click(screen.getByRole("button", { name: /^Close$/i }));

            expect(onOpenChange).toHaveBeenCalledWith(false);
        });

        test("Go to Agent navigates to Agents' Undeployed tab pinned to the agent", async () => {
            renderDialog({ agentId: "agent-uuid-123" });

            await userEvent.click(screen.getByRole("button", { name: /Go to Agent/i }));

            await waitFor(() => {
                expect(screen.getByTestId("location").textContent).toBe("/agents?id=agent-uuid-123&tab=undeployed");
            });
        });

        test("Go to Agent falls back to plain Undeployed tab when no agentId is provided", async () => {
            renderDialog({ agentId: undefined });

            await userEvent.click(screen.getByRole("button", { name: /Go to Agent/i }));

            await waitFor(() => {
                expect(screen.getByTestId("location").textContent).toBe("/agents?tab=undeployed");
            });
        });

        test("Go to Agent closes the dialog before navigating", async () => {
            const { onOpenChange } = renderDialog();

            await userEvent.click(screen.getByRole("button", { name: /Go to Agent/i }));

            await waitFor(() => {
                expect(onOpenChange).toHaveBeenCalledWith(false);
            });
        });
    });
});
