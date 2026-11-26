// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { RPC_CODE_BUDGET_EXHAUSTED, RPC_CODE_BUDGET_UNCONFIRMED, useBudgetStatus } from "@/api/budget-status";

import { BuilderContext, type BuilderContextValue } from "@/providers/contexts/BuilderContext";
import { ChatContext } from "@/providers/contexts/ChatContext";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ChatProvider } from "./ChatProvider";

const BUDGET_TEXT = "Sorry, I can't take new requests from you right now: your usage budget is used up until Nov 1.";

let statusRequests = 0;

const server = setupServer(
    http.get("*/api/v1/budget/status", () => {
        statusRequests += 1;
        return HttpResponse.json({ ownLevel: "limit", meshLimitReached: false, periodEnd: "2099-11-01T00:00:00.000Z" });
    })
);
let trackSpy: ReturnType<typeof vi.spyOn>;

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "debug").mockImplementation(() => {});
});
beforeEach(() => {
    localStorage.clear();
    statusRequests = 0;
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});
afterEach(() => {
    server.resetHandlers();
    trackSpy.mockRestore();
});
afterAll(() => {
    server.close();
    vi.restoreAllMocks();
});

const builderContextValue: BuilderContextValue = {
    builderMode: false,
    setBuilderMode: () => {},
    builderCreationState: { isBuilding: false, components: [], overallStatus: "idle" },
    setBuilderCreationState: () => {},
    selectedBuilderNode: null,
    setSelectedBuilderNode: () => {},
    builderTabContent: null,
};

function Consumer() {
    const ctx = React.useContext(ChatContext);
    useBudgetStatus();
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="responding">{String(ctx.isResponding)}</span>
            <ul>
                {ctx.messages.map((m, i) => (
                    <li key={i} data-testid={m.isUser ? "user-message" : m.isWarning ? "warning-message" : m.isError ? "error-message" : "agent-message"}>
                        {m.parts.map(p => (p.kind === "text" ? p.text : "")).join("")}
                    </li>
                ))}
            </ul>
            <button data-testid="pick-agent" onClick={() => ctx.setSelectedAgentName("TestAgent")}>
                Pick Agent
            </button>
            <button data-testid="submit" onClick={() => void ctx.handleSubmit({ preventDefault: () => {} } as unknown as React.FormEvent, null, "hello")}>
                Submit
            </button>
        </div>
    );
}

async function submitRefusedWith(status: number, body: Record<string, unknown>) {
    server.use(http.post("*/api/v1/message:stream", () => HttpResponse.json(body, { status })));
    render(
        <MemoryRouter initialEntries={["/"]}>
            <StoryProvider shouldIsolateQueryClient userContextValues={{ userHasCapability: () => false }} configContextValues={{ persistenceEnabled: true }}>
                <BuilderContext.Provider value={builderContextValue}>
                    <ChatProvider disableBuilderBridge>
                        <Consumer />
                    </ChatProvider>
                </BuilderContext.Provider>
            </StoryProvider>
        </MemoryRouter>
    );
    await waitFor(() => expect(statusRequests).toBe(1));
    await act(async () => {
        fireEvent.click(screen.getByTestId("pick-agent"));
    });
    await act(async () => {
        fireEvent.click(screen.getByTestId("submit"));
    });
}

const rpcRefusal = (code: number | undefined, message: string) => ({ jsonrpc: "2.0", id: "req-1", error: { code, message } });

describe("a submit refused because of a usage budget", () => {
    test.each([RPC_CODE_BUDGET_EXHAUSTED, RPC_CODE_BUDGET_UNCONFIRMED])("code %i lands in the transcript as a warning, with no error dialog", async code => {
        await submitRefusedWith(429, rpcRefusal(code, BUDGET_TEXT));

        await waitFor(() => expect(screen.getByTestId("warning-message")).toHaveTextContent(BUDGET_TEXT));
        expect(screen.getByTestId("user-message")).toBeInTheDocument();
        expect(screen.getByTestId("responding").textContent).toBe("false");
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorCategory: "budget_exhausted", errorSource: "api" }));
    });

    test("a spent budget refreshes the budget status", async () => {
        await submitRefusedWith(429, rpcRefusal(RPC_CODE_BUDGET_EXHAUSTED, BUDGET_TEXT));

        await waitFor(() => expect(statusRequests).toBe(2));
    });
});

describe("any other refused submit", () => {
    test("keeps the error dialog and adds nothing to the transcript", async () => {
        await submitRefusedWith(429, rpcRefusal(-32000, "Too many requests"));

        expect(await screen.findByRole("dialog")).toHaveTextContent("Too many requests");
        expect(screen.queryByTestId("warning-message")).not.toBeInTheDocument();
        expect(screen.queryByTestId("error-message")).not.toBeInTheDocument();
        expect(screen.getByTestId("responding").textContent).toBe("false");
    });

    test.each([
        ["a 429 with another code", 429, -32000],
        ["a 429 without a code", 429, undefined],
        ["a 500", 500, -32603],
        ["an unconfirmed budget", 429, RPC_CODE_BUDGET_UNCONFIRMED],
    ])("leaves the budget status alone for %s", async (_label, status, code) => {
        await submitRefusedWith(status, rpcRefusal(code, "refused"));

        await waitFor(() => expect(screen.getByTestId("responding").textContent).toBe("false"));
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(statusRequests).toBe(1);
    });
});
