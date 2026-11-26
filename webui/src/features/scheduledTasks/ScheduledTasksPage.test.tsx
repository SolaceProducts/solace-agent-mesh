// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { queryClient } from "@/providers/query/QueryClient";

import type { AgentCard } from "@/types/a2a";

import { makeAgentCard } from "@/testing/data/agentCards";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ScheduledTasksPage } from "./ScheduledTasksPage";

vi.mock("@/common/dialogs", () => ({
    useNavigationBlocker: () => ({ allowNavigation: (fn: () => void) => fn(), NavigationBlocker: () => null, setBlockingEnabled: () => {} }),
}));

const NO_ACCESS_TITLE = "No Agents or Workflows";
const NO_ACCESS_SUBTITLE = /Contact your administrator if you should have access to scheduled tasks/i;

function scheduledTask(name: string) {
    return {
        id: "task-1",
        name,
        description: "Generate a daily summary report",
        namespace: "default",
        createdBy: "user-1",
        scheduleType: "cron",
        scheduleExpression: "0 9 * * *",
        timezone: "UTC",
        targetAgentName: "report-agent",
        targetType: "agent",
        taskMessage: [{ type: "text", text: "Generate report" }],
        enabled: true,
        status: "active",
        maxRetries: 3,
        retryDelaySeconds: 60,
        timeoutSeconds: 300,
        consecutiveFailureCount: 0,
        runCount: 10,
        createdAt: new Date(0).toISOString(),
        updatedAt: new Date(0).toISOString(),
        nextRunTime: new Date(0).toISOString(),
    };
}

function routeGet(apiGetSpy: ReturnType<typeof vi.spyOn>, agentCards: AgentCard[] | "error", tasks: ReturnType<typeof scheduledTask>[] = []) {
    apiGetSpy.mockImplementation((endpoint: unknown) => {
        const url = String(endpoint);
        if (url.includes("/api/v1/scheduledTasks")) {
            return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ data: tasks, meta: { pagination: { pageNumber: 1, count: tasks.length, pageSize: 100, nextPage: null, totalPages: 1 } } }) });
        }
        if (url.includes("/api/v1/agentCards")) {
            return agentCards === "error" ? Promise.reject(new Error("gateway unreachable")) : Promise.resolve({ data: agentCards });
        }
        return Promise.resolve({ data: [] });
    });
}

describe("ScheduledTasksPage invoke-access gate", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    test("shows the no-access page when the user can invoke nothing", async () => {
        routeGet(apiGetSpy, []);

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText(NO_ACCESS_TITLE)).toBeInTheDocument();
        expect(screen.getByText(NO_ACCESS_SUBTITLE)).toBeInTheDocument();
        expect(screen.queryByText("Create Manually")).not.toBeInTheDocument();
    });

    test("Refresh re-asks the gateway, so a user granted access mid-session isn't stuck", async () => {
        routeGet(apiGetSpy, []);

        renderWithProviders(<ScheduledTasksPage />);
        await screen.findByText(NO_ACCESS_TITLE);

        routeGet(apiGetSpy, [makeAgentCard({ name: "Weather Expert" })]);
        await userEvent.click(screen.getByText("Refresh"));

        expect(await screen.findByText("No Scheduled Tasks Found")).toBeInTheDocument();
        expect(screen.queryByText(NO_ACCESS_TITLE)).not.toBeInTheDocument();
    });

    test("treats an internal-only card as no access — the Builder isn't a schedulable target", async () => {
        routeGet(apiGetSpy, [makeAgentCard({ name: "Builder", isInternal: true })]);

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText(NO_ACCESS_TITLE)).toBeInTheDocument();
    });

    test("shows an existing task instead of the empty state, so its owner can stop it", async () => {
        routeGet(apiGetSpy, [], [scheduledTask("Daily Report")]);

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText("Daily Report")).toBeInTheDocument();
        expect(screen.queryByText(NO_ACCESS_TITLE)).not.toBeInTheDocument();
    });

    test("renders the task library when an invokable agent exists", async () => {
        routeGet(apiGetSpy, [makeAgentCard({ name: "Weather Expert" })]);

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText("No Scheduled Tasks Found")).toBeInTheDocument();
        expect(screen.queryByText(NO_ACCESS_TITLE)).not.toBeInTheDocument();
    });

    test("renders the task library when only a workflow is invokable", async () => {
        routeGet(apiGetSpy, [makeAgentCard({ name: "Nightly Report", isWorkflow: true })]);

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText("No Scheduled Tasks Found")).toBeInTheDocument();
        expect(screen.queryByText(NO_ACCESS_TITLE)).not.toBeInTheDocument();
    });

    test("reports a card-fetch failure as an outage, not as a denial", async () => {
        routeGet(apiGetSpy, "error");

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText("Unable to Load Agents")).toBeInTheDocument();
        expect(screen.queryByText(NO_ACCESS_TITLE)).not.toBeInTheDocument();
    });

    test("a card-fetch failure never hides tasks the user already has", async () => {
        routeGet(apiGetSpy, "error", [scheduledTask("Daily Report")]);

        renderWithProviders(<ScheduledTasksPage />);

        expect(await screen.findByText("Daily Report")).toBeInTheDocument();
        expect(screen.queryByText("Unable to Load Agents")).not.toBeInTheDocument();
    });

    test("an open builder survives a refetch that empties the card list", async () => {
        routeGet(apiGetSpy, [makeAgentCard({ name: "Weather Expert" })]);

        renderWithProviders(<ScheduledTasksPage />);
        await screen.findByText("No Scheduled Tasks Found");
        await userEvent.click(screen.getByText("Create Manually"));
        expect(await screen.findByText("Create Scheduled Task")).toBeInTheDocument();

        routeGet(apiGetSpy, []);
        await act(async () => {
            await queryClient.refetchQueries({ queryKey: ["agentCards"] });
        });

        expect(screen.getByText("Create Scheduled Task")).toBeInTheDocument();
        expect(screen.queryByText(NO_ACCESS_TITLE)).not.toBeInTheDocument();
    });

    test("a ?mode=create deep link lands on the gate instead of an unusable builder", async () => {
        routeGet(apiGetSpy, []);

        renderWithProviders(<ScheduledTasksPage />, { initialPath: "/?mode=create" });

        expect(await screen.findByText(NO_ACCESS_TITLE)).toBeInTheDocument();
        expect(screen.queryByText("Create Scheduled Task")).not.toBeInTheDocument();
    });

    test("a failed pause surfaces a notification instead of failing silently", async () => {
        routeGet(apiGetSpy, [makeAgentCard({ name: "Weather Expert" })], [scheduledTask("Daily Report")]);
        const postSpy = vi.spyOn(api.webui, "post").mockRejectedValue(new Error("boom"));
        const addNotification = vi.fn();

        renderWithProviders(<ScheduledTasksPage />, { chatContextValues: { addNotification } });
        await screen.findByText("Daily Report");

        fireEvent.pointerDown(screen.getByRole("button", { name: /actions/i }));
        fireEvent.click(await screen.findByText("Pause Task"));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Pause failed"), "warning"));
        postSpy.mockRestore();
    });
});
