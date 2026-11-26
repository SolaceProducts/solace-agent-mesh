// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { evaluationsApi } from "@/api/evaluations/versioned";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const STUB_REPORT = {
    runGroupId: "rg-1",
    experimentName: "Demo Experiment",
    targetAgent: "agent-broker-name",
    status: "completed" as const,
    startedAt: "2026-01-01T00:00:00.000+00:00",
    completedAt: "2026-01-01T00:01:00.000+00:00",
    summaryScores: null,
    modelConfigs: [],
    runCount: 1,
};

const reports = vi.hoisted(() => ({
    lastOptions: undefined as { targetType?: string } | undefined,
    targets: [] as { name: string; targetType: "agent" | "workflow" }[],
}));

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return {
        ...actual,
        useEvalReportsList: (options: { targetType?: string }) => {
            reports.lastOptions = options;
            return {
                data: {
                    data: [STUB_REPORT],
                    meta: { pagination: { pageNumber: 1, pageSize: 20, count: 1, totalPages: 1, nextPage: null } },
                },
                isLoading: false,
                refetch: vi.fn(),
            };
        },
        useEvalReportAgents: () => ({ data: reports.targets, isLoading: false }),
    };
});

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useEvalWatchlist: () => ({ data: [], isLoading: false }),
        useEvalWatchlistChartData: () => ({ data: [], isPending: false, isError: false }),
        useSetEvalWatchlist: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

let sampleExperimentData: { id: string; name: string } | null = null;

vi.mock("@/api/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations/hooks");
    return {
        ...actual,
        useSampleExperiment: () => ({ data: sampleExperimentData, isLoading: false, refetch: vi.fn() }),
    };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            triggerRun: vi.fn(),
        },
    };
});

import { EvalOverviewPage } from "./EvalOverviewPage";

function renderPage() {
    return render(
        <StoryProvider>
            <MemoryRouter initialEntries={["/evaluations/lab"]}>
                <Routes>
                    <Route path="/evaluations/lab" element={<EvalOverviewPage />} />
                    <Route path="/evaluations/runGroups/group-1" element={<p>Report group-1</p>} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalOverviewPage — Reports QuickStart banner dismissibility", () => {
    const STORAGE_KEY = "eval.reportsQuickStartDismissed";

    afterEach(() => {
        vi.clearAllMocks();
        window.localStorage.removeItem(STORAGE_KEY);
        sampleExperimentData = null;
    });

    test("renders the banner by default and persists dismissal across remounts", async () => {
        const user = userEvent.setup();

        const { unmount } = renderPage();

        const dismiss = await screen.findByRole("button", { name: "Dismiss" });
        await user.click(dismiss);

        await waitFor(() => expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument());
        expect(window.localStorage.getItem(STORAGE_KEY)).toBe("true");

        unmount();
        renderPage();
        await waitFor(() => expect(screen.getByText("Demo Experiment")).toBeInTheDocument());
        expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
    });

    test("does not collide with the Experiments-page storage key", async () => {
        window.localStorage.setItem("eval.experimentsQuickStartDismissed", "true");

        renderPage();

        expect(await screen.findByRole("button", { name: "Dismiss" })).toBeInTheDocument();
        window.localStorage.removeItem("eval.experimentsQuickStartDismissed");
    });
});

describe("EvalOverviewPage — Reports QuickStart CTA reflects sample availability", () => {
    const STORAGE_KEY = "eval.reportsQuickStartDismissed";

    afterEach(() => {
        vi.restoreAllMocks();
        window.localStorage.removeItem(STORAGE_KEY);
        sampleExperimentData = null;
    });

    test("shows Run Sample Experiment when the sample is seeded", async () => {
        sampleExperimentData = { id: "sample-exp-1", name: "Sample" };

        renderPage();

        expect(await screen.findByTestId("runSampleExperiment")).toHaveTextContent("Run Sample Experiment");
        expect(screen.queryByTestId("createExperimentBanner")).not.toBeInTheDocument();
    });

    test("Run Sample Experiment triggers the sample and opens its report", async () => {
        sampleExperimentData = { id: "sample-exp-1", name: "Sample" };
        const trigger = vi.spyOn(evaluationsApi, "triggerRun").mockResolvedValue({ runGroupId: "group-1" });

        renderPage();
        await userEvent.click(await screen.findByTestId("runSampleExperiment"));

        await waitFor(() => expect(trigger).toHaveBeenCalledWith("sample-exp-1", {}));
        expect(await screen.findByText("Report group-1")).toBeInTheDocument();
    });

    test("a sample trigger that names no run group says so and stays put", async () => {
        sampleExperimentData = { id: "sample-exp-1", name: "Sample" };
        vi.spyOn(evaluationsApi, "triggerRun").mockResolvedValue({});
        vi.spyOn(console, "error").mockImplementation(() => {});

        renderPage();
        await userEvent.click(await screen.findByTestId("runSampleExperiment"));

        expect(await screen.findByText(/Could not run the sample experiment: trigger returned no runs/)).toBeInTheDocument();
        expect(screen.queryByText("Report group-1")).not.toBeInTheDocument();
    });

    test("falls back to Create Experiment when the sample is absent", async () => {
        sampleExperimentData = null;

        renderPage();

        expect(await screen.findByTestId("createExperimentBanner")).toHaveTextContent("Create Experiment");
        expect(screen.queryByTestId("runSampleExperiment")).not.toBeInTheDocument();
    });
});

describe("EvalOverviewPage — workflow targets", () => {
    afterEach(() => {
        delete (STUB_REPORT as { targetType?: string }).targetType;
    });

    test("labels a workflow report's target and names the column Target", async () => {
        (STUB_REPORT as { targetType?: string }).targetType = "workflow";
        render(
            <StoryProvider flagValues={{ workflow_evals: true }}>
                <MemoryRouter initialEntries={["/evaluations/lab"]}>
                    <EvalOverviewPage />
                </MemoryRouter>
            </StoryProvider>
        );

        expect(await screen.findByTestId("targetTypeIcon")).toHaveAttribute("data-target-type", "workflow");
        expect(screen.getByRole("columnheader", { name: "Target" })).toBeInTheDocument();
    });

    const renderWithWorkflowEvals = () =>
        render(
            <StoryProvider flagValues={{ workflow_evals: true }}>
                <MemoryRouter initialEntries={["/evaluations/lab"]}>
                    <EvalOverviewPage />
                </MemoryRouter>
            </StoryProvider>
        );

    test("a type choice narrows the reports and the target list by the type the server gives each target", async () => {
        reports.targets = [
            { name: "Orchestrator", targetType: "agent" },
            { name: "OrderDesk", targetType: "workflow" },
        ];
        renderWithWorkflowEvals();

        await userEvent.click(await screen.findByTestId("reportsTargetType"));
        await userEvent.click(await screen.findByRole("option", { name: "Workflows" }));
        await waitFor(() => expect(reports.lastOptions?.targetType).toBe("workflow"));

        await userEvent.click(screen.getByRole("combobox", { name: "" }));
        expect(await screen.findByRole("option", { name: "OrderDesk" })).toBeInTheDocument();
        expect(screen.queryByRole("option", { name: "Orchestrator" })).toBeNull();
        reports.targets = [];
    });

    test("lists a name evaluated as both an agent and a workflow once under all types", async () => {
        reports.targets = [
            { name: "OrderDesk", targetType: "agent" },
            { name: "OrderDesk", targetType: "workflow" },
        ];
        renderWithWorkflowEvals();

        await userEvent.click(screen.getByRole("combobox", { name: "" }));
        expect(await screen.findAllByRole("option", { name: "OrderDesk" })).toHaveLength(1);
        reports.targets = [];
    });

    test("keeps the agent wording with workflow evals off", async () => {
        renderPage();
        expect(await screen.findByRole("columnheader", { name: "Agent" })).toBeInTheDocument();
        expect(screen.queryByTestId("targetTypeIcon")).toBeNull();
        expect(screen.queryByTestId("reportsTargetType")).toBeNull();
        expect(screen.getByText("All Agents")).toBeInTheDocument();
        expect(reports.lastOptions?.targetType).toBeUndefined();
    });
});
