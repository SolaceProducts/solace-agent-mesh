// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, type MockInstance, test, vi } from "vitest";

import { ToastContainer } from "@/common/toast";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ChatProvider } from "./ChatProvider";
import { useChatContext } from "./contexts";

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const TOAST_LIFETIME_MS = 4000;

function Raiser() {
    const { addNotification } = useChatContext();
    return (
        <>
            <button data-testid="raise-burst" onClick={() => ["first toast", "second toast", "third toast"].forEach(message => addNotification(message, "warning"))} />
            <button data-testid="raise-early" onClick={() => addNotification("early toast", "info")} />
            <button data-testid="raise-late" onClick={() => addNotification("late toast", "info")} />
        </>
    );
}

function renderProvider() {
    const router = createMemoryRouter(
        [
            {
                path: "*",
                element: (
                    <StoryProvider shouldIsolateQueryClient>
                        <ChatProvider disableBuilderBridge>
                            <Raiser />
                            <ToastContainer />
                        </ChatProvider>
                    </StoryProvider>
                ),
            },
        ],
        { initialEntries: ["/chat"] }
    );
    render(<RouterProvider router={router} />);
}

describe("ChatProvider — notification ids", () => {
    let consoleError: MockInstance<typeof console.error>;

    beforeEach(() => {
        vi.spyOn(Date, "now").mockReturnValue(1_700_000_000_000);
        consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    test("toasts raised in the same millisecond render with distinct ids and no duplicate-key warning", async () => {
        renderProvider();
        await waitFor(() => expect(screen.getByTestId("raise-burst")).toBeInTheDocument());

        await act(async () => {
            screen.getByTestId("raise-burst").click();
        });

        const duplicateKeyWarnings = consoleError.mock.calls.filter(args => String(args[0]).includes("same key"));
        expect(duplicateKeyWarnings).toEqual([]);

        const ids = ["first toast", "second toast", "third toast"].map(message => screen.getByText(message).closest("[id]")?.id);
        expect(new Set(ids).size).toBe(3);
    });

    test("a toast raised in the same millisecond as an earlier one is not removed by the earlier toast's timer", async () => {
        renderProvider();
        await waitFor(() => expect(screen.getByTestId("raise-early")).toBeInTheDocument());

        vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });

        act(() => {
            screen.getByTestId("raise-early").click();
        });
        act(() => {
            vi.advanceTimersByTime(TOAST_LIFETIME_MS - 1000);
        });
        act(() => {
            screen.getByTestId("raise-late").click();
        });
        expect(screen.getByText("late toast")).toBeInTheDocument();

        act(() => {
            vi.advanceTimersByTime(1500);
        });

        expect(screen.queryByText("early toast")).not.toBeInTheDocument();
        expect(screen.getByText("late toast")).toBeInTheDocument();

        act(() => {
            vi.advanceTimersByTime(TOAST_LIFETIME_MS);
        });
        expect(screen.queryByText("late toast")).not.toBeInTheDocument();
    });
});
