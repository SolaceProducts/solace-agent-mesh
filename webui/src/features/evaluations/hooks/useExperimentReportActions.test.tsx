// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { EvalExperiment, EvalRunGroup } from "@/types/eval";

import { makeEvalRun } from "@/testing/helpers/evaluations";

import { EVAL_ROUTES } from "../constants";

function makeExperiment(overrides: Partial<EvalExperiment> = {}): EvalExperiment {
    return {
        id: "exp-1",
        name: "My Experiment",
        targetAgent: "Orchestrator",
        runsPerExample: 1,
        maxWorkers: 4,
        createdBy: "alice",
        updatedBy: "alice",
        ...overrides,
    } as EvalExperiment;
}

function makeGroup(overrides: Partial<EvalRunGroup> = {}): EvalRunGroup {
    return {
        id: "group-1",
        experimentId: "exp-1",
        runName: "Smoke",
        status: "running",
        progressCurrent: 0,
        progressTotal: 0,
        startedAt: "2026-01-01T00:00:00Z",
        runs: [makeEvalRun({ id: "r1", status: "running" }), makeEvalRun({ id: "r2", status: "pending" }), makeEvalRun({ id: "r3", status: "completed" })],
        ...overrides,
    } as EvalRunGroup;
}

describe("useExperimentReportActions", () => {
    let useExperimentReportActions: typeof import("./useExperimentReportActions").useExperimentReportActions;
    type AnyFn = ((...args: unknown[]) => unknown) & { mock: { calls: unknown[][] }; mockReturnValue: (v: unknown) => AnyFn; mockResolvedValue: (v: unknown) => AnyFn };
    let exportRunResultsMock: AnyFn;
    let urlCreate: AnyFn;
    let urlRevoke: AnyFn;
    let anchorClick: AnyFn;

    beforeEach(async () => {
        vi.resetModules();
        exportRunResultsMock = vi.fn() as unknown as AnyFn;
        vi.doMock("@/api/evaluations/versioned", async () => {
            const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
            return {
                ...actual,
                evaluationsApi: {
                    ...actual.evaluationsApi,
                    exportRunResults: (...args: unknown[]) => exportRunResultsMock(...args),
                },
            };
        });

        urlCreate = vi.fn().mockReturnValue("blob:url") as unknown as AnyFn;
        urlRevoke = vi.fn() as unknown as AnyFn;
        (globalThis.URL as unknown as { createObjectURL: AnyFn; revokeObjectURL: AnyFn }).createObjectURL = urlCreate;
        (globalThis.URL as unknown as { createObjectURL: AnyFn; revokeObjectURL: AnyFn }).revokeObjectURL = urlRevoke;

        anchorClick = vi.fn() as unknown as AnyFn;
        vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(anchorClick as unknown as () => void);

        const mod = await import("./useExperimentReportActions");
        useExperimentReportActions = mod.useExperimentReportActions;
    });

    type ReportActionsHookOverrides = {
        group?: EvalRunGroup | null;
        experiment?: EvalExperiment | null;
        cancelRun?: (runId: string) => Promise<unknown>;
        deleteMutation?: { mutateAsync: ReturnType<typeof vi.fn> };
        navigate?: ReturnType<typeof vi.fn>;
    };

    function renderActions(overrides: ReportActionsHookOverrides = {}) {
        const navigate = overrides.navigate ?? vi.fn();
        const deleteMutation = overrides.deleteMutation ?? { mutateAsync: vi.fn().mockResolvedValue(undefined) };
        const cancelRun = overrides.cancelRun ?? vi.fn().mockResolvedValue(undefined);
        const hook = renderHook(
            () =>
                useExperimentReportActions({
                    group: overrides.group === undefined ? makeGroup() : overrides.group,
                    experiment: overrides.experiment === undefined ? makeExperiment() : overrides.experiment,
                    deleteRunGroupMutation: deleteMutation as unknown as Parameters<typeof useExperimentReportActions>[0]["deleteRunGroupMutation"],
                    cancelRun,
                    navigate: navigate as unknown as Parameters<typeof useExperimentReportActions>[0]["navigate"],
                }),
            { wrapper: ({ children }) => <OpenFeatureTestProvider flagValueMap={{}}>{children}</OpenFeatureTestProvider> }
        );
        return { hook, navigate, deleteMutation, cancelRun };
    }

    test("openCancel clears any prior error and opens the dialog", async () => {
        const cancelRun = vi.fn().mockRejectedValue(new Error("boom"));
        const { hook } = renderActions({ cancelRun });

        await act(async () => {
            await hook.result.current.confirmCancel();
        });
        expect(hook.result.current.cancelError).not.toBeNull();

        act(() => hook.result.current.openCancel());
        expect(hook.result.current.cancelOpen).toBe(true);
        expect(hook.result.current.cancelError).toBeNull();
    });

    test("confirmCancel is a no-op when there is no group", async () => {
        const cancelRun = vi.fn();
        const { hook } = renderActions({ group: null, cancelRun });

        await act(async () => {
            await hook.result.current.confirmCancel();
        });

        expect(cancelRun).not.toHaveBeenCalled();
        expect(hook.result.current.cancelling).toBe(false);
    });

    test("confirmCancel only cancels running and pending runs and clears state on success", async () => {
        const cancelRun = vi.fn().mockResolvedValue(undefined);
        const { hook } = renderActions({ cancelRun });

        await act(async () => {
            await hook.result.current.confirmCancel();
        });

        expect(cancelRun).toHaveBeenCalledTimes(2);
        expect(cancelRun).toHaveBeenCalledWith("r1");
        expect(cancelRun).toHaveBeenCalledWith("r2");
        expect(cancelRun).not.toHaveBeenCalledWith("r3");
        expect(hook.result.current.cancelling).toBe(false);
        expect(hook.result.current.cancelError).toBeNull();
        expect(hook.result.current.cancelOpen).toBe(false);
    });

    test("confirmCancel surfaces the all-failed message when every run rejects", async () => {
        const cancelRun = vi.fn().mockRejectedValue(new Error("nope"));
        const { hook } = renderActions({ cancelRun });

        await act(async () => {
            await hook.result.current.confirmCancel();
        });

        expect(hook.result.current.cancelError).toMatch(/Failed to cancel the experiment/);
    });

    test("confirmCancel surfaces the partial-failure count when some runs reject", async () => {
        const cancelRun = vi.fn().mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("nope"));
        const { hook } = renderActions({ cancelRun });

        await act(async () => {
            await hook.result.current.confirmCancel();
        });

        expect(hook.result.current.cancelError).toBe("Cancelled 1 of 2 runs; the rest failed to cancel.");
    });

    test("confirmDelete invokes the mutation and routes back to the experiment page when known", async () => {
        const experiment = makeExperiment({ id: "exp-42" });
        const deleteMutation = { mutateAsync: vi.fn().mockResolvedValue(undefined) };
        const navigate = vi.fn();
        const { hook } = renderActions({ experiment, deleteMutation, navigate });

        await act(async () => {
            await hook.result.current.confirmDelete();
        });

        expect(deleteMutation.mutateAsync).toHaveBeenCalledWith("group-1");
        expect(navigate).toHaveBeenCalledWith(EVAL_ROUTES.experiment("exp-42"));
        expect(hook.result.current.deleteOpen).toBe(false);
    });

    test("confirmDelete routes to the experiments tab when experiment is unknown", async () => {
        const deleteMutation = { mutateAsync: vi.fn().mockResolvedValue(undefined) };
        const navigate = vi.fn();
        const { hook } = renderActions({ experiment: null, deleteMutation, navigate });

        await act(async () => {
            await hook.result.current.confirmDelete();
        });

        expect(navigate).toHaveBeenCalledWith(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
    });

    test("confirmDelete is a no-op without a group", async () => {
        const deleteMutation = { mutateAsync: vi.fn() };
        const navigate = vi.fn();
        const { hook } = renderActions({ group: null, deleteMutation, navigate });

        await act(async () => {
            await hook.result.current.confirmDelete();
        });

        expect(deleteMutation.mutateAsync).not.toHaveBeenCalled();
        expect(navigate).not.toHaveBeenCalled();
    });

    test("handleExport downloads one file per run and revokes each object URL", async () => {
        const group = makeGroup({
            runs: [makeEvalRun({ id: "a", model: "gpt-4" }), makeEvalRun({ id: "b", model: "claude" })],
        });
        exportRunResultsMock.mockResolvedValue(new Blob(["data"], { type: "text/csv" }));
        const { hook } = renderActions({ group });

        await act(async () => {
            await hook.result.current.handleExport("csv");
        });

        expect(exportRunResultsMock).toHaveBeenCalledTimes(2);
        expect(exportRunResultsMock).toHaveBeenNthCalledWith(1, "a", "csv");
        expect(exportRunResultsMock).toHaveBeenNthCalledWith(2, "b", "csv");
        expect(anchorClick).toHaveBeenCalledTimes(2);
        expect(urlCreate).toHaveBeenCalledTimes(2);
        expect(urlRevoke).toHaveBeenCalledTimes(2);
    });

    test("handleExport is a no-op without a group", async () => {
        const { hook } = renderActions({ group: null });

        await act(async () => {
            await hook.result.current.handleExport("json");
        });

        expect(exportRunResultsMock).not.toHaveBeenCalled();
    });

    test("openDelete / closeDelete and closeCancel toggle the dialog flags", () => {
        const { hook } = renderActions();

        act(() => hook.result.current.openDelete());
        expect(hook.result.current.deleteOpen).toBe(true);
        act(() => hook.result.current.closeDelete());
        expect(hook.result.current.deleteOpen).toBe(false);

        act(() => hook.result.current.openCancel());
        expect(hook.result.current.cancelOpen).toBe(true);
        act(() => hook.result.current.closeCancel());
        expect(hook.result.current.cancelOpen).toBe(false);
    });
});
