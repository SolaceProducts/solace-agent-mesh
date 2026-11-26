// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { A2UISurface, MessageFE } from "@/types";

import { UserInputMessage } from "./UserInputMessage";

vi.mock("@/providers/contexts", async importOriginal => ({
    ...(await importOriginal<typeof import("@/providers/contexts")>()),
    useChatContext: () => ({ setMessages: vi.fn(), displayError: vi.fn() }),
}));

beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
    vi.mocked(Element.prototype.scrollIntoView).mockClear();
});

const longHtml = `<h1>All About Cats</h1>${"<p>cats are great</p>".repeat(40)}`;

function respondedToolApproval(entries: { question: string; answer: string; contentType?: string }[]): MessageFE {
    return {
        role: "agent",
        isUser: false,
        parts: [],
        userInputRequest: {
            requestId: "r1",
            expiresAt: "2026-01-01T00:00:00.000Z",
            source: "tool_approval",
            surface: { surfaceId: "s", catalogId: "", components: [], dataModel: {} },
            taskId: "t1",
            agentName: "a",
            responded: true,
            responseText: JSON.stringify(entries),
        },
    } as MessageFE;
}

function approvalSurface(): A2UISurface {
    return {
        surfaceId: "test",
        catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
        components: [
            { id: "approve-label", component: "Text", text: { literalString: "Approve" } },
            {
                id: "approve-btn",
                component: "Button",
                variant: "primary",
                child: "approve-label",
                action: { event: { name: "submit", context: {} } },
            },
            { id: "main-col", component: "Column", children: { explicitList: ["approve-btn"] } },
            { id: "root", component: "Card", child: "main-col" },
        ],
        dataModel: {},
    };
}

function approvalMessage(overrides: Partial<NonNullable<MessageFE["userInputRequest"]>> = {}): MessageFE {
    return {
        isUser: false,
        metadata: { messageId: "m1" },
        userInputRequest: {
            requestId: "r1",
            expiresAt: "2099-01-01T00:00:00.000+00:00",
            source: "tool_approval",
            surface: approvalSurface(),
            taskId: "t1",
            agentName: "orchestrator",
            ...overrides,
        },
    } as MessageFE;
}

function renderMessage(message: MessageFE) {
    return render(
        <TooltipProvider>
            <UserInputMessage message={message} />
        </TooltipProvider>
    );
}

describe("UserInputMessage — denied/approved tool-call summary", () => {
    it("renders a short answer inline (no collapse control)", () => {
        renderMessage(respondedToolApproval([{ question: "Decision", answer: "Denied" }]));
        expect(screen.getByText("Denied")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Show value/i })).toBeNull();
    });

    it("collapses a long argument value behind a code block, hidden by default", () => {
        const { container } = renderMessage(
            respondedToolApproval([
                { question: "Decision", answer: "Denied" },
                { question: "body", answer: longHtml },
            ])
        );
        expect(screen.getByText("Denied")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Show value/i })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Copy/i })).toBeInTheDocument();
        expect(container.textContent).not.toContain("All About Cats");
    });

    it("reveals a plain-text value as raw text when expanded", () => {
        const longText = `plain prose ${"and more ".repeat(40)}`;
        const { container } = renderMessage(respondedToolApproval([{ question: "note", answer: longText, contentType: "text" }]));
        fireEvent.click(screen.getByRole("button", { name: /Show value/i }));
        expect(container.querySelector("pre")?.textContent).toContain("plain prose");
        expect(screen.getByRole("button", { name: /Hide value/i })).toBeInTheDocument();
    });

    it("reveals an html value as a sandboxed preview, not raw tags", () => {
        const { container } = renderMessage(respondedToolApproval([{ question: "body", answer: longHtml, contentType: "html" }]));
        fireEvent.click(screen.getByRole("button", { name: /Show value/i }));
        const iframe = container.querySelector("iframe");
        expect(iframe).not.toBeNull();
        expect(iframe?.getAttribute("srcdoc") ?? "").toContain("All About Cats");
        expect(container.textContent).not.toContain("All About Cats");
    });
});

describe("UserInputMessage — CTA visibility", () => {
    it("scrolls the pending approval CTA into view on mount", () => {
        renderWithProviders(<UserInputMessage message={approvalMessage()} />);

        expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
        expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ block: "end" });
    });

    it("does not scroll once the request has been responded to", () => {
        renderWithProviders(<UserInputMessage message={approvalMessage({ responded: true, responseText: "Approved" })} />);

        expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();
    });
});

describe("UserInputMessage — voided prompt", () => {
    it("renders a cancelled banner instead of the input form when the request was cancelled/superseded", () => {
        renderMessage(approvalMessage({ cancelled: true }));
        expect(screen.getByText("Request was cancelled")).toBeInTheDocument();
        expect(screen.queryByText("Approve")).toBeNull();
    });

    it("shows the cancelled banner even when reload force-sets responded on a non-last, never-answered prompt", () => {
        renderMessage(approvalMessage({ responded: true, responseText: "", cancelled: true }));
        expect(screen.getByText("Request was cancelled")).toBeInTheDocument();
    });

    it("still shows a real answer (responded, not cancelled)", () => {
        renderMessage(approvalMessage({ responded: true, responseText: "Approved" }));
        expect(screen.getByText("Approved")).toBeInTheDocument();
        expect(screen.queryByText("Request was cancelled")).toBeNull();
    });
});

describe("UserInputMessage — expired prompt", () => {
    it("collapses to a compact expired banner (not the full surface) once past expiry and never answered", () => {
        renderMessage(approvalMessage({ expiresAt: "2020-01-01T00:00:00.000Z" }));
        expect(screen.getByText("Request expired")).toBeInTheDocument();
        expect(screen.queryByText("Approve")).toBeNull();
    });

    it("still renders the full interactive surface before expiry", () => {
        renderMessage(approvalMessage());
        expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
        expect(screen.queryByText("Request expired")).toBeNull();
    });

    it("shows the real answer even if the prompt is past expiry (responded wins)", () => {
        renderMessage(approvalMessage({ expiresAt: "2020-01-01T00:00:00.000Z", responded: true, responseText: "Approved" }));
        expect(screen.getByText("Approved")).toBeInTheDocument();
        expect(screen.queryByText("Request expired")).toBeNull();
    });

    it("collapses a live prompt to the expired banner once its deadline lapses", async () => {
        renderMessage(approvalMessage({ expiresAt: new Date(Date.now() + 30).toISOString() }));
        expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 80));
        });
        expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
        expect(screen.getByText("Request expired")).toBeInTheDocument();
    });

    it("does not wrongly expire a prompt whose deadline is far in the future (setTimeout overflow guard)", async () => {
        renderMessage(approvalMessage({ expiresAt: new Date(Date.now() + 30 * 24 * 60 * 60 * 1000).toISOString() }));
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 80));
        });
        expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
        expect(screen.queryByText("Request expired")).toBeNull();
    });

    it("collapses to the expired banner on a backend timeout event (req.timedOut)", () => {
        renderMessage(approvalMessage({ timedOut: true }));
        expect(screen.getByText("Request expired")).toBeInTheDocument();
        expect(screen.queryByText("Approve")).toBeNull();
    });
});
