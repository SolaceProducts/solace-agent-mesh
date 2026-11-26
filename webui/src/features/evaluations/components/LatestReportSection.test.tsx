// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { ExperimentModelConfig } from "@/types/eval";

import { LatestReportSection } from "@/features/evaluations/components/LatestReportSection";

import { makeEvalRun } from "@/testing/helpers/evaluations";

function makeModelConfig(overrides: Partial<ExperimentModelConfig> = {}): ExperimentModelConfig {
    return {
        modelConfigId: "model-config-1",
        model: "gpt-4",
        alias: "GPT-4",
        ...overrides,
    };
}

describe("LatestReportSection", () => {
    test("renders with completed runs and calculates progress", () => {
        const runs = [makeEvalRun({ id: "run-1", progressCurrent: 10, progressTotal: 10 }), makeEvalRun({ id: "run-2", model: "model-config-2", progressCurrent: 8, progressTotal: 10 })];
        const models = [makeModelConfig({ modelConfigId: "model-config-1", model: "gpt-4" }), makeModelConfig({ modelConfigId: "model-config-2", model: "claude-3" })];

        const { container } = render(
            <LatestReportSection
                groupId="group-1"
                runName="Test Run"
                runs={runs}
                status="completed"
                completedAt="2026-01-01T01:00:00Z"
                startedAt="2026-01-01T00:00:00Z"
                models={models}
                evaluatorIds={[]}
                evaluatorNameMap={new Map()}
                onViewFullReport={vi.fn()}
            />
        );

        expect(container.textContent).toContain("Test Run");
        expect(container.textContent).toContain("Complete");
    });

    test("renders in-progress status with progress badge", () => {
        const runs = [makeEvalRun({ id: "run-1", status: "running", progressCurrent: 5, progressTotal: 10 })];
        const models = [makeModelConfig()];

        const { container } = render(
            <LatestReportSection groupId="group-1" runName="Running Test" runs={runs} status="running" startedAt="2026-01-01T00:00:00Z" models={models} evaluatorIds={[]} evaluatorNameMap={new Map()} onViewFullReport={vi.fn()} />
        );

        expect(container.textContent).toContain("Running Test");
        expect(container.textContent).toContain("5/10 completed");
    });

    test("renders default title when runName is undefined", () => {
        const runs = [makeEvalRun()];
        const models = [makeModelConfig()];

        const { container } = render(
            <LatestReportSection groupId="group-1" runs={runs} status="completed" completedAt="2026-01-01T01:00:00Z" startedAt="2026-01-01T00:00:00Z" models={models} evaluatorIds={[]} evaluatorNameMap={new Map()} onViewFullReport={vi.fn()} />
        );

        expect(container.textContent).toContain("Untitled run");
    });
});
