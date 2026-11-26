// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { PageLabel } from "@/common/layout";

import { formatDuration } from "@/utils/format";

import { Button } from "@/ui/button";
import type { EvalRun, ExperimentModelConfig } from "@/types/eval";

import { buildModelColorIndex } from "../utils";

import { aggregateEvaluatorStats } from "./decisionScore";
import { EvaluatorResultCard } from "./EvaluatorResultCard";
import { calculateRunGroupProgress, displayStatusLabel, runGroupAggregateStatusBadge } from "./runGroupStatus";

interface LatestReportSectionProps {
    readonly groupId: string;
    readonly runName?: string;
    readonly runs: EvalRun[];
    readonly status: EvalRun["status"];
    readonly completedAt?: string;
    readonly startedAt: string;
    readonly models: ExperimentModelConfig[];
    readonly evaluatorIds: string[];
    readonly evaluatorNameMap: Map<string, string>;
    readonly onViewFullReport: () => void;
}

const STORAGE_KEY = "eval.latestReportCollapsed";

function readPersistedCollapsed(groupId: string): boolean {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) return false;
        const parsed = JSON.parse(raw) as { groupId?: string; collapsed?: boolean };
        return parsed.groupId === groupId && parsed.collapsed === true;
    } catch {
        return false;
    }
}

function writePersistedCollapsed(groupId: string, collapsed: boolean) {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify({ groupId, collapsed }));
    } catch {}
}

export function LatestReportSection({ groupId, runName, runs, status, completedAt, startedAt, models, evaluatorIds, evaluatorNameMap, onViewFullReport }: LatestReportSectionProps) {
    const [collapsed, setCollapsed] = useState<boolean>(() => readPersistedCollapsed(groupId));

    useEffect(() => {
        setCollapsed(readPersistedCollapsed(groupId));
    }, [groupId]);

    const toggle = () => {
        setCollapsed(prev => {
            const next = !prev;
            writePersistedCollapsed(groupId, next);
            return next;
        });
    };

    const modelColorIndex = buildModelColorIndex(models);
    const scoresByEvaluator = (evaluatorKey: string): Record<string, number | undefined> => {
        const result: Record<string, number | undefined> = {};
        for (const m of models) {
            if (!m.modelConfigId) continue;
            const run = runs.find(r => r.model === m.modelConfigId);
            result[m.modelConfigId] = run?.summaryScores?.[evaluatorKey];
        }
        return result;
    };

    const duration = completedAt ? formatDuration(new Date(completedAt).getTime() - new Date(startedAt).getTime()) : null;

    const progress = calculateRunGroupProgress(runs);

    const Chevron = collapsed ? ChevronRight : ChevronDown;
    const badge = runGroupAggregateStatusBadge(status, runs, progress);

    return (
        <section className="flex flex-col gap-4">
            <div className="flex flex-wrap items-start justify-between gap-4">
                <button type="button" onClick={toggle} className="flex min-w-0 flex-1 items-start gap-3 text-left">
                    <Chevron className="mt-0.5 size-5 shrink-0 text-(--secondary-text-wMain)" aria-hidden="true" />
                    <div className="flex min-w-0 flex-col gap-0.5">
                        <PageLabel>Latest Report</PageLabel>
                        <span className="truncate text-base font-medium text-(--primary-text-wMain)">{runName ?? "Untitled run"}</span>
                    </div>
                </button>
                <div className="flex items-start gap-8">
                    <div className="flex flex-col gap-0.5">
                        <span className="text-sm text-(--secondary-text-wMain)">Status</span>
                        {badge}
                    </div>
                    <div className="flex flex-col gap-0.5">
                        <span className="text-sm text-(--secondary-text-wMain)">Completed On</span>
                        <span className="text-sm text-(--primary-text-wMain)">{completedAt ? new Date(completedAt).toLocaleString() : displayStatusLabel(status, runs)}</span>
                    </div>
                    <div className="flex flex-col gap-0.5">
                        <PageLabel>Duration</PageLabel>
                        <span className="text-sm text-(--primary-text-wMain)">{duration ?? "—"}</span>
                    </div>
                    <Button onClick={onViewFullReport}>View Full Report</Button>
                </div>
            </div>
            {!collapsed && evaluatorIds.length > 0 && (
                <div className="grid gap-4" style={{ gridTemplateColumns: `repeat(${Math.min(evaluatorIds.length, 3)}, minmax(0, 1fr))` }}>
                    {evaluatorIds.map(evalId => (
                        <EvaluatorResultCard
                            key={evalId}
                            evaluatorName={evaluatorNameMap.get(evalId) ?? evalId}
                            models={models}
                            scoresByModel={scoresByEvaluator(evalId)}
                            modelColorIndex={modelColorIndex}
                            stats={aggregateEvaluatorStats(runs, evalId)}
                        />
                    ))}
                </div>
            )}
        </section>
    );
}
