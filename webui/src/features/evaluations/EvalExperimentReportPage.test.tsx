// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

function buildQueryWrapper(): ({ children }: { children: ReactNode }) => ReactNode {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }) => createElement(OpenFeatureTestProvider, { flagValueMap: {} }, createElement(QueryClientProvider, { client }, children));
}

import type { EvalExperiment, EvalRunGroup } from "@/types/eval";

import { ExperimentReportBanner } from "./components";
import { EVAL_ROUTES } from "./constants";

function makeGroup(overrides: Partial<EvalRunGroup> = {}): EvalRunGroup {
    return {
        id: "group-1",
        experimentId: "exp-1",
        runName: "",
        status: "completed",
        progressCurrent: 5,
        progressTotal: 5,
        startedAt: "2026-01-01T00:00:00Z",
        runs: [],
        ...overrides,
    } as EvalRunGroup;
}

function makeExperiment(overrides: Partial<EvalExperiment> = {}): EvalExperiment {
    return {
        id: "exp-1",
        name: "Acme Eval",
        targetAgent: "Orchestrator",
        runsPerExample: 1,
        maxWorkers: 4,
        createdBy: "alice",
        updatedBy: "alice",
        ...overrides,
    } as EvalExperiment;
}

describe("EvalExperimentReportPage helpers", () => {
    let helpers: typeof import("./EvalExperimentReportPage");

    beforeEach(async () => {
        vi.resetModules();
        helpers = await import("./EvalExperimentReportPage");
    });

    describe("buildReportTitle", () => {
        test("prefers the run name when present", () => {
            expect(helpers.buildReportTitle(makeGroup({ runName: "Smoke run" }), makeExperiment())).toBe("Experiment Report: Smoke run");
        });

        test("falls back to the experiment name when run name is empty or blank", () => {
            expect(helpers.buildReportTitle(makeGroup({ runName: "   " }), makeExperiment({ name: "Acme" }))).toBe("Experiment Report: Acme");
            expect(helpers.buildReportTitle(null, makeExperiment({ name: "Acme" }))).toBe("Experiment Report: Acme");
        });

        test("returns the plain title when both inputs are missing", () => {
            expect(helpers.buildReportTitle(null, null)).toBe("Experiment Report");
        });
    });

    describe("buildBreadcrumbs", () => {
        test("emits only the Experiments and Report crumbs when no experiment is loaded", () => {
            const navigate = vi.fn();
            const crumbs = helpers.buildBreadcrumbs(null, navigate);
            expect(crumbs.map(c => c.label)).toEqual(["Experiments", "Experiment Report"]);

            crumbs[0].onClick?.();
            expect(navigate).toHaveBeenCalledWith(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
        });

        test("inserts an experiment-name crumb that links to the experiment page", () => {
            const navigate = vi.fn();
            const crumbs = helpers.buildBreadcrumbs(makeExperiment({ id: "exp-42", name: "Acme" }), navigate);
            expect(crumbs.map(c => c.label)).toEqual(["Experiments", "Acme", "Experiment Report"]);

            crumbs[1].onClick?.();
            expect(navigate).toHaveBeenCalledWith(EVAL_ROUTES.experiment("exp-42"));
        });
    });

    describe("buildEvaluatorInfo", () => {
        test("deduplicates evaluator snapshots across runs and preserves first-seen order", () => {
            const group = {
                runs: [
                    {
                        evaluatorSnapshot: [
                            { id: "e1", name: "Exact", type: "heuristic", description: "exact match" },
                            { id: "e2", name: "LLM", type: "llm_judge" },
                        ],
                    },
                    {
                        evaluatorSnapshot: [
                            { id: "e1", name: "Exact (dup)", type: "heuristic" },
                            { id: "e3", name: "Custom", type: "other" },
                        ],
                    },
                ],
            } as Parameters<typeof helpers.buildEvaluatorInfo>[0];

            const out = helpers.buildEvaluatorInfo(group);
            expect(out.map(e => e.id)).toEqual(["e1", "e2", "e3"]);
            expect(out[0]).toMatchObject({ id: "e1", name: "Exact", type: "heuristic", description: "exact match" });
            expect(out[1]).toMatchObject({ id: "e2", name: "LLM", type: "llm_judge" });
            expect(out[2].type).toBeUndefined();
        });

        test("returns an empty array when the group is null", () => {
            expect(helpers.buildEvaluatorInfo(null)).toEqual([]);
        });

        test("skips snapshot entries missing id or name", () => {
            const group = {
                runs: [{ evaluatorSnapshot: [{ id: "e1" }, { name: "no-id" }, { id: "e2", name: "ok" }] }],
            } as unknown as Parameters<typeof helpers.buildEvaluatorInfo>[0];
            const out = helpers.buildEvaluatorInfo(group);
            expect(out.map(e => e.id)).toEqual(["e2"]);
        });

        test("carries a decision evaluator's configured scoring", () => {
            const group = {
                runs: [{ evaluatorSnapshot: [{ id: "d1", name: "Grounded", type: "decision", decision_config: { question: { type: "choice", instructions: "?" }, scoring: "expected", reads: ["response"] } }] }],
            } as unknown as Parameters<typeof helpers.buildEvaluatorInfo>[0];
            expect(helpers.buildEvaluatorInfo(group)[0]).toMatchObject({ id: "d1", type: "decision", scoring: "expected" });
        });
    });

    describe("buildScoresByEvaluatorByModel", () => {
        test("groups numeric scores by evaluator and model, ignoring null/missing scores", () => {
            const results = {
                resultsByModel: {
                    "gpt-4": [
                        {
                            scores: [
                                { evaluatorId: "e1", score: 1 },
                                { evaluatorId: "e2", score: 0.5 },
                            ],
                        },
                        { scores: [{ evaluatorId: "e1", score: 0 }] },
                        { scores: [{ evaluatorId: "e1", score: null }] },
                    ],
                    claude: [{ scores: [{ evaluatorId: "e1", score: 0.75 }] }],
                },
            };
            const evaluators = [
                { id: "e1", name: "Exact" },
                { id: "e2", name: "LLM" },
            ];
            const out = helpers.buildScoresByEvaluatorByModel(results, evaluators, ["gpt-4", "claude"]);

            expect(out.get("e1")).toEqual({ "gpt-4": [1, 0], claude: [0.75] });
            expect(out.get("e2")).toEqual({ "gpt-4": [0.5], claude: [] });
        });

        test("returns an empty map when results are missing", () => {
            const out = helpers.buildScoresByEvaluatorByModel(null, [{ id: "e1", name: "x" }], ["m1"]);
            expect(out.size).toBe(0);
        });
    });

    describe("buildModelScoreScope", () => {
        test("restricts the score map to the visible models and fills gaps with empty arrays", () => {
            const out = helpers.buildModelScoreScope({ "gpt-4": [1, 2], claude: [3] }, ["gpt-4", "gemini"]);
            expect(out).toEqual({ "gpt-4": [1, 2], gemini: [] });
        });
    });
});

describe("EvalExperimentReportPage render states", () => {
    let EvalExperimentReportPage: typeof import("./EvalExperimentReportPage").EvalExperimentReportPage;

    const stubs: {
        loaderData: { groupId?: string };
        navigate: ReturnType<typeof vi.fn>;
        runGroup: { group: EvalRunGroup | null; loading: boolean; isActive: boolean };
        runGroupResults: { results: unknown; loading: boolean };
        evalRuns: { cancelRun: ReturnType<typeof vi.fn>; runs: unknown[] };
        evalModelAxis: { effectiveModels: string[]; allModels: string[] };
        experiment: EvalExperiment | null;
    } = {
        loaderData: { groupId: "group-1" },
        navigate: vi.fn(),
        runGroup: { group: null, loading: true, isActive: false },
        runGroupResults: { results: null, loading: false },
        evalRuns: { cancelRun: vi.fn(), runs: [] },
        evalModelAxis: { effectiveModels: [], allModels: [] },
        experiment: null,
    };

    beforeEach(async () => {
        vi.resetModules();
        stubs.loaderData = { groupId: "group-1" };
        stubs.navigate = vi.fn();
        stubs.runGroup = { group: null, loading: true, isActive: false };
        stubs.runGroupResults = { results: null, loading: false };
        stubs.evalRuns = { cancelRun: vi.fn(), runs: [] };
        stubs.evalModelAxis = { effectiveModels: [], allModels: [] };
        stubs.experiment = null;

        vi.doMock("react-router-dom", async () => {
            const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
            return {
                ...actual,
                useLoaderData: () => stubs.loaderData,
                useNavigate: () => stubs.navigate,
            };
        });
        vi.doMock("@/api/evaluations/versioned", async () => {
            const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
            return {
                ...actual,
                evaluationsApi: {
                    ...actual.evaluationsApi,
                    getExperiment: vi.fn(() => Promise.resolve(stubs.experiment)),
                },
            };
        });
        vi.doMock("./hooks", () => ({
            useEvalRunGroup: () => stubs.runGroup,
            useEvalRunGroupResults: () => stubs.runGroupResults,
            useEvalRuns: () => stubs.evalRuns,
            useEvalModelAxis: () => stubs.evalModelAxis,
            useResizableSidePanel: () => ({ containerRef: { current: null }, width: 640, handleDragStart: vi.fn() }),
            useExperimentReportActions: () => ({
                deleteOpen: false,
                setDeleteOpen: vi.fn(),
                openDelete: vi.fn(),
                closeDelete: vi.fn(),
                confirmDelete: vi.fn(),
                cancelOpen: false,
                setCancelOpen: vi.fn(),
                openCancel: vi.fn(),
                closeCancel: vi.fn(),
                confirmCancel: vi.fn(),
                cancelling: false,
                cancelError: null,
                handleExport: vi.fn(),
            }),
        }));

        const mod = await import("./EvalExperimentReportPage");
        EvalExperimentReportPage = mod.EvalExperimentReportPage;
    });

    test("renders a loading state while the run group is being fetched", () => {
        stubs.runGroup = { group: null, loading: true, isActive: false };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });
        expect(screen.getByText(/Loading…/)).toBeInTheDocument();
    });

    test("renders a not-found message with a Back-to-Experiments action when the group is missing", () => {
        stubs.runGroup = { group: null, loading: false, isActive: false };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });
        expect(screen.getByText(/Report not found/)).toBeInTheDocument();
        const button = screen.getByRole("button", { name: /Back to Experiments/i });
        button.click();
        expect(stubs.navigate).toHaveBeenCalled();
    });

    test("renders the report content with the run name in the title when the group is loaded", async () => {
        stubs.runGroup = {
            group: makeGroup({ runName: "Smoke run" }),
            loading: false,
            isActive: false,
        };
        const { container } = render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });
        expect(container.textContent).toContain("Experiment Report: Smoke run");
        expect(screen.getAllByText(/Export/i).length).toBeGreaterThan(0);
    });

    test("Delete Report is hidden while a run of the group is active", () => {
        stubs.runGroup = { group: makeGroup({ runs: [{ id: "run-1", status: "running" }] as EvalRunGroup["runs"] }), loading: false, isActive: true };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });
        expect(screen.queryByRole("button", { name: "Delete Report" })).not.toBeInTheDocument();
    });

    test("Delete Report shows once every run of the group has finished", () => {
        stubs.runGroup = { group: makeGroup({ runs: [{ id: "run-1", status: "completed" }] as EvalRunGroup["runs"] }), loading: false, isActive: false };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });
        expect(screen.getByRole("button", { name: "Delete Report" })).toBeInTheDocument();
    });

    test("auth-required trials surface the Connect Tools banner", async () => {
        stubs.runGroup = { group: makeGroup({}), loading: false, isActive: false };
        stubs.runGroupResults = {
            results: {
                groupId: "group-1",
                resultsByModel: { "gpt-4": [{ id: "r1", runId: "run-1", exampleId: "ex-1", trialIndex: 0, failureKind: "auth_required", scores: [], createdAt: "" }] },
            },
            loading: false,
        };

        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });
        expect(await screen.findByTestId("authRequiredBanner")).toBeInTheDocument();
        expect(screen.getByText("1 example blocked on tool authorization")).toBeInTheDocument();
    });

    test("an evaluator tile says how consistently each example passed across its trials", async () => {
        const trialOf = (id: string, passed: boolean) => ({ id, runId: "run-1", exampleId: "ex-1", trialIndex: 0, scores: [{ id: `s-${id}`, evaluatorId: "ev-1", score: passed ? 1 : 0, passed }], createdAt: "" });
        stubs.runGroup = {
            group: makeGroup({ runs: [{ id: "run-1", status: "completed", model: "gpt-4", evaluatorSnapshot: [{ id: "ev-1", name: "Output Facts", type: "heuristic" }] }] as unknown as EvalRunGroup["runs"] }),
            loading: false,
            isActive: false,
        };
        stubs.evalModelAxis = { effectiveModels: [], allModels: ["gpt-4"] };
        stubs.runGroupResults = { results: { groupId: "group-1", resultsByModel: { "gpt-4": [trialOf("r1", true), trialOf("r2", false)] } }, loading: false };

        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });

        expect(await screen.findByTestId("evaluatorConsistencyLine")).toHaveTextContent("0 of 1 example passed all runs. 1 example partially failed.");
    });

    test("a workflow experiment's Connect Tools banner speaks of the workflow", async () => {
        stubs.experiment = makeExperiment({ targetType: "workflow", targetAgent: "", targetWorkflow: "order_flow" });
        stubs.runGroup = { group: makeGroup({}), loading: false, isActive: false };
        stubs.runGroupResults = {
            results: { groupId: "group-1", resultsByModel: { "gpt-4": [{ id: "r1", runId: "run-1", exampleId: "ex-1", trialIndex: 0, failureKind: "auth_required", targetType: "workflow", scores: [], createdAt: "" }] } },
            loading: false,
        };

        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });

        expect(await screen.findByText(/The workflow needed a tool login/)).toBeInTheDocument();
    });

    test("a run that failed only on tool authorization shows just the Connect Tools banner", async () => {
        stubs.runGroup = { group: makeGroup({ status: "failed" }), loading: false, isActive: false };
        stubs.runGroupResults = {
            results: {
                groupId: "group-1",
                resultsByModel: { "gpt-4": [{ id: "r1", runId: "run-1", exampleId: "ex-1", trialIndex: 0, errorMessage: "auth", failureKind: "auth_required", scores: [], createdAt: "" }] },
            },
            loading: false,
        };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });

        expect(await screen.findByTestId("authRequiredBanner")).toBeInTheDocument();
        expect(screen.queryByTestId("experimentReportBanner")).not.toBeInTheDocument();
        expect(screen.queryByText("Experiment partially completed with failures")).not.toBeInTheDocument();
    });

    test("a run with both kinds of failure shows both banners", async () => {
        stubs.runGroup = { group: makeGroup({ status: "failed" }), loading: false, isActive: false };
        stubs.runGroupResults = {
            results: {
                groupId: "group-1",
                resultsByModel: {
                    "gpt-4": [
                        { id: "r1", runId: "run-1", exampleId: "ex-1", trialIndex: 0, failureKind: "auth_required", scores: [], createdAt: "" },
                        { id: "r2", runId: "run-1", exampleId: "ex-2", trialIndex: 0, errorMessage: "agent timed out", scores: [], createdAt: "" },
                    ],
                },
            },
            loading: false,
        };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });

        expect(await screen.findByTestId("authRequiredBanner")).toBeInTheDocument();
        expect(screen.getByTestId("experimentReportBanner")).toHaveAttribute("data-banner-status", "failed");
    });

    test("a failed run with no categorized trial failures keeps the generic banner", async () => {
        stubs.runGroup = { group: makeGroup({ status: "failed" }), loading: false, isActive: false };
        stubs.runGroupResults = {
            results: {
                groupId: "group-1",
                resultsByModel: { "gpt-4": [{ id: "r1", runId: "run-1", exampleId: "ex-1", trialIndex: 0, scores: [], createdAt: "" }] },
            },
            loading: false,
        };
        render(<EvalExperimentReportPage />, { wrapper: buildQueryWrapper() });

        expect(await screen.findByText("Experiment partially completed with failures")).toBeInTheDocument();
        expect(screen.queryByTestId("authRequiredBanner")).not.toBeInTheDocument();
    });
});

describe("ExperimentReportBanner success state", () => {
    test("renders the completed-with-warnings copy and a dismiss control", () => {
        render(<ExperimentReportBanner status="completed_with_warnings" progressCurrent={5} progressTotal={5} />);
        expect(screen.getByText(/Experiment complete with warnings/)).toBeInTheDocument();
        expect(screen.getByText(/Some examples reported warnings/)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Dismiss/i })).toBeInTheDocument();
    });

    test("renders the plain success copy when no warnings are present", () => {
        render(<ExperimentReportBanner status="completed" progressCurrent={5} progressTotal={5} />);
        expect(screen.getByText(/Experiment complete$/)).toBeInTheDocument();
        expect(screen.getByText(/All examples were evaluated successfully/)).toBeInTheDocument();
    });
});
