// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { makeAgentCard } from "@/testing/data/agentCards";

if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
}
if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
    Element.prototype.setPointerCapture = () => {};
    Element.prototype.releasePointerCapture = () => {};
}
if (!window.matchMedia) {
    window.matchMedia = (query: string) =>
        ({ matches: false, media: query, onchange: null, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false }) as unknown as MediaQueryList;
}
if (!global.ResizeObserver) {
    global.ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
    };
}

const mockGet = vi.fn();
const mockPost = vi.fn();

function serveAgentCards(cards: ReturnType<typeof makeAgentCard>[]) {
    mockGet.mockImplementation((url: string) => {
        if (url.includes("agentCards")) return Promise.resolve({ data: cards });
        return Promise.resolve({});
    });
}

const FILL = {
    name: "Weekly Jira Report",
    description: "weekly report",
    scheduleType: "INTERVAL",
    scheduleExpression: "5m",
    targetAgentName: "Orchestrator",
    taskMessage: "Retrieve all Jira items created in the last week and produce a weekly report",
    timezone: "UTC",
    targetType: "agent",
};

describe("TaskTemplateBuilder", () => {
    let TaskTemplateBuilder: React.ComponentType<{ onBack: () => void; onSuccess: () => void; initialMode?: "ai-assisted" | "manual" }>;

    beforeEach(async () => {
        vi.resetModules();
        mockGet.mockReset();
        mockPost.mockReset();

        serveAgentCards([]);
        mockPost.mockImplementation((url: string) => {
            if (url.includes("validate-conflict")) {
                return Promise.resolve({ conflict: true, reason: "Weekly report scheduled to run every 5 minutes", affectedFields: ["instructions", "schedule"] });
            }
            if (url.includes("/scheduledTasks/")) return Promise.resolve({ id: "task-1" });
            return Promise.resolve({});
        });

        vi.doMock("@/api/client", () => ({
            api: { webui: { get: mockGet, post: mockPost, patch: vi.fn(), delete: vi.fn() }, getFullUrl: (e: string) => e },
        }));

        vi.doMock("@/common/dialogs", () => ({
            useNavigationBlocker: () => ({ allowNavigation: (fn: () => void) => fn(), NavigationBlocker: () => null, setBlockingEnabled: () => {} }),
        }));

        vi.doMock("@/features/scheduledTasks/TaskBuilderChat", () => ({
            TaskBuilderChat: ({ onConfigUpdate, availableAgents = [] }: { onConfigUpdate: (c: Record<string, unknown>) => void; availableAgents?: Array<{ name: string }> }) => (
                <>
                    <button data-testid="ai-fill" onClick={() => onConfigUpdate(FILL)}>
                        fill
                    </button>
                    <ul data-testid="builder-agents">
                        {availableAgents.map(a => (
                            <li key={a.name}>{a.name}</li>
                        ))}
                    </ul>
                </>
            ),
        }));

        const mod = await import("@/features/scheduledTasks/TaskTemplateBuilder");
        TaskTemplateBuilder = mod.TaskTemplateBuilder as typeof TaskTemplateBuilder;
    });

    function renderBuilder(initialMode: "ai-assisted" | "manual" = "ai-assisted") {
        const onBack = vi.fn();
        const onSuccess = vi.fn();
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        render(
            <OpenFeatureTestProvider flagValueMap={{}}>
                <QueryClientProvider client={queryClient}>
                    <TaskTemplateBuilder onBack={onBack} onSuccess={onSuccess} initialMode={initialMode} />
                </QueryClientProvider>
            </OpenFeatureTestProvider>
        );
        return { onBack, onSuccess };
    }

    test("AI-set interval lands in the preview even when scheduleType arrives uppercase", async () => {
        renderBuilder();
        fireEvent.click(screen.getByTestId("ai-fill"));

        const intervalRow = (await screen.findByText("Minutes")).closest("div") as HTMLElement;
        expect(within(intervalRow).getByText("5")).toBeInTheDocument();
        expect(screen.queryByText(/No interval yet/i)).not.toBeInTheDocument();
    });

    test("internal agents are not offered as scheduled-task targets", async () => {
        serveAgentCards([makeAgentCard({ name: "Weather Expert" }), makeAgentCard({ name: "Builder", isInternal: true })]);

        renderBuilder("manual");

        const builderAgents = await screen.findByTestId("builder-agents");
        await waitFor(() => expect(within(builderAgents).getByText("Weather Expert")).toBeInTheDocument());
        expect(within(builderAgents).queryByText("Builder")).not.toBeInTheDocument();

        fireEvent.click(screen.getByText("Select a agent"));
        const listbox = await screen.findByRole("listbox");
        expect(within(listbox).getByText("Weather Expert")).toBeInTheDocument();
        expect(within(listbox).queryByText("Builder")).not.toBeInTheDocument();
    });

    function selectShowing(text: string): HTMLElement {
        const trigger = screen.getAllByRole("combobox").find(el => el.textContent === text);
        if (!trigger) throw new Error(`no select is showing "${text}"`);
        return trigger;
    }

    async function chooseOption(triggerText: string, optionText: string) {
        fireEvent.click(selectShowing(triggerText));
        const listbox = await screen.findByRole("listbox");
        fireEvent.click(within(listbox).getByText(optionText));
    }

    test("Create and Activate posts what the user typed, enabled", async () => {
        serveAgentCards([makeAgentCard({ name: "Weather Expert" })]);

        renderBuilder("manual");
        await screen.findByText("Basic Information");

        await chooseOption("Recurring Schedule", "Interval");
        fireEvent.change(screen.getByLabelText(/Task Name/), { target: { value: "Weekly Jira Report" } });
        fireEvent.change(screen.getByLabelText(/Description/), { target: { value: "weekly report" } });
        fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: "Summarise last week" } });
        await chooseOption("Select a agent", "Weather Expert");

        fireEvent.click(screen.getByRole("button", { name: "Create and Activate" }));

        await waitFor(() => expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(true));
        const payload = mockPost.mock.calls.find(c => c[0] === "/api/v1/scheduledTasks/")?.[1];
        expect(payload).toMatchObject({
            name: "Weekly Jira Report",
            description: "weekly report",
            scheduleType: "interval",
            scheduleExpression: "30m",
            targetAgentName: "Weather Expert",
            targetType: "agent",
            taskMessage: [{ type: "text", text: "Summarise last week" }],
            enabled: true,
        });
    });

    test("an expression entered for one schedule type survives a detour through another", async () => {
        renderBuilder("manual");
        await screen.findByText("Basic Information");

        await chooseOption("Recurring Schedule", "Interval");
        fireEvent.change(screen.getByDisplayValue("30"), { target: { value: "45" } });
        await chooseOption("Minutes", "Hours");

        await chooseOption("Interval", "Recurring Schedule");
        await chooseOption("Recurring Schedule", "Interval");

        expect(screen.getByDisplayValue("45")).toBeInTheDocument();
        expect(selectShowing("Hours")).toBeInTheDocument();
    });

    test("switching the target type clears the agent chosen for the old one", async () => {
        serveAgentCards([makeAgentCard({ name: "Weather Expert" })]);

        renderBuilder("manual");
        await screen.findByText("Basic Information");
        await chooseOption("Select a agent", "Weather Expert");
        expect(selectShowing("Weather Expert")).toBeInTheDocument();

        await chooseOption("Agent", "Workflow");

        expect(selectShowing("Select a workflow")).toBeInTheDocument();
    });

    describe("scheduling a workflow that requires structured input", () => {
        const tripSchema = {
            type: "object",
            properties: { destination: { type: "string" }, needsHotel: { type: "boolean" }, notes: { type: "string" } },
            required: ["destination", "needsHotel"],
        };
        const tripTemplate = '{\n  "destination": "",\n  "needsHotel": false\n}';

        async function openFormOnTripPlanner() {
            serveAgentCards([makeAgentCard({ name: "TripPlanner", isWorkflow: true, inputSchema: tripSchema }), makeAgentCard({ name: "Plain Flow", isWorkflow: true }), makeAgentCard({ name: "Weather Expert" })]);
            renderBuilder("manual");
            await screen.findByText("Basic Information");
            await chooseOption("Agent", "Workflow");
            await waitFor(() => expect(screen.getAllByRole("combobox").some(el => el.textContent === "Select a workflow")).toBe(true));
            await chooseOption("Select a workflow", "TripPlanner");
        }

        async function openFormOnTripPlannerAndOrderFlow() {
            serveAgentCards([
                makeAgentCard({ name: "TripPlanner", isWorkflow: true, inputSchema: tripSchema }),
                makeAgentCard({ name: "Order Flow", isWorkflow: true, inputSchema: { properties: { orderId: { type: "integer" } }, required: ["orderId"] } }),
            ]);
            renderBuilder("manual");
            await screen.findByText("Basic Information");
            await chooseOption("Agent", "Workflow");
            await waitFor(() => expect(screen.getAllByRole("combobox").some(el => el.textContent === "Select a workflow")).toBe(true));
            await chooseOption("Select a workflow", "TripPlanner");
        }

        function createOnInterval(name: string) {
            return (async () => {
                await chooseOption("Recurring Schedule", "Interval");
                fireEvent.change(screen.getByLabelText(/Task Name/), { target: { value: name } });
                fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
            })();
        }

        test("shows the required shape and starts the instructions from a template", async () => {
            await openFormOnTripPlanner();

            const hint = await screen.findByTestId("workflow-input-hint");
            expect(hint).toHaveTextContent("Required: destination (string), needsHotel (boolean)");
            expect(hint).toHaveTextContent("Optional: notes (string)");
            expect(screen.getByLabelText(/Instructions/)).toHaveValue(tripTemplate);
        });

        test("Create sends the JSON the user filled in as the instruction text", async () => {
            await openFormOnTripPlanner();
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: '{"destination":"Porto","needsHotel":true}' } });

            await createOnInterval("Trip");

            await waitFor(() => expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(true));
            expect(mockPost.mock.calls.find(c => c[0] === "/api/v1/scheduledTasks/")?.[1]).toMatchObject({
                targetType: "workflow",
                taskMessage: [{ type: "text", text: '{"destination":"Porto","needsHotel":true}' }],
            });
        });

        test("Create refuses prose and says what to send, without calling the API", async () => {
            await openFormOnTripPlanner();
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: "Plan me a trip to Porto" } });

            await createOnInterval("Trip");

            expect((await screen.findAllByText(/Required: destination \(string\), needsHotel \(boolean\)\./)).length).toBeGreaterThan(0);
            expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(false);
        });

        test("Create names a missing field", async () => {
            await openFormOnTripPlanner();
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: '{"needsHotel":true}' } });

            await createOnInterval("Trip");

            expect((await screen.findAllByText(/missing "destination"/)).length).toBeGreaterThan(0);
            expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(false);
        });

        test("the hint lists the required and optional fields and offers no button to insert the template", async () => {
            await openFormOnTripPlanner();

            const hint = await screen.findByTestId("workflow-input-hint");
            expect(within(hint).getByText("Required:")).toBeInTheDocument();
            expect(within(hint).getByText("Optional:")).toBeInTheDocument();
            expect(within(hint).queryByRole("button")).not.toBeInTheDocument();
            expect(screen.queryByRole("button", { name: "Insert input template" })).not.toBeInTheDocument();
        });

        test("the hint stays after the user clears the pre-filled template", async () => {
            await openFormOnTripPlanner();

            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: "" } });

            expect(screen.getByLabelText(/Instructions/)).toHaveValue("");
            expect(screen.getByTestId("workflow-input-hint")).toHaveTextContent("Required: destination (string), needsHotel (boolean)");
        });

        test("moving to another structured workflow keeps the input the user wrote", async () => {
            await openFormOnTripPlannerAndOrderFlow();
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: '{"destination":"Rome","needsHotel":false}' } });

            await chooseOption("TripPlanner", "Order Flow");

            expect(screen.getByLabelText(/Instructions/)).toHaveValue('{"destination":"Rome","needsHotel":false}');
        });

        test("moving to a workflow without a schema drops the template but keeps text the user wrote", async () => {
            await openFormOnTripPlanner();

            await chooseOption("TripPlanner", "Plain Flow");
            expect(screen.getByLabelText(/Instructions/)).toHaveValue("");
            expect(screen.queryByTestId("workflow-input-hint")).not.toBeInTheDocument();

            await chooseOption("Plain Flow", "TripPlanner");
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: '{"destination":"Rome","needsHotel":false}' } });
            await chooseOption("TripPlanner", "Plain Flow");
            expect(screen.getByLabelText(/Instructions/)).toHaveValue('{"destination":"Rome","needsHotel":false}');
        });

        test("moving to another structured workflow replaces the untouched template with that workflow's", async () => {
            await openFormOnTripPlannerAndOrderFlow();

            await chooseOption("TripPlanner", "Order Flow");

            expect(screen.getByLabelText(/Instructions/)).toHaveValue('{\n  "orderId": 0\n}');
        });

        test("a workflow whose schema the text wrapper satisfies keeps the free-text form", async () => {
            serveAgentCards([makeAgentCard({ name: "Summariser", isWorkflow: true, inputSchema: { properties: { text: { type: "string" }, language: { type: "string" } }, required: ["text"] } })]);
            renderBuilder("manual");
            await screen.findByText("Basic Information");
            await chooseOption("Agent", "Workflow");
            await waitFor(() => expect(screen.getAllByRole("combobox").some(el => el.textContent === "Select a workflow")).toBe(true));
            await chooseOption("Select a workflow", "Summariser");

            expect(screen.queryByTestId("workflow-input-hint")).not.toBeInTheDocument();
            expect(screen.getByLabelText(/Instructions/)).toHaveValue("");
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: "Summarise last week" } });
            await createOnInterval("Summary");

            await waitFor(() => expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(true));
        });

        test("going back to an agent clears the template and the hint", async () => {
            await openFormOnTripPlanner();

            await chooseOption("Workflow", "Agent");

            expect(screen.getByLabelText(/Instructions/)).toHaveValue("");
            expect(screen.queryByTestId("workflow-input-hint")).not.toBeInTheDocument();
        });

        test("an agent keeps the free-text form, and prose is accepted", async () => {
            serveAgentCards([makeAgentCard({ name: "Weather Expert" })]);
            renderBuilder("manual");
            await screen.findByText("Basic Information");
            await chooseOption("Select a agent", "Weather Expert");

            expect(screen.queryByTestId("workflow-input-hint")).not.toBeInTheDocument();
            fireEvent.change(screen.getByLabelText(/Instructions/), { target: { value: "Summarise last week" } });
            await createOnInterval("Weather");

            await waitFor(() => expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(true));
        });
    });

    test("Create names every missing field at once rather than one at a time", async () => {
        renderBuilder("manual");
        await screen.findByText("Basic Information");

        fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));

        expect(await screen.findByText("Please fix the following:")).toBeInTheDocument();
        expect(screen.getAllByRole("listitem").length).toBeGreaterThan(1);
    });

    test("conflict warning is advisory and never blocks a single-click Create", async () => {
        const { onSuccess } = renderBuilder("manual");
        fireEvent.click(screen.getByTestId("ai-fill"));

        await waitFor(() => expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/builder/validate-conflict")).toBe(true), { timeout: 2000 });
        expect((await screen.findAllByText(/Weekly report scheduled to run every 5 minutes/i)).length).toBeGreaterThan(0);

        fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));
        await waitFor(() => expect(mockPost.mock.calls.some(c => c[0] === "/api/v1/scheduledTasks/")).toBe(true));
        await waitFor(() => expect(onSuccess).toHaveBeenCalledWith("task-1"));
    });
});
