// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { AuthenticationMessage } from "./AuthenticationMessage";

function authMessage(targetAgent?: string, overrides: Partial<NonNullable<MessageFE["authenticationLink"]>> = {}): MessageFE {
    return {
        isUser: false,
        authenticationLink: {
            url: "https://example.com/oauth",
            text: "Authenticate",
            targetAgent,
            ...overrides,
        },
        metadata: { messageId: "m1" },
    } as MessageFE;
}

describe("AuthenticationMessage", () => {
    it("renders the agent name bold and without surrounding quotes", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator")} />, {
            chatContextValues: { isResponding: true, agentNameDisplayNameMap: { orchestrator: "Orchestrator" } },
        });

        const nameEl = screen.getByText("Orchestrator");
        expect(nameEl.tagName).toBe("SPAN");
        expect(nameEl.className).toContain("font-semibold");

        expect(screen.queryByText(/"orchestrator"/i)).not.toBeInTheDocument();
        expect(screen.getByText(/needs you to sign in/)).toBeInTheDocument();
    });

    it("names the service when a serviceName is present", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { serviceName: "Atlassian" })} />, {
            chatContextValues: { isResponding: true, agentNameDisplayNameMap: { orchestrator: "Orchestrator" } },
        });

        const serviceEl = screen.getByText("Atlassian");
        expect(serviceEl.tagName).toBe("SPAN");
        expect(serviceEl.className).toContain("font-semibold");
        expect(screen.getByText(/sign in/)).toBeInTheDocument();
    });

    it("falls back to a generic prompt when no serviceName is present", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator")} />, {
            chatContextValues: { isResponding: true },
        });

        expect(screen.getByText(/before it can continue\./)).toBeInTheDocument();
    });

    it("keeps an enabled Cancel button after authentication is initiated so the user can back out", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { authenticationAttempted: true })} />, {
            chatContextValues: { isResponding: true },
        });

        const cancel = screen.getByRole("button", { name: "Cancel" });
        expect(cancel).toBeEnabled();
        expect(screen.queryByRole("button", { name: "Deny" })).not.toBeInTheDocument();
    });

    it("cancels the pending auth (unsticks the conversation) when Cancel is clicked", () => {
        const handleCancel = vi.fn();
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { authenticationAttempted: true })} />, {
            chatContextValues: { isResponding: true, handleCancel },
        });

        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

        expect(handleCancel).toHaveBeenCalledTimes(1);
    });

    it("falls back to the raw target agent when no display-name mapping exists", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("figma-agent")} />, {
            chatContextValues: { isResponding: true },
        });

        const nameEl = screen.getByText("figma-agent");
        expect(nameEl.tagName).toBe("SPAN");
        expect(nameEl.className).toContain("font-semibold");
    });

    it("shows an in-progress 'Authenticating' button once auth has been initiated", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { authenticationAttempted: true })} />, {
            chatContextValues: { isResponding: true },
        });

        const button = screen.getByRole("button", { name: "Authenticating" });
        expect(button).toHaveAttribute("aria-busy", "true");
        expect(button).toBeDisabled();
        expect(screen.queryByRole("button", { name: "Authenticate" })).not.toBeInTheDocument();
    });

    it("resolves to a persisted 'Authenticated' state with no buttons on success", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { authenticationAttempted: true, outcome: "authenticated", serviceName: "Atlassian" })} />, {
            chatContextValues: { isResponding: true, agentNameDisplayNameMap: { orchestrator: "Orchestrator" } },
        });

        expect(screen.getByText("Signed In")).toBeInTheDocument();
        expect(screen.getByText("Authenticated to Atlassian")).toBeInTheDocument();
        expect(screen.getByText(/is now signed in/)).toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("resolves to a 'failed' state when the auth outcome is failure", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { authenticationAttempted: true, outcome: "failed", serviceName: "Atlassian" })} />, {
            chatContextValues: { isResponding: true },
        });

        expect(screen.getByText("Authentication to Atlassian failed")).toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("resolves to a rejected state when the user denies the prompt", () => {
        renderWithProviders(<AuthenticationMessage message={authMessage("orchestrator", { rejected: true })} />, {
            chatContextValues: { isResponding: true },
        });

        expect(screen.getByText("Authentication request was rejected")).toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});
