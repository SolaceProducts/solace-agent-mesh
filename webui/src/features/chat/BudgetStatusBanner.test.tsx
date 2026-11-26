// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { BudgetStatus } from "@/api/budget-status";

import { queryClient as sharedQueryClient } from "@/providers/query/QueryClient";

import { MockEventSource } from "@/testing/helpers/mockEventSource";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BudgetStatusBanner } from "./BudgetStatusBanner";

const PERIOD_END = "2099-11-01T00:00:00.000Z";
const NEXT_PERIOD_END = "2099-12-01T00:00:00.000Z";

let status: BudgetStatus;
let statusRequests = 0;
let failStatusWith: number | null = null;
let holdStatusResponse: Promise<void> | null = null;

const server = setupServer(
    http.get("*/api/v1/budget/status", async () => {
        statusRequests += 1;
        if (failStatusWith !== null) return HttpResponse.json({ message: "failure" }, { status: failStatusWith });
        const readStatus = status;
        if (holdStatusResponse) await holdStatusResponse;
        return HttpResponse.json(readStatus);
    })
);

function notificationStream(): MockEventSource {
    const stream = MockEventSource.instances.find(source => source.url.includes("/api/v1/sse/notifications"));
    if (!stream) throw new Error("the notification stream was not opened");
    return stream;
}

async function pushStatus(next: BudgetStatus) {
    await waitFor(() => notificationStream());
    act(() => notificationStream().emit("budget_status", JSON.stringify(next)));
}

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
    localStorage.clear();
    statusRequests = 0;
    failStatusWith = null;
    holdStatusResponse = null;
    status = { ownLevel: "none", meshLimitReached: false, periodEnd: PERIOD_END, capKey: "cap-a" };
});
afterEach(() => {
    server.resetHandlers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
});
afterAll(() => server.close());

function renderBanner({ username = "alice", isolate = true }: { username?: string | null; isolate?: boolean } = {}) {
    return render(
        <StoryProvider shouldIsolateQueryClient={isolate} userContextValues={{ username }}>
            <BudgetStatusBanner />
        </StoryProvider>
    );
}

async function waitForStatusLoad() {
    await waitFor(() => expect(statusRequests).toBeGreaterThan(0));
}

const WARN_TEXT = /close to your usage budget/;
const OWN_LIMIT_TEXT = /You've used up your usage budget/;
const MESH_LIMIT_TEXT = /This deployment's usage budget is used up/;

describe("BudgetStatusBanner", () => {
    test("shows nothing while under budget", async () => {
        renderBanner();
        await waitForStatusLoad();
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });

    test("warns with the reset date when the user nears their budget", async () => {
        status = { ...status, ownLevel: "warn" };
        renderBanner();
        expect(await screen.findByText(WARN_TEXT)).toHaveTextContent("It resets on November 1, 2099 (UTC).");
        expect(screen.getByRole("button", { name: "Dismiss" })).toBeInTheDocument();
    });

    test("tells the user their own budget is used up, without a dismiss", async () => {
        status = { ...status, ownLevel: "limit" };
        renderBanner();
        expect(await screen.findByText(OWN_LIMIT_TEXT)).toHaveTextContent("It resets on November 1, 2099 (UTC).");
        expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
    });

    test.each(["none", "warn", "limit"] as const)("the mesh limit wins over an own level of %s", async ownLevel => {
        status = { ...status, ownLevel, meshLimitReached: true };
        renderBanner();
        expect(await screen.findByText(MESH_LIMIT_TEXT)).toBeInTheDocument();
        expect(screen.getAllByTestId("messageBanner")).toHaveLength(1);
        expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
    });

    describe("dismissing the warning", () => {
        test("keeps it hidden for the same user, period and cap across visits", async () => {
            status = { ...status, ownLevel: "warn" };
            const { unmount } = renderBanner();
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            expect(screen.queryByText(WARN_TEXT)).not.toBeInTheDocument();
            unmount();

            renderBanner();
            await waitForStatusLoad();
            await waitFor(() => expect(statusRequests).toBe(2));
            expect(screen.queryByText(WARN_TEXT)).not.toBeInTheDocument();
        });

        test("shows it again in a new period", async () => {
            status = { ...status, ownLevel: "warn" };
            const { unmount } = renderBanner();
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            unmount();

            status = { ...status, periodEnd: NEXT_PERIOD_END };
            renderBanner();
            expect(await screen.findByText(WARN_TEXT)).toHaveTextContent("It resets on December 1, 2099 (UTC).");
        });

        test("keeps it hidden when the same period end is written differently", async () => {
            status = { ...status, ownLevel: "warn" };
            const { unmount } = renderBanner();
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            unmount();

            status = { ...status, periodEnd: "2099-11-01T00:00:00.000000+00:00" };
            renderBanner();
            await waitFor(() => expect(statusRequests).toBe(2));
            expect(screen.queryByText(WARN_TEXT)).not.toBeInTheDocument();
        });

        test("shows it again when the cap changes in the same period", async () => {
            status = { ...status, ownLevel: "warn" };
            const { unmount } = renderBanner();
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            unmount();

            status = { ...status, capKey: "cap-b" };
            renderBanner();
            expect(await screen.findByText(WARN_TEXT)).toBeInTheDocument();
        });

        test("still shows the limit once the budget runs out", async () => {
            status = { ...status, ownLevel: "warn" };
            const { unmount } = renderBanner();
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            unmount();

            status = { ...status, ownLevel: "limit" };
            renderBanner();
            expect(await screen.findByText(OWN_LIMIT_TEXT)).toBeInTheDocument();
        });

        test("does not hide it for another user on the same browser", async () => {
            status = { ...status, ownLevel: "warn" };
            const { unmount } = renderBanner({ username: "alice" });
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            unmount();

            renderBanner({ username: "bob" });
            expect(await screen.findByText(WARN_TEXT)).toBeInTheDocument();
        });

        test("still hides it when browser storage is unavailable", async () => {
            const storage = window.localStorage;
            const realGetItem = storage.getItem.bind(storage);
            const realSetItem = storage.setItem.bind(storage);
            const denyBudgetKeys = (key: string) => {
                if (key.startsWith("budget-warn-banner-dismissed")) throw new Error("denied");
            };
            vi.spyOn(storage, "getItem").mockImplementation(key => {
                denyBudgetKeys(key);
                return realGetItem(key);
            });
            vi.spyOn(storage, "setItem").mockImplementation((key, value) => {
                denyBudgetKeys(key);
                realSetItem(key, value);
            });
            status = { ...status, ownLevel: "warn" };
            renderBanner();
            fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
            const [key] = vi.mocked(storage.setItem).mock.calls.find(([name]) => name.startsWith("budget-warn-banner-dismissed")) ?? [];
            expect(key).toMatch(/^budget-warn-banner-dismissed:[0-9a-z]+$/);
            expect(key).not.toContain("alice");
            expect(screen.queryByText(WARN_TEXT)).not.toBeInTheDocument();
        });
    });

    test("follows the status the notification stream delivers, without polling", async () => {
        renderBanner();
        await waitForStatusLoad();
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();

        await pushStatus({ ...status, ownLevel: "limit", capKey: null });
        expect(await screen.findByText(OWN_LIMIT_TEXT)).toBeInTheDocument();

        await pushStatus({ ...status, ownLevel: "none" });
        await waitFor(() => expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument());
        expect(statusRequests).toBe(1);
    });

    test("keeps a pushed status when an older read of the status lands afterwards", async () => {
        let releaseRead = () => {};
        holdStatusResponse = new Promise<void>(resolve => {
            releaseRead = resolve;
        });
        status = { ...status, ownLevel: "warn" };
        renderBanner();
        await waitForStatusLoad();

        await pushStatus({ ...status, ownLevel: "limit" });
        expect(await screen.findByText(OWN_LIMIT_TEXT)).toBeInTheDocument();

        releaseRead();
        await new Promise(resolve => setTimeout(resolve, 100));
        expect(screen.getByText(OWN_LIMIT_TEXT)).toBeInTheDocument();
        expect(screen.queryByText(WARN_TEXT)).not.toBeInTheDocument();
    });

    test("refetches the status when the stream delivers an unreadable one", async () => {
        renderBanner();
        await waitForStatusLoad();

        status = { ...status, meshLimitReached: true };
        await waitFor(() => notificationStream());
        act(() => notificationStream().emit("budget_status", "{not json"));
        expect(await screen.findByText(MESH_LIMIT_TEXT)).toBeInTheDocument();
        expect(statusRequests).toBe(2);
    });

    describe("when the status endpoint is unavailable", () => {
        describe("with retries landing immediately", () => {
            const defaultOptions = sharedQueryClient.getDefaultOptions();

            beforeEach(() => {
                sharedQueryClient.clear();
                sharedQueryClient.setDefaultOptions({ ...defaultOptions, queries: { ...defaultOptions.queries, retryDelay: 0 } });
            });
            afterEach(() => {
                sharedQueryClient.setDefaultOptions(defaultOptions);
                sharedQueryClient.clear();
            });

            test.each([404, 403])("shows nothing and does not retry after a %i", async code => {
                failStatusWith = code;
                renderBanner({ isolate: false });
                await waitForStatusLoad();
                await new Promise(resolve => setTimeout(resolve, 100));
                expect(statusRequests).toBe(1);
                expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
            });
        });
    });

    test.each([
        { banner: "own-limit", change: { ownLevel: "limit" as const }, text: OWN_LIMIT_TEXT },
        { banner: "deployment-limit", change: { meshLimitReached: true }, text: MESH_LIMIT_TEXT },
    ])("shows the $banner banner before the user is known", async ({ change, text }) => {
        status = { ...status, ...change };
        renderBanner({ username: null });
        expect(await screen.findByText(text)).toBeInTheDocument();
    });

    test("shows nothing until the user is known, then shows the warning", async () => {
        status = { ...status, ownLevel: "warn" };
        const view = renderBanner({ username: null });
        await waitForStatusLoad();
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();

        view.rerender(
            <StoryProvider shouldIsolateQueryClient userContextValues={{ username: "alice" }}>
                <BudgetStatusBanner />
            </StoryProvider>
        );
        expect(await screen.findByText(WARN_TEXT)).toBeInTheDocument();
    });
});
